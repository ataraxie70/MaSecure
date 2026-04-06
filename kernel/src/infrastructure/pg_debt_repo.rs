//! Implémentation PostgreSQL du DebtRepository
//!
//! Les créances membres sont créées atomiquement avec leur entrée ledger.
//! Leur mise à jour (remboursement partiel/total) suit le même pattern transactionnel.

use async_trait::async_trait;
use sqlx::{PgPool, Postgres, Row, Transaction};
use time::OffsetDateTime;
use uuid::Uuid;

use crate::domain::debt::{Debt, DebtId, DebtReason, DebtState};
use crate::domain::identity::IdentityId;
use crate::ports::debt_repo::DebtRepository;

pub struct PgDebtRepository {
    pool: PgPool,
}

impl PgDebtRepository {
    pub fn new(pool: PgPool) -> Self {
        Self { pool }
    }
}

#[async_trait]
impl DebtRepository for PgDebtRepository {
    async fn get_active_debts_for_member(
        &self,
        debtor_id: IdentityId,
        cycle_id: Uuid,
    ) -> anyhow::Result<Vec<Debt>> {
        let rows = sqlx::query(
            "SELECT id, group_id, cycle_id, debtor_id, original_amount_minor,
                    remaining_amount_minor, reason, state, ledger_entry_id,
                    created_at, repaid_at
             FROM member_debts
             WHERE debtor_id = $1
               AND cycle_id  = $2
               AND state IN ('active', 'partially_repaid')
             ORDER BY created_at ASC",
        )
        .bind(debtor_id.0)
        .bind(cycle_id)
        .fetch_all(&self.pool)
        .await?;

        rows.into_iter().map(|r| map_debt_row(&r)).collect()
    }

    async fn get_group_active_debts(&self, group_id: Uuid) -> anyhow::Result<Vec<Debt>> {
        let rows = sqlx::query(
            "SELECT id, group_id, cycle_id, debtor_id, original_amount_minor,
                    remaining_amount_minor, reason, state, ledger_entry_id,
                    created_at, repaid_at
             FROM member_debts
             WHERE group_id = $1
               AND state IN ('active', 'partially_repaid')
             ORDER BY created_at DESC",
        )
        .bind(group_id)
        .fetch_all(&self.pool)
        .await?;

        rows.into_iter().map(|r| map_debt_row(&r)).collect()
    }

    async fn insert_debt_in_tx(
        &self,
        debt: &Debt,
        ledger_entry_id: Uuid,
        tx: &mut Transaction<'_, Postgres>,
    ) -> anyhow::Result<()> {
        sqlx::query(
            "INSERT INTO member_debts (
                id, group_id, cycle_id, debtor_id,
                original_amount_minor, remaining_amount_minor,
                reason, state, ledger_entry_id, created_at
             ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)",
        )
        .bind(debt.id.0)
        .bind(debt.group_id)
        .bind(debt.cycle_id)
        .bind(debt.debtor_id.0)
        .bind(debt.original_amount_minor)
        .bind(debt.remaining_amount_minor)
        .bind(map_debt_reason(&debt.reason))
        .bind(map_debt_state(&debt.state))
        .bind(ledger_entry_id)
        .bind(debt.created_at)
        .execute(&mut **tx)
        .await?;
        Ok(())
    }

    async fn update_debt_in_tx(
        &self,
        debt: &Debt,
        tx: &mut Transaction<'_, Postgres>,
    ) -> anyhow::Result<()> {
        sqlx::query(
            "UPDATE member_debts
             SET remaining_amount_minor = $2,
                 state                  = $3,
                 repaid_at              = $4
             WHERE id = $1",
        )
        .bind(debt.id.0)
        .bind(debt.remaining_amount_minor)
        .bind(map_debt_state(&debt.state))
        .bind(debt.repaid_at)
        .execute(&mut **tx)
        .await?;
        Ok(())
    }

    async fn write_off_debts(
        &self,
        debt_ids: &[Uuid],
        tx: &mut Transaction<'_, Postgres>,
    ) -> anyhow::Result<()> {
        sqlx::query(
            "UPDATE member_debts
             SET remaining_amount_minor = 0,
                 state = 'written_off'
             WHERE id = ANY($1)",
        )
        .bind(debt_ids)
        .execute(&mut **tx)
        .await?;
        Ok(())
    }
}

// ── Mappers ────────────────────────────────────────────────────────────────────

fn map_debt_row(row: &sqlx::postgres::PgRow) -> anyhow::Result<Debt> {
    let id: Uuid = row.try_get("id")?;
    let group_id: Uuid = row.try_get("group_id")?;
    let cycle_id: Uuid = row.try_get("cycle_id")?;
    let debtor_id: Uuid = row.try_get("debtor_id")?;
    let original: i64 = row.try_get("original_amount_minor")?;
    let remaining: i64 = row.try_get("remaining_amount_minor")?;
    let reason_str: String = row.try_get("reason")?;
    let state_str: String = row.try_get("state")?;
    let ledger_id: Option<Uuid> = row.try_get("ledger_entry_id")?;
    let created_at: OffsetDateTime = row.try_get("created_at")?;
    let repaid_at: Option<OffsetDateTime> = row.try_get("repaid_at")?;

    Ok(Debt {
        id: DebtId(id),
        group_id,
        cycle_id,
        debtor_id: IdentityId(debtor_id),
        original_amount_minor: original,
        remaining_amount_minor: remaining,
        reason: parse_debt_reason(&reason_str),
        state: parse_debt_state(&state_str),
        created_at,
        repaid_at,
        ledger_entry_id: ledger_id,
    })
}

fn parse_debt_reason(s: &str) -> DebtReason {
    match s {
        "partial_contribution" => DebtReason::PartialContribution,
        "working_capital_advance" => DebtReason::WorkingCapitalAdvance,
        _ => DebtReason::LateContribution,
    }
}

fn parse_debt_state(s: &str) -> DebtState {
    match s {
        "partially_repaid" => DebtState::PartiallyRepaid,
        "repaid" => DebtState::Repaid,
        "written_off" => DebtState::WrittenOff,
        _ => DebtState::Active,
    }
}

fn map_debt_reason(r: &DebtReason) -> &'static str {
    match r {
        DebtReason::LateContribution => "late_contribution",
        DebtReason::PartialContribution => "partial_contribution",
        DebtReason::WorkingCapitalAdvance => "working_capital_advance",
    }
}

fn map_debt_state(s: &DebtState) -> &'static str {
    match s {
        DebtState::Active => "active",
        DebtState::PartiallyRepaid => "partially_repaid",
        DebtState::Repaid => "repaid",
        DebtState::WrittenOff => "written_off",
    }
}
