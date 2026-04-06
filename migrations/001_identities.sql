-- =============================================================================
-- MaSecure — Migration 001 : Identités et Wallets
-- =============================================================================
CREATE TYPE identity_status AS ENUM ('active', 'suspended', 'deactivated');
CREATE TYPE wallet_provider  AS ENUM ('orange_money', 'moov_money', 'wave', 'other');
CREATE TYPE binding_status   AS ENUM ('pending', 'active', 'revoked', 'quarantine');

CREATE TABLE identities (
    id            UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    full_name     VARCHAR(120) NOT NULL,
    display_label VARCHAR(60),
    status        identity_status NOT NULL DEFAULT 'active',
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE TABLE wallet_bindings (
    id            UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    identity_id   UUID         NOT NULL REFERENCES identities(id) ON DELETE RESTRICT,
    msisdn        VARCHAR(20)  NOT NULL,
    provider      wallet_provider NOT NULL,
    is_primary    BOOLEAN      NOT NULL DEFAULT FALSE,
    verified_at   TIMESTAMPTZ,
    status        binding_status NOT NULL DEFAULT 'pending',
    valid_from    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    valid_to      TIMESTAMPTZ,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- Un seul wallet primaire actif par identité
CREATE UNIQUE INDEX idx_wallet_one_primary
    ON wallet_bindings (identity_id)
    WHERE is_primary = TRUE AND status = 'active';

CREATE INDEX idx_wallet_msisdn ON wallet_bindings (msisdn) WHERE status = 'active';

CREATE TABLE payment_instruments (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    provider         wallet_provider NOT NULL,
    msisdn           VARCHAR(20) NOT NULL,
    identity_id      UUID        REFERENCES identities(id),
    trust_level      SMALLINT    NOT NULL DEFAULT 0 CHECK (trust_level BETWEEN 0 AND 3),
    quarantine_state BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX idx_instrument_msisdn_provider
    ON payment_instruments (msisdn, provider);

CREATE OR REPLACE FUNCTION touch_updated_at()
RETURNS TRIGGER AS $$
BEGIN NEW.updated_at = NOW(); RETURN NEW; END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER identities_updated_at
    BEFORE UPDATE ON identities
    FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
