//! Scheduler du Kernel Financier
//!
//! Ce module orchestre les évaluations périodiques du moteur de payout.
//! Il s'exécute en boucle avec un intervalle configurable et déclenche
//! PayoutService::run_payout_evaluation() à chaque tick.
//!
//! Design : une seule instance du scheduler est active à la fois.
//! Si plusieurs instances du kernel sont déployées, le `FOR UPDATE SKIP LOCKED`
//! sur les cycles dans la vue `payout_eligible_cycles` garantit qu'un seul
//! processus traite chaque cycle.

use std::sync::Arc;
use std::time::Duration;
use tokio::time;
use tracing::{error, info};

use crate::application::payout_service::PayoutService;

pub struct PayoutScheduler {
    service: Arc<PayoutService>,
    interval: Duration,
}

impl PayoutScheduler {
    pub fn new(service: Arc<PayoutService>, interval_secs: u64) -> Self {
        Self {
            service,
            interval: Duration::from_secs(interval_secs),
        }
    }

    /// Démarre la boucle de scheduling. S'arrête quand `shutdown` est résolu.
    pub async fn run(self, mut shutdown: tokio::sync::oneshot::Receiver<()>) {
        info!(
            interval_secs = self.interval.as_secs(),
            "PayoutScheduler started"
        );

        let mut ticker = time::interval(self.interval);
        ticker.set_missed_tick_behavior(time::MissedTickBehavior::Skip);

        loop {
            tokio::select! {
                _ = ticker.tick() => {
                    info!("PayoutScheduler: running evaluation cycle");
                    let svc = Arc::clone(&self.service);
                    // Exécution dans une tâche séparée pour ne pas bloquer le tick suivant
                    tokio::spawn(async move {
                        if let Err(e) = svc.run_payout_evaluation().await {
                            error!("PayoutScheduler evaluation error: {}", e);
                        }
                    });
                }
                _ = &mut shutdown => {
                    info!("PayoutScheduler received shutdown signal");
                    break;
                }
            }
        }
        info!("PayoutScheduler stopped");
    }
}
