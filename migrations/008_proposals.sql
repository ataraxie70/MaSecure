-- =============================================================================
-- MaSecure — Migration 008 : Gouvernance — Propositions et Votes
-- =============================================================================
--
-- Implémente le système de gouvernance versionnée décrit dans le CDC §5.2.
-- Une proposition matérialise un diff entre deux versions de configuration.
-- Elle est soumise au vote des membres actifs selon le quorum configuré.
-- Si approuvée, elle s'applique au cycle suivant sans interrompre le cycle actif.

-- ── Types ENUM ────────────────────────────────────────────────────────────────

CREATE TYPE proposal_status AS ENUM (
    'open',       -- En attente de votes
    'approved',   -- Quorum atteint, changement appliqué au cycle suivant
    'rejected',   -- Majorité contre ou quorum non atteint
    'expired'     -- Délai de vote dépassé sans résolution
);

CREATE TYPE vote_decision AS ENUM ('approve', 'reject');

-- ── Table : proposals ─────────────────────────────────────────────────────────
-- Chaque proposition représente un changement de configuration soumis au vote.
-- Elle lie une config de base (état actuel) à une nouvelle config (état proposé).

CREATE TABLE proposals (
    id              UUID            PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id        UUID            NOT NULL REFERENCES tontine_groups(id) ON DELETE CASCADE,
    proposed_by     UUID            NOT NULL REFERENCES identities(id),
    -- Config de référence sur laquelle le diff est calculé
    base_config_id  UUID            NOT NULL REFERENCES group_configs(id),
    -- Nouvelle config créée par le diff (en état 'draft' jusqu'à approbation)
    new_config_id   UUID            NOT NULL REFERENCES group_configs(id),
    -- Résumé des changements au format JSONB : { "champ": { "from": X, "to": Y } }
    diff_summary    JSONB           NOT NULL DEFAULT '{}',
    status          proposal_status NOT NULL DEFAULT 'open',
    -- Délai de vote : par défaut 72h après la proposition
    expires_at      TIMESTAMPTZ     NOT NULL DEFAULT NOW() + INTERVAL '72 hours',
    created_at      TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    resolved_at     TIMESTAMPTZ,
    -- Snapshot du quorum requis au moment de la proposition (en %)
    quorum_pct      SMALLINT        NOT NULL DEFAULT 67
);

-- Index pour les proposals ouvertes d'un groupe
CREATE INDEX idx_proposals_group_open
    ON proposals (group_id, status)
    WHERE status = 'open';

-- Index pour le nettoyage des propositions expirées
CREATE INDEX idx_proposals_expires
    ON proposals (expires_at)
    WHERE status = 'open';

-- ── Table : proposal_votes ─────────────────────────────────────────────────────
-- Chaque membre actif ne peut voter qu'une fois par proposition.

CREATE TABLE proposal_votes (
    id           UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    proposal_id  UUID          NOT NULL REFERENCES proposals(id) ON DELETE CASCADE,
    identity_id  UUID          NOT NULL REFERENCES identities(id),
    decision     vote_decision NOT NULL,
    voted_at     TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    -- Contrainte forte : un seul vote par membre par proposition
    UNIQUE (proposal_id, identity_id)
);

CREATE INDEX idx_proposal_votes_proposal ON proposal_votes (proposal_id);

-- ── Trigger : expiration automatique ─────────────────────────────────────────
-- Passe les propositions expirées en statut 'expired' lors d'un SELECT.
-- Note : en production, un cron job ou le Service Social le fait explicitement.

CREATE OR REPLACE FUNCTION expire_stale_proposals()
RETURNS void AS $$
BEGIN
    UPDATE proposals
    SET status = 'expired', resolved_at = NOW()
    WHERE status = 'open'
      AND expires_at < NOW();
END;
$$ LANGUAGE plpgsql;

-- ── Trigger : immuabilité d'une proposition résolue ───────────────────────────
-- Une proposition approuvée, rejetée ou expirée ne peut plus être modifiée.

CREATE OR REPLACE FUNCTION prevent_resolved_proposal_mutation()
RETURNS TRIGGER AS $$
BEGIN
    IF OLD.status IN ('approved', 'rejected', 'expired') THEN
        IF NEW.status <> OLD.status OR NEW.resolved_at <> OLD.resolved_at THEN
            RAISE EXCEPTION
                'MASECURE_INVARIANT: Cannot mutate resolved proposal %', OLD.id;
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER enforce_proposal_immutability
    BEFORE UPDATE ON proposals
    FOR EACH ROW EXECUTE FUNCTION prevent_resolved_proposal_mutation();

-- ── Vue : proposals_with_stats ────────────────────────────────────────────────
-- Vue agrégée pour le tableau de bord de gouvernance.

CREATE OR REPLACE VIEW proposals_with_stats AS
SELECT
    p.id,
    p.group_id,
    p.proposed_by,
    p.base_config_id,
    p.new_config_id,
    p.diff_summary,
    p.status,
    p.quorum_pct,
    p.expires_at,
    p.created_at,
    p.resolved_at,
    -- Votes agrégés
    COUNT(pv.id) FILTER (WHERE pv.decision = 'approve') AS approve_count,
    COUNT(pv.id) FILTER (WHERE pv.decision = 'reject')  AS reject_count,
    COUNT(pv.id)                                          AS total_votes
FROM proposals p
LEFT JOIN proposal_votes pv ON pv.proposal_id = p.id
GROUP BY p.id;

COMMENT ON TABLE proposals IS
    'Propositions de changement de configuration de groupe soumises au vote des membres.';
COMMENT ON TABLE proposal_votes IS
    'Votes des membres actifs sur les propositions de configuration.';
