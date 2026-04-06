//! Repository identité et wallet bindings
use async_trait::async_trait;
use sqlx::{PgPool, Row};
use uuid::Uuid;

use crate::domain::identity::{Identity, IdentityId, IdentityStatus, Msisdn, WalletProvider};
use crate::ports::identity_repo::IdentityRepository;

pub struct PgIdentityRepository {
    pool: PgPool,
}

impl PgIdentityRepository {
    pub fn new(pool: PgPool) -> Self {
        Self { pool }
    }

    /// Crée un nouveau membre avec son identité stable
    pub async fn create_identity(
        &self,
        full_name: String,
        display_label: Option<String>,
    ) -> anyhow::Result<Identity> {
        let id = Uuid::new_v4();
        sqlx::query(
            r#"
            INSERT INTO identities (id, full_name, display_label, status)
            VALUES ($1, $2, $3, 'active')
        "#,
        )
        .bind(id)
        .bind(&full_name)
        .bind(&display_label)
        .execute(&self.pool)
        .await?;

        tracing::info!(identity_id = %id, name = %full_name, "Identity created");
        Ok(Identity {
            id: IdentityId(id),
            full_name,
            display_label,
            status: IdentityStatus::Active,
        })
    }

    /// Lie un numéro Mobile Money à une identité
    pub async fn bind_wallet(
        &self,
        identity_id: IdentityId,
        msisdn: &Msisdn,
        provider: WalletProvider,
        is_primary: bool,
    ) -> anyhow::Result<Uuid> {
        let binding_id = Uuid::new_v4();

        // Si c'est le wallet primaire, révoquer l'actuel d'abord
        if is_primary {
            sqlx::query(
                r#"
                UPDATE wallet_bindings
                SET status = 'revoked', valid_to = NOW()
                WHERE identity_id = $1 AND is_primary = TRUE AND status = 'active'
            "#,
            )
            .bind(identity_id.inner())
            .execute(&self.pool)
            .await?;
        }

        sqlx::query(
            r#"
            INSERT INTO wallet_bindings (
                id, identity_id, msisdn, provider,
                is_primary, status, valid_from
            ) VALUES ($1, $2, $3, $4::wallet_provider, $5, 'active', NOW())
        "#,
        )
        .bind(binding_id)
        .bind(identity_id.inner())
        .bind(msisdn.as_str())
        .bind(provider_str(&provider))
        .bind(is_primary)
        .execute(&self.pool)
        .await?;

        tracing::info!(
            binding_id  = %binding_id,
            identity_id = %identity_id,
            msisdn      = %msisdn,
            is_primary,
            "Wallet bound"
        );
        Ok(binding_id)
    }

    /// Résout un MSISDN en IdentityId (pour les callbacks Mobile Money)
    /// Retourne None si le MSISDN est inconnu → quarantaine
    pub async fn resolve_msisdn(&self, msisdn: &str) -> anyhow::Result<Option<IdentityId>> {
        let row = sqlx::query(
            r#"
            SELECT identity_id FROM wallet_bindings
            WHERE msisdn = $1 AND status = 'active'
            LIMIT 1
        "#,
        )
        .bind(msisdn)
        .fetch_optional(&self.pool)
        .await?;

        Ok(row.map(|r| {
            let uuid: Uuid = r.try_get("identity_id").unwrap();
            IdentityId(uuid)
        }))
    }

    /// Vérifie si un MSISDN est déjà enregistré comme instrument de paiement
    /// Sinon, l'enregistre en quarantaine pour réconciliation manuelle
    pub async fn ensure_payment_instrument(
        &self,
        msisdn: &str,
        provider: &str,
        identity_id: Option<Uuid>,
    ) -> anyhow::Result<bool> {
        let result = sqlx::query(
            r#"
            INSERT INTO payment_instruments (msisdn, provider, identity_id, quarantine_state)
            VALUES ($1, $2::wallet_provider, $3, $4)
            ON CONFLICT (msisdn, provider) DO UPDATE
                SET identity_id = COALESCE($3, payment_instruments.identity_id)
            RETURNING quarantine_state
        "#,
        )
        .bind(msisdn)
        .bind(provider)
        .bind(identity_id)
        .bind(identity_id.is_none()) // quarantaine si identité inconnue
        .fetch_one(&self.pool)
        .await?;

        let in_quarantine: bool = result.try_get("quarantine_state")?;
        Ok(!in_quarantine) // true = connu, false = quarantaine
    }
}

#[async_trait]
impl IdentityRepository for PgIdentityRepository {
    async fn resolve_msisdn(&self, msisdn: &str) -> anyhow::Result<Option<IdentityId>> {
        self.resolve_msisdn(msisdn).await
    }

    async fn ensure_payment_instrument_in_tx(
        &self,
        msisdn: &str,
        provider: &str,
        identity_id: Option<IdentityId>,
        tx: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    ) -> anyhow::Result<bool> {
        let result = sqlx::query(
            r#"
            INSERT INTO payment_instruments (msisdn, provider, identity_id, quarantine_state)
            VALUES ($1, $2::wallet_provider, $3, $4)
            ON CONFLICT (msisdn, provider) DO UPDATE
                SET identity_id = COALESCE($3, payment_instruments.identity_id),
                    quarantine_state = $4
            RETURNING quarantine_state
            "#,
        )
        .bind(msisdn)
        .bind(provider)
        .bind(identity_id.map(|id| id.inner()))
        .bind(identity_id.is_none())
        .fetch_one(&mut **tx)
        .await?;

        let in_quarantine: bool = result.try_get("quarantine_state")?;
        Ok(!in_quarantine)
    }
}

fn provider_str(p: &WalletProvider) -> &str {
    match p {
        WalletProvider::OrangeMoney => "orange_money",
        WalletProvider::MoovMoney => "moov_money",
        WalletProvider::Wave => "wave",
        WalletProvider::Other(s) => s.as_str(),
    }
}
