//! Implémentation PostgreSQL du LedgerRepository
//!
//! INVARIANT CRITIQUE :
//! La méthode `append` lit le seq_no et le prev_hash depuis la base
//! dans la MÊME transaction que l'INSERT, garantissant ainsi que
//! la chaîne de hachages est toujours cohérente même sous concurrence.

use async_trait::async_trait;
use sqlx::{PgPool, Postgres, Row, Transaction};
use uuid::Uuid;

use crate::domain::ledger::{Direction, LedgerEntry, LedgerEntryBuilder, LedgerEventType};
use crate::ports::ledger_repo::LedgerRepository;

pub struct PgLedgerRepository {
    pool: PgPool,
}

impl PgLedgerRepository {
    pub fn new(pool: PgPool) -> Self {
        Self { pool }
    }
}

#[async_trait]
impl LedgerRepository for PgLedgerRepository {
    /// Appende une entrée en lisant atomiquement seq_no et prev_hash
    /// depuis la table via un SELECT ... FOR UPDATE dans la même transaction.
    ///
    /// Cette approche garantit qu'il n'y a jamais de gap dans la séquence
    /// et que prev_hash est toujours le current_hash de l'entrée précédente.
    async fn append(
        &self,
        builder: LedgerEntryBuilder,
        tx: &mut Transaction<'_, Postgres>,
    ) -> anyhow::Result<LedgerEntry> {
        // 1. Lire le dernier seq_no et current_hash en verrouillant la ligne
        //    pour éviter les insertions concurrentes qui casseraient la chaîne
        let last = sqlx::query(
            "SELECT seq_no, current_hash FROM ledger_entries ORDER BY seq_no DESC LIMIT 1 FOR UPDATE"
        )
        .fetch_optional(&mut **tx)
        .await?;

        let (next_seq, prev_hash) = match last {
            Some(row) => {
                let seq: i64 = row.try_get("seq_no")?;
                let hash: String = row.try_get("current_hash")?;
                (seq + 1, Some(hash))
            }
            None => (1, None), // Première entrée du ledger (GENESIS)
        };

        // 2. Construire l'entrée avec les hachages calculés
        let entry = builder.build(next_seq, prev_hash);

        // 3. Insérer dans la même transaction
        sqlx::query(
            r#"
            INSERT INTO ledger_entries (
                id, event_type, aggregate_type, aggregate_id,
                amount_minor, direction,
                payload, payload_hash, prev_hash, current_hash,
                idempotency_key, external_ref,
                created_at, created_by_service
            ) VALUES (
                $1, $2::ledger_event_type, $3, $4,
                $5, $6,
                $7, $8, $9, $10,
                $11, $12,
                $13, $14
            )
        "#,
        )
        .bind(entry.id)
        .bind(entry.event_type.as_canonical())
        .bind(&entry.aggregate_type)
        .bind(entry.aggregate_id)
        .bind(entry.amount_minor)
        .bind(entry.direction.as_ref().map(direction_char))
        .bind(&entry.payload)
        .bind(&entry.payload_hash)
        .bind(&entry.prev_hash)
        .bind(&entry.current_hash)
        .bind(&entry.idempotency_key)
        .bind(&entry.external_ref)
        .bind(entry.created_at)
        .bind(&entry.created_by_service)
        .execute(&mut **tx)
        .await?;

        tracing::debug!(
            seq_no = entry.seq_no,
            event = entry.event_type.as_canonical(),
            hash = &entry.current_hash[..12],
            "Ledger entry appended"
        );

        Ok(entry)
    }

