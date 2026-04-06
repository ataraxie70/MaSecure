//! Service applicatif : traitement des contributions entrantes.
//!
//! Ce service est invoqué uniquement depuis l'endpoint interne du kernel.
//! Il persiste la contribution, met à jour la projection de collecte du cycle,
//! écrit dans le ledger puis publie l'événement correspondant dans l'outbox.

use std::sync::Arc;

use anyhow::Context;
use tracing::{info, warn};
use uuid::Uuid;

use crate::domain::events::DomainEvent;
use crate::domain::identity::{IdentityId, Msisdn};
use crate::domain::ledger::{Direction, LedgerEntryBuilder, LedgerEventType};
use crate::ports::contribution_repo::{
    ContributionRepository, QuarantinedContributionRecord, ReconciledContributionRecord,
};
use crate::ports::identity_repo::IdentityRepository;
use crate::ports::ledger_repo::LedgerRepository;
use crate::ports::outbox_repo::OutboxRepository;

/// Callback normalisé reçu depuis l'API publique.
#[derive(Debug, Clone)]
pub struct MobileMoneyCallback {
    pub provider_tx_ref: String,
    pub payer_msisdn: String,
    pub amount_minor: i64,
    pub cycle_id: Uuid,
    pub provider: String,
    pub timestamp: time::OffsetDateTime,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum CallbackOutcome {
    Reconciled,
    Quarantined,
    AlreadyProcessed,
    IgnoredCycleState,
}

pub struct ContributionService {
    pub identity_repo: Arc<dyn IdentityRepository>,
    pub contribution_repo: Arc<dyn ContributionRepository>,
    pub ledger_repo: Arc<dyn LedgerRepository>,
    pub outbox_repo: Arc<dyn OutboxRepository>,
    pub db: sqlx::PgPool,
    pub service_name: String,
}

impl ContributionService {
    /// Traite un callback entrant Mobile Money.
    /// L'idempotence est garantie par `contributions.provider_tx_ref`
    /// et par la clé d'idempotence inscrite dans le ledger.
    pub async fn handle_callback(
        &self,
        callback: MobileMoneyCallback,
    ) -> anyhow::Result<CallbackOutcome> {
        if self
            .contribution_repo
            .find_by_provider_tx_ref(&callback.provider_tx_ref)
            .await?
            .is_some()
        {
            info!(
                provider_tx_ref = %callback.provider_tx_ref,
                cycle_id = %callback.cycle_id,
                "Contribution callback already processed"
            );
            return Ok(CallbackOutcome::AlreadyProcessed);
        }

        let msisdn =
            Msisdn::new(&callback.payer_msisdn).context("Invalid payer MSISDN in callback")?;

        let cycle_ctx = self
            .contribution_repo
            .get_cycle_context(callback.cycle_id)
            .await?
            .ok_or_else(|| anyhow::anyhow!("Cycle {} not found", callback.cycle_id))?;
        if !cycle_ctx.accepts_callbacks() {
            warn!(
                provider_tx_ref = %callback.provider_tx_ref,
                cycle_id = %callback.cycle_id,
                cycle_state = %cycle_ctx.state,
                "Contribution callback ignored because cycle does not accept callbacks"
            );
            return Ok(CallbackOutcome::IgnoredCycleState);
        }

        let resolution = self.identity_repo.resolve_msisdn(msisdn.as_str()).await?;
        match resolution {
            Some(identity_id) => {
                self.reconcile_contribution(callback, msisdn, cycle_ctx.beneficiary_id, identity_id)
                    .await
            }
            None => {
                self.quarantine_contribution(callback, msisdn, cycle_ctx.beneficiary_id)
                    .await
            }
        }
    }

