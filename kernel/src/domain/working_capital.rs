//! Domaine : Fonds de Roulement (Working Capital)
//!
//! Le fonds de roulement est une réserve collective détenue par un groupe.
//! Il permet d'avancer le versement à un bénéficiaire même si toutes les
//! cotisations ne sont pas encore reçues à l'échéance du cycle.
//!
//! INVARIANTS FONDAMENTAUX :
//! 1. Le solde disponible ne peut jamais être négatif
//! 2. Tout avancement crée automatiquement une créance sur les membres en retard
//! 3. Le remboursement d'une créance recrédite exactement le montant avancé
//! 4. Chaque opération est inscrite dans le ledger append-only

use serde::{Deserialize, Serialize};
use uuid::Uuid;

use super::cycle::AmountMinor;
use super::identity::DomainError;

/// Identifiant stable du fonds de roulement d'un groupe.
/// Un seul fonds par groupe — créé à l'initialisation du groupe.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub struct WorkingCapitalId(pub Uuid);

/// État en mémoire du fonds de roulement d'un groupe.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WorkingCapital {
    pub id: WorkingCapitalId,
    pub group_id: Uuid,
    /// Solde total du fonds (contributions des membres + abondements initiaux)
    pub balance_minor: AmountMinor,
    /// Montant actuellement réservé pour des avances en cours de recouvrement
    pub reserved_minor: AmountMinor,
}

impl WorkingCapital {
    /// Crée un fonds de roulement vide pour un groupe.
    pub fn new(group_id: Uuid) -> Self {
        Self {
            id: WorkingCapitalId(Uuid::new_v4()),
            group_id,
            balance_minor: 0,
            reserved_minor: 0,
        }
    }

    /// Montant disponible = solde total - réservé
    pub fn available_minor(&self) -> AmountMinor {
        self.balance_minor - self.reserved_minor
    }

    /// Évalue si le fonds peut couvrir un avancement de `needed_minor`.
    pub fn can_advance(&self, needed_minor: AmountMinor) -> bool {
        self.available_minor() >= needed_minor
    }

    /// Calcule le montant à avancer pour combler le gap d'un cycle.
    ///
    /// Si `collected < threshold`, retourne min(gap, available).
    /// Si le fonds ne peut couvrir qu'une partie, retourne ce partiel.
    pub fn compute_advance(
        &self,
        collected_minor: AmountMinor,
        threshold_minor: AmountMinor,
    ) -> AdvanceDecision {
        if collected_minor >= threshold_minor {
            return AdvanceDecision::NotNeeded;
        }

        let gap = threshold_minor - collected_minor;
        let available = self.available_minor();

        if available == 0 {
            return AdvanceDecision::InsufficientFunds { gap_minor: gap };
        }

        let advance = gap.min(available);
        let covers_fully = advance == gap;

        AdvanceDecision::Advance {
            advance_minor: advance,
            remaining_gap_minor: gap - advance,
            covers_fully,
        }
    }

    /// Applique un avancement : réserve les fonds (ne les débite pas encore).
    /// Le débit réel se fait à la confirmation du payout.
    pub fn reserve_advance(&mut self, amount_minor: AmountMinor) -> Result<(), DomainError> {
        if amount_minor <= 0 {
            return Err(DomainError::CycleInvariantViolation(
                "Advance amount must be positive".into(),
            ));
        }
        if self.available_minor() < amount_minor {
            return Err(DomainError::CycleInvariantViolation(format!(
                "Insufficient working capital: available={}, needed={}",
                self.available_minor(),
                amount_minor
            )));
        }
        self.reserved_minor += amount_minor;
        Ok(())
    }

    /// Confirme l'utilisation d'une avance réservée : débite définitivement.
    pub fn confirm_advance(&mut self, amount_minor: AmountMinor) -> Result<(), DomainError> {
        if self.reserved_minor < amount_minor {
            return Err(DomainError::CycleInvariantViolation(
                "Cannot confirm advance: not enough reserved".into(),
            ));
        }
        self.reserved_minor -= amount_minor;
        self.balance_minor -= amount_minor;
        Ok(())
    }

    /// Annule une réserve (ex: cycle annulé avant confirmation du payout).
    pub fn cancel_advance_reservation(&mut self, amount_minor: AmountMinor) {
        self.reserved_minor = (self.reserved_minor - amount_minor).max(0);
    }

    /// Reçoit un remboursement d'une créance : recrédite le fonds.
    pub fn receive_repayment(&mut self, amount_minor: AmountMinor) {
        self.balance_minor += amount_minor;
    }

