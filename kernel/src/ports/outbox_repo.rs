use crate::domain::events::DomainEvent;
use async_trait::async_trait;
use uuid::Uuid;

#[async_trait]
pub trait OutboxRepository: Send + Sync {
    async fn insert_in_tx(
        &self,
        event: &DomainEvent,
        idempotency_key: String,
        ledger_entry_id: Option<Uuid>,
        tx: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    ) -> anyhow::Result<Uuid>;

    async fn get_pending(&self, limit: i32) -> anyhow::Result<Vec<OutboxRow>>;
    async fn mark_delivered(&self, id: Uuid, external_ref: Option<String>) -> anyhow::Result<()>;
    async fn schedule_retry(&self, id: Uuid, attempts: i16, error: String) -> anyhow::Result<()>;
    async fn mark_dead_letter(&self, id: Uuid, final_error: String) -> anyhow::Result<()>;
}

#[derive(Debug, Clone)]
pub struct OutboxRow {
    pub id: Uuid,
    pub event_type: String,
    pub aggregate_id: Uuid,
    pub payload: serde_json::Value,
    pub target_service: String,
    pub idempotency_key: String,
    pub attempts: i16,
}
