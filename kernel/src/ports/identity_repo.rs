use async_trait::async_trait;

use crate::domain::identity::IdentityId;

#[async_trait]
pub trait IdentityRepository: Send + Sync {
    async fn resolve_msisdn(&self, msisdn: &str) -> anyhow::Result<Option<IdentityId>>;

    async fn ensure_payment_instrument_in_tx(
        &self,
        msisdn: &str,
        provider: &str,
        identity_id: Option<IdentityId>,
        tx: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    ) -> anyhow::Result<bool>;
}