    /// Abondement direct par les membres (contribution périodique au fonds).
    pub fn deposit(&mut self, amount_minor: AmountMinor) {
        self.balance_minor += amount_minor;
    }
}

/// Décision de l'évaluation de l'avancement possible.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub enum AdvanceDecision {
    /// Aucun avancement nécessaire — seuil déjà atteint
    NotNeeded,
    /// Avancement possible, éventuellement partiel
    Advance {
        advance_minor: AmountMinor,
        remaining_gap_minor: AmountMinor,
        covers_fully: bool,
    },
    /// Fonds insuffisants pour toute avance
    InsufficientFunds { gap_minor: AmountMinor },
}

/// Règle pro-rata : calcule le montant payable avec les fonds actuels.
///
/// Si le seuil n'est pas atteint et qu'aucune avance n'est disponible,
/// on peut opter pour un versement pro-rata de ce qui est collecté.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ProRataDecision {
    /// Montant collecté à distribuer
    pub distributable_minor: AmountMinor,
    /// Fraction du total attendu (pour affichage / notification)
    pub fraction_pct: u8,
    /// Montant restant à recouvrer pour atteindre le seuil complet
    pub shortfall_minor: AmountMinor,
}

impl ProRataDecision {
    /// Calcule un versement pro-rata depuis les fonds collectés.
    pub fn compute(
        collected_minor: AmountMinor,
        threshold_minor: AmountMinor,
    ) -> Option<ProRataDecision> {
        if threshold_minor == 0 || collected_minor <= 0 {
            return None;
        }
        let fraction_pct = ((collected_minor * 100) / threshold_minor).min(100) as u8;
        Some(ProRataDecision {
            distributable_minor: collected_minor,
            fraction_pct,
            shortfall_minor: (threshold_minor - collected_minor).max(0),
        })
    }
}

/// Politique de résilience d'un cycle — détermine comment gérer un seuil non atteint.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub enum ResiliencePolicy {
    /// Bloquer le versement jusqu'à l'atteinte du seuil (comportement par défaut Phase 1-2)
    WaitForThreshold,
    /// Utiliser le fonds de roulement pour combler le gap si possible
    UseWorkingCapital,
    /// Verser au pro-rata de ce qui est collecté
    ProRata,
    /// Utiliser le fonds de roulement en premier, puis pro-rata si insuffisant
    WorkingCapitalThenProRata,
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_working_capital_advance_full() {
        let mut wc = WorkingCapital::new(Uuid::new_v4());
        wc.balance_minor = 50_000;

        match wc.compute_advance(30_000, 50_000) {
            AdvanceDecision::Advance { advance_minor, covers_fully, .. } => {
                assert_eq!(advance_minor, 20_000);
                assert!(covers_fully);
            }
            other => panic!("expected Advance, got {:?}", other),
        }
    }

    #[test]
    fn test_working_capital_advance_partial() {
        let mut wc = WorkingCapital::new(Uuid::new_v4());
        wc.balance_minor = 5_000; // insuffisant pour couvrir tout le gap

        match wc.compute_advance(30_000, 50_000) {
            AdvanceDecision::Advance { advance_minor, covers_fully, remaining_gap_minor } => {
                assert_eq!(advance_minor, 5_000);
                assert!(!covers_fully);
                assert_eq!(remaining_gap_minor, 15_000);
            }
            other => panic!("expected partial Advance, got {:?}", other),
        }
    }

    #[test]
    fn test_reserve_and_confirm() {
        let mut wc = WorkingCapital::new(Uuid::new_v4());
        wc.balance_minor = 50_000;

        wc.reserve_advance(20_000).unwrap();
        assert_eq!(wc.available_minor(), 30_000);
        assert_eq!(wc.reserved_minor, 20_000);

        wc.confirm_advance(20_000).unwrap();
        assert_eq!(wc.balance_minor, 30_000);
        assert_eq!(wc.reserved_minor, 0);
    }

    #[test]
    fn test_pro_rata_calculation() {
        let decision = ProRataDecision::compute(35_000, 50_000).unwrap();
        assert_eq!(decision.distributable_minor, 35_000);
        assert_eq!(decision.fraction_pct, 70);
        assert_eq!(decision.shortfall_minor, 15_000);
    }

    #[test]
    fn test_reserve_fails_on_insufficient_funds() {
        let mut wc = WorkingCapital::new(Uuid::new_v4());
        wc.balance_minor = 1_000;

        assert!(wc.reserve_advance(5_000).is_err());
    }
}
