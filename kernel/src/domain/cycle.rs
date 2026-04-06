//! Agrégat Cycle — automate d'état déterministe
//!
//! RÈGLE FONDAMENTALE : le déclenchement d'un payout ne dépend jamais
//! d'une action humaine. Il résulte d'une évaluation pure de l'état interne.

use serde::{Deserialize, Serialize};
use time::{format_description::well_known::Rfc3339, OffsetDateTime};
use uuid::Uuid;

use super::identity::{DomainError, IdentityId, Msisdn, WalletProvider};

pub type AmountMinor = i64; // Montant en unité de base (centimes XOF)
pub type CycleNumber = i32;

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub enum CycleState {
    Open,
    Committed,
    PayoutTriggered,
    Closed,
    Disputed,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub enum PayoutState {
    NotSent,
    Pending,
    Sent,
    Confirmed,
    Failed,
}

/// Cycle financier — représentation en mémoire de l'agrégat
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Cycle {
    pub id: Uuid,
    pub group_id: Uuid,
    pub config_id: Uuid,
    pub cycle_number: CycleNumber,
    pub beneficiary_id: IdentityId,
    pub due_date: OffsetDateTime,
    pub payout_threshold_minor: AmountMinor,
    pub collected_amount_minor: AmountMinor,
    pub state: CycleState,
    pub payout_state: PayoutState,
    pub payout_idempotency_key: Option<String>,
}

/// Décision prise par l'automate d'éligibilité au payout
#[derive(Debug, Clone, Serialize, Deserialize)]
pub enum PayoutDecision {
    /// Le cycle est éligible : tous les invariants sont satisfaits
    Eligible {
        beneficiary_id: IdentityId,
        amount_minor: AmountMinor,
        idempotency_key: String,
        beneficiary_msisdn: Msisdn,
        beneficiary_provider: WalletProvider,
    },
    /// Pas encore : l'échéance n'est pas atteinte
    NotYet { reason: &'static str },
    /// En attente : le seuil de collecte n'est pas atteint
    Pending { reason: &'static str },
    /// Bloqué : un invariant d'état est violé
    Blocked { reason: &'static str },
}

impl Cycle {
    /// =========================================================================
    /// AUTOMATE DÉTERMINISTE D'ÉLIGIBILITÉ
    ///
    /// Cette fonction est la seule source de vérité pour décider si un payout
    /// peut être déclenché. Elle ne produit JAMAIS d'effet de bord.
    /// Elle lit des faits immuables et retourne une décision.
    /// =========================================================================
    pub fn evaluate_payout_eligibility(
        &self,
        now: OffsetDateTime,
        beneficiary_msisdn: Msisdn,
        beneficiary_provider: WalletProvider,
    ) -> PayoutDecision {
        // Invariant 1 : l'échéance doit être passée
        if now < self.due_date {
            return PayoutDecision::NotYet {
                reason: "due_date_not_reached",
            };
        }
        // Invariant 2 : le seuil de collecte doit être atteint
        if self.collected_amount_minor < self.payout_threshold_minor {
            return PayoutDecision::Pending {
                reason: "threshold_not_met",
            };
        }
        // Invariant 3 : le cycle doit être dans l'état COMMITTED
        if self.state != CycleState::Committed {
            return PayoutDecision::Blocked {
                reason: "cycle_not_committed",
            };
        }
        // Invariant 4 : le payout ne doit pas déjà avoir été initié
        if self.payout_state != PayoutState::NotSent {
            return PayoutDecision::Blocked {
                reason: "payout_already_initiated",
            };
        }

        // Tous les invariants satisfaits → payout éligible
        let idempotency_key = format!("payout:{}:{}:{}", self.group_id, self.id, self.cycle_number);

        PayoutDecision::Eligible {
            beneficiary_id: self.beneficiary_id,
            amount_minor: self.collected_amount_minor,
            idempotency_key,
            beneficiary_msisdn,
            beneficiary_provider,
        }
    }

    /// Transition d'état : COMMITTED → PAYOUT_TRIGGERED
    /// Retourne Err si la transition est illégale.
    pub fn mark_payout_triggered(&mut self, idempotency_key: String) -> Result<(), DomainError> {
        if self.state != CycleState::Committed || self.payout_state != PayoutState::NotSent {
            return Err(DomainError::CycleInvariantViolation(format!(
                "Illegal transition to PayoutTriggered from state={:?}/payout={:?}",
                self.state, self.payout_state
            )));
        }
        self.state = CycleState::PayoutTriggered;
        self.payout_state = PayoutState::Pending;
        self.payout_idempotency_key = Some(idempotency_key);
        Ok(())
    }

    /// Transition : confirmation de réception par l'opérateur Mobile Money
    pub fn mark_payout_confirmed(&mut self) -> Result<(), DomainError> {
        if self.payout_state != PayoutState::Pending && self.payout_state != PayoutState::Sent {
            return Err(DomainError::CycleInvariantViolation(
                "Cannot confirm payout: not in pending/sent state".into(),
            ));
        }
        self.payout_state = PayoutState::Confirmed;
        self.state = CycleState::Closed;
        Ok(())
    }

    /// Transition : échec définitif du payout → dispute
    pub fn mark_payout_failed(&mut self) -> Result<(), DomainError> {
        self.payout_state = PayoutState::Failed;
        self.state = CycleState::Disputed;
        Ok(())
    }
}

/// Commande de payout — produite uniquement par le kernel financier
/// après évaluation positive de tous les invariants.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PayoutCommand {
    pub cycle_id: Uuid,
    pub group_id: Uuid,
    pub beneficiary_id: IdentityId,
    pub beneficiary_msisdn: Msisdn,
    pub beneficiary_provider: WalletProvider,
    pub amount_minor: AmountMinor,
    pub idempotency_key: String,
    pub initiated_at: String,
}

impl PayoutCommand {
    pub fn new(cycle: &Cycle, decision: PayoutDecision) -> Result<Self, DomainError> {
        match decision {
            PayoutDecision::Eligible {
                beneficiary_id,
                amount_minor,
                idempotency_key,
                beneficiary_msisdn,
                beneficiary_provider,
            } => {
                let initiated_at = OffsetDateTime::now_utc().format(&Rfc3339).map_err(|err| {
                    DomainError::CycleInvariantViolation(format!(
                        "Cannot format payout timestamp: {}",
                        err
                    ))
                })?;

                Ok(PayoutCommand {
                    cycle_id: cycle.id,
                    group_id: cycle.group_id,
                    beneficiary_id,
                    beneficiary_msisdn,
                    beneficiary_provider,
                    amount_minor,
                    idempotency_key,
                    initiated_at,
                })
            }
            _ => Err(DomainError::CycleInvariantViolation(
                "PayoutCommand can only be built from an Eligible decision".into(),
            )),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::domain::identity::IdentityId;

    fn sample_cycle() -> Cycle {
        Cycle {
            id: Uuid::new_v4(),
            group_id: Uuid::new_v4(),
            config_id: Uuid::new_v4(),
            cycle_number: 1,
            beneficiary_id: IdentityId(Uuid::new_v4()),
            due_date: OffsetDateTime::now_utc() - time::Duration::hours(1),
            payout_threshold_minor: 10_000,
            collected_amount_minor: 10_000,
            state: CycleState::Committed,
            payout_state: PayoutState::NotSent,
            payout_idempotency_key: None,
        }
    }

    #[test]
    fn payout_becomes_eligible_when_all_invariants_are_met() {
        let cycle = sample_cycle();
        let decision = cycle.evaluate_payout_eligibility(
            OffsetDateTime::now_utc(),
            Msisdn::new("+22670000000").unwrap(),
            WalletProvider::OrangeMoney,
        );

        match decision {
            PayoutDecision::Eligible { amount_minor, .. } => {
                assert_eq!(amount_minor, 10_000);
            }
            other => panic!("expected eligible decision, got {:?}", other),
        }
    }

    #[test]
    fn payout_is_blocked_if_cycle_is_not_committed() {
        let mut cycle = sample_cycle();
        cycle.state = CycleState::Open;

        let decision = cycle.evaluate_payout_eligibility(
            OffsetDateTime::now_utc(),
            Msisdn::new("+22670000000").unwrap(),
            WalletProvider::Wave,
        );

        assert!(matches!(
            decision,
            PayoutDecision::Blocked {
                reason: "cycle_not_committed"
            }
        ));
    }

    #[test]
    fn confirmed_payout_closes_the_cycle() {
        let mut cycle = sample_cycle();
        cycle.payout_state = PayoutState::Pending;

        cycle.mark_payout_confirmed().unwrap();

        assert_eq!(cycle.payout_state, PayoutState::Confirmed);
        assert_eq!(cycle.state, CycleState::Closed);
    }

    #[test]
    fn payout_command_serializes_timestamp_as_string() {
        let cycle = sample_cycle();
        let decision = cycle.evaluate_payout_eligibility(
            OffsetDateTime::now_utc(),
            Msisdn::new("+22670000000").unwrap(),
            WalletProvider::OrangeMoney,
        );

        let command = PayoutCommand::new(&cycle, decision).unwrap();
        let payload = serde_json::to_value(&command).unwrap();

        assert!(payload
            .get("initiated_at")
            .and_then(|value| value.as_str())
            .is_some());
    }
}
