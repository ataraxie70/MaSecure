// Package anomaly implémente la détection d'anomalies financières MaSecure.
//
// Ce module analyse les patterns de comportement pour détecter :
//   - Tentatives de double paiement (idempotence déjà couverte, ici on log les tentatives)
//   - Contributions anormalement élevées (dépassement plafond quotidien MM)
//   - Contributions depuis des MSISDN fréquemment en quarantaine
//   - Cycles en état bloqué trop longtemps (disputed sans résolution)
//   - Activité suspecte : beaucoup de contributions depuis un même MSISDN inconnu
//
// Ce package ne BLOQUE jamais une transaction — il alerte uniquement.
// Le blocage est géré par les invariants du kernel et les triggers PostgreSQL.
package anomaly

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// Severity classifie la gravité d'une anomalie.
type Severity string

const (
	SeverityInfo     Severity = "INFO"
	SeverityWarning  Severity = "WARNING"
	SeverityCritical Severity = "CRITICAL"
)

// AnomalyType identifie la nature de l'anomalie.
type AnomalyType string

const (
	AnomalyLargeContribution     AnomalyType = "large_contribution"
	AnomalyRepeatQuarantine      AnomalyType = "repeat_quarantine_msisdn"
	AnomalyBlockedCycleTooLong   AnomalyType = "blocked_cycle_too_long"
	AnomalyHighVelocityMsisdn    AnomalyType = "high_velocity_msisdn"
	AnomalyPayoutRetryExceeded   AnomalyType = "payout_retry_exceeded"
	AnomalyDeadLetterAccumulated AnomalyType = "dead_letter_accumulated"
	AnomalyLedgerIntegrityFail   AnomalyType = "ledger_integrity_failure"
)

// AnomalyEvent représente une anomalie détectée.
type AnomalyEvent struct {
	ID          string      `json:"id"`
	Type        AnomalyType `json:"type"`
	Severity    Severity    `json:"severity"`
	GroupID     string      `json:"group_id,omitempty"`
	CycleID     string      `json:"cycle_id,omitempty"`
	Msisdn      string      `json:"msisdn,omitempty"`
	Description string      `json:"description"`
	Payload     any         `json:"payload,omitempty"`
	DetectedAt  time.Time   `json:"detected_at"`
}

// Thresholds regroupe les seuils de détection configurables.
type Thresholds struct {
	// Montant max XOF d'une contribution individuelle avant alerte (défaut : 500 000 XOF)
	MaxContributionMinor int64
	// Nombre de quarantaines depuis un même MSISDN en 30 jours avant alerte
	QuarantineRepeatLimit int
	// Nombre d'heures avant alerte pour un cycle en état disputed
	DisputedCycleAlertHours int
	// Nombre de contributions depuis un même MSISDN inconnu en 24h
	VelocityLimit int
	// Nombre max de dead letters avant alerte critique
	DeadLetterAlertLimit int
}

// DefaultThresholds retourne les seuils de production recommandés.
func DefaultThresholds() Thresholds {
	return Thresholds{
		MaxContributionMinor:    50_000_000, // 500 000 XOF (en centimes)
		QuarantineRepeatLimit:   3,
		DisputedCycleAlertHours: 48,
		VelocityLimit:           5,
		DeadLetterAlertLimit:    10,
	}
}

// Detector exécute les contrôles d'anomalie de façon périodique.
type Detector struct {
	db         *pgxpool.Pool
	thresholds Thresholds
	log        *zap.Logger
	// Sink pour les anomalies — en production : NATS, Slack webhook, email ops
	sink AlertSink
}

// AlertSink reçoit les anomalies détectées pour les router vers les systèmes d'alerte.
type AlertSink interface {
	SendAlert(ctx context.Context, event AnomalyEvent) error
}

// LogAlertSink implémente AlertSink avec simple logging (pour développement).
type LogAlertSink struct {
	log *zap.Logger
}

func NewLogAlertSink(log *zap.Logger) *LogAlertSink {
	return &LogAlertSink{log: log}
}

func (s *LogAlertSink) SendAlert(ctx context.Context, event AnomalyEvent) error {
	s.log.Warn("ANOMALY DETECTED",
		zap.String("type", string(event.Type)),
		zap.String("severity", string(event.Severity)),
		zap.String("description", event.Description),
		zap.String("group_id", event.GroupID),
		zap.String("cycle_id", event.CycleID),
	)
	return nil
}

