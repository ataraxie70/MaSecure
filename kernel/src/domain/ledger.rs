//! Ledger Append-Only — types et calcul des hachages de chaîne
//!
//! Chaque LedgerEntry contient le hash de la précédente.
//! La chaîne est vérifiable par n'importe quel observateur ayant accès
//! aux entrées dans l'ordre de leur seq_no.

use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use time::OffsetDateTime;
use uuid::Uuid;

use super::cycle::AmountMinor;

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub enum LedgerEventType {
    ContributionReceived,
    ContributionQuarantined,
    PayoutInitiated,
    PayoutSent,
    PayoutConfirmed,
    PayoutFailed,
    PayoutReversed,
    AdvanceIssued,
    DebtRecorded,
    DebtRepaid,
    FeeCharged,
    Adjustment,
    CycleOpened,
    CycleClosed,
    IdentityCreated,
    WalletBound,
    WalletRevoked,
}

impl LedgerEventType {
    /// Représentation canonique pour le calcul du hash
    pub fn as_canonical(&self) -> &'static str {
        match self {
            Self::ContributionReceived => "contribution_received",
            Self::ContributionQuarantined => "contribution_quarantined",
            Self::PayoutInitiated => "payout_initiated",
            Self::PayoutSent => "payout_sent",
            Self::PayoutConfirmed => "payout_confirmed",
            Self::PayoutFailed => "payout_failed",
            Self::PayoutReversed => "payout_reversed",
            Self::AdvanceIssued => "advance_issued",
            Self::DebtRecorded => "debt_recorded",
            Self::DebtRepaid => "debt_repaid",
            Self::FeeCharged => "fee_charged",
            Self::Adjustment => "adjustment",
            Self::CycleOpened => "cycle_opened",
            Self::CycleClosed => "cycle_closed",
            Self::IdentityCreated => "identity_created",
            Self::WalletBound => "wallet_bound",
            Self::WalletRevoked => "wallet_revoked",
        }
    }
}

/// Direction d'un mouvement financier
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub enum Direction {
    Credit,
    Debit,
}

/// Entrée du ledger — immuable une fois créée
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct LedgerEntry {
    pub id: Uuid,
    pub seq_no: i64,
    pub event_type: LedgerEventType,
    pub aggregate_type: String,
    pub aggregate_id: Uuid,
    pub amount_minor: Option<AmountMinor>,
    pub direction: Option<Direction>,
    pub payload: serde_json::Value,
    pub payload_hash: String, // SHA-256 hex du payload canonique
    pub prev_hash: Option<String>,
    pub current_hash: String, // SHA-256 chaîné
    pub idempotency_key: Option<String>,
    pub external_ref: Option<String>,
    pub created_at: OffsetDateTime,
    pub created_by_service: String,
}

/// Builder pour construire une entrée de ledger proprement
pub struct LedgerEntryBuilder {
    event_type: LedgerEventType,
    aggregate_type: String,
    aggregate_id: Uuid,
    amount_minor: Option<AmountMinor>,
    direction: Option<Direction>,
    payload: serde_json::Value,
    idempotency_key: Option<String>,
    external_ref: Option<String>,
    created_by_service: String,
}

impl LedgerEntryBuilder {
    pub fn new(
        event_type: LedgerEventType,
        aggregate_type: impl Into<String>,
        aggregate_id: Uuid,
        payload: serde_json::Value,
        service: impl Into<String>,
    ) -> Self {
        Self {
            event_type,
            aggregate_type: aggregate_type.into(),
            aggregate_id,
            payload,
            amount_minor: None,
            direction: None,
            idempotency_key: None,
            external_ref: None,
            created_by_service: service.into(),
        }
    }

    pub fn amount(mut self, amount: AmountMinor, dir: Direction) -> Self {
        self.amount_minor = Some(amount);
        self.direction = Some(dir);
        self
    }

    pub fn idempotency(mut self, key: impl Into<String>) -> Self {
        self.idempotency_key = Some(key.into());
        self
    }

    pub fn external_ref(mut self, r: impl Into<String>) -> Self {
        self.external_ref = Some(r.into());
        self
    }

    /// Finalise l'entrée en calculant les hachages.
    /// `seq_no` et `prev_hash` sont fournis par le repository juste avant l'écriture.
    pub fn build(self, seq_no: i64, prev_hash: Option<String>) -> LedgerEntry {
        let payload_hash = Self::sha256_hex(
            serde_json::to_string(&self.payload)
                .expect("payload serialization never fails")
                .as_bytes(),
        );

        let chain_input = format!(
            "{}:{}:{}:{}",
            seq_no,
            self.event_type.as_canonical(),
            payload_hash,
            prev_hash.as_deref().unwrap_or("GENESIS")
        );
        let current_hash = Self::sha256_hex(chain_input.as_bytes());

        LedgerEntry {
            id: Uuid::new_v4(),
            seq_no,
            event_type: self.event_type,
            aggregate_type: self.aggregate_type,
            aggregate_id: self.aggregate_id,
            amount_minor: self.amount_minor,
            direction: self.direction,
            payload: self.payload,
            payload_hash,
            prev_hash,
            current_hash,
            idempotency_key: self.idempotency_key,
            external_ref: self.external_ref,
            created_at: OffsetDateTime::now_utc(),
            created_by_service: self.created_by_service,
        }
    }

