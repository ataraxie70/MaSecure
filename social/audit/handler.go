package audit

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

// Handler expose les endpoints d'audit pour le Social Service.
type Handler struct {
	svc *Service
	log *zap.Logger
}

func NewHandler(svc *Service, log *zap.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

// RegisterRoutes monte les routes d'audit sur un routeur chi.
func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/groups/{groupID}/audit", h.ExportAudit)
}

// ExportAudit génère et retourne le rapport d'audit d'un groupe.
//
// GET /groups/{groupID}/audit?from=2025-01-01&to=2025-12-31
//
// Répond avec le rapport JSON complet incluant tous les événements ledger,
// les contributions et la vérification d'intégrité de la chaîne de hachages.
func (h *Handler) ExportAudit(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "groupID")
	if groupID == "" {
		http.Error(w, "group_id required", http.StatusBadRequest)
		return
	}

	// Paramètres de période (défaut : 12 derniers mois)
	fromStr := r.URL.Query().Get("from")
	toStr := r.URL.Query().Get("to")

	to := time.Now().UTC()
	from := to.AddDate(-1, 0, 0) // 12 mois en arrière

	if fromStr != "" {
		var err error
		from, err = time.Parse("2006-01-02", fromStr)
		if err != nil {
			http.Error(w, "invalid 'from' date (expected YYYY-MM-DD)", http.StatusBadRequest)
			return
		}
	}
	if toStr != "" {
		var err error
		to, err = time.Parse("2006-01-02", toStr)
		if err != nil {
			http.Error(w, "invalid 'to' date (expected YYYY-MM-DD)", http.StatusBadRequest)
			return
		}
	}

	if from.After(to) {
		http.Error(w, "'from' must be before 'to'", http.StatusBadRequest)
		return
	}

	h.log.Info("Generating audit report",
		zap.String("group_id", groupID),
		zap.Time("from", from),
		zap.Time("to", to),
	)

	report, err := h.svc.ExportGroupAudit(r.Context(), groupID, from, to)
	if err != nil {
		h.log.Error("Audit export failed", zap.String("group_id", groupID), zap.Error(err))
		http.Error(w, "internal error generating audit", http.StatusInternalServerError)
		return
	}

	// En-tête pour déclencher le téléchargement du fichier dans un navigateur
	filename := "masecure-audit-" + groupID[:8] + "-" + from.Format("20060102") + ".json"
	w.Header().Set("Content-Disposition", "attachment; filename="+filename)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		h.log.Error("Audit encode failed", zap.Error(err))
	}
}
