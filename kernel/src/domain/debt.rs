//! Domaine : Dettes membres (recouvrement des avances)
//!
//! Une dette est créée automatiquement quand le fonds de roulement avance
//! des fonds pour un cycle dont certains membres n'ont pas payé.
//! Elle est remboursée dès que la contribution tardive est reçue.
//!
//! INVARIANT : Le montant total des dettes actives d'un groupe ne peut jamais
//! dépasser le solde du fonds de roulement (elles sont symétriques).

use serde::{Deserialize, Serialize};
use time::OffsetDateTime;
use uuid::Uuid;

use super::cycle::AmountMinor;
use super::identity::IdentityId;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub struct DebtId(pub Uuid);

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub enum DebtState {
    /// Créance active — le membre doit encore payer
    Active,
    /// Partiellement remboursée
    PartiallyRepaid,
    /// Entièrement remboursée
    Repaid,
    /// Passée en perte (décision de gouvernance après X cycles d'inactivité)
    WrittenOff,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub enum DebtReason {
    /// Cotisation non reçue à l'échéance du cycle
    LateContribution,
    /// Contribution partielle (montant inférieur au montant dû)
    PartialContribution,
    /// Avance du fonds de roulement utilisée pour ce cycle
    WorkingCapitalAdvance,
}

/// Représentation en mémoire d'une créance d'un membre.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Debt {
    pub id: DebtId,
    pub group_id: Uuid,
    /// Cycle qui a généré cette créance
    pub cycle_id: Uuid,
    /// Membre débiteur
    pub debtor_id: IdentityId,
    /// Montant initial de la dette
    pub original_amount_minor: AmountMinor,
    /// Montant restant dû
    pub remaining_amount_minor: AmountMinor,
    pub reason: DebtReason,
    pub state: DebtState,
    pub created_at: OffsetDateTime,
    pub repaid_at: Option<OffsetDateTime>,
    /// Référence ledger de l'entrée debt_recorded
    pub ledger_entry_id: Option<Uuid>,
}

impl Debt {
    /// Crée une nouvelle dette pour un membre qui n'a pas payé.
    pub fn new(
        group_id: Uuid,
        cycle_id: Uuid,
        debtor_id: IdentityId,
        amount_minor: AmountMinor,
        reason: DebtReason,
    ) -> Self {
        Self {
            id: DebtId(Uuid::new_v4()),
            group_id,
            cycle_id,
            debtor_id,
            original_amount_minor: amount_minor,
            remaining_amount_minor: amount_minor,
            reason,
            state: DebtState::Active,
            created_at: OffsetDateTime::now_utc(),
            repaid_at: None,
            ledger_entry_id: None,
        }
    }

    /// Applique un remboursement partiel ou total.
    /// Retourne le montant effectivement remboursé (peut être inférieur si trop-perçu).
    pub fn apply_repayment(&mut self, amount_minor: AmountMinor) -> AmountMinor {
        let effective = amount_minor.min(self.remaining_amount_minor);
        self.remaining_amount_minor -= effective;

        if self.remaining_amount_minor == 0 {
            self.state = DebtState::Repaid;
            self.repaid_at = Some(OffsetDateTime::now_utc());
        } else {
            self.state = DebtState::PartiallyRepaid;
        }

        effective
    }

    /// Passe la dette en perte (write-off) — décision de gouvernance.
    pub fn write_off(&mut self) {
        self.state = DebtState::WrittenOff;
        self.remaining_amount_minor = 0;
    }

    pub fn is_active(&self) -> bool {
        matches!(self.state, DebtState::Active | DebtState::PartiallyRepaid)
    }

    pub fn is_fully_repaid(&self) -> bool {
        self.state == DebtState::Repaid
    }
}

/// Résumé des dettes actives d'un groupe pour affichage dashboard.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct GroupDebtSummary {
    pub group_id: Uuid,
    pub total_debt_minor: AmountMinor,
    pub active_debtors: u32,
    pub debts: Vec<DebtItem>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct DebtItem {
    pub debtor_id: IdentityId,
    pub cycle_id: Uuid,
    pub original_minor: AmountMinor,
    pub remaining_minor: AmountMinor,
    pub reason: DebtReason,
    pub created_at: OffsetDateTime,
}

#[cfg(test)]
mod tests {
    use super::*;

    fn sample_debt() -> Debt {
        Debt::new(
            Uuid::new_v4(),
            Uuid::new_v4(),
            IdentityId(Uuid::new_v4()),
            10_000,
            DebtReason::LateContribution,
        )
    }

    #[test]
    fn test_full_repayment() {
        let mut debt = sample_debt();
        let repaid = debt.apply_repayment(10_000);
        assert_eq!(repaid, 10_000);
        assert_eq!(debt.remaining_amount_minor, 0);
        assert!(debt.is_fully_repaid());
        assert!(debt.repaid_at.is_some());
    }

    #[test]
    fn test_partial_repayment() {
        let mut debt = sample_debt();
        let repaid = debt.apply_repayment(6_000);
        assert_eq!(repaid, 6_000);
        assert_eq!(debt.remaining_amount_minor, 4_000);
        assert!(debt.is_active());
        assert!(matches!(debt.state, DebtState::PartiallyRepaid));
    }

    #[test]
    fn test_overpayment_is_capped() {
        let mut debt = sample_debt();
        let repaid = debt.apply_repayment(15_000); // Plus que la dette
        assert_eq!(repaid, 10_000); // On ne rembourse que ce qui est dû
        assert!(debt.is_fully_repaid());
    }

    #[test]
    fn test_write_off() {
        let mut debt = sample_debt();
        debt.write_off();
        assert_eq!(debt.remaining_amount_minor, 0);
        assert!(matches!(debt.state, DebtState::WrittenOff));
    }
}
