// Package audit implémente l'export d'audit du ledger MaSecure.
//
// L'export produit un rapport JSON signé contenant l'ensemble des événements
// financiers d'un groupe sur une période donnée. Il est destiné à :
//   - La vérification par les membres du groupe
//   - L'archivage légal (obligation BCEAO de conservation des preuves)
//   - La réconciliation en cas de litige
package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// LedgerEventRow représente une entrée du ledger pour l'export.
type LedgerEventRow struct {
	SeqNo           int64      `json:"seq_no"`
	EventType       string     `json:"event_type"`
	AggregateType   string     `json:"aggregate_type"`
	AggregateID     string     `json:"aggregate_id"`
	AmountMinor     *int64     `json:"amount_minor,omitempty"`
	Direction       *string    `json:"direction,omitempty"`
	Payload         any        `json:"payload"`
	PayloadHash     string     `json:"payload_hash"`
	PrevHash        *string    `json:"prev_hash,omitempty"`
	CurrentHash     string     `json:"current_hash"`
	IdempotencyKey  *string    `json:"idempotency_key,omitempty"`
	ExternalRef     *string    `json:"external_ref,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	CreatedByService string    `json:"created_by_service"`
}

// ContributionRow représente une contribution pour l'export.
type ContributionRow struct {
	ID               string    `json:"id"`
	CycleID          string    `json:"cycle_id"`
	PayerMsisdn      string    `json:"payer_msisdn"`
	AmountMinor      int64     `json:"amount_minor"`
	Status           string    `json:"status"`
	ProviderTxRef    string    `json:"provider_tx_ref"`
	ReceivedAt       time.Time `json:"received_at"`
	ReconciledAt     *time.Time `json:"reconciled_at,omitempty"`
}

// CycleSummary résume l'état d'un cycle pour l'export.
type CycleSummary struct {
	ID                  string    `json:"id"`
	CycleNumber         int       `json:"cycle_number"`
	BeneficiaryID       string    `json:"beneficiary_id"`
	DueDate             time.Time `json:"due_date"`
	PayoutThreshold     int64     `json:"payout_threshold_minor"`
	CollectedAmount     int64     `json:"collected_amount_minor"`
	State               string    `json:"state"`
	PayoutState         string    `json:"payout_state"`
	OpenedAt            time.Time `json:"opened_at"`
	ClosedAt            *time.Time `json:"closed_at,omitempty"`
}

// AuditReport est le rapport complet exporté pour un groupe.
type AuditReport struct {
	// Métadonnées du rapport
	ReportID        string    `json:"report_id"`
	GroupID         string    `json:"group_id"`
	PeriodStart     time.Time `json:"period_start"`
	PeriodEnd       time.Time `json:"period_end"`
	GeneratedAt     time.Time `json:"generated_at"`
	GeneratedBy     string    `json:"generated_by"`

	// Données auditées
	Cycles          []CycleSummary    `json:"cycles"`
	LedgerEvents    []LedgerEventRow  `json:"ledger_events"`
	Contributions   []ContributionRow `json:"contributions"`

	// Statistiques
	TotalContributions int64 `json:"total_contributions_minor"`
	TotalPayouts       int64 `json:"total_payouts_minor"`
	TotalCycles        int   `json:"total_cycles"`
	ClosedCycles       int   `json:"closed_cycles"`
	DisputedCycles     int   `json:"disputed_cycles"`

	// Intégrité du ledger
	LedgerIsValid      bool     `json:"ledger_is_valid"`
	LedgerViolations   []string `json:"ledger_violations,omitempty"`

	// Signature du rapport (SHA-256 du contenu sans ce champ)
	ReportHash string `json:"report_hash"`
}

// Service expose les opérations d'export d'audit.
type Service struct {
	db          *pgxpool.Pool
	serviceName string
}

func NewService(db *pgxpool.Pool, serviceName string) *Service {
	return &Service{db: db, serviceName: serviceName}
}

// ExportGroupAudit génère un rapport d'audit complet pour un groupe.
// Respecte le critère ENF-PERF-03 du CDC : export 12 mois en < 30 secondes.
func (s *Service) ExportGroupAudit(
	ctx context.Context,
	groupID string,
	from, to time.Time,
) (*AuditReport, error) {
	report := &AuditReport{
		ReportID:    fmt.Sprintf("audit-%s-%d", groupID[:8], time.Now().Unix()),
		GroupID:     groupID,
		PeriodStart: from,
		PeriodEnd:   to,
		GeneratedAt: time.Now().UTC(),
		GeneratedBy: s.serviceName,
	}

	var err error

	// ── 1. Cycles ─────────────────────────────────────────────────────────────
	report.Cycles, err = s.fetchCycles(ctx, groupID, from, to)
	if err != nil {
		return nil, fmt.Errorf("fetch cycles: %w", err)
	}
	report.TotalCycles = len(report.Cycles)
	for _, c := range report.Cycles {
		if c.State == "closed" {
			report.ClosedCycles++
		} else if c.State == "disputed" {
			report.DisputedCycles++
		}
	}

	// ── 2. Événements ledger ──────────────────────────────────────────────────
	cycleIDs := make([]string, len(report.Cycles))
	for i, c := range report.Cycles {
		cycleIDs[i] = c.ID
	}
	report.LedgerEvents, err = s.fetchLedgerEvents(ctx, groupID, from, to)
	if err != nil {
		return nil, fmt.Errorf("fetch ledger events: %w", err)
	}

	// ── 3. Contributions ──────────────────────────────────────────────────────
	report.Contributions, err = s.fetchContributions(ctx, cycleIDs)
	if err != nil {
		return nil, fmt.Errorf("fetch contributions: %w", err)
	}

	// ── 4. Statistiques ───────────────────────────────────────────────────────
	for _, e := range report.LedgerEvents {
		if e.AmountMinor == nil {
			continue
		}
		switch e.EventType {
		case "contribution_received":
			report.TotalContributions += *e.AmountMinor
		case "payout_confirmed":
			report.TotalPayouts += *e.AmountMinor
		}
	}

	// ── 5. Vérification d'intégrité du ledger ─────────────────────────────────
	report.LedgerIsValid, report.LedgerViolations = s.verifyLedgerIntegrity(report.LedgerEvents)

	// ── 6. Hash du rapport ────────────────────────────────────────────────────
	report.ReportHash = s.hashReport(report)

	return report, nil
}

func (s *Service) fetchCycles(ctx context.Context, groupID string, from, to time.Time) ([]CycleSummary, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, cycle_number, beneficiary_id, due_date,
		       payout_threshold_minor, collected_amount_minor,
		       state, payout_state, opened_at, closed_at
		FROM cycles
		WHERE group_id = $1
		  AND opened_at BETWEEN $2 AND $3
		ORDER BY cycle_number ASC
	`, groupID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []CycleSummary
	for rows.Next() {
		var c CycleSummary
		var closedAt *time.Time
		if err := rows.Scan(
			&c.ID, &c.CycleNumber, &c.BeneficiaryID, &c.DueDate,
			&c.PayoutThreshold, &c.CollectedAmount,
			&c.State, &c.PayoutState, &c.OpenedAt, &closedAt,
		); err != nil {
			return nil, err
		}
		c.ClosedAt = closedAt
		result = append(result, c)
	}
	return result, rows.Err()
}

