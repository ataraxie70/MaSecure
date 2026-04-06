// Package dashboard implémente les endpoints de tableau de bord MaSecure.
//
// Ces endpoints sont en lecture seule et exposent des métriques agrégées
// permettant aux membres de suivre l'état de leur groupe en temps réel.
// Aucun de ces endpoints ne peut déclencher une action financière.
package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// GroupDashboard représente le tableau de bord d'un groupe.
type GroupDashboard struct {
	GroupID   string      `json:"group_id"`
	GroupName string      `json:"group_name"`
	FetchedAt time.Time   `json:"fetched_at"`

	// Cycle actif
	ActiveCycle *CycleDashboard `json:"active_cycle,omitempty"`

	// Fonds de roulement
	WorkingCapital WorkingCapitalInfo `json:"working_capital"`

	// Créances membres actives
	ActiveDebts []DebtInfo `json:"active_debts"`

	// Historique récent (5 derniers cycles)
	RecentCycles []CycleSummary `json:"recent_cycles"`
}

type CycleDashboard struct {
	ID                  string    `json:"id"`
	CycleNumber         int       `json:"cycle_number"`
	BeneficiaryID       string    `json:"beneficiary_id"`
	DueDate             time.Time `json:"due_date"`
	PayoutThresholdMinor int64    `json:"payout_threshold_minor"`
	CollectedMinor      int64     `json:"collected_amount_minor"`
	ProgressPct         int       `json:"progress_pct"`
	State               string    `json:"state"`
	PayoutState         string    `json:"payout_state"`
	ResiliencePolicy    string    `json:"resilience_policy"`
	DaysUntilDue        int       `json:"days_until_due"`
}

type WorkingCapitalInfo struct {
	BalanceMinor    int64 `json:"balance_minor"`
	ReservedMinor   int64 `json:"reserved_minor"`
	AvailableMinor  int64 `json:"available_minor"`
	TotalAdvanced   int64 `json:"total_advanced_minor"`
	TotalRepaid     int64 `json:"total_repaid_minor"`
}

type DebtInfo struct {
	DebtorID       string    `json:"debtor_id"`
	CycleID        string    `json:"cycle_id"`
	CycleNumber    int       `json:"cycle_number"`
	OriginalMinor  int64     `json:"original_amount_minor"`
	RemainingMinor int64     `json:"remaining_amount_minor"`
	Reason         string    `json:"reason"`
	State          string    `json:"state"`
	CreatedAt      time.Time `json:"created_at"`
}

type CycleSummary struct {
	ID           string     `json:"id"`
	CycleNumber  int        `json:"cycle_number"`
	State        string     `json:"state"`
	PayoutState  string     `json:"payout_state"`
	CollectedMinor int64    `json:"collected_amount_minor"`
	ThresholdMinor int64    `json:"payout_threshold_minor"`
	ClosedAt     *time.Time `json:"closed_at,omitempty"`
}

// Handler expose les endpoints dashboard.
type Handler struct {
	db  *pgxpool.Pool
	log *zap.Logger
}

func NewHandler(db *pgxpool.Pool, log *zap.Logger) *Handler {
	return &Handler{db: db, log: log}
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/groups/{groupID}/dashboard", h.GetGroupDashboard)
	r.Get("/groups/{groupID}/resilience", h.GetResilienceDashboard)
}

