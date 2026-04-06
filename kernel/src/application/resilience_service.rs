//! Service applicatif : Résilience — Fonds de Roulement & Recouvrement
//!
//! Ce service orchestre les cas d'usage liés à la résilience financière :
//!   - Évaluation pro-rata quand le seuil n'est pas atteint à l'échéance
//!   - Avancement depuis le fonds de roulement
//!   - Création et suivi des créances membres
//!   - Remboursement automatique lors des contributions tardives
//!
//! RÈGLE FONDAMENTALE : comme PayoutService, ce service ne peut être invoqué
//! que depuis le scheduler interne, jamais depuis un endpoint HTTP public.

use std::sync::Arc;

use tracing::info;
use uuid::Uuid;

use crate::domain::cycle::{Cycle, CycleState, PayoutState};
use crate::domain::debt::{Debt, DebtReason};
use crate::domain::events::DomainEvent;
use crate::domain::identity::IdentityId;
use crate::domain::ledger::{Direction, LedgerEntryBuilder, LedgerEventType};
use crate::domain::working_capital::{AdvanceDecision, ProRataDecision, ResiliencePolicy, WorkingCapital};
use crate::ports::cycle_repo::CycleRepository;
use crate::ports::debt_repo::DebtRepository;
use crate::ports::ledger_repo::LedgerRepository;
use crate::ports::outbox_repo::OutboxRepository;
use crate::ports::working_capital_repo::WorkingCapitalRepository;

pub struct ResilienceService {
    pub cycle_repo: Arc<dyn CycleRepository>,
    pub ledger_repo: Arc<dyn LedgerRepository>,
    pub outbox_repo: Arc<dyn OutboxRepository>,
    pub wc_repo: Arc<dyn WorkingCapitalRepository>,
    pub debt_repo: Arc<dyn DebtRepository>,
    pub db: sqlx::PgPool,
    pub service_name: String,
}

/// Résultat de l'évaluation de résilience pour un cycle dépassé.
#[derive(Debug, Clone)]
pub enum ResilienceOutcome {
    /// Le seuil était déjà atteint — rien à faire ici
    ThresholdAlreadyMet,
    /// Avancement total depuis le fonds de roulement — payout initié
    FullAdvanceUsed { advance_minor: i64 },
    /// Versement pro-rata effectué avec les fonds collectés
    ProRataDispatched { amount_minor: i64, fraction_pct: u8 },
    /// Avancement partiel + pro-rata du reste
    HybridDispatched { advance_minor: i64, amount_minor: i64 },
    /// Impossible d'agir — fonds insuffisants et pro-rata non autorisé
    Blocked { reason: &'static str },
}

impl ResilienceService {
    /// Point d'entrée principal : évalue et applique la politique de résilience
    /// pour un cycle dont l'échéance est dépassée mais le seuil non atteint.
    pub async fn evaluate_resilience(
        &self,
        cycle_id: Uuid,
        policy: ResiliencePolicy,
    ) -> anyhow::Result<ResilienceOutcome> {
        let cycle = self
            .cycle_repo
            .get_by_id(cycle_id)
            .await?
            .ok_or_else(|| anyhow::anyhow!("Cycle {} not found", cycle_id))?;

        // Seuls les cycles COMMITTED et dont l'échéance est passée sont éligibles
        if cycle.state != CycleState::Committed {
            return Ok(ResilienceOutcome::Blocked {
                reason: "cycle_not_committed",
            });
        }
        if cycle.payout_state != PayoutState::NotSent {
            return Ok(ResilienceOutcome::Blocked {
                reason: "payout_already_initiated",
            });
        }

        let collected = cycle.collected_amount_minor;
        let threshold = cycle.payout_threshold_minor;

        if collected >= threshold {
            return Ok(ResilienceOutcome::ThresholdAlreadyMet);
        }

        // Charger le fonds de roulement du groupe
        let wc = self
            .wc_repo
            .get_by_group_id(cycle.group_id)
            .await?
            .unwrap_or_else(|| WorkingCapital::new(cycle.group_id));

        match policy {
            ResiliencePolicy::WaitForThreshold => Ok(ResilienceOutcome::Blocked {
                reason: "policy_wait_for_threshold",
            }),

            ResiliencePolicy::UseWorkingCapital => {
                self.try_full_advance(cycle, wc, collected, threshold).await
            }

            ResiliencePolicy::ProRata => {
                self.apply_pro_rata(cycle, collected, threshold).await
            }

            ResiliencePolicy::WorkingCapitalThenProRata => {
                let advance_decision = wc.compute_advance(collected, threshold);
                match advance_decision {
                    AdvanceDecision::NotNeeded => Ok(ResilienceOutcome::ThresholdAlreadyMet),
                    AdvanceDecision::Advance { covers_fully: true, .. } => {
                        self.try_full_advance(cycle, wc, collected, threshold).await
                    }
                    AdvanceDecision::Advance { advance_minor, .. } => {
                        self.apply_hybrid(cycle, wc, collected, threshold, advance_minor).await
                    }
                    AdvanceDecision::InsufficientFunds { .. } => {
                        self.apply_pro_rata(cycle, collected, threshold).await
                    }
                }
            }
        }
    }