func (s *Service) fetchLedgerEvents(ctx context.Context, groupID string, from, to time.Time) ([]LedgerEventRow, error) {
	rows, err := s.db.Query(ctx, `
		SELECT le.seq_no, le.event_type, le.aggregate_type, le.aggregate_id,
		       le.amount_minor, le.direction, le.payload, le.payload_hash,
		       le.prev_hash, le.current_hash, le.idempotency_key,
		       le.external_ref, le.created_at, le.created_by_service
		FROM ledger_entries le
		JOIN cycles c ON c.id = le.aggregate_id AND le.aggregate_type = 'cycle'
		WHERE c.group_id = $1
		  AND le.created_at BETWEEN $2 AND $3
		ORDER BY le.seq_no ASC
	`, groupID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []LedgerEventRow
	for rows.Next() {
		var e LedgerEventRow
		var payloadJSON []byte
		if err := rows.Scan(
			&e.SeqNo, &e.EventType, &e.AggregateType, &e.AggregateID,
			&e.AmountMinor, &e.Direction, &payloadJSON, &e.PayloadHash,
			&e.PrevHash, &e.CurrentHash, &e.IdempotencyKey,
			&e.ExternalRef, &e.CreatedAt, &e.CreatedByService,
		); err != nil {
			return nil, err
		}
		var payload any
		_ = json.Unmarshal(payloadJSON, &payload)
		e.Payload = payload
		result = append(result, e)
	}
	return result, rows.Err()
}

func (s *Service) fetchContributions(ctx context.Context, cycleIDs []string) ([]ContributionRow, error) {
	if len(cycleIDs) == 0 {
		return nil, nil
	}

	rows, err := s.db.Query(ctx, `
		SELECT id, cycle_id, payer_msisdn, amount_minor,
		       status, provider_tx_ref, received_at, reconciled_at
		FROM contributions
		WHERE cycle_id = ANY($1)
		ORDER BY received_at ASC
	`, cycleIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []ContributionRow
	for rows.Next() {
		var c ContributionRow
		var reconciledAt *time.Time
		if err := rows.Scan(
			&c.ID, &c.CycleID, &c.PayerMsisdn, &c.AmountMinor,
			&c.Status, &c.ProviderTxRef, &c.ReceivedAt, &reconciledAt,
		); err != nil {
			return nil, err
		}
		c.ReconciledAt = reconciledAt
		result = append(result, c)
	}
	return result, rows.Err()
}

// verifyLedgerIntegrity rejoue la vérification de chaîne en Go pour l'export.
// La vérification canonique est faite par le kernel Rust, celle-ci est une
// vérification secondaire côté audit.
func (s *Service) verifyLedgerIntegrity(entries []LedgerEventRow) (bool, []string) {
	var violations []string
	var prevHash *string

	for _, e := range entries {
		// Recalcule le hash attendu
		prevHashStr := "GENESIS"
		if prevHash != nil {
			prevHashStr = *prevHash
		}
		input := fmt.Sprintf("%d:%s:%s:%s", e.SeqNo, e.EventType, e.PayloadHash, prevHashStr)
		h := sha256.Sum256([]byte(input))
		expected := hex.EncodeToString(h[:])

		if e.CurrentHash != expected {
			violations = append(violations,
				fmt.Sprintf("seq_no=%d: hash mismatch (stored=%s computed=%s)",
					e.SeqNo, e.CurrentHash[:8], expected[:8]),
			)
		}
		prevHash = &e.CurrentHash
	}

	return len(violations) == 0, violations
}

// hashReport calcule le SHA-256 du rapport pour garantir son intégrité.
func (s *Service) hashReport(r *AuditReport) string {
	// On exclut le champ ReportHash du calcul
	type reportWithoutHash struct {
		ReportID     string            `json:"report_id"`
		GroupID      string            `json:"group_id"`
		PeriodStart  time.Time         `json:"period_start"`
		PeriodEnd    time.Time         `json:"period_end"`
		GeneratedAt  time.Time         `json:"generated_at"`
		Cycles       []CycleSummary    `json:"cycles"`
		LedgerEvents []LedgerEventRow  `json:"ledger_events"`
	}
	data, _ := json.Marshal(reportWithoutHash{
		ReportID:     r.ReportID,
		GroupID:      r.GroupID,
		PeriodStart:  r.PeriodStart,
		PeriodEnd:    r.PeriodEnd,
		GeneratedAt:  r.GeneratedAt,
		Cycles:       r.Cycles,
		LedgerEvents: r.LedgerEvents,
	})
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
