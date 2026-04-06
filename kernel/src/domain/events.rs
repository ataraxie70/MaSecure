//! Événements de domaine — émis par le kernel, consommés par les services externes
use serde::{Deserialize, Serialize};
use uuid::Uuid;

use super::cycle::{AmountMinor, PayoutCommand};
use super::identity::{IdentityId, Msisdn, WalletProvider};

/// Événements produits par le kernel financier
/// Ces événements alimentent l'outbox et les notifications.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(tag = "type", rename_all = "snake_case")]
pub enum DomainEvent {
    ContributionReceived {
        cycle_id: Uuid,
        identity_id: Option<IdentityId>,
        payer_msisdn: Msisdn,
        amount_minor: AmountMinor,
        provider_tx_ref: String,
        ledger_seq_no: i64,
    },
    ContributionQuarantined {
        cycle_id: Uuid,
        payer_msisdn: Msisdn,
        amount_minor: AmountMinor,
        provider_tx_ref: String,
        reason: String,
    },
    PayoutTriggered {
        command: PayoutCommand,
        ledger_seq_no: i64,
    },
    PayoutConfirmed {
        cycle_id: Uuid,
        external_ref: String,
        ledger_seq_no: i64,
    },
    PayoutFailed {
        cycle_id: Uuid,
        reason: String,
        ledger_seq_no: i64,
    },
    ProRataDispatched {
        cycle_id: Uuid,
        distributable_minor: AmountMinor,
        fraction_pct: u8,
    },
    WalletBound {
        identity_id: IdentityId,
        msisdn: Msisdn,
        provider: WalletProvider,
        is_primary: bool,
    },
}

impl DomainEvent {
    /// Type string canonique pour le routage dans l'outbox
    pub fn event_type_str(&self) -> &'static str {
        match self {
            Self::ContributionReceived { .. } => "contribution.received",
            Self::ContributionQuarantined { .. } => "contribution.quarantined",
            Self::PayoutTriggered { .. } => "payout.triggered",
            Self::PayoutConfirmed { .. } => "payout.confirmed",
            Self::PayoutFailed { .. } => "payout.failed",
            Self::ProRataDispatched { .. } => "pro_rata.dispatched",
            Self::WalletBound { .. } => "wallet.bound",
        }
    }

    /// Service cible dans l'outbox
    pub fn target_service(&self) -> &'static str {
        match self {
            Self::PayoutTriggered { .. } => "mobile-money-gw",
            Self::PayoutConfirmed { .. } => "notification-svc",
            Self::PayoutFailed { .. } => "notification-svc",
            Self::ProRataDispatched { .. } => "notification-svc",
            _ => "notification-svc",
        }
    }
}

// NOTE : extensions Phase 4-5 — ajoutées en bas pour préserver la compatibilité

impl DomainEvent {
    // Phase 4-5 extension: new event types added above in the main DomainEvent enum
}

/// Événements Phase 4 — Résilience
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
#[serde(tag = "type", rename_all = "snake_case")]
pub enum ResilienceEvent {
    AdvanceIssued {
        cycle_id: Uuid,
        group_id: Uuid,
        advance_minor: i64,
        collected_minor: i64,
    },
    ProRataDispatched {
        cycle_id: Uuid,
        distributable_minor: i64,
        fraction_pct: u8,
    },
    DebtCreated {
        debt_id: Uuid,
        debtor_id: crate::domain::identity::IdentityId,
        cycle_id: Uuid,
        amount_minor: i64,
    },
    DebtRepaid {
        debt_id: Uuid,
        debtor_id: crate::domain::identity::IdentityId,
        repaid_minor: i64,
        remaining_minor: i64,
    },
}

impl ResilienceEvent {
    pub fn event_type_str(&self) -> &'static str {
        match self {
            Self::AdvanceIssued { .. } => "advance.issued",
            Self::ProRataDispatched { .. } => "pro_rata.dispatched",
            Self::DebtCreated { .. } => "debt.created",
            Self::DebtRepaid { .. } => "debt.repaid",
        }
    }
    pub fn target_service(&self) -> &'static str {
        "notification-svc"
    }
}
