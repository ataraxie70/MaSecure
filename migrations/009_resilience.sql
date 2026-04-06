-- =============================================================================
-- MaSecure — Migration 009 : Fonds de Roulement & Dettes Membres (Phase 4)
-- =============================================================================
--
-- Le fonds de roulement permet d'avancer les versements quand le seuil n'est
-- pas atteint à l'échéance. Chaque avance crée des créances sur les membres
-- en retard, remboursées automatiquement dès réception de leur cotisation.
--
-- INVARIANT (enforced par trigger) :
--   Le montant total des dettes actives <= solde du fonds de roulement.

-- ── Types ENUM ────────────────────────────────────────────────────────────────

CREATE TYPE debt_state AS ENUM (
    'active',
    'partially_repaid',
    'repaid',
    'written_off'
);

CREATE TYPE debt_reason AS ENUM (
    'late_contribution',
    'partial_contribution',
    'working_capital_advance'
);

CREATE TYPE resilience_policy AS ENUM (
    'wait_for_threshold',       -- Comportement Phase 1 (défaut)
    'use_working_capital',      -- Avancer depuis le fonds
    'pro_rata',                 -- Verser au pro-rata du collecté
    'working_capital_then_pro_rata'  -- Hybride
);

-- ── Table : working_capital ───────────────────────────────────────────────────
-- Un seul fonds par groupe. Créé lors de l'activation du groupe.

