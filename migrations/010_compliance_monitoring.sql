-- =============================================================================
-- MaSecure — Migration 010 : Conformité & Monitoring (Phase 5)
-- =============================================================================
--
-- Implémente les obligations légales BCEAO :
--   - Archives de transactions avec rétention 10 ans (BCEAORetentionYears)
--   - Signalement AML/LCB-FT des transactions suspectes
--   - Journal de monitoring des anomalies détectées
--   - Métriques opérationnelles pour alertes automatiques

-- ── Table : compliance_archives ───────────────────────────────────────────────

CREATE TABLE compliance_archives (
    id                UUID         PRIMARY KEY,
    group_id          UUID         NOT NULL REFERENCES tontine_groups(id),
    period_start      TIMESTAMPTZ  NOT NULL,
    period_end        TIMESTAMPTZ  NOT NULL,
    generated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    -- Rétention légale BCEAO : 10 ans minimum
    retain_until      TIMESTAMPTZ  NOT NULL,
    total_volume_minor BIGINT      NOT NULL DEFAULT 0,
    transaction_count INTEGER      NOT NULL DEFAULT 0,
    -- SHA-256 du contenu pour vérification d'intégrité
    content_hash      CHAR(64)     NOT NULL,
    -- normal | flagged_aml | under_review | submitted_to_authorities
    compliance_status VARCHAR(40)  NOT NULL DEFAULT 'normal',
    -- Horodatage de soumission aux autorités si applicable
    submitted_at      TIMESTAMPTZ,
    CONSTRAINT chk_period_valid CHECK (period_end > period_start)
);

-- Interdit la suppression avant la date de rétention légale
CREATE OR REPLACE FUNCTION prevent_early_archive_deletion()
RETURNS TRIGGER AS $$
BEGIN
    IF OLD.retain_until > NOW() THEN
        RAISE EXCEPTION
            'MASECURE_COMPLIANCE: Archive % cannot be deleted before retention date %',
            OLD.id, OLD.retain_until;
    END IF;
    RETURN OLD;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER enforce_archive_retention
    BEFORE DELETE ON compliance_archives
    FOR EACH ROW EXECUTE FUNCTION prevent_early_archive_deletion();

CREATE INDEX idx_compliance_archives_group ON compliance_archives (group_id, period_start DESC);
CREATE INDEX idx_compliance_archives_status ON compliance_archives (compliance_status) WHERE compliance_status != 'normal';

-- ── Table : aml_flags ─────────────────────────────────────────────────────────

CREATE TABLE aml_flags (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id        UUID         NOT NULL REFERENCES tontine_groups(id),
    cycle_id        UUID         REFERENCES cycles(id),
    -- MSISDN impliqué (peut être inconnu si quarantaine)
    msisdn          VARCHAR(20)  NOT NULL,
    identity_id     UUID         REFERENCES identities(id),
    amount_minor    BIGINT       NOT NULL,
    -- large_transaction | velocity_breach | repeat_quarantine | pattern_anomaly
    reason          VARCHAR(60)  NOT NULL,
    flagged_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    reviewed        BOOLEAN      NOT NULL DEFAULT FALSE,
    reviewed_at     TIMESTAMPTZ,
    reviewed_by     UUID         REFERENCES identities(id),
    review_outcome  VARCHAR(40),  -- cleared | escalated | reported_bceao
    notes           TEXT
);

CREATE INDEX idx_aml_flags_pending ON aml_flags (flagged_at DESC) WHERE reviewed = FALSE;
CREATE INDEX idx_aml_flags_group ON aml_flags (group_id, flagged_at DESC);

-- ── Table : monitoring_events ─────────────────────────────────────────────────
-- Journal de toutes les anomalies détectées par le module anomaly.

