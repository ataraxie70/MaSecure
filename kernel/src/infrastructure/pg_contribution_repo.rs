//! Repository PostgreSQL des contributions.
//!
//! Cette projection matérialise l'état de collecte d'un cycle tout en gardant
//! le ledger comme source d'audit. Toute mise à jour de `cycles.collected_amount_minor`
//! se fait dans la même transaction que l'insertion de la contribution.

use async_trait::async_trait;
use sqlx::{PgPool, Postgres, Row, Transaction};
use uuid::Uuid;

use crate::domain::identity::IdentityId;
use crate::ports::contribution_repo::{
    ContributionCycleContext, ContributionRepository, QuarantinedContributionRecord,
    ReconciledContributionRecord, StoredContribution,
};

pub struct PgContributionRepository {
    pool: PgPool,
}

impl PgContributionRepository {
    pub fn new(pool: PgPool) -> Self {
        Self { pool }
    }
}

#[async_trait]
impl ContributionRepository for PgContributionRepository {
    async fn find_by_provider_tx_ref(
        &self,
        provider_tx_ref: &str,
    ) -> anyhow::Result<Option<StoredContribution>> {
        let row = sqlx::query(
            r#"
            SELECT provider_tx_ref, cycle_id, status::text AS status
            FROM contributions
            WHERE provider_tx_ref = $1
            LIMIT 1
            "#,
        )
        .bind(provider_tx_ref)
        .fetch_optional(&self.pool)
        .await?;

        row.map(|r| {
            Ok(StoredContribution {
                provider_tx_ref: r.try_get("provider_tx_ref")?,
                cycle_id: r.try_get("cycle_id")?,
                status: r.try_get("status")?,
            })
        })
        .transpose()
    }

    async fn get_cycle_context(
        &self,
        cycle_id: Uuid,
    ) -> anyhow::Result<Option<ContributionCycleContext>> {
        let row = sqlx::query(
            r#"
            SELECT beneficiary_id, state::text AS state
            FROM cycles
            WHERE id = $1
            LIMIT 1
            "#,
        )
        .bind(cycle_id)
        .fetch_optional(&self.pool)
        .await?;

        row.map(|r| {
            let beneficiary_id: Uuid = r.try_get("beneficiary_id")?;
            Ok(ContributionCycleContext {
                cycle_id,
                beneficiary_id: IdentityId(beneficiary_id),
                state: r.try_get("state")?,
            })
        })
        .transpose()
    }

    async fn insert_reconciled_in_tx(
        &self,
        record: ReconciledContributionRecord,
        tx: &mut Transaction<'_, Postgres>,
    ) -> anyhow::Result<()> {
        sqlx::query(
            r#"
            INSERT INTO contributions (
                id,
                cycle_id,
                beneficiary_id,
                payer_identity_id,
                payer_msisdn,
                amount_minor,
                status,
                provider_tx_ref,
                ledger_entry_id,
                reconciled_at
            ) VALUES (
                $1,
                $2,
                $3,
                $4,
                $5,
                $6,
                'reconciled',
                $7,
                $8,
                NOW()
            )
            "#,
        )
        .bind(Uuid::new_v4())
        .bind(record.cycle_id)
        .bind(record.beneficiary_id.inner())
        .bind(record.payer_identity_id.inner())
        .bind(record.payer_msisdn)
        .bind(record.amount_minor)
        .bind(record.provider_tx_ref)
        .bind(record.ledger_entry_id)
        .execute(&mut **tx)
        .await?;

        sqlx::query(
            r#"
            UPDATE cycles
            SET collected_amount_minor = collected_amount_minor + $1
            WHERE id = $2
            "#,
        )
        .bind(record.amount_minor)
        .bind(record.cycle_id)
        .execute(&mut **tx)
        .await?;

        Ok(())
    }

    async fn insert_quarantined_in_tx(
        &self,
        record: QuarantinedContributionRecord,
        tx: &mut Transaction<'_, Postgres>,
    ) -> anyhow::Result<()> {
        sqlx::query(
            r#"
            INSERT INTO contributions (
                id,
                cycle_id,
                beneficiary_id,
                payer_msisdn,
                amount_minor,
                status,
                provider_tx_ref,
                ledger_entry_id,
                notes
            ) VALUES (
                $1,
                $2,
                $3,
                $4,
                $5,
                'quarantined',
                $6,
                $7,
                $8
            )
            "#,
        )
        .bind(Uuid::new_v4())
        .bind(record.cycle_id)
        .bind(record.beneficiary_id.inner())
        .bind(record.payer_msisdn)
        .bind(record.amount_minor)
        .bind(record.provider_tx_ref)
        .bind(record.ledger_entry_id)
        .bind(record.notes)
        .execute(&mut **tx)
        .await?;

        Ok(())
    }
}