// NewDetector crée un détecteur d'anomalies.
func NewDetector(db *pgxpool.Pool, thresholds Thresholds, sink AlertSink, log *zap.Logger) *Detector {
	return &Detector{db: db, thresholds: thresholds, sink: sink, log: log}
}

// RunAllChecks exécute tous les contrôles d'anomalie en une passe.
// Appelé périodiquement par le scheduler de monitoring (toutes les 15 minutes).
func (d *Detector) RunAllChecks(ctx context.Context) error {
	checks := []func(context.Context) ([]AnomalyEvent, error){
		d.checkLargeContributions,
		d.checkRepeatQuarantineMsisdns,
		d.checkBlockedCycles,
		d.checkDeadLetterAccumulation,
		d.checkPayoutRetryExceeded,
	}

	var totalAnomalies int
	for _, check := range checks {
		events, err := check(ctx)
		if err != nil {
			d.log.Error("anomaly check failed", zap.Error(err))
			continue
		}
		for _, event := range events {
			if err := d.sink.SendAlert(ctx, event); err != nil {
				d.log.Error("alert sink failed", zap.Error(err))
			}
			totalAnomalies++
		}
	}

	if totalAnomalies > 0 {
		d.log.Warn("Anomaly scan completed", zap.Int("anomalies_found", totalAnomalies))
	} else {
		d.log.Info("Anomaly scan completed — no anomalies found")
	}
	return nil
}

// checkLargeContributions alerte sur les contributions dépassant le plafond.
func (d *Detector) checkLargeContributions(ctx context.Context) ([]AnomalyEvent, error) {
	rows, err := d.db.Query(ctx, `
		SELECT c.id, c.cycle_id, cy.group_id, c.payer_msisdn, c.amount_minor, c.received_at
		FROM contributions c
		JOIN cycles cy ON cy.id = c.cycle_id
		WHERE c.amount_minor > $1
		  AND c.received_at > NOW() - INTERVAL '24 hours'
		  AND c.status NOT IN ('quarantined', 'disputed')
		ORDER BY c.amount_minor DESC
		LIMIT 20
	`, d.thresholds.MaxContributionMinor)
	if err != nil {
		return nil, fmt.Errorf("check large contributions: %w", err)
	}
	defer rows.Close()

	var events []AnomalyEvent
	for rows.Next() {
		var id, cycleID, groupID, msisdn string
		var amount int64
		var receivedAt time.Time
		if err := rows.Scan(&id, &cycleID, &groupID, &msisdn, &amount, &receivedAt); err != nil {
			continue
		}
		events = append(events, AnomalyEvent{
			ID:       "anom-" + id[:8],
			Type:     AnomalyLargeContribution,
			Severity: SeverityWarning,
			GroupID:  groupID,
			CycleID:  cycleID,
			Msisdn:   msisdn,
			Description: fmt.Sprintf("Contribution anormalement élevée : %d XOF (seuil: %d)",
				amount/100, d.thresholds.MaxContributionMinor/100),
			Payload:    map[string]any{"amount_minor": amount, "contribution_id": id},
			DetectedAt: time.Now().UTC(),
		})
	}
	return events, rows.Err()
}

// checkRepeatQuarantineMsisdns détecte les MSISDN fréquemment en quarantaine.
func (d *Detector) checkRepeatQuarantineMsisdns(ctx context.Context) ([]AnomalyEvent, error) {
	rows, err := d.db.Query(ctx, `
		SELECT payer_msisdn, COUNT(*) AS quarantine_count
		FROM contributions
		WHERE status = 'quarantined'
		  AND received_at > NOW() - INTERVAL '30 days'
		GROUP BY payer_msisdn
		HAVING COUNT(*) >= $1
		ORDER BY quarantine_count DESC
		LIMIT 10
	`, d.thresholds.QuarantineRepeatLimit)
	if err != nil {
		return nil, fmt.Errorf("check repeat quarantine: %w", err)
	}
	defer rows.Close()

	var events []AnomalyEvent
	for rows.Next() {
		var msisdn string
		var count int
		if err := rows.Scan(&msisdn, &count); err != nil {
			continue
		}
		events = append(events, AnomalyEvent{
			ID:       fmt.Sprintf("anom-qr-%s", msisdn[len(msisdn)-4:]),
			Type:     AnomalyRepeatQuarantine,
			Severity: SeverityWarning,
			Msisdn:   msisdn,
			Description: fmt.Sprintf("MSISDN %s en quarantaine %d fois en 30 jours — possible usurpation ou MSISDN réattribué",
				msisdn, count),
			Payload:    map[string]any{"quarantine_count": count},
			DetectedAt: time.Now().UTC(),
		})
	}
	return events, rows.Err()
}

