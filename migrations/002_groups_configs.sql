-- =============================================================================
-- MaSecure — Migration 002 : Groupes et Configurations versionnées
-- =============================================================================
CREATE TYPE group_status  AS ENUM ('forming', 'active', 'paused', 'closed');
CREATE TYPE config_state  AS ENUM ('draft', 'review', 'committed', 'superseded');
CREATE TYPE cycle_period  AS ENUM ('weekly', 'biweekly', 'monthly');

CREATE TABLE tontine_groups (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name             VARCHAR(100) NOT NULL,
    founder_id       UUID        NOT NULL REFERENCES identities(id),
    status           group_status NOT NULL DEFAULT 'forming',
    active_config_id UUID,  -- FK ajoutée après group_configs
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TRIGGER tontine_groups_updated_at
    BEFORE UPDATE ON tontine_groups
    FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

-- Configuration versionnée : chaque version est immuable une fois committée.
-- Un seul diff entre deux versions est jamais appliqué rétroactivement.
CREATE TABLE group_configs (
    id                    UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id              UUID        NOT NULL REFERENCES tontine_groups(id),
    version_no            INTEGER     NOT NULL,
    amount_minor          BIGINT      NOT NULL CHECK (amount_minor > 0),
    periodicity           cycle_period NOT NULL,
    payout_policy         JSONB       NOT NULL DEFAULT '{"threshold_pct": 100, "advance_enabled": false, "pro_rata_enabled": false}'::jsonb,
    member_order          UUID[]      NOT NULL,  -- tableau ordonné des identity UUIDs
    quorum_pct            SMALLINT    NOT NULL DEFAULT 67 CHECK (quorum_pct BETWEEN 51 AND 100),
    state                 config_state NOT NULL DEFAULT 'draft',
    committed_at          TIMESTAMPTZ,
    effective_from_cycle  INTEGER,
    created_by            UUID        NOT NULL REFERENCES identities(id),
    prev_config_id        UUID        REFERENCES group_configs(id),
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Unicité version par groupe
CREATE UNIQUE INDEX idx_group_config_version ON group_configs (group_id, version_no);

-- Un seul config committed par groupe à la fois
CREATE UNIQUE INDEX idx_group_one_committed
    ON group_configs (group_id)
    WHERE state = 'committed';

-- FK circulaire tontine_groups → group_configs
ALTER TABLE tontine_groups
    ADD CONSTRAINT fk_active_config
    FOREIGN KEY (active_config_id) REFERENCES group_configs(id) DEFERRABLE INITIALLY DEFERRED;

-- Membres d'un groupe (table pivot pour les statuts individuels)
CREATE TABLE group_members (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id     UUID        NOT NULL REFERENCES tontine_groups(id),
    identity_id  UUID        NOT NULL REFERENCES identities(id),
    joined_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    status       VARCHAR(20) NOT NULL DEFAULT 'active' CHECK (status IN ('active','suspended','left')),
    UNIQUE (group_id, identity_id)
);

-- Propositions de modification de configuration (gouvernance)
CREATE TABLE config_proposals (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id     UUID        NOT NULL REFERENCES tontine_groups(id),
    proposed_by  UUID        NOT NULL REFERENCES identities(id),
    base_config_id UUID      NOT NULL REFERENCES group_configs(id),
    new_config_id  UUID      NOT NULL REFERENCES group_configs(id),
    diff_summary   JSONB     NOT NULL,  -- résumé des changements calculés
    status         VARCHAR(20) NOT NULL DEFAULT 'open' CHECK (status IN ('open','approved','rejected','expired')),
    votes          JSONB     NOT NULL DEFAULT '[]'::jsonb,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at  TIMESTAMPTZ
);
