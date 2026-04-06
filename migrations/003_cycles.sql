-- =============================================================================
-- MaSecure — Migration 003 : Cycles Financiers
-- =============================================================================
CREATE TYPE cycle_state  AS ENUM ('open', 'committed', 'payout_triggered', 'closed', 'disputed');
CREATE TYPE payout_state AS ENUM ('not_sent', 'pending', 'sent', 'confirmed', 'failed');

CREATE TABLE cycles (
    id                     UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id               UUID        NOT NULL REFERENCES tontine_groups(id),
    config_id              UUID        NOT NULL REFERENCES group_configs(id),
    cycle_number           INTEGER     NOT NULL CHECK (cycle_number > 0),
    beneficiary_id         UUID        NOT NULL REFERENCES identities(id),
    due_date               TIMESTAMPTZ NOT NULL,
    payout_threshold_minor BIGINT      NOT NULL CHECK (payout_threshold_minor > 0),
    collected_amount_minor BIGINT      NOT NULL DEFAULT 0 CHECK (collected_amount_minor >= 0),
    state                  cycle_state  NOT NULL DEFAULT 'open',
    payout_state           payout_state NOT NULL DEFAULT 'not_sent',
    opened_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    closed_at              TIMESTAMPTZ,
    payout_idempotency_key VARCHAR(128) UNIQUE,
    UNIQUE (group_id, cycle_number)
);

-- ===========================================================================
-- CONTRAINTE FONDAMENTALE D'IMMUABILITÉ
-- Un cycle committed/payout_triggered/closed ne peut pas changer de config.
-- C'est la garantie que l'audit est toujours cohérent.
-- ===========================================================================
CREATE OR REPLACE FUNCTION prevent_committed_cycle_mutation()
RETURNS TRIGGER AS $$
BEGIN
    IF OLD.state IN ('committed', 'payout_triggered', 'closed') THEN
        IF NEW.config_id <> OLD.config_id THEN
            RAISE EXCEPTION
                'MASECURE_INVARIANT_VIOLATION: Cannot mutate config_id of committed cycle %. state=%, attempted_new_config=%',
                OLD.id, OLD.state, NEW.config_id;
        END IF;
        -- On ne peut pas revenir à un état précédent
        IF (OLD.state = 'closed' AND NEW.state <> 'closed') OR
           (OLD.state = 'payout_triggered' AND NEW.state = 'open') THEN
            RAISE EXCEPTION
                'MASECURE_INVARIANT_VIOLATION: Illegal state transition for cycle %. % -> %',
                OLD.id, OLD.state, NEW.state;
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER enforce_cycle_immutability
    BEFORE UPDATE ON cycles
    FOR EACH ROW EXECUTE FUNCTION prevent_committed_cycle_mutation();

-- Automate d'état : vue calculée des cycles éligibles au payout
-- Utilisée par le kernel scheduler (lecture seule)
CREATE VIEW payout_eligible_cycles AS
SELECT
    c.id,
    c.group_id,
    c.beneficiary_id,
    c.collected_amount_minor,
    c.payout_threshold_minor,
    c.due_date,
    c.payout_idempotency_key,
    wb.msisdn  AS beneficiary_msisdn,
    wb.provider AS beneficiary_provider
FROM cycles c
JOIN wallet_bindings wb ON wb.identity_id = c.beneficiary_id
    AND wb.is_primary = TRUE AND wb.status = 'active'
WHERE
    c.state = 'committed'
    AND c.payout_state = 'not_sent'
    AND NOW() >= c.due_date
    AND c.collected_amount_minor >= c.payout_threshold_minor;
