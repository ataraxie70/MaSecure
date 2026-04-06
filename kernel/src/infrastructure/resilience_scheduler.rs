//! Scheduler de résilience du Kernel Financier (Phase 4)
//!
//! Ce scheduler complète le PayoutScheduler existant.
//! Il se déclenche sur les cycles en état COMMITTED dont l'échéance
//! est dépassée mais le seuil non atteint, et applique la politique
//! de résilience configurée sur chaque cycle.
//!
//! SÉQUENCE dans main.rs :
//!   PayoutScheduler → évalue les cycles éligibles (seuil atteint)
//!   ResilienceScheduler → évalue les cycles bloqués (seuil non atteint)
//!
//! Les deux schedulers utilisent FOR UPDATE SKIP LOCKED — pas de collision possible.

use std::sync::Arc;
use std::time::Duration;
use tokio::time;
use tracing::{error, info, warn};
use uuid::Uuid;

use crate::application::resilience_service::{ResilienceOutcome, ResilienceService};
use crate::domain::working_capital::ResiliencePolicy;

pub struct ResilienceScheduler {
    service: Arc<ResilienceService>,
    interval: Duration,
}

impl ResilienceScheduler {
    pub fn new(service: Arc<ResilienceService>, interval_secs: u64) -> Self {
        Self {
            service,
            interval: Duration::from_secs(interval_secs),
        }
    }

    pub async fn run(self, mut shutdown: tokio::sync::oneshot::Receiver<()>) {
        info!(
            interval_secs = self.interval.as_secs(),
            "ResilienceScheduler started"
        );

        let mut ticker = time::interval(self.interval);
        ticker.set_missed_tick_behavior(time::MissedTickBehavior::Skip);

        loop {
            tokio::select! {
                _ = ticker.tick() => {
                    let svc = Arc::clone(&self.service);
                    tokio::spawn(async move {
                        if let Err(e) = run_resilience_pass(svc).await {
                            error!("ResilienceScheduler error: {}", e);
                        }
                    });
                }
                _ = &mut shutdown => {
                    info!("ResilienceScheduler received shutdown signal");
                    break;
                }
            }
        }
        info!("ResilienceScheduler stopped");
    }
}

/// Passe de résilience : charge tous les cycles COMMITTED dont l'échéance
/// est dépassée et le seuil non atteint, applique la politique configurée.
async fn run_resilience_pass(svc: Arc<ResilienceService>) -> anyhow::Result<()> {
    let overdue_cycles = svc
        .cycle_repo
        .get_overdue_below_threshold()
        .await?;

    info!(
        count = overdue_cycles.len(),
        "ResilienceScheduler: {} overdue cycles found", overdue_cycles.len()
    );

    for row in overdue_cycles {
        let policy = parse_policy(&row.resilience_policy);
        match svc.evaluate_resilience(row.cycle_id, policy).await {
            Ok(ResilienceOutcome::ThresholdAlreadyMet) => {
                info!(cycle_id = %row.cycle_id, "Cycle threshold already met — PayoutScheduler will handle");
            }
            Ok(ResilienceOutcome::FullAdvanceUsed { advance_minor }) => {
                info!(cycle_id = %row.cycle_id, advance_minor, "Full WC advance issued");
            }
            Ok(ResilienceOutcome::ProRataDispatched { amount_minor, fraction_pct }) => {
                info!(cycle_id = %row.cycle_id, amount_minor, fraction_pct, "Pro-rata dispatched");
            }
            Ok(ResilienceOutcome::HybridDispatched { advance_minor, amount_minor }) => {
                info!(cycle_id = %row.cycle_id, advance_minor, amount_minor, "Hybrid advance+prorata dispatched");
            }
            Ok(ResilienceOutcome::Blocked { reason }) => {
                warn!(cycle_id = %row.cycle_id, reason, "Cycle blocked by resilience policy");
            }
            Err(e) => {
                error!(cycle_id = %row.cycle_id, "Resilience evaluation failed: {}", e);
            }
        }
    }
    Ok(())
}

fn parse_policy(s: &str) -> ResiliencePolicy {
    match s {
        "use_working_capital" => ResiliencePolicy::UseWorkingCapital,
        "pro_rata" => ResiliencePolicy::ProRata,
        "working_capital_then_pro_rata" => ResiliencePolicy::WorkingCapitalThenProRata,
        _ => ResiliencePolicy::WaitForThreshold,
    }
}

/// Ligne de résultat de la vue `overdue_below_threshold_cycles`
#[derive(Debug)]
pub struct OverdueCycleRow {
    pub cycle_id: Uuid,
    pub group_id: Uuid,
    pub resilience_policy: String,
}
