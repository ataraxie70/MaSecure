-- =============================================================================
-- MaSecure — Migration 005 : Transactional Outbox
-- =============================================================================
-- L'outbox est écrit dans la MÊME transaction que le ledger.
-- Garantie : si le COMMIT réussit, l'événement SERA livré (at-least-once).
-- =============================================================================
CREATE TYPE outbox_status AS ENUM ('pending','processing','delivered','failed','dead_letter');

CREATE TABLE outbox_events (
    id               UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type       VARCHAR(80)   NOT NULL,
    aggregate_id     UUID          NOT NULL,
    payload          JSONB         NOT NULL,
    target_service   VARCHAR(60)   NOT NULL,    -- 'mobile-money-gw' | 'notification-svc'
    idempotency_key  VARCHAR(128)  NOT NULL UNIQUE,
    status           outbox_status NOT NULL DEFAULT 'pending',
    attempts         SMALLINT      NOT NULL DEFAULT 0,
    next_retry_at    TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    error_detail     TEXT,
    created_at       TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    delivered_at     TIMESTAMPTZ,
    ledger_entry_id  UUID          REFERENCES ledger_entries(id)  -- lien causal
);

-- Worker polling index : seuls les événements prêts à être traités
CREATE INDEX idx_outbox_pending
    ON outbox_events (next_retry_at ASC, target_service)
    WHERE status IN ('pending', 'failed');

-- Éviter les doublons de PayoutCommand par idempotency_key (anti double paiement)
CREATE UNIQUE INDEX idx_outbox_idem_key_active
    ON outbox_events (idempotency_key)
    WHERE status NOT IN ('dead_letter');
