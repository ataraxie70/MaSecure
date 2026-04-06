//! Implémentation PostgreSQL du WorkingCapitalRepository
//!
//! Toutes les mutations du fonds de roulement passent par des transactions
//! PostgreSQL atomiques incluant le ledger_entry correspondant.
//! Le trigger `enforce_wc_non_negative` en base constitue une seconde ligne
//! de défense contre tout solde négatif ou réservé > solde.

use async_trait::async_trait;
use sqlx::{PgPool, Postgres, Row, Transaction};
use uuid::Uuid;

use crate::domain::working_capital::{WorkingCapital, WorkingCapitalId};
use crate::ports::working_capital_repo::WorkingCapitalRepository;

pub struct PgWorkingCapitalRepository {
    pool: PgPool,
}

impl PgWorkingCapitalRepository {
    pub fn new(pool: PgPool) -> Self {
        Self { pool }
    }
}

#[async_trait]
impl WorkingCapitalRepository for PgWorkingCapitalRepository {
    async fn get_by_group_id(&self, group_id: Uuid) -> anyhow::Result<Option<WorkingCapital>> {
        let row = sqlx::query(
            "SELECT id, group_id, balance_minor, reserved_minor \
             FROM working_capital WHERE group_id = $1",
        )
        .bind(group_id)
        .fetch_optional(&self.pool)
        .await?;

        match row {
            None => Ok(None),
            Some(r) => {
                let id: Uuid = r.try_get("id")?;
                let balance: i64 = r.try_get("balance_minor")?;
                let reserved: i64 = r.try_get("reserved_minor")?;
                Ok(Some(WorkingCapital {
                    id: WorkingCapitalId(id),
                    group_id,
                    balance_minor: balance,
                    reserved_minor: reserved,
                }))
            }
        }
    }

    async fn upsert(&self, wc: &WorkingCapital) -> anyhow::Result<()> {
        sqlx::query(
            "INSERT INTO working_capital (id, group_id, balance_minor, reserved_minor)
             VALUES ($1, $2, $3, $4)
             ON CONFLICT (group_id) DO UPDATE
             SET balance_minor  = EXCLUDED.balance_minor,
                 reserved_minor = EXCLUDED.reserved_minor,
                 updated_at     = NOW()",
        )
        .bind(wc.id.0)
        .bind(wc.group_id)
        .bind(wc.balance_minor)
        .bind(wc.reserved_minor)
        .execute(&self.pool)
        .await?;
        Ok(())
    }

    async fn confirm_advance_in_tx(
        &self,
        wc_id: WorkingCapitalId,
        amount_minor: i64,
        ledger_entry_id: Uuid,
        tx: &mut Transaction<'_, Postgres>,
    ) -> anyhow::Result<()> {
        // Débite le solde et libère la réserve en une seule opération
        sqlx::query(
            "UPDATE working_capital
             SET balance_minor        = balance_minor  - $2,
                 reserved_minor       = reserved_minor - $2,
                 total_advanced_minor = total_advanced_minor + $2,
                 updated_at           = NOW()
             WHERE id = $1",
        )
        .bind(wc_id.0)
        .bind(amount_minor)
        .execute(&mut **tx)
        .await?;

        // Journal des transactions du fonds
        sqlx::query(
            "INSERT INTO wc_transactions (wc_id, event_type, amount_minor, ledger_entry_id)
             VALUES ($1, 'advance', $2, $3)",
        )
        .bind(wc_id.0)
        .bind(amount_minor)
        .bind(ledger_entry_id)
        .execute(&mut **tx)
        .await?;

        Ok(())
    }

    async fn receive_repayment_in_tx(
        &self,
        wc_id: WorkingCapitalId,
        amount_minor: i64,
        ledger_entry_id: Uuid,
        tx: &mut Transaction<'_, Postgres>,
    ) -> anyhow::Result<()> {
        sqlx::query(
            "UPDATE working_capital
             SET balance_minor      = balance_minor + $2,
                 total_repaid_minor = total_repaid_minor + $2,
                 updated_at         = NOW()
             WHERE id = $1",
        )
        .bind(wc_id.0)
        .bind(amount_minor)
        .execute(&mut **tx)
        .await?;

        sqlx::query(
            "INSERT INTO wc_transactions (wc_id, event_type, amount_minor, ledger_entry_id)
             VALUES ($1, 'repayment', $2, $3)",
        )
        .bind(wc_id.0)
        .bind(amount_minor)
        .bind(ledger_entry_id)
        .execute(&mut **tx)
        .await?;

        Ok(())
    }

    async fn deposit_in_tx(
        &self,
        wc_id: WorkingCapitalId,
        amount_minor: i64,
        ledger_entry_id: Uuid,
        tx: &mut Transaction<'_, Postgres>,
    ) -> anyhow::Result<()> {
        sqlx::query(
            "UPDATE working_capital
             SET balance_minor = balance_minor + $2,
                 updated_at    = NOW()
             WHERE id = $1",
        )
        .bind(wc_id.0)
        .bind(amount_minor)
        .execute(&mut **tx)
        .await?;

        sqlx::query(
            "INSERT INTO wc_transactions (wc_id, event_type, amount_minor, ledger_entry_id)
             VALUES ($1, 'deposit', $2, $3)",
        )
        .bind(wc_id.0)
        .bind(amount_minor)
        .bind(ledger_entry_id)
        .execute(&mut **tx)
        .await?;

        Ok(())
    }
}
