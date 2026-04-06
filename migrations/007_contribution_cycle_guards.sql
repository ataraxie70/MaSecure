-- =============================================================================
-- MaSecure — Migration 007 : Garde-fous de contribution par état de cycle
-- =============================================================================
--
-- Les callbacks Mobile Money ne doivent pouvoir matérialiser une contribution
-- que sur un cycle `committed`. Le kernel applique déjà cette règle, mais la
-- base la ré-enforce pour éviter toute dérive si une régression applicative
-- réapparaît plus tard.

CREATE OR REPLACE FUNCTION prevent_non_committed_cycle_contribution()
RETURNS TRIGGER AS $$
DECLARE
    current_cycle_state cycle_state;
BEGIN
    SELECT state
    INTO current_cycle_state
    FROM cycles
    WHERE id = NEW.cycle_id;

    IF current_cycle_state IS NULL THEN
        RAISE EXCEPTION
            'MASECURE_INVARIANT_VIOLATION: Unknown cycle % for contribution %',
            NEW.cycle_id, NEW.provider_tx_ref;
    END IF;

    IF current_cycle_state <> 'committed' THEN
        RAISE EXCEPTION
            'MASECURE_INVARIANT_VIOLATION: Cannot insert contribution for cycle %. state=% tx_ref=%',
            NEW.cycle_id, current_cycle_state, NEW.provider_tx_ref;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER enforce_contribution_cycle_state
    BEFORE INSERT ON contributions
    FOR EACH ROW EXECUTE FUNCTION prevent_non_committed_cycle_contribution();
