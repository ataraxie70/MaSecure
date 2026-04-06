//! Port (trait) pour le dépôt du fonds de roulement

use async_trait::async_trait;
use uuid::Uuid;

use crate::domain::working_capital::{WorkingCapital, WorkingCapitalId};

#[async_trait]
pub trait WorkingCapitalRepository: Send + Sync {
    /// Charge le fonds de roulement d'un groupe (None si pas encore créé)
    async fn get_by_group_id(&self, group_id: Uuid) -> anyhow::Result<Option<WorkingCapital>>;

    /// Crée ou met à jour le fonds de roulement
    async fn upsert(&self, wc: &WorkingCapital) -> anyhow::Result<()>;

    /// Confirme une avance dans une transaction existante (débite le fonds)
    async fn confirm_advance_in_tx(
        &self,
        wc_id: WorkingCapitalId,
        amount_minor: i64,
        ledger_entry_id: Uuid,
        tx: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    ) -> anyhow::Result<()>;

    /// Reçoit un remboursement dans une transaction existante (recrédite le fonds)
    async fn receive_repayment_in_tx(
        &self,
        wc_id: WorkingCapitalId,
        amount_minor: i64,
        ledger_entry_id: Uuid,
        tx: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    ) -> anyhow::Result<()>;

    /// Dépôt direct (abondement par les membres)
    async fn deposit_in_tx(
        &self,
        wc_id: WorkingCapitalId,
        amount_minor: i64,
        ledger_entry_id: Uuid,
        tx: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    ) -> anyhow::Result<()>;
}