CREATE TABLE working_capital (
    id               UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id         UUID         NOT NULL UNIQUE REFERENCES tontine_groups(id) ON DELETE CASCADE,
    -- Solde total (contributions au fonds + abondements)
    balance_minor    BIGINT       NOT NULL DEFAULT 0 CHECK (balance_minor >= 0),
    -- Montant réservé pour des avances en attente de confirmation
    reserved_minor   BIGINT       NOT NULL DEFAULT 0 CHECK (reserved_minor >= 0),
    -- Montant total débité depuis la création (pour reporting)
    total_advanced_minor BIGINT   NOT NULL DEFAULT 0 CHECK (total_advanced_minor >= 0),
    -- Montant total remboursé depuis la création
    total_repaid_minor   BIGINT   NOT NULL DEFAULT 0 CHECK (total_repaid_minor >= 0),
    created_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- Contrainte : le réservé ne peut pas dépasser le solde
ALTER TABLE working_capital
    ADD CONSTRAINT chk_reserved_le_balance
    CHECK (reserved_minor <= balance_minor);

-- ── Table : member_debts ──────────────────────────────────────────────────────

CREATE TABLE member_debts (
    id                    UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id              UUID         NOT NULL REFERENCES tontine_groups(id),
    cycle_id              UUID         NOT NULL REFERENCES cycles(id),
    debtor_id             UUID         NOT NULL REFERENCES identities(id),
    original_amount_minor BIGINT       NOT NULL CHECK (original_amount_minor > 0),
    remaining_amount_minor BIGINT      NOT NULL CHECK (remaining_amount_minor >= 0),
    reason                debt_reason  NOT NULL,
    state                 debt_state   NOT NULL DEFAULT 'active',
    -- Entrée ledger debt_recorded associée
    ledger_entry_id       UUID         REFERENCES ledger_entries(id),
    created_at            TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    repaid_at             TIMESTAMPTZ,
    CONSTRAINT chk_remaining_le_original
        CHECK (remaining_amount_minor <= original_amount_minor)
);

CREATE INDEX idx_member_debts_debtor_active
    ON member_debts (debtor_id, state)
    WHERE state IN ('active', 'partially_repaid');

CREATE INDEX idx_member_debts_group
    ON member_debts (group_id, state);

CREATE INDEX idx_member_debts_cycle
    ON member_debts (cycle_id);

-- ── Table : wc_transactions ───────────────────────────────────────────────────
-- Journal des mouvements du fonds de roulement.

CREATE TABLE wc_transactions (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    wc_id           UUID         NOT NULL REFERENCES working_capital(id),
    event_type      VARCHAR(40)  NOT NULL,  -- advance | repayment | deposit | reserve | cancel
    amount_minor    BIGINT       NOT NULL,
    -- Référence ledger associée pour l'audit
    ledger_entry_id UUID         REFERENCES ledger_entries(id),
    debt_id         UUID         REFERENCES member_debts(id),
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_wc_transactions_wc ON wc_transactions (wc_id, created_at DESC);

-- ── Trigger : immuabilité du solde négatif ────────────────────────────────────

CREATE OR REPLACE FUNCTION prevent_negative_wc_balance()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.balance_minor < 0 THEN
        RAISE EXCEPTION
            'MASECURE_INVARIANT: Working capital balance cannot be negative for group %',
            NEW.group_id;
    END IF;
    IF NEW.reserved_minor > NEW.balance_minor THEN
        RAISE EXCEPTION
            'MASECURE_INVARIANT: Reserved amount % exceeds balance % for wc %',
            NEW.reserved_minor, NEW.balance_minor, NEW.id;
    END IF;
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER enforce_wc_non_negative
    BEFORE UPDATE ON working_capital
    FOR EACH ROW EXECUTE FUNCTION prevent_negative_wc_balance();

-- ── Extension du schéma des cycles : politique de résilience ─────────────────
-- Chaque cycle porte sa propre politique héritée de la config du groupe.

ALTER TABLE cycles
    ADD COLUMN IF NOT EXISTS resilience_policy resilience_policy NOT NULL DEFAULT 'wait_for_threshold';

-- ── Extension de group_configs : politique de résilience par défaut ───────────

ALTER TABLE group_configs
    ADD COLUMN IF NOT EXISTS default_resilience_policy resilience_policy NOT NULL DEFAULT 'wait_for_threshold';

-- ── Vue : group_resilience_dashboard ─────────────────────────────────────────

CREATE OR REPLACE VIEW group_resilience_dashboard AS
SELECT
    tg.id                               AS group_id,
    tg.name                             AS group_name,
    COALESCE(wc.balance_minor, 0)       AS wc_balance_minor,
    COALESCE(wc.reserved_minor, 0)      AS wc_reserved_minor,
    COALESCE(wc.balance_minor, 0) - COALESCE(wc.reserved_minor, 0)
                                        AS wc_available_minor,
    COALESCE(wc.total_advanced_minor, 0) AS total_advanced_minor,
    COALESCE(wc.total_repaid_minor, 0)  AS total_repaid_minor,
    COUNT(md.id) FILTER (WHERE md.state IN ('active', 'partially_repaid'))
                                        AS active_debts_count,
    COALESCE(SUM(md.remaining_amount_minor) FILTER (WHERE md.state IN ('active', 'partially_repaid')), 0)
                                        AS total_outstanding_debt_minor
FROM tontine_groups tg
LEFT JOIN working_capital wc ON wc.group_id = tg.id
LEFT JOIN member_debts md ON md.group_id = tg.id
GROUP BY tg.id, tg.name, wc.balance_minor, wc.reserved_minor,
         wc.total_advanced_minor, wc.total_repaid_minor;

COMMENT ON TABLE working_capital IS
    'Fonds de roulement des groupes tontine — permet d''avancer les versements en cas de collecte incomplète.';
COMMENT ON TABLE member_debts IS
    'Créances sur les membres en retard de cotisation, remboursées automatiquement.';

-- ── Vue : overdue_below_threshold_cycles ─────────────────────────────────────
-- Utilisée par le ResilienceScheduler pour identifier les cycles à traiter.
-- Cycles COMMITTED, échéance dépassée, seuil non atteint, payout non initié.

CREATE OR REPLACE VIEW overdue_below_threshold_cycles AS
SELECT
    c.id                    AS cycle_id,
    c.group_id,
    c.cycle_number,
    c.beneficiary_id,
    c.collected_amount_minor,
    c.payout_threshold_minor,
    c.due_date,
    c.resilience_policy::TEXT AS resilience_policy
FROM cycles c
WHERE c.state         = 'committed'
  AND c.payout_state  = 'not_sent'
  AND c.due_date      < NOW()
  AND c.collected_amount_minor < c.payout_threshold_minor;

COMMENT ON VIEW overdue_below_threshold_cycles IS
    'Cycles COMMITTED dont l''échéance est dépassée et le seuil de collecte non atteint — candidats au ResilienceScheduler.';
