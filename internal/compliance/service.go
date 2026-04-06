// Package compliance gère les obligations légales d'archivage BCEAO.
//
// La BCEAO (Banque Centrale des États de l'Afrique de l'Ouest) impose :
//   - Conservation des preuves de transaction pendant 10 ans minimum
//   - Journaux d'audit immuables pour toute opération financière
//   - Capacité de restitution à la demande des autorités
//   - Signalement des transactions suspectes (LCB-FT)
//
// Ce package implémente :
//   1. La politique de rétention des données (soft-delete avec expiry)
//   2. La génération d'archives légales signées (hash SHA-256)
//   3. Le rapport de conformité KYC/AML simplifié
//   4. L'API de restitution pour les autorités
package compliance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

const (
	// BCEAORetentionYears : durée légale de conservation des preuves de transaction
	BCEAORetentionYears = 10

	// AMLThresholdMinor : seuil de signalement AML en XOF centimes (1 000 000 XOF = 1M)
	// À ajuster selon la réglementation BCEAO en vigueur
	AMLThresholdMinor = 100_000_000 // 1 000 000 XOF

	// KYCVerificationRequiredAboveMinor : seuil KYC renforcé (500 000 XOF)
	KYCVerificationRequiredAboveMinor = 50_000_000
)

// ArchiveRecord représente une archive légale d'un groupe pour une période.
type ArchiveRecord struct {
	ArchiveID       string    `json:"archive_id"`
	GroupID         string    `json:"group_id"`
	PeriodStart     time.Time `json:"period_start"`
	PeriodEnd       time.Time `json:"period_end"`
	GeneratedAt     time.Time `json:"generated_at"`
	RetainUntil     time.Time `json:"retain_until"` // GeneratedAt + 10 ans
	TotalVolume     int64     `json:"total_volume_minor"`
	TransactionCount int      `json:"transaction_count"`
	// SHA-256 du contenu JSON pour vérification d'intégrité
	ContentHash string `json:"content_hash"`
	// Statut de restitution (normal | flagged_aml | under_review)
	ComplianceStatus string `json:"compliance_status"`
}

// AMLFlag représente une transaction signalée pour analyse AML.
type AMLFlag struct {
	FlagID      string    `json:"flag_id"`
	GroupID     string    `json:"group_id"`
	CycleID     string    `json:"cycle_id"`
	Msisdn      string    `json:"msisdn"`
	AmountMinor int64     `json:"amount_minor"`
	Reason      string    `json:"reason"`
	FlaggedAt   time.Time `json:"flagged_at"`
	Reviewed    bool      `json:"reviewed"`
}

// KYCStatus résume le niveau de vérification d'un membre.
type KYCStatus struct {
	IdentityID   string    `json:"identity_id"`
	TrustLevel   int       `json:"trust_level"` // 0=inconnu, 1=non-vérifié, 2=vérifié, 3=certifié
	VerifiedAt   *time.Time `json:"verified_at,omitempty"`
	WalletCount  int       `json:"wallet_count"`
	// Total transactionnel sur 30 jours — pour évaluation KYC renforcé
	Volume30dMinor int64  `json:"volume_30d_minor"`
	RequiresEnhancedKYC bool `json:"requires_enhanced_kyc"`
}

// Service implémente les vérifications de conformité.
type Service struct {
	db  *pgxpool.Pool
	log *zap.Logger
}

func NewService(db *pgxpool.Pool, log *zap.Logger) *Service {
	return &Service{db: db, log: log}
}

// GenerateGroupArchive crée une archive légale signée pour un groupe.
// Doit être appelé mensuellement ou sur demande des autorités.
func (s *Service) GenerateGroupArchive(
	ctx context.Context,
	groupID string,
	periodStart, periodEnd time.Time,
) (*ArchiveRecord, error) {
	// Agréger les données financières de la période
	var totalVolume int64
	var txCount int
	if err := s.db.QueryRow(ctx, `
		SELECT
		    COALESCE(SUM(le.amount_minor), 0),
		    COUNT(le.id)
		FROM ledger_entries le
		JOIN cycles c ON c.id = le.aggregate_id AND le.aggregate_type = 'cycle'
		WHERE c.group_id = $1
		  AND le.created_at BETWEEN $2 AND $3
		  AND le.event_type IN ('contribution_received', 'payout_confirmed')
	`, groupID, periodStart, periodEnd).Scan(&totalVolume, &txCount); err != nil {
		return nil, fmt.Errorf("compute archive stats: %w", err)
	}

	// Vérifier les flags AML
	complianceStatus := "normal"
	var amlCount int
	s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM contributions c
		JOIN cycles cy ON cy.id = c.cycle_id
		WHERE cy.group_id = $1
		  AND c.amount_minor > $2
		  AND c.received_at BETWEEN $3 AND $4
	`, groupID, AMLThresholdMinor, periodStart, periodEnd).Scan(&amlCount)
	if amlCount > 0 {
		complianceStatus = "flagged_aml"
		s.log.Warn("AML flag triggered for group",
			zap.String("group_id", groupID),
			zap.Int("flagged_transactions", amlCount),
		)
	}

	archive := &ArchiveRecord{
		ArchiveID:        fmt.Sprintf("arch-%s-%d", groupID[:8], time.Now().Unix()),
		GroupID:          groupID,
		PeriodStart:      periodStart,
		PeriodEnd:        periodEnd,
		GeneratedAt:      time.Now().UTC(),
		RetainUntil:      time.Now().UTC().AddDate(BCEAORetentionYears, 0, 0),
		TotalVolume:      totalVolume,
		TransactionCount: txCount,
		ComplianceStatus: complianceStatus,
	}

	// Calculer le hash de l'archive pour vérification d'intégrité
	archive.ContentHash = s.computeArchiveHash(archive)

	// Persister l'archive
	if err := s.persistArchive(ctx, archive); err != nil {
		return nil, fmt.Errorf("persist archive: %w", err)
	}

	s.log.Info("Compliance archive generated",
		zap.String("archive_id", archive.ArchiveID),
		zap.String("group_id", groupID),
		zap.String("status", complianceStatus),
		zap.Int64("volume_minor", totalVolume),
	)

	return archive, nil
}

// CheckMemberKYC évalue le niveau KYC d'un membre et signale si renforcé requis.
func (s *Service) CheckMemberKYC(ctx context.Context, identityID string) (*KYCStatus, error) {
	status := &KYCStatus{IdentityID: identityID}

	// Trust level et vérification wallet
	if err := s.db.QueryRow(ctx, `
		SELECT
		    COALESCE(MAX(pi.trust_level), 0),
		    COUNT(wb.id),
		    MAX(wb.verified_at)
		FROM identities i
		LEFT JOIN payment_instruments pi ON pi.identity_id = i.id
		LEFT JOIN wallet_bindings wb ON wb.identity_id = i.id AND wb.status = 'active'
		WHERE i.id = $1
	`, identityID).Scan(&status.TrustLevel, &status.WalletCount, &status.VerifiedAt); err != nil {
		return nil, fmt.Errorf("check kyc: %w", err)
	}

	// Volume transactionnel 30 jours
	s.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(c.amount_minor), 0)
		FROM contributions c
		WHERE c.payer_identity_id = $1
		  AND c.received_at > NOW() - INTERVAL '30 days'
		  AND c.status = 'reconciled'
	`, identityID).Scan(&status.Volume30dMinor)

	status.RequiresEnhancedKYC = status.Volume30dMinor > KYCVerificationRequiredAboveMinor
	if status.RequiresEnhancedKYC {
		s.log.Warn("Enhanced KYC required",
			zap.String("identity_id", identityID),
			zap.Int64("volume_30d_minor", status.Volume30dMinor),
		)
	}

	return status, nil
}