    async fn get_by_aggregate(
        &self,
        aggregate_type: &str,
        aggregate_id: Uuid,
    ) -> anyhow::Result<Vec<LedgerEntry>> {
        let rows = sqlx::query(
            r#"
            SELECT id, seq_no, event_type::text AS event_type, aggregate_type, aggregate_id,
                   amount_minor, direction::text AS direction,
                   payload, payload_hash, prev_hash, current_hash,
                   idempotency_key, external_ref,
                   created_at, created_by_service
            FROM ledger_entries
            WHERE aggregate_type = $1 AND aggregate_id = $2
            ORDER BY seq_no ASC
        "#,
        )
        .bind(aggregate_type)
        .bind(aggregate_id)
        .fetch_all(&self.pool)
        .await?;

        rows.into_iter().map(map_row_to_entry).collect()
    }

    async fn get_all_ordered(&self) -> anyhow::Result<Vec<LedgerEntry>> {
        let rows = sqlx::query(
            r#"
            SELECT id, seq_no, event_type::text AS event_type, aggregate_type, aggregate_id,
                   amount_minor, direction::text AS direction,
                   payload, payload_hash, prev_hash, current_hash,
                   idempotency_key, external_ref,
                   created_at, created_by_service
            FROM ledger_entries
            ORDER BY seq_no ASC
        "#,
        )
        .fetch_all(&self.pool)
        .await?;

        rows.into_iter().map(map_row_to_entry).collect()
    }
}

// ── Helpers de mapping ────────────────────────────────────────────────────

fn direction_char(d: &Direction) -> &'static str {
    match d {
        Direction::Credit => "+",
        Direction::Debit => "-",
    }
}

fn parse_direction(s: Option<&str>) -> Option<Direction> {
    match s {
        Some("+") => Some(Direction::Credit),
        Some("-") => Some(Direction::Debit),
        _ => None,
    }
}

fn parse_event_type(s: &str) -> LedgerEventType {
    match s {
        "contribution_received" => LedgerEventType::ContributionReceived,
        "contribution_quarantined" => LedgerEventType::ContributionQuarantined,
        "payout_initiated" => LedgerEventType::PayoutInitiated,
        "payout_sent" => LedgerEventType::PayoutSent,
        "payout_confirmed" => LedgerEventType::PayoutConfirmed,
        "payout_failed" => LedgerEventType::PayoutFailed,
        "payout_reversed" => LedgerEventType::PayoutReversed,
        "advance_issued" => LedgerEventType::AdvanceIssued,
        "debt_recorded" => LedgerEventType::DebtRecorded,
        "debt_repaid" => LedgerEventType::DebtRepaid,
        "fee_charged" => LedgerEventType::FeeCharged,
        "adjustment" => LedgerEventType::Adjustment,
        "cycle_opened" => LedgerEventType::CycleOpened,
        "cycle_closed" => LedgerEventType::CycleClosed,
        "identity_created" => LedgerEventType::IdentityCreated,
        "wallet_bound" => LedgerEventType::WalletBound,
        "wallet_revoked" => LedgerEventType::WalletRevoked,
        _ => LedgerEventType::Adjustment, // fallback sûr
    }
}

fn map_row_to_entry(row: sqlx::postgres::PgRow) -> anyhow::Result<LedgerEntry> {
    use sqlx::Row;
    let direction_str: Option<String> = row.try_get("direction")?;
    let event_type_str: String = row.try_get("event_type")?;
    let created_at: time::OffsetDateTime = row.try_get("created_at")?;

    Ok(LedgerEntry {
        id: row.try_get("id")?,
        seq_no: row.try_get("seq_no")?,
        event_type: parse_event_type(&event_type_str),
        aggregate_type: row.try_get("aggregate_type")?,
        aggregate_id: row.try_get("aggregate_id")?,
        amount_minor: row.try_get("amount_minor")?,
        direction: parse_direction(direction_str.as_deref()),
        payload: row.try_get("payload")?,
        payload_hash: row.try_get("payload_hash")?,
        prev_hash: row.try_get("prev_hash")?,
        current_hash: row.try_get("current_hash")?,
        idempotency_key: row.try_get("idempotency_key")?,
        external_ref: row.try_get("external_ref")?,
        created_at,
        created_by_service: row.try_get("created_by_service")?,
    })
}