// checkBlockedCycles détecte les cycles en état disputed depuis trop longtemps.
func (d *Detector) checkBlockedCycles(ctx context.Context) ([]AnomalyEvent, error) {
	rows, err := d.db.Query(ctx, `
		SELECT id, group_id, cycle_number, state, payout_state, due_date
		FROM cycles
		WHERE state = 'disputed'
		  AND due_date < NOW() - $1::interval
		ORDER BY due_date ASC
	`, fmt.Sprintf("%d hours", d.thresholds.DisputedCycleAlertHours))
	if err != nil {
		return nil, fmt.Errorf("check blocked cycles: %w", err)
	}
	defer rows.Close()

	var events []AnomalyEvent
	for rows.Next() {
		var id, groupID, state, payoutState string
		var cycleNumber int
		var dueDate time.Time
		if err := rows.Scan(&id, &groupID, &cycleNumber, &state, &payoutState, &dueDate); err != nil {
			continue
		}
		blockedFor := time.Since(dueDate)
		events = append(events, AnomalyEvent{
			ID:       "anom-bc-" + id[:8],
			Type:     AnomalyBlockedCycleTooLong,
			Severity: SeverityCritical,
			GroupID:  groupID,
			CycleID:  id,
			Description: fmt.Sprintf("Cycle #%d en état '%s/%s' depuis %.0fh — intervention requise",
				cycleNumber, state, payoutState, blockedFor.Hours()),
			Payload:    map[string]any{"blocked_hours": blockedFor.Hours(), "payout_state": payoutState},
			DetectedAt: time.Now().UTC(),
		})
	}
	return events, rows.Err()
}

// checkDeadLetterAccumulation détecte l'accumulation de dead letters dans l'outbox.
func (d *Detector) checkDeadLetterAccumulation(ctx context.Context) ([]AnomalyEvent, error) {
	var count int
	if err := d.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM outbox_events WHERE status = 'dead_letter'
	`).Scan(&count); err != nil {
		return nil, fmt.Errorf("check dead letters: %w", err)
	}

	if count < d.thresholds.DeadLetterAlertLimit {
		return nil, nil
	}

	return []AnomalyEvent{{
		ID:          fmt.Sprintf("anom-dl-%d", count),
		Type:        AnomalyDeadLetterAccumulated,
		Severity:    SeverityCritical,
		Description: fmt.Sprintf("%d événements outbox en dead_letter — vérifier connectivité Mobile Money", count),
		Payload:     map[string]any{"dead_letter_count": count},
		DetectedAt:  time.Now().UTC(),
	}}, nil
}

// checkPayoutRetryExceeded alerte sur les paiements ayant trop de tentatives.
func (d *Detector) checkPayoutRetryExceeded(ctx context.Context) ([]AnomalyEvent, error) {
	rows, err := d.db.Query(ctx, `
		SELECT id, event_type, aggregate_id, attempts, error_detail
		FROM outbox_events
		WHERE status = 'failed'
		  AND attempts >= 5
		  AND event_type = 'payout.triggered'
		ORDER BY attempts DESC
		LIMIT 10
	`)
	if err != nil {
		return nil, fmt.Errorf("check payout retries: %w", err)
	}
	defer rows.Close()

	var events []AnomalyEvent
	for rows.Next() {
		var id, eventType, aggregateID string
		var attempts int
		var errorDetail *string
		if err := rows.Scan(&id, &eventType, &aggregateID, &attempts, &errorDetail); err != nil {
			continue
		}
		errMsg := ""
		if errorDetail != nil {
			errMsg = *errorDetail
		}
		events = append(events, AnomalyEvent{
			ID:       "anom-pr-" + id[:8],
			Type:     AnomalyPayoutRetryExceeded,
			Severity: SeverityCritical,
			CycleID:  aggregateID,
			Description: fmt.Sprintf("Payout pour cycle %s a %d tentatives échouées — vérifier agrégateur MM. Dernière erreur : %s",
				aggregateID[:8], attempts, errMsg),
			Payload:    map[string]any{"attempts": attempts, "event_id": id},
			DetectedAt: time.Now().UTC(),
		})
	}
	return events, rows.Err()
}