    /// Avancement total depuis le fonds de roulement.
    /// Le payout est déclenché pour le montant complet (threshold).
    async fn try_full_advance(
        &self,
        cycle: Cycle,
        mut wc: WorkingCapital,
        collected: i64,
        threshold: i64,
    ) -> anyhow::Result<ResilienceOutcome> {
        let _gap = threshold - collected;

        match wc.compute_advance(collected, threshold) {
            AdvanceDecision::Advance { advance_minor, covers_fully: true, .. } => {
                wc.reserve_advance(advance_minor)?;

                let mut tx = self.db.begin().await?;

                // 1. Enregistrer l'avance dans le ledger
                let advance_payload = serde_json::json!({
                    "cycle_id": cycle.id,
                    "group_id": cycle.group_id,
                    "beneficiary_id": cycle.beneficiary_id,
                    "advance_minor": advance_minor,
                    "collected_minor": collected,
                    "threshold_minor": threshold,
                });
                let idem_key = format!("advance:{}:{}", cycle.id, advance_minor);
                let advance_entry = self
                    .ledger_repo
                    .append(
                        LedgerEntryBuilder::new(
                            LedgerEventType::AdvanceIssued,
                            "cycle",
                            cycle.id,
                            advance_payload,
                            &self.service_name,
                        )
                        .amount(advance_minor, Direction::Debit)
                        .idempotency(idem_key.clone()),
                        &mut tx,
                    )
                    .await?;

                // 2. Créer les créances pour chaque membre en retard
                // (implémentation simplifiée — la liste des membres en retard
                //  est calculée depuis les contributions manquantes)
                self.create_debt_records_in_tx(
                    &cycle,
                    advance_minor,
                    DebtReason::WorkingCapitalAdvance,
                    advance_entry.id,
                    &mut tx,
                )
                .await?;

                // 3. Mettre à jour le solde du fonds de roulement
                self.wc_repo
                    .confirm_advance_in_tx(wc.id, advance_minor, advance_entry.id, &mut tx)
                    .await?;

                tx.commit().await?;

                info!(
                    cycle_id = %cycle.id,
                    advance_minor,
                    "Working capital advance issued — payout will follow from PayoutService"
                );

                Ok(ResilienceOutcome::FullAdvanceUsed { advance_minor })
            }
            _ => Ok(ResilienceOutcome::Blocked {
                reason: "insufficient_working_capital_for_full_advance",
            }),
        }
    }

    /// Versement pro-rata : distribue exactement ce qui est collecté.
    async fn apply_pro_rata(
        &self,
        cycle: Cycle,
        collected: i64,
        threshold: i64,
    ) -> anyhow::Result<ResilienceOutcome> {
        match ProRataDecision::compute(collected, threshold) {
            None => Ok(ResilienceOutcome::Blocked {
                reason: "nothing_collected_for_pro_rata",
            }),
            Some(decision) => {
                let mut tx = self.db.begin().await?;

                let payload = serde_json::json!({
                    "cycle_id": cycle.id,
                    "distributable_minor": decision.distributable_minor,
                    "fraction_pct": decision.fraction_pct,
                    "shortfall_minor": decision.shortfall_minor,
                    "threshold_minor": threshold,
                });
                let idem_key = format!("prorata:{}:{}", cycle.id, decision.distributable_minor);

                let _entry = self
                    .ledger_repo
                    .append(
                        LedgerEntryBuilder::new(
                            LedgerEventType::Adjustment,
                            "cycle",
                            cycle.id,
                            payload.clone(),
                            &self.service_name,
                        )
                        .amount(decision.distributable_minor, Direction::Debit)
                        .idempotency(idem_key.clone()),
                        &mut tx,
                    )
                    .await?;

                // Notifier via outbox que le cycle est en versement pro-rata
                // Le PayoutService prendra le relais avec le montant réduit
                self.outbox_repo
                    .insert_in_tx(
                        &DomainEvent::ProRataDispatched {
                            cycle_id: cycle.id,
                            distributable_minor: decision.distributable_minor,
                            fraction_pct: decision.fraction_pct,
                        },
                        idem_key,
                        None,
                        &mut tx,
                    )
                    .await?;

                tx.commit().await?;

                Ok(ResilienceOutcome::ProRataDispatched {
                    amount_minor: decision.distributable_minor,
                    fraction_pct: decision.fraction_pct,
                })
            }
        }
    }

