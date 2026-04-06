package governance

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

type HTTPHandler struct {
	service *Service
	log     *zap.Logger
}

func NewHTTPHandler(service *Service, log *zap.Logger) *HTTPHandler {
	return &HTTPHandler{
		service: service,
		log:     log,
	}
}

func (h *HTTPHandler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/health", h.handleHealth)
	r.Route("/internal/governance", func(r chi.Router) {
		r.Post("/proposals", h.handleCreateProposal)
		r.Get("/proposals/{proposalID}", h.handleGetProposal)
		r.Post("/proposals/{proposalID}/votes", h.handleCastVote)
		r.Post("/proposals/{proposalID}/resolve", h.handleResolveProposal)
	})
	return r
}

func (h *HTTPHandler) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"service": "masecure-social",
	})
}

func (h *HTTPHandler) handleCreateProposal(w http.ResponseWriter, r *http.Request) {
	var input CreateProposalInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	proposal, err := h.service.CreateProposal(r.Context(), input)
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, proposal)
}

func (h *HTTPHandler) handleGetProposal(w http.ResponseWriter, r *http.Request) {
	proposal, err := h.service.GetProposal(r.Context(), chi.URLParam(r, "proposalID"))
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, proposal)
}

func (h *HTTPHandler) handleCastVote(w http.ResponseWriter, r *http.Request) {
	var input CastVoteInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	proposal, err := h.service.CastVote(r.Context(), chi.URLParam(r, "proposalID"), input)
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, proposal)
}

func (h *HTTPHandler) handleResolveProposal(w http.ResponseWriter, r *http.Request) {
	var input ResolveProposalInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	result, err := h.service.ResolveProposal(r.Context(), chi.URLParam(r, "proposalID"), input)
	if err != nil {
		if errors.Is(err, ErrProposalPending) {
			writeJSON(w, http.StatusOK, result)
			return
		}
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *HTTPHandler) writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrForbidden):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrDuplicateVote), errors.Is(err, ErrProposalClosed):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, ErrInvalidInput), errors.Is(err, ErrNoConfigChanges):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		h.log.Error("governance request failed", zap.Error(err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// RegisterRoutes monte les routes de gouvernance sur un routeur chi existant.
// Utilisé par social/main_v2.go pour l'assemblage du Social Service.
func (h *HTTPHandler) RegisterRoutes(r chi.Router) {
	r.Route("/governance", func(r chi.Router) {
		r.Post("/proposals", h.handleCreateProposal)
		r.Get("/proposals/{proposalID}", h.handleGetProposal)
		r.Post("/proposals/{proposalID}/vote", h.handleCastVote)
		r.Post("/proposals/{proposalID}/resolve", h.handleResolveProposal)
	})
}
