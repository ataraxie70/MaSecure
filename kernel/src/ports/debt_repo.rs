//! Port (trait) pour le dépôt des dettes membres

use async_trait::async_trait;
use uuid::Uuid;

use crate::domain::debt::Debt;
use crate::domain::identity::IdentityId;

#[async_trait]
pub trait DebtRepository: Send + Sync {
    /// Retourne les dettes actives d'un membre pour un cycle donné
    async fn get_active_debts_for_member(
        &self,
        debtor_id: IdentityId,
        cycle_id: Uuid,
    ) -> anyhow::Result<Vec<Debt>>;

    /// Retourne toutes les dettes actives d'un groupe (pour dashboard)
    async fn get_group_active_debts(
        &self,
        group_id: Uuid,
    ) -> anyhow::Result<Vec<Debt>>;

    /// Insère une nouvelle dette dans une transaction existante
    async fn insert_debt_in_tx(
        &self,
        debt: &Debt,
        ledger_entry_id: Uuid,
        tx: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    ) -> anyhow::Result<()>;

    /// Met à jour l'état d'une dette (remboursement partiel/total) dans une transaction
    async fn update_debt_in_tx(
        &self,
        debt: &Debt,
        tx: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    ) -> anyhow::Result<()>;

    /// Passe des dettes en write-off (décision gouvernance)
    async fn write_off_debts(
        &self,
        debt_ids: &[Uuid],
        tx: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    ) -> anyhow::Result<()>;
}
