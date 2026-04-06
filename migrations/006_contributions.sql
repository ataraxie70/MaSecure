-- =============================================================================
-- MaSecure — Migration 006 : Contributions
-- =============================================================================
CREATE TYPE contribution_status AS ENUM (
    'pending', 'reconciled', 'quarantined', 'disputed', 'refunded'
);

CREATE TABLE contributions (
    id                UUID                PRIMARY KEY DEFAULT gen_random_uuid(),
    cycle_id          UUID                NOT NULL REFERENCES cycles(id),
    beneficiary_id    UUID                NOT NULL REFERENCES identities(id),
    payer_identity_id UUID                REFERENCES identities(id),  -- NULL si tiers inconnu
    payer_msisdn      VARCHAR(20)         NOT NULL,
    amount_minor      BIGINT              NOT NULL CHECK (amount_minor > 0),
    status            contribution_status NOT NULL DEFAULT 'pending',
    provider_tx_ref   VARCHAR(100)        NOT NULL UNIQUE,  -- ref opérateur (anti-doublon)
    ledger_entry_id   UUID                REFERENCES ledger_entries(id),
    received_at       TIMESTAMPTZ         NOT NULL DEFAULT NOW(),
    reconciled_at     TIMESTAMPTZ,
    notes             TEXT
);

CREATE INDEX idx_contributions_cycle      ON contributions (cycle_id);
CREATE INDEX idx_contributions_payer      ON contributions (payer_msisdn);
CREATE INDEX idx_contributions_status     ON contributions (status) WHERE status <> 'reconciled';
CREATE INDEX idx_contributions_quarantine ON contributions (cycle_id) WHERE status = 'quarantined';

-- ===========================================================================
-- Vue de réconciliation par cycle
-- Montre le statut consolidé de collecte d'un cycle
-- ===========================================================================
CREATE VIEW cycle_collection_summary AS
SELECT
    c.id                    AS cycle_id,
    c.group_id,
    c.cycle_number,
    c.beneficiary_id,
    c.due_date,
    c.state                 AS cycle_state,
    c.payout_threshold_minor,
    COALESCE(SUM(co.amount_minor) FILTER (WHERE co.status = 'reconciled'), 0) AS reconciled_amount,
    COALESCE(SUM(co.amount_minor) FILTER (WHERE co.status = 'quarantined'), 0) AS quarantined_amount,
    COUNT(co.id) FILTER (WHERE co.status = 'reconciled')   AS reconciled_count,
    COUNT(co.id) FILTER (WHERE co.status = 'quarantined')  AS quarantine_count,
    ROUND(
        100.0 * COALESCE(SUM(co.amount_minor) FILTER (WHERE co.status = 'reconciled'), 0)
        / NULLIF(c.payout_threshold_minor, 0),
        2
    ) AS collection_pct
FROM cycles c
LEFT JOIN contributions co ON co.cycle_id = c.id
GROUP BY c.id, c.group_id, c.cycle_number, c.beneficiary_id,
         c.due_date, c.state, c.payout_threshold_minor;