    /// Hybride : avance partielle + pro-rata du reste.
    async fn apply_hybrid(
        &self,
        cycle: Cycle,
        wc: WorkingCapital,
        collected: i64,
        threshold: i64,
        advance_minor: i64,
    ) -> anyhow::Result<ResilienceOutcome> {
        let total_distributable = collected + advance_minor;

        let mut tx = self.db.begin().await?;

        let payload = serde_json::json!({
            "cycle_id": cycle.id,
            "collected_minor": collected,
            "advance_minor": advance_minor,
            "total_distributable_minor": total_distributable,
            "threshold_minor": threshold,
        });
        let idem_key = format!("hybrid:{}:{}", cycle.id, total_distributable);

        let _entry = self
            .ledger_repo
            .append(
                LedgerEntryBuilder::new(
                    LedgerEventType::AdvanceIssued,
                    "cycle",
                    cycle.id,
                    payload,
                    &self.service_name,
                )
                .amount(advance_minor, Direction::Debit)
                .idempotency(idem_key.clone()),
                &mut tx,
            )
            .await?;

        self.wc_repo
            .confirm_advance_in_tx(wc.id, advance_minor, Uuid::new_v4(), &mut tx)
            .await?;

        tx.commit().await?;

        Ok(ResilienceOutcome::HybridDispatched {
            advance_minor,
            amount_minor: total_distributable,
        })
    }

    /// Remboursement automatique d'une créance quand une contribution tardive arrive.
    /// Appelé par ContributionService après réconciliation d'une contribution.
    pub async fn process_late_repayment(
        &self,
        debtor_id: IdentityId,
        cycle_id: Uuid,
        amount_minor: i64,
    ) -> anyhow::Result<()> {
        let debts = self
            .debt_repo
            .get_active_debts_for_member(debtor_id, cycle_id)
            .await?;

        if debts.is_empty() {
            return Ok(()); // Aucune créance active — paiement normal
        }

        let mut remaining = amount_minor;
        let mut tx = self.db.begin().await?;

        for mut debt in debts {
            if remaining == 0 {
                break;
            }
            let repaid = debt.apply_repayment(remaining);
            remaining -= repaid;

            let payload = serde_json::json!({
                "debt_id": debt.id,
                "debtor_id": debtor_id,
                "cycle_id": cycle_id,
                "repaid_minor": repaid,
                "remaining_minor": debt.remaining_amount_minor,
            });
            let idem_key = format!("repay:{:?}:{}", debt.id, repaid);

            let entry = self
                .ledger_repo
                .append(
                    LedgerEntryBuilder::new(
                        LedgerEventType::DebtRepaid,
                        "cycle",
                        cycle_id,
                        payload,
                        &self.service_name,
                    )
                    .amount(repaid, Direction::Credit)
                    .idempotency(idem_key),
                    &mut tx,
                )
                .await?;

            // Recréditer le fonds de roulement
            if let Some(wc) = self.wc_repo.get_by_group_id(debt.group_id).await? {
                self.wc_repo
                    .receive_repayment_in_tx(wc.id, repaid, entry.id, &mut tx)
                    .await?;
            }

            self.debt_repo.update_debt_in_tx(&debt, &mut tx).await?;
        }

        tx.commit().await?;
        info!(debtor_id = %debtor_id.0, amount_minor, "Late repayment processed");
        Ok(())
    }

    async fn create_debt_records_in_tx(
        &self,
        cycle: &Cycle,
        total_advance_minor: i64,
        reason: DebtReason,
        ledger_entry_id: Uuid,
        tx: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    ) -> anyhow::Result<()> {
        // Dans une implémentation complète, on charge la liste des membres
        // qui n'ont pas payé depuis les contributions et on crée une dette par membre.
        // Pour l'instant, on crée une dette globale sur le bénéficiaire du cycle suivant.
        // TODO: distribuer équitablement entre les membres en retard
        let debt = Debt::new(
            cycle.group_id,
            cycle.id,
            cycle.beneficiary_id,
            total_advance_minor,
            reason,
        );

        self.debt_repo
            .insert_debt_in_tx(&debt, ledger_entry_id, tx)
            .await?;

        let payload = serde_json::json!({
            "debt_id": debt.id.0,
            "debtor_id": debt.debtor_id,
            "cycle_id": cycle.id,
            "amount_minor": total_advance_minor,
            "reason": "working_capital_advance",
        });
        let idem_key = format!("debt:{:?}", debt.id);

        self.ledger_repo
            .append(
                LedgerEntryBuilder::new(
                    LedgerEventType::DebtRecorded,
                    "cycle",
                    cycle.id,
                    payload,
                    &self.service_name,
                )
                .amount(total_advance_minor, Direction::Debit)
                .idempotency(idem_key),
                tx,
            )
            .await?;

        Ok(())
    }
}