    async fn reconcile_contribution(
        &self,
        callback: MobileMoneyCallback,
        msisdn: Msisdn,
        beneficiary_id: IdentityId,
        payer_identity_id: IdentityId,
    ) -> anyhow::Result<CallbackOutcome> {
        let idem_key = format!("contrib:{}", callback.provider_tx_ref);
        let payload = serde_json::json!({
            "cycle_id": callback.cycle_id,
            "beneficiary_id": beneficiary_id,
            "payer_identity": payer_identity_id,
            "payer_msisdn": msisdn.as_str(),
            "amount_minor": callback.amount_minor,
            "provider_tx_ref": callback.provider_tx_ref.clone(),
            "provider": callback.provider.clone(),
            "timestamp": callback.timestamp,
        });

        let mut tx = self.db.begin().await?;
        self.identity_repo
            .ensure_payment_instrument_in_tx(
                msisdn.as_str(),
                &callback.provider,
                Some(payer_identity_id),
                &mut tx,
            )
            .await?;

        let ledger_builder = LedgerEntryBuilder::new(
            LedgerEventType::ContributionReceived,
            "cycle",
            callback.cycle_id,
            payload,
            &self.service_name,
        )
        .amount(callback.amount_minor, Direction::Credit)
        .idempotency(idem_key.clone())
        .external_ref(callback.provider_tx_ref.clone());

        let ledger_entry = match self.ledger_repo.append(ledger_builder, &mut tx).await {
            Ok(entry) => entry,
            Err(err) if is_unique_violation(&err) => {
                info!(
                    provider_tx_ref = %callback.provider_tx_ref,
                    "Duplicate contribution detected while appending ledger"
                );
                return Ok(CallbackOutcome::AlreadyProcessed);
            }
            Err(err) => return Err(err),
        };

        let record = ReconciledContributionRecord {
            cycle_id: callback.cycle_id,
            beneficiary_id,
            payer_identity_id,
            payer_msisdn: msisdn.as_str().to_owned(),
            amount_minor: callback.amount_minor,
            provider_tx_ref: callback.provider_tx_ref.clone(),
            ledger_entry_id: ledger_entry.id,
        };
        if let Err(err) = self
            .contribution_repo
            .insert_reconciled_in_tx(record, &mut tx)
            .await
        {
            if is_unique_violation(&err) {
                info!(
                    provider_tx_ref = %callback.provider_tx_ref,
                    "Duplicate contribution detected while inserting contribution row"
                );
                return Ok(CallbackOutcome::AlreadyProcessed);
            }
            return Err(err);
        }

        let event = DomainEvent::ContributionReceived {
            cycle_id: callback.cycle_id,
            identity_id: Some(payer_identity_id),
            payer_msisdn: msisdn.clone(),
            amount_minor: callback.amount_minor,
            provider_tx_ref: callback.provider_tx_ref.clone(),
            ledger_seq_no: ledger_entry.seq_no,
        };
        self.outbox_repo
            .insert_in_tx(&event, idem_key, Some(ledger_entry.id), &mut tx)
            .await?;

        tx.commit().await?;

        info!(
            cycle_id = %callback.cycle_id,
            identity = %payer_identity_id,
            amount = callback.amount_minor,
            "Contribution reconciled and projected"
        );

        Ok(CallbackOutcome::Reconciled)
    }

    async fn quarantine_contribution(
        &self,
        callback: MobileMoneyCallback,
        msisdn: Msisdn,
        beneficiary_id: IdentityId,
    ) -> anyhow::Result<CallbackOutcome> {
        let idem_key = format!("quarantine:{}", callback.provider_tx_ref);
        let payload = serde_json::json!({
            "cycle_id": callback.cycle_id,
            "beneficiary_id": beneficiary_id,
            "payer_msisdn": msisdn.as_str(),
            "amount_minor": callback.amount_minor,
            "provider_tx_ref": callback.provider_tx_ref.clone(),
            "provider": callback.provider.clone(),
            "reason": "unknown_msisdn",
            "timestamp": callback.timestamp,
        });

        let mut tx = self.db.begin().await?;
        self.identity_repo
            .ensure_payment_instrument_in_tx(msisdn.as_str(), &callback.provider, None, &mut tx)
            .await?;

        let ledger_builder = LedgerEntryBuilder::new(
            LedgerEventType::ContributionQuarantined,
            "cycle",
            callback.cycle_id,
            payload,
            &self.service_name,
        )
        .amount(callback.amount_minor, Direction::Credit)
        .idempotency(idem_key.clone())
        .external_ref(callback.provider_tx_ref.clone());

        let ledger_entry = match self.ledger_repo.append(ledger_builder, &mut tx).await {
            Ok(entry) => entry,
            Err(err) if is_unique_violation(&err) => {
                info!(
                    provider_tx_ref = %callback.provider_tx_ref,
                    "Duplicate quarantined contribution detected while appending ledger"
                );
                return Ok(CallbackOutcome::AlreadyProcessed);
            }
            Err(err) => return Err(err),
        };

        let record = QuarantinedContributionRecord {
            cycle_id: callback.cycle_id,
            beneficiary_id,
            payer_msisdn: msisdn.as_str().to_owned(),
            amount_minor: callback.amount_minor,
            provider_tx_ref: callback.provider_tx_ref.clone(),
            ledger_entry_id: ledger_entry.id,
            notes: Some("unknown_msisdn".into()),
        };
        if let Err(err) = self
            .contribution_repo
            .insert_quarantined_in_tx(record, &mut tx)
            .await
        {
            if is_unique_violation(&err) {
                info!(
                    provider_tx_ref = %callback.provider_tx_ref,
                    "Duplicate quarantined contribution detected while inserting contribution row"
                );
                return Ok(CallbackOutcome::AlreadyProcessed);
            }
            return Err(err);
        }

        let event = DomainEvent::ContributionQuarantined {
            cycle_id: callback.cycle_id,
            payer_msisdn: msisdn.clone(),
            amount_minor: callback.amount_minor,
            provider_tx_ref: callback.provider_tx_ref.clone(),
            reason: "unknown_msisdn".into(),
        };
        self.outbox_repo
            .insert_in_tx(&event, idem_key, Some(ledger_entry.id), &mut tx)
            .await?;

        tx.commit().await?;

        warn!(
            cycle_id = %callback.cycle_id,
            msisdn = %msisdn,
            amount = callback.amount_minor,
            "Contribution quarantined"
        );

        Ok(CallbackOutcome::Quarantined)
    }
}

fn is_unique_violation(err: &anyhow::Error) -> bool {
    err.chain().any(|cause| {
        cause
            .downcast_ref::<sqlx::Error>()
            .and_then(|sqlx_err| match sqlx_err {
                sqlx::Error::Database(db_err) => db_err.code(),
                _ => None,
            })
            .is_some_and(|code| code == "23505")
    })
}
