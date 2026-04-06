//! Service applicatif : orchestration du payout
//!
//! Ce service est le seul endroit où un PayoutCommand peut être produit.
//! Il est appelé par le scheduler périodique, jamais par un endpoint HTTP direct.

use anyhow::Context;
use std::sync::Arc;
use tracing::{error, info, warn};
use uuid::Uuid;

use crate::domain::cycle::{Cycle, PayoutCommand, PayoutDecision, PayoutState};
use crate::domain::events::DomainEvent;
use crate::domain::identity::{Msisdn, WalletProvider};
use crate::domain::ledger::{Direction, LedgerEntryBuilder, LedgerEventType};
use crate::ports::cycle_repo::{CycleRepository, EligibleCycleRow};
use crate::ports::ledger_repo::LedgerRepository;
use crate::ports::outbox_repo::OutboxRepository;

pub struct PayoutService {
    pub cycle_repo: Arc<dyn CycleRepository>,
    pub ledger_repo: Arc<dyn LedgerRepository>,
    pub outbox_repo: Arc<dyn OutboxRepository>,
    pub db: sqlx::PgPool,
    pub service_name: String,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum PayoutConfirmationOutcome {
    Confirmed,
    AlreadyConfirmed,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum PayoutFailureOutcome {
    Failed,
    AlreadyFailed,
    IgnoredAlreadyConfirmed,
}

impl PayoutService {
    /// Point d'entrée du scheduler : évalue tous les cycles éligibles
    /// et initie les payouts en une seule passe déterministe.
    pub async fn run_payout_evaluation(&self) -> anyhow::Result<()> {
        let eligible = self
            .cycle_repo
            .get_payout_eligible()
            .await
            .context("Failed to fetch eligible cycles")?;

        info!(
            count = eligible.len(),
            "Payout evaluation: {} eligible cycles found",
            eligible.len()
        );

        for row in eligible {
            if let Err(e) = self.process_eligible_cycle(row).await {
                error!("Payout failed for cycle, continuing: {}", e);
            }
        }
        Ok(())
    }

    async fn process_eligible_cycle(&self, row: EligibleCycleRow) -> anyhow::Result<()> {
        let msisdn = Msisdn::new(&row.beneficiary_msisdn)
            .context("Invalid beneficiary MSISDN in eligible view")?;

        let provider = parse_provider(&row.beneficiary_provider);

        // Reconstituer l'agrégat cycle depuis la base
        let cycle = self
            .cycle_repo
            .get_by_id(row.cycle_id)
            .await?
            .ok_or_else(|| anyhow::anyhow!("Cycle {} not found", row.cycle_id))?;

        // === AUTOMATE DÉTERMINISTE ===
        let decision =
            cycle.evaluate_payout_eligibility(time::OffsetDateTime::now_utc(), msisdn, provider);

        match decision {
            PayoutDecision::Eligible { .. } => {
                info!(cycle_id = %row.cycle_id, "Cycle eligible: initiating payout");
                self.initiate_payout(cycle, decision).await?;
            }
            PayoutDecision::NotYet { reason } => {
                warn!(cycle_id = %row.cycle_id, reason, "Cycle in eligible view but NotYet — skipping");
            }
            PayoutDecision::Pending { reason } | PayoutDecision::Blocked { reason } => {
                warn!(cycle_id = %row.cycle_id, reason, "Cycle not eligible at evaluation time");
            }
        }
        Ok(())
    }

    /// Initie le payout de façon atomique :
    /// ledger_entry + outbox_event dans la MÊME transaction PostgreSQL.
    async fn initiate_payout(&self, cycle: Cycle, decision: PayoutDecision) -> anyhow::Result<()> {
        let command = PayoutCommand::new(&cycle, decision.clone())
            .context("Failed to build PayoutCommand")?;

        let idempotency_key = command.idempotency_key.clone();
        let payload =
            serde_json::to_value(&command).context("Failed to serialize PayoutCommand")?;

        // Transaction atomique : ledger + outbox + cycle state
        let mut tx = self
            .db
            .begin()
            .await
            .context("Failed to begin transaction")?;

        // 1. Écrire dans le ledger
        let ledger_builder = LedgerEntryBuilder::new(
            LedgerEventType::PayoutInitiated,
            "cycle",
            cycle.id,
            payload.clone(),
            &self.service_name,
        )
        .amount(command.amount_minor, Direction::Debit)
        .idempotency(idempotency_key.clone());

        let ledger_entry = self
            .ledger_repo
            .append(ledger_builder, &mut tx)
            .await
            .context("Failed to append payout_initiated to ledger")?;

        // 2. Écrire dans l'outbox (même transaction)
        let event = DomainEvent::PayoutTriggered {
            command: command.clone(),
            ledger_seq_no: ledger_entry.seq_no,
        };
        self.outbox_repo
            .insert_in_tx(
                &event,
                idempotency_key.clone(),
                Some(ledger_entry.id),
                &mut tx,
            )
            .await
            .context("Failed to insert payout command into outbox")?;

        // 3. Mettre à jour le cycle (même transaction)
        self.cycle_repo
            .update_payout_state(
                cycle.id,
                crate::domain::cycle::PayoutState::Pending,
                Some(idempotency_key),
                &mut tx,
            )
            .await
            .context("Failed to update cycle payout_state")?;

        // 4. COMMIT — si succès, l'Outbox Worker livrera la commande
        tx.commit()
            .await
            .context("Failed to commit payout transaction")?;

        info!(
            cycle_id = %cycle.id,
            beneficiary = %command.beneficiary_id,
            amount_minor = command.amount_minor,
            "Payout initiated successfully (ledger + outbox committed)"
        );
        Ok(())
    }

    /// Confirme un payout depuis un point d'entrée interne unique.
    /// Cette opération est idempotente : une confirmation rejouée ne produit
    /// jamais un second effet métier.
    pub async fn confirm_payout(
        &self,
        cycle_id: Uuid,
        external_ref: String,
    ) -> anyhow::Result<PayoutConfirmationOutcome> {
        let cycle = self
            .cycle_repo
            .get_by_id(cycle_id)
            .await?
            .ok_or_else(|| anyhow::anyhow!("Cycle {} not found", cycle_id))?;

        if cycle.payout_state == PayoutState::Confirmed {
            info!(cycle_id = %cycle_id, external_ref = %external_ref, "Payout already confirmed");
            return Ok(PayoutConfirmationOutcome::AlreadyConfirmed);
        }

        let mut domain_cycle = cycle.clone();
        domain_cycle
            .mark_payout_confirmed()
            .context("Illegal payout confirmation state transition")?;

        let confirmed_at = time::OffsetDateTime::now_utc();
        let idempotency_key = format!("payout-confirm:{}", external_ref);
        let payload = serde_json::json!({
            "cycle_id": cycle_id,
            "external_ref": external_ref.clone(),
            "confirmed_at": confirmed_at,
        });

        let mut tx = self
            .db
            .begin()
            .await
            .context("Failed to begin confirmation transaction")?;

        let ledger_builder = LedgerEntryBuilder::new(
            LedgerEventType::PayoutConfirmed,
            "cycle",
            cycle_id,
            payload,
            &self.service_name,
        )
        .idempotency(idempotency_key.clone())
        .external_ref(external_ref.clone());

        let ledger_entry = match self.ledger_repo.append(ledger_builder, &mut tx).await {
            Ok(entry) => entry,
            Err(err) if is_unique_violation(&err) => {
                info!(
                    cycle_id = %cycle_id,
                    external_ref = %external_ref,
                    "Duplicate payout confirmation detected"
                );
                return Ok(PayoutConfirmationOutcome::AlreadyConfirmed);
            }
            Err(err) => return Err(err),
        };

        let event = DomainEvent::PayoutConfirmed {
            cycle_id,
            external_ref: external_ref.clone(),
            ledger_seq_no: ledger_entry.seq_no,
        };
        self.outbox_repo
            .insert_in_tx(&event, idempotency_key, Some(ledger_entry.id), &mut tx)
            .await
            .context("Failed to insert payout confirmation event into outbox")?;

        self.cycle_repo
            .update_payout_state(cycle_id, PayoutState::Confirmed, None, &mut tx)
            .await
            .context("Failed to mark cycle as confirmed")?;

        tx.commit()
            .await
            .context("Failed to commit payout confirmation transaction")?;

        info!(
            cycle_id = %cycle_id,
            external_ref = %external_ref,
            "Payout confirmed and cycle closed"
        );
        Ok(PayoutConfirmationOutcome::Confirmed)
    }

    /// Marque un payout comme échoué après retour opérateur.
    /// Cette opération reste idempotente et n'écrase jamais un payout déjà confirmé.
    pub async fn fail_payout(
        &self,
        cycle_id: Uuid,
        external_ref: String,
        reason: String,
    ) -> anyhow::Result<PayoutFailureOutcome> {
        let cycle = self
            .cycle_repo
            .get_by_id(cycle_id)
            .await?
            .ok_or_else(|| anyhow::anyhow!("Cycle {} not found", cycle_id))?;

        if cycle.payout_state == PayoutState::Confirmed {
            warn!(
                cycle_id = %cycle_id,
                external_ref = %external_ref,
                "Ignoring payout failure because payout is already confirmed"
            );
            return Ok(PayoutFailureOutcome::IgnoredAlreadyConfirmed);
        }

        if cycle.payout_state == PayoutState::Failed {
            info!(
                cycle_id = %cycle_id,
                external_ref = %external_ref,
                "Payout already marked as failed"
            );
            return Ok(PayoutFailureOutcome::AlreadyFailed);
        }

        let mut domain_cycle = cycle.clone();
        domain_cycle
            .mark_payout_failed()
            .context("Illegal payout failure state transition")?;

        let failed_at = time::OffsetDateTime::now_utc();
        let idempotency_key = format!("payout-failed:{}", external_ref);
        let payload = serde_json::json!({
            "cycle_id": cycle_id,
            "external_ref": external_ref.clone(),
            "reason": reason.clone(),
            "failed_at": failed_at,
        });

        let mut tx = self
            .db
            .begin()
            .await
            .context("Failed to begin payout failure transaction")?;

        let ledger_builder = LedgerEntryBuilder::new(
            LedgerEventType::PayoutFailed,
            "cycle",
            cycle_id,
            payload,
            &self.service_name,
        )
        .idempotency(idempotency_key.clone())
        .external_ref(external_ref.clone());

        let ledger_entry = match self.ledger_repo.append(ledger_builder, &mut tx).await {
            Ok(entry) => entry,
            Err(err) if is_unique_violation(&err) => {
                info!(
                    cycle_id = %cycle_id,
                    external_ref = %external_ref,
                    "Duplicate payout failure detected"
                );
                return Ok(PayoutFailureOutcome::AlreadyFailed);
            }
            Err(err) => return Err(err),
        };

        let event = DomainEvent::PayoutFailed {
            cycle_id,
            reason: reason.clone(),
            ledger_seq_no: ledger_entry.seq_no,
        };
        self.outbox_repo
            .insert_in_tx(&event, idempotency_key, Some(ledger_entry.id), &mut tx)
            .await
            .context("Failed to insert payout failure event into outbox")?;

        self.cycle_repo
            .update_payout_state(cycle_id, PayoutState::Failed, None, &mut tx)
            .await
            .context("Failed to mark cycle as failed")?;

        tx.commit()
            .await
            .context("Failed to commit payout failure transaction")?;

        warn!(
            cycle_id = %cycle_id,
            external_ref = %external_ref,
            reason,
            "Payout failed and cycle moved to disputed"
        );
        Ok(PayoutFailureOutcome::Failed)
    }
}

fn parse_provider(s: &str) -> WalletProvider {
    match s {
        "orange_money" => WalletProvider::OrangeMoney,
        "moov_money" => WalletProvider::MoovMoney,
        "wave" => WalletProvider::Wave,
        other => WalletProvider::Other(other.into()),
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
