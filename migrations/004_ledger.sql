-- =============================================================================
-- MaSecure — Migration 004 : Ledger Append-Only
-- =============================================================================
-- PROPRIÉTÉ FONDAMENTALE : aucune ligne de ce ledger ne peut être modifiée.
-- La chaîne de hachages garantit l'intégrité de bout en bout.
-- =============================================================================
CREATE TYPE ledger_event_type AS ENUM (
    'contribution_received',
    'contribution_quarantined',
    'payout_initiated',
    'payout_sent',
    'payout_confirmed',
    'payout_failed',
    'payout_reversed',
    'advance_issued',
    'debt_recorded',
    'debt_repaid',
    'fee_charged',
    'adjustment',
    'cycle_opened',
    'cycle_closed',
    'identity_created',
    'wallet_bound',
    'wallet_revoked'
);

CREATE TABLE ledger_entries (
    -- Identité et ordonnancement
    id                  UUID             PRIMARY KEY DEFAULT gen_random_uuid(),
    seq_no              BIGSERIAL        NOT NULL UNIQUE,  -- séquence globale stricte

    -- Typage événementiel
    event_type          ledger_event_type NOT NULL,
    aggregate_type      VARCHAR(40)      NOT NULL,   -- 'cycle' | 'identity' | 'group'
    aggregate_id        UUID             NOT NULL,

    -- Données financières (NULL pour événements non-monétaires)
    amount_minor        BIGINT,
    direction           CHAR(1)          CHECK (direction IN ('+', '-')),

    -- Payload canonique et intégrité
    payload             JSONB            NOT NULL,
    payload_hash        CHAR(64)         NOT NULL,   -- SHA-256 du payload JSON canonique
    prev_hash           CHAR(64),                    -- NULL uniquement pour la 1ère entrée globale
    current_hash        CHAR(64)         NOT NULL,   -- SHA-256(seq_no||event_type||payload_hash||prev_hash)

    -- Idempotence et référence externe
    idempotency_key     VARCHAR(128)     UNIQUE,     -- NULL pour événements sans contrainte d'idempotence
    external_ref        VARCHAR(200),                -- txn_id de l'opérateur Mobile Money

    -- Traçabilité
    created_at          TIMESTAMPTZ      NOT NULL DEFAULT NOW(),
    created_by_service  VARCHAR(60)      NOT NULL    -- ex: 'kernel-financial@1.0.0'
);

-- Index de recherche métier
CREATE INDEX idx_ledger_aggregate    ON ledger_entries (aggregate_type, aggregate_id);
CREATE INDEX idx_ledger_event_type   ON ledger_entries (event_type);
CREATE INDEX idx_ledger_created_at   ON ledger_entries (created_at DESC);
CREATE INDEX idx_ledger_external_ref ON ledger_entries (external_ref) WHERE external_ref IS NOT NULL;

-- ===========================================================================
-- PROTECTION CONTRE LES MODIFICATIONS
-- Toute tentative d'UPDATE ou DELETE sur le ledger est bloquée au niveau BD.
-- ===========================================================================
CREATE OR REPLACE FUNCTION deny_ledger_mutation()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION
        'MASECURE_LEDGER_IMMUTABLE: Operation % is forbidden on ledger_entries. The ledger is append-only.',
        TG_OP;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER ledger_no_update
    BEFORE UPDATE ON ledger_entries
    FOR EACH ROW EXECUTE FUNCTION deny_ledger_mutation();

CREATE TRIGGER ledger_no_delete
    BEFORE DELETE ON ledger_entries
    FOR EACH ROW EXECUTE FUNCTION deny_ledger_mutation();

-- Vue de vérification de la chaîne de hachages (pour audit externe)
-- Chaque ligne compare le prev_hash déclaré avec le current_hash de la ligne précédente.
CREATE VIEW ledger_chain_audit AS
SELECT
    cur.seq_no,
    cur.id,
    cur.event_type,
    cur.aggregate_id,
    cur.current_hash,
    cur.prev_hash,
    prev.current_hash AS expected_prev_hash,
    CASE
        WHEN cur.prev_hash IS NULL AND cur.seq_no = 1 THEN 'GENESIS'
        WHEN cur.prev_hash = prev.current_hash         THEN 'OK'
        ELSE                                                'BROKEN'
    END AS chain_status
FROM ledger_entries cur
LEFT JOIN ledger_entries prev ON prev.seq_no = cur.seq_no - 1
ORDER BY cur.seq_no;
