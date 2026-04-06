//! Agrégat Identité — découplé du MSISDN
use serde::{Deserialize, Serialize};
use uuid::Uuid;

/// Identifiant stable d'un membre. Ne change JAMAIS,
/// même si le numéro de téléphone change.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub struct IdentityId(pub Uuid);

impl IdentityId {
    pub fn new() -> Self {
        Self(Uuid::new_v4())
    }
    pub fn inner(&self) -> Uuid {
        self.0
    }
}

impl std::fmt::Display for IdentityId {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}", self.0)
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub enum IdentityStatus {
    Active,
    Suspended,
    Deactivated,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Identity {
    pub id: IdentityId,
    pub full_name: String,
    pub display_label: Option<String>,
    pub status: IdentityStatus,
}

/// Vecteur de paiement — peut changer, n'est pas l'identité
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub enum WalletProvider {
    OrangeMoney,
    MoovMoney,
    Wave,
    Other(String),
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub enum BindingStatus {
    Pending,
    Active,
    Revoked,
    Quarantine,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WalletBinding {
    pub id: Uuid,
    pub identity_id: IdentityId,
    pub msisdn: Msisdn,
    pub provider: WalletProvider,
    pub is_primary: bool,
    pub status: BindingStatus,
}

/// Numéro de téléphone validé (format E.164)
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub struct Msisdn(String);

impl Msisdn {
    pub fn new(raw: impl Into<String>) -> Result<Self, DomainError> {
        let s = raw.into();
        // Format E.164 minimal : commence par + et contient 7 à 15 chiffres
        let digits: String = s.chars().filter(|c| c.is_ascii_digit()).collect();
        if s.starts_with('+') && digits.len() >= 7 && digits.len() <= 15 {
            Ok(Self(s))
        } else {
            Err(DomainError::InvalidMsisdn(s))
        }
    }
    pub fn as_str(&self) -> &str {
        &self.0
    }
}

impl std::fmt::Display for Msisdn {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}", self.0)
    }
}

#[derive(Debug, thiserror::Error)]
pub enum DomainError {
    #[error("MSISDN invalide : {0}")]
    InvalidMsisdn(String),
    #[error("Montant invalide : doit être > 0")]
    InvalidAmount,
    #[error("Invariant de cycle violé : {0}")]
    CycleInvariantViolation(String),
    #[error("Clé d'idempotence manquante")]
    MissingIdempotencyKey,
}
