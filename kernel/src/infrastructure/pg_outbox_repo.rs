//! Implémentation PostgreSQL de l'OutboxRepository
//!
//! GARANTIE FONDAMENTALE :
//! `insert_in_tx` écrit dans la MÊME transaction que le ledger.
//! Si le COMMIT échoue, ni le ledger ni l'outbox ne sont modifiés.
//! Si le COMMIT réussit, le worker délivrera l'événement (at-least-once).

use async_trait::async_trait;
use sqlx::{PgPool, Postgres, Row, Transaction};
use uuid::Uuid;

use crate::domain::events::DomainEvent;
use crate::ports::outbox_repo::{OutboxRepository, OutboxRow};

pub struct PgOutboxRepository {
    pool: PgPool,
}

impl PgOutboxRepository {
    pub fn new(pool: PgPool) -> Self {
        Self { pool }
    }
}

#[async_trait]
impl OutboxRepository for PgOutboxRepository {
    /// Insère l'événement dans l'outbox dans la même transaction que le ledger.
    /// La contrainte UNIQUE sur idempotency_key empêche les doublons même
    /// en cas de retry de la transaction.
    async fn insert_in_tx(
        &self,
        event: &DomainEvent,
        idempotency_key: String,
        ledger_entry_id: Option<Uuid>,
        tx: &mut Transaction<'_, Postgres>,
    ) -> anyhow::Result<Uuid> {
        let id = Uuid::new_v4();
        let payload = serde_json::to_value(event)?;

        sqlx::query(
            r#"
            INSERT INTO outbox_events (
                id, event_type, aggregate_id, payload,
                target_service, idempotency_key,
                status, attempts, next_retry_at,
                ledger_entry_id
            ) VALUES (
                $1, $2, $3, $4,
                $5, $6,
                'pending', 0, NOW(),
                $7
            )
            ON CONFLICT (idempotency_key) DO NOTHING
        "#,
        )
        .bind(id)
        .bind(event.event_type_str())
        .bind(aggregate_id_from_event(event))
        .bind(&payload)
        .bind(event.target_service())
        .bind(&idempotency_key)
        .bind(ledger_entry_id)
        .execute(&mut **tx)
        .await?;

        tracing::debug!(
            outbox_id       = %id,
            event_type      = event.event_type_str(),
            idempotency_key = %idempotency_key,
            "Outbox event inserted in transaction"
        );

        Ok(id)
    }

    /// Retourne les événements prêts pour livraison.
    /// `FOR UPDATE SKIP LOCKED` permet plusieurs workers concurrents sans deadlock.
    async fn get_pending(&self, limit: i32) -> anyhow::Result<Vec<OutboxRow>> {
        let rows = sqlx::query(
            r#"
            SELECT id, event_type, aggregate_id, payload,
                   target_service, idempotency_key, attempts
            FROM outbox_events
            WHERE status IN ('pending','failed')
              AND next_retry_at <= NOW()
            ORDER BY next_retry_at ASC
            LIMIT $1
            FOR UPDATE SKIP LOCKED
        "#,
        )
        .bind(limit)
        .fetch_all(&self.pool)
        .await?;

        rows.into_iter()
            .map(|r| {
                Ok(OutboxRow {
                    id: r.try_get("id")?,
                    event_type: r.try_get("event_type")?,
                    aggregate_id: r.try_get("aggregate_id")?,
                    payload: r.try_get("payload")?,
                    target_service: r.try_get("target_service")?,
                    idempotency_key: r.try_get("idempotency_key")?,
                    attempts: r.try_get("attempts")?,
                })
            })
            .collect()
    }

    async fn mark_delivered(&self, id: Uuid, external_ref: Option<String>) -> anyhow::Result<()> {
        sqlx::query(
            r#"
            UPDATE outbox_events
            SET status       = 'delivered',
                delivered_at = NOW(),
                error_detail = NULL
            WHERE id = $1
        "#,
        )
        .bind(id)
        .execute(&self.pool)
        .await?;
        tracing::info!(outbox_id = %id, external_ref = ?external_ref, "Outbox event delivered");
        Ok(())
    }

    async fn schedule_retry(&self, id: Uuid, attempts: i16, error: String) -> anyhow::Result<()> {
        let delay_secs: i64 = {
            let base: i64 = 30;
            let exp = base.saturating_mul(1_i64 << attempts.min(10));
            exp.min(86_400) // plafonné à 24h
        };
        sqlx::query(
            r#"
            UPDATE outbox_events
            SET status        = 'failed',
                attempts      = $1,
                next_retry_at = NOW() + ($2 * interval '1 second'),
                error_detail  = $3
            WHERE id = $4
        "#,
        )
        .bind(attempts)
        .bind(delay_secs)
        .bind(&error)
        .bind(id)
        .execute(&self.pool)
        .await?;
        tracing::warn!(outbox_id = %id, attempts, delay_secs, error = %error, "Outbox retry scheduled");
        Ok(())
    }

    async fn mark_dead_letter(&self, id: Uuid, final_error: String) -> anyhow::Result<()> {
        sqlx::query(
            r#"
            UPDATE outbox_events
            SET status = 'dead_letter', error_detail = $1
            WHERE id = $2
        "#,
        )
        .bind(&final_error)
        .bind(id)
        .execute(&self.pool)
        .await?;
        tracing::error!(outbox_id = %id, error = %final_error, "Outbox event moved to dead_letter");
        Ok(())
    }
}

/// Extrait l'aggregate_id de l'événement pour l'outbox
fn aggregate_id_from_event(event: &DomainEvent) -> Uuid {
    match event {
        DomainEvent::ContributionReceived { cycle_id, .. } => *cycle_id,
        DomainEvent::ContributionQuarantined { cycle_id, .. } => *cycle_id,
        DomainEvent::PayoutTriggered { command, .. } => command.cycle_id,
        DomainEvent::PayoutConfirmed { cycle_id, .. } => *cycle_id,
        DomainEvent::PayoutFailed { cycle_id, .. } => *cycle_id,
        DomainEvent::ProRataDispatched { cycle_id, .. } => *cycle_id,
        DomainEvent::WalletBound { identity_id, .. } => identity_id.inner(),
    }
}
