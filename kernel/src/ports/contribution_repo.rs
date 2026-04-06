use async_trait::async_trait;
use uuid::Uuid;

use crate::domain::identity::IdentityId;

#[derive(Debug, Clone)]
pub struct ContributionCycleContext {
    pub cycle_id: Uuid,
    pub beneficiary_id: IdentityId,
    pub state: String,
}

impl ContributionCycleContext {
    pub fn accepts_callbacks(&self) -> bool {
        self.state == "committed"
    }
}

#[derive(Debug, Clone)]
pub struct StoredContribution {
    pub provider_tx_ref: String,
    pub cycle_id: Uuid,
    pub status: String,
}

#[derive(Debug, Clone)]
pub struct ReconciledContributionRecord {
    pub cycle_id: Uuid,
    pub beneficiary_id: IdentityId,
    pub payer_identity_id: IdentityId,
    pub payer_msisdn: String,
    pub amount_minor: i64,
    pub provider_tx_ref: String,
    pub ledger_entry_id: Uuid,
}

#[derive(Debug, Clone)]
pub struct QuarantinedContributionRecord {
    pub cycle_id: Uuid,
    pub beneficiary_id: IdentityId,
    pub payer_msisdn: String,
    pub amount_minor: i64,
    pub provider_tx_ref: String,
    pub ledger_entry_id: Uuid,
    pub notes: Option<String>,
}

#[async_trait]
pub trait ContributionRepository: Send + Sync {
    async fn find_by_provider_tx_ref(
        &self,
        provider_tx_ref: &str,
    ) -> anyhow::Result<Option<StoredContribution>>;

    async fn get_cycle_context(
        &self,
        cycle_id: Uuid,
    ) -> anyhow::Result<Option<ContributionCycleContext>>;

    async fn insert_reconciled_in_tx(
        &self,
        record: ReconciledContributionRecord,
        tx: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    ) -> anyhow::Result<()>;

    async fn insert_quarantined_in_tx(
        &self,
        record: QuarantinedContributionRecord,
        tx: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    ) -> anyhow::Result<()>;
}