// RunPeriodicCompliance exécute les vérifications périodiques (appelé mensuellement).
func (s *Service) RunPeriodicCompliance(ctx context.Context) error {
	s.log.Info("Running periodic compliance checks")

	// 1. Vérifier les transactions dépassant le seuil AML
	rows, err := s.db.Query(ctx, `
		SELECT c.id, cy.group_id, c.cycle_id, c.payer_msisdn, c.amount_minor
		FROM contributions c
		JOIN cycles cy ON cy.id = c.cycle_id
		WHERE c.amount_minor > $1
		  AND c.received_at > NOW() - INTERVAL '30 days'
		  AND c.status = 'reconciled'
	`, AMLThresholdMinor)
	if err != nil {
		return fmt.Errorf("aml scan: %w", err)
	}
	defer rows.Close()

	var flagged int
	for rows.Next() {
		var id, groupID, cycleID, msisdn string
		var amount int64
		if err := rows.Scan(&id, &groupID, &cycleID, &msisdn, &amount); err != nil {
			continue
		}
		s.log.Warn("AML threshold exceeded",
			zap.String("contribution_id", id),
			zap.String("group_id", groupID),
			zap.Int64("amount_minor", amount),
		)
		flagged++
	}

	// 2. Vérifier les membres nécessitant un KYC renforcé
	kycRows, err := s.db.Query(ctx, `
		SELECT DISTINCT c.payer_identity_id
		FROM contributions c
		WHERE c.payer_identity_id IS NOT NULL
		  AND c.received_at > NOW() - INTERVAL '30 days'
		GROUP BY c.payer_identity_id
		HAVING SUM(c.amount_minor) > $1
	`, KYCVerificationRequiredAboveMinor)
	if err == nil {
		defer kycRows.Close()
		for kycRows.Next() {
			var identityID string
			if err := kycRows.Scan(&identityID); err == nil {
				s.CheckMemberKYC(ctx, identityID)
			}
		}
	}

	s.log.Info("Periodic compliance check completed",
		zap.Int("aml_flags", flagged),
	)
	return nil
}

// computeArchiveHash calcule le SHA-256 de l'archive (sans le champ hash lui-même).
func (s *Service) computeArchiveHash(a *ArchiveRecord) string {
	type hashableArchive struct {
		ArchiveID        string    `json:"archive_id"`
		GroupID          string    `json:"group_id"`
		PeriodStart      time.Time `json:"period_start"`
		PeriodEnd        time.Time `json:"period_end"`
		GeneratedAt      time.Time `json:"generated_at"`
		TotalVolume      int64     `json:"total_volume_minor"`
		TransactionCount int       `json:"transaction_count"`
	}
	data, _ := json.Marshal(hashableArchive{
		ArchiveID:        a.ArchiveID,
		GroupID:          a.GroupID,
		PeriodStart:      a.PeriodStart,
		PeriodEnd:        a.PeriodEnd,
		GeneratedAt:      a.GeneratedAt,
		TotalVolume:      a.TotalVolume,
		TransactionCount: a.TransactionCount,
	})
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func (s *Service) persistArchive(ctx context.Context, a *ArchiveRecord) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO compliance_archives (
		    id, group_id, period_start, period_end, generated_at, retain_until,
		    total_volume_minor, transaction_count, content_hash, compliance_status
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (id) DO NOTHING
	`,
		a.ArchiveID, a.GroupID, a.PeriodStart, a.PeriodEnd, a.GeneratedAt, a.RetainUntil,
		a.TotalVolume, a.TransactionCount, a.ContentHash, a.ComplianceStatus,
	)
	return err
}
