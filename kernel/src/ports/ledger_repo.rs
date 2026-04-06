use crate::domain::ledger::{LedgerEntry, LedgerEntryBuilder};
use async_trait::async_trait;
use uuid::Uuid;

#[async_trait]
pub trait LedgerRepository: Send + Sync {
    async fn append(
        &self,
        builder: LedgerEntryBuilder,
        tx: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    ) -> anyhow::Result<LedgerEntry>;

    async fn get_by_aggregate(
        &self,
        aggregate_type: &str,
        aggregate_id: Uuid,
    ) -> anyhow::Result<Vec<LedgerEntry>>;

    async fn get_all_ordered(&self) -> anyhow::Result<Vec<LedgerEntry>>;
}