// GetGroupDashboard retourne le tableau de bord complet d'un groupe.
//
// GET /groups/{groupID}/dashboard
func (h *Handler) GetGroupDashboard(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "groupID")

	dash, err := h.buildDashboard(r.Context(), groupID)
	if err != nil {
		h.log.Error("dashboard build failed", zap.String("group_id", groupID), zap.Error(err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(dash)
}

// GetResilienceDashboard retourne l'état du fonds de roulement et des dettes.
//
// GET /groups/{groupID}/resilience
func (h *Handler) GetResilienceDashboard(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "groupID")

	type ResilienceView struct {
		GroupID        string             `json:"group_id"`
		FetchedAt      time.Time          `json:"fetched_at"`
		WorkingCapital WorkingCapitalInfo `json:"working_capital"`
		ActiveDebts    []DebtInfo         `json:"active_debts"`
		TotalDebtMinor int64              `json:"total_outstanding_debt_minor"`
	}

	wc, err := h.fetchWorkingCapital(r.Context(), groupID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	debts, err := h.fetchActiveDebts(r.Context(), groupID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var totalDebt int64
	for _, d := range debts {
		totalDebt += d.RemainingMinor
	}

	view := ResilienceView{
		GroupID:        groupID,
		FetchedAt:      time.Now().UTC(),
		WorkingCapital: wc,
		ActiveDebts:    debts,
		TotalDebtMinor: totalDebt,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(view)
}

// ── Queries ───────────────────────────────────────────────────────────────────

func (h *Handler) buildDashboard(ctx context.Context, groupID string) (*GroupDashboard, error) {
	// Infos groupe
	var name string
	if err := h.db.QueryRow(ctx,
		`SELECT name FROM tontine_groups WHERE id = $1`, groupID,
	).Scan(&name); err != nil {
		return nil, err
	}

	dash := &GroupDashboard{
		GroupID:   groupID,
		GroupName: name,
		FetchedAt: time.Now().UTC(),
	}

	// Cycle actif (committed ou payout_triggered)
	var activeCycle CycleDashboard
	err := h.db.QueryRow(ctx, `
		SELECT id, cycle_number, beneficiary_id, due_date,
		       payout_threshold_minor, collected_amount_minor,
		       state, payout_state, resilience_policy
		FROM cycles
		WHERE group_id = $1
		  AND state IN ('open', 'committed', 'payout_triggered')
		ORDER BY cycle_number DESC
		LIMIT 1
	`, groupID).Scan(
		&activeCycle.ID, &activeCycle.CycleNumber, &activeCycle.BeneficiaryID,
		&activeCycle.DueDate, &activeCycle.PayoutThresholdMinor, &activeCycle.CollectedMinor,
		&activeCycle.State, &activeCycle.PayoutState, &activeCycle.ResiliencePolicy,
	)
	if err == nil {
		if activeCycle.PayoutThresholdMinor > 0 {
			activeCycle.ProgressPct = int(activeCycle.CollectedMinor * 100 / activeCycle.PayoutThresholdMinor)
		}
		activeCycle.DaysUntilDue = int(time.Until(activeCycle.DueDate).Hours() / 24)
		dash.ActiveCycle = &activeCycle
	}

	// Fonds de roulement
	dash.WorkingCapital, _ = h.fetchWorkingCapital(ctx, groupID)

	// Dettes actives
	dash.ActiveDebts, _ = h.fetchActiveDebts(ctx, groupID)

	// Cycles récents
	rows, err := h.db.Query(ctx, `
		SELECT id, cycle_number, state, payout_state,
		       collected_amount_minor, payout_threshold_minor, closed_at
		FROM cycles
		WHERE group_id = $1
		ORDER BY cycle_number DESC
		LIMIT 5
	`, groupID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var cs CycleSummary
			var closedAt *time.Time
			if err := rows.Scan(&cs.ID, &cs.CycleNumber, &cs.State, &cs.PayoutState,
				&cs.CollectedMinor, &cs.ThresholdMinor, &closedAt); err == nil {
				cs.ClosedAt = closedAt
				dash.RecentCycles = append(dash.RecentCycles, cs)
			}
		}
	}

	return dash, nil
}

func (h *Handler) fetchWorkingCapital(ctx context.Context, groupID string) (WorkingCapitalInfo, error) {
	var wc WorkingCapitalInfo
	err := h.db.QueryRow(ctx, `
		SELECT balance_minor, reserved_minor,
		       balance_minor - reserved_minor AS available_minor,
		       total_advanced_minor, total_repaid_minor
		FROM working_capital
		WHERE group_id = $1
	`, groupID).Scan(
		&wc.BalanceMinor, &wc.ReservedMinor, &wc.AvailableMinor,
		&wc.TotalAdvanced, &wc.TotalRepaid,
	)
	if err != nil {
		return WorkingCapitalInfo{}, nil // Pas de fonds encore créé — zéro par défaut
	}
	return wc, nil
}

func (h *Handler) fetchActiveDebts(ctx context.Context, groupID string) ([]DebtInfo, error) {
	rows, err := h.db.Query(ctx, `
		SELECT md.debtor_id, md.cycle_id, c.cycle_number,
		       md.original_amount_minor, md.remaining_amount_minor,
		       md.reason, md.state, md.created_at
		FROM member_debts md
		JOIN cycles c ON c.id = md.cycle_id
		WHERE md.group_id = $1
		  AND md.state IN ('active', 'partially_repaid')
		ORDER BY md.created_at DESC
	`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var debts []DebtInfo
	for rows.Next() {
		var d DebtInfo
		if err := rows.Scan(
			&d.DebtorID, &d.CycleID, &d.CycleNumber,
			&d.OriginalMinor, &d.RemainingMinor,
			&d.Reason, &d.State, &d.CreatedAt,
		); err == nil {
			debts = append(debts, d)
		}
	}
	return debts, rows.Err()
}
