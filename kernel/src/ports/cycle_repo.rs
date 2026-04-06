use crate::domain::cycle::{Cycle, PayoutState};
use async_trait::async_trait;
use uuid::Uuid;

#[async_trait]
pub trait CycleRepository: Send + Sync {
    async fn get_payout_eligible(&self) -> anyhow::Result<Vec<EligibleCycleRow>>;
    async fn update_payout_state(
        &self,
        cycle_id: Uuid,
        payout_state: PayoutState,
        idempotency_key: Option<String>,
        tx: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    ) -> anyhow::Result<()>;
    async fn get_by_id(&self, cycle_id: Uuid) -> anyhow::Result<Option<Cycle>>;
    async fn get_overdue_below_threshold(&self) -> anyhow::Result<Vec<OverdueCycleRow>>;
}

#[derive(Debug)]
pub struct EligibleCycleRow {
    pub cycle_id: Uuid,
    pub group_id: Uuid,
    pub beneficiary_id: Uuid,
    pub collected_amount_minor: i64,
    pub beneficiary_msisdn: String,
    pub beneficiary_provider: String,
    pub payout_idempotency_key: Option<String>,
}

/// Cycles COMMITTED dont l'échéance est dépassée et le seuil non atteint.
/// Utilisé par le ResilienceScheduler (Phase 4).
#[derive(Debug)]
pub struct OverdueCycleRow {
    pub cycle_id: uuid::Uuid,
    pub group_id: uuid::Uuid,
    pub resilience_policy: String,
    pub collected_amount_minor: i64,
    pub payout_threshold_minor: i64,
}


