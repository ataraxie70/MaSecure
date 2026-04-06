//! Implémentation PostgreSQL du CycleRepository
use async_trait::async_trait;
use sqlx::{PgPool, Postgres, Row, Transaction};
use uuid::Uuid;

use crate::domain::cycle::{Cycle, CycleState, PayoutState};
use crate::ports::cycle_repo::{CycleRepository, EligibleCycleRow, OverdueCycleRow};

pub struct PgCycleRepository {
    pool: PgPool,
}

impl PgCycleRepository {
    pub fn new(pool: PgPool) -> Self {
        Self { pool }
    }
}

#[async_trait]
impl CycleRepository for PgCycleRepository {
    /// Lit depuis la vue `payout_eligible_cycles` — ne retourne que les
    /// cycles qui satisfont TOUS les critères de l'automate d'état en base.
    /// Le kernel revalide ensuite en mémoire (défense en profondeur).
    async fn get_payout_eligible(&self) -> anyhow::Result<Vec<EligibleCycleRow>> {
        let rows = sqlx::query(
            r#"
            SELECT
                id             AS cycle_id,
                group_id,
                beneficiary_id,
                collected_amount_minor,
                beneficiary_msisdn,
                beneficiary_provider::text AS beneficiary_provider,
                payout_idempotency_key
            FROM payout_eligible_cycles
        "#,
        )
        .fetch_all(&self.pool)
        .await?;

        let mut result = Vec::with_capacity(rows.len());
        for row in rows {
            result.push(EligibleCycleRow {
                cycle_id: row.try_get("cycle_id")?,
                group_id: row.try_get("group_id")?,
                beneficiary_id: row.try_get("beneficiary_id")?,
                collected_amount_minor: row.try_get("collected_amount_minor")?,
                beneficiary_msisdn: row.try_get("beneficiary_msisdn")?,
                beneficiary_provider: row.try_get("beneficiary_provider")?,
                payout_idempotency_key: row.try_get("payout_idempotency_key")?,
            });
        }
        Ok(result)
    }

    /// Met à jour payout_state et state dans la même transaction que le ledger.
    /// Le trigger `enforce_cycle_immutability` en base bloque toute
    /// transition illégale même si le code applicatif fait une erreur.
    async fn update_payout_state(
        &self,
        cycle_id: Uuid,
        payout_state: PayoutState,
        idempotency_key: Option<String>,
        tx: &mut Transaction<'_, Postgres>,
    ) -> anyhow::Result<()> {
        let new_cycle_state = match payout_state {
            PayoutState::Pending => "payout_triggered",
            PayoutState::Confirmed => "closed",
            PayoutState::Failed => "disputed",
            PayoutState::Sent => "payout_triggered",
            PayoutState::NotSent => "committed",
        };
        let new_payout_state = payout_state_str(&payout_state);

        sqlx::query(
            r#"
            UPDATE cycles
            SET payout_state          = $1::payout_state,
                state                 = $2::cycle_state,
                payout_idempotency_key = COALESCE($3, payout_idempotency_key),
                closed_at             = CASE WHEN $2 = 'closed' THEN NOW() ELSE closed_at END
            WHERE id = $4
        "#,
        )
        .bind(new_payout_state)
        .bind(new_cycle_state)
        .bind(&idempotency_key)
        .bind(cycle_id)
        .execute(&mut **tx)
        .await?;

        tracing::info!(
            cycle_id    = %cycle_id,
            payout_state = new_payout_state,
            cycle_state  = new_cycle_state,
            "Cycle state updated"
        );
        Ok(())
    }

    async fn get_by_id(&self, cycle_id: Uuid) -> anyhow::Result<Option<Cycle>> {
        let row = sqlx::query(
            r#"
            SELECT id, group_id, config_id, cycle_number, beneficiary_id,
                   due_date, payout_threshold_minor, collected_amount_minor,
                   state::text AS state,
                   payout_state::text AS payout_state,
                   payout_idempotency_key, opened_at, closed_at
            FROM cycles
            WHERE id = $1
        "#,
        )
        .bind(cycle_id)
        .fetch_optional(&self.pool)
        .await?;

        match row {
            None => Ok(None),
            Some(r) => Ok(Some(map_row_to_cycle(r)?)),
        }
    }

    /// Charge les cycles COMMITTED dont l'échéance est dépassée et le seuil non atteint.
    /// Utilisé par le ResilienceScheduler (Phase 4).
    async fn get_overdue_below_threshold(&self) -> anyhow::Result<Vec<OverdueCycleRow>> {
        let rows = sqlx::query(
            r#"
            SELECT
                id              AS cycle_id,
                group_id,
                resilience_policy,
                collected_amount_minor,
                payout_threshold_minor
            FROM cycles
            WHERE state = 'committed'
              AND payout_state = 'not_sent'
              AND due_date < NOW()
              AND collected_amount_minor < payout_threshold_minor
            ORDER BY due_date ASC
            "#,
        )
        .fetch_all(&self.pool)
        .await?;

        let mut result = Vec::with_capacity(rows.len());
        for row in rows {
            result.push(OverdueCycleRow {
                cycle_id: row.try_get("cycle_id")?,
                group_id: row.try_get("group_id")?,
                resilience_policy: row.try_get("resilience_policy")?,
                collected_amount_minor: row.try_get("collected_amount_minor")?,
                payout_threshold_minor: row.try_get("payout_threshold_minor")?,
            });
        }
        Ok(result)
    }
}

fn payout_state_str(s: &PayoutState) -> &'static str {
    match s {
        PayoutState::NotSent => "not_sent",
        PayoutState::Pending => "pending",
        PayoutState::Sent => "sent",
        PayoutState::Confirmed => "confirmed",
        PayoutState::Failed => "failed",
    }
}

fn parse_cycle_state(s: &str) -> CycleState {
    match s {
        "committed" => CycleState::Committed,
        "payout_triggered" => CycleState::PayoutTriggered,
        "closed" => CycleState::Closed,
        "disputed" => CycleState::Disputed,
        _ => CycleState::Open,
    }
}

fn parse_payout_state(s: &str) -> PayoutState {
    match s {
        "pending" => PayoutState::Pending,
        "sent" => PayoutState::Sent,
        "confirmed" => PayoutState::Confirmed,
        "failed" => PayoutState::Failed,
        _ => PayoutState::NotSent,
    }
}

fn map_row_to_cycle(row: sqlx::postgres::PgRow) -> anyhow::Result<Cycle> {
    use crate::domain::identity::IdentityId;
    use sqlx::Row;

    let state_str: String = row.try_get("state")?;
    let payout_str: String = row.try_get("payout_state")?;
    let beneficiary_uuid: Uuid = row.try_get("beneficiary_id")?;
    let due_date: time::OffsetDateTime = row.try_get("due_date")?;

    Ok(Cycle {
        id: row.try_get("id")?,
        group_id: row.try_get("group_id")?,
        config_id: row.try_get("config_id")?,
        cycle_number: row.try_get("cycle_number")?,
        beneficiary_id: IdentityId(beneficiary_uuid),
        due_date,
        payout_threshold_minor: row.try_get("payout_threshold_minor")?,
        collected_amount_minor: row.try_get("collected_amount_minor")?,
        state: parse_cycle_state(&state_str),
        payout_state: parse_payout_state(&payout_str),
        payout_idempotency_key: row.try_get("payout_idempotency_key")?,
    })
}