    fn sha256_hex(data: &[u8]) -> String {
        let mut hasher = Sha256::new();
        hasher.update(data);
        hex::encode(hasher.finalize())
    }
}

// ============================================================================
// Vérification de la chaîne d'intégrité
// ============================================================================

#[derive(Debug, Serialize, Deserialize)]
pub struct IntegrityViolation {
    pub seq_no: i64,
    pub kind: ViolationKind,
    pub detail: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub enum ViolationKind {
    HashMismatch, // current_hash ne correspond pas au calcul attendu
    ChainBreak,   // prev_hash ne correspond pas au current_hash précédent
    SequenceGap,  // seq_no non-contigu
}

#[derive(Debug, Serialize, Deserialize)]
pub struct IntegrityReport {
    pub total_entries: usize,
    pub violations: Vec<IntegrityViolation>,
    pub is_valid: bool,
}

/// Vérifie la chaîne de hachages du ledger dans l'ordre des seq_no.
/// Complexité O(n) — une seule passe.
pub fn verify_chain(entries: &[LedgerEntry]) -> IntegrityReport {
    let mut violations = Vec::new();
    let mut prev_hash: Option<&str> = None;
    let mut prev_seq: Option<i64> = None;

    for entry in entries {
        // Vérifier la continuité de la séquence
        if let Some(ps) = prev_seq {
            if entry.seq_no != ps + 1 {
                violations.push(IntegrityViolation {
                    seq_no: entry.seq_no,
                    kind: ViolationKind::SequenceGap,
                    detail: format!(
                        "Gap detected: expected seq={}, got={}",
                        ps + 1,
                        entry.seq_no
                    ),
                });
            }
        }

        // Vérifier que prev_hash correspond au current_hash précédent
        let expected_prev = if entry.seq_no == 1 { None } else { prev_hash };
        if entry.prev_hash.as_deref() != expected_prev {
            violations.push(IntegrityViolation {
                seq_no: entry.seq_no,
                kind: ViolationKind::ChainBreak,
                detail: format!(
                    "prev_hash mismatch at seq={}: declared={:?}, expected={:?}",
                    entry.seq_no, entry.prev_hash, expected_prev
                ),
            });
        }

        // Recalculer le current_hash attendu
        let chain_input = format!(
            "{}:{}:{}:{}",
            entry.seq_no,
            entry.event_type.as_canonical(),
            entry.payload_hash,
            entry.prev_hash.as_deref().unwrap_or("GENESIS")
        );
        let expected_hash = {
            let mut h = Sha256::new();
            h.update(chain_input.as_bytes());
            hex::encode(h.finalize())
        };

        if entry.current_hash != expected_hash {
            violations.push(IntegrityViolation {
                seq_no: entry.seq_no,
                kind: ViolationKind::HashMismatch,
                detail: format!(
                    "current_hash mismatch at seq={}: stored={}, computed={}",
                    entry.seq_no, entry.current_hash, expected_hash
                ),
            });
        }

        prev_hash = Some(&entry.current_hash);
        prev_seq = Some(entry.seq_no);
    }

    IntegrityReport {
        total_entries: entries.len(),
        is_valid: violations.is_empty(),
        violations,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn make_entry(seq_no: i64, prev_hash: Option<String>) -> LedgerEntry {
        LedgerEntryBuilder::new(
            LedgerEventType::CycleOpened,
            "cycle",
            Uuid::new_v4(),
            serde_json::json!({"test": seq_no}),
            "test-service",
        )
        .build(seq_no, prev_hash)
    }

    #[test]
    fn test_chain_valid() {
        let e1 = make_entry(1, None);
        let h1 = e1.current_hash.clone();
        let e2 = make_entry(2, Some(h1.clone()));
        let h2 = e2.current_hash.clone();
        let e3 = make_entry(3, Some(h2));

        let report = verify_chain(&[e1, e2, e3]);
        assert!(
            report.is_valid,
            "Chain doit être valide : {:?}",
            report.violations
        );
        assert_eq!(report.total_entries, 3);
    }

    #[test]
    fn test_chain_tampered() {
        let e1 = make_entry(1, None);
        let h1 = e1.current_hash.clone();
        let mut e2 = make_entry(2, Some(h1));
        // On falsifie le payload_hash pour simuler une altération
        e2.payload_hash = "0000000000000000000000000000000000000000000000000000000000000000".into();
        let h2 = e2.current_hash.clone();
        let e3 = make_entry(3, Some(h2));

        let report = verify_chain(&[e1, e2, e3]);
        assert!(!report.is_valid, "La falsification doit être détectée");
        assert!(!report.violations.is_empty());
    }
}