CREATE TABLE monitoring_events (
    id           UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type   VARCHAR(60)  NOT NULL,
    severity     VARCHAR(20)  NOT NULL,  -- INFO | WARNING | CRITICAL
    group_id     UUID         REFERENCES tontine_groups(id),
    cycle_id     UUID         REFERENCES cycles(id),
    msisdn       VARCHAR(20),
    description  TEXT         NOT NULL,
    payload      JSONB,
    detected_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    -- Statut de résolution
    resolved     BOOLEAN      NOT NULL DEFAULT FALSE,
    resolved_at  TIMESTAMPTZ,
    auto_resolved BOOLEAN     NOT NULL DEFAULT FALSE
);

CREATE INDEX idx_monitoring_unresolved ON monitoring_events (severity, detected_at DESC) WHERE resolved = FALSE;
CREATE INDEX idx_monitoring_group ON monitoring_events (group_id, detected_at DESC);

-- ── Table : operator_multi_config ────────────────────────────────────────────
-- Support multi-opérateurs Phase 5 : chaque groupe peut configurer
-- un opérateur préféré par défaut et des règles de fallback.

CREATE TABLE operator_routing_rules (
    id              UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id        UUID          NOT NULL REFERENCES tontine_groups(id),
    -- Opérateur prioritaire pour les payouts sortants
    primary_provider wallet_provider NOT NULL DEFAULT 'orange_money',
    -- Fallback si le provider principal échoue
    fallback_provider wallet_provider,
    -- Actif ou désactivé
    active          BOOLEAN       NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    UNIQUE (group_id)
);

-- ── Vue : ops_health_dashboard ────────────────────────────────────────────────
-- Tableau de bord opérationnel pour le monitoring en temps réel.

CREATE OR REPLACE VIEW ops_health_dashboard AS
SELECT
    -- Outbox santé
    COUNT(*) FILTER (WHERE oe.status = 'pending')     AS outbox_pending,
    COUNT(*) FILTER (WHERE oe.status = 'processing')  AS outbox_processing,
    COUNT(*) FILTER (WHERE oe.status = 'failed')      AS outbox_failed,
    COUNT(*) FILTER (WHERE oe.status = 'dead_letter') AS outbox_dead_letter,
    -- Cycles actifs
    COUNT(DISTINCT c.id) FILTER (WHERE c.state = 'committed')         AS cycles_committed,
    COUNT(DISTINCT c.id) FILTER (WHERE c.state = 'payout_triggered')  AS cycles_payout_triggered,
    COUNT(DISTINCT c.id) FILTER (WHERE c.state = 'disputed')          AS cycles_disputed,
    -- Contributions dernières 24h
    COUNT(DISTINCT co.id) FILTER (WHERE co.received_at > NOW() - INTERVAL '24 hours')  AS contributions_24h,
    COUNT(DISTINCT co.id) FILTER (WHERE co.status = 'quarantined' AND co.received_at > NOW() - INTERVAL '24 hours') AS quarantines_24h,
    -- Anomalies non résolues
    COUNT(DISTINCT me.id) FILTER (WHERE me.resolved = FALSE AND me.severity = 'CRITICAL') AS critical_anomalies,
    COUNT(DISTINCT me.id) FILTER (WHERE me.resolved = FALSE AND me.severity = 'WARNING')  AS warning_anomalies,
    -- Timestamp
    NOW() AS refreshed_at
FROM outbox_events oe
FULL OUTER JOIN cycles c ON TRUE
FULL OUTER JOIN contributions co ON TRUE
FULL OUTER JOIN monitoring_events me ON TRUE;

COMMENT ON TABLE compliance_archives IS
    'Archives légales de transactions MaSecure — rétention 10 ans BCEAO. Suppression interdite avant retain_until.';
COMMENT ON TABLE aml_flags IS
    'Signalements LCB-FT (Lutte Contre le Blanchiment et le Financement du Terrorisme).';
COMMENT ON TABLE monitoring_events IS
    'Journal des anomalies détectées par le module de monitoring automatique.';
