package handlers

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

type MobileMoneyPayoutStatusPayload struct {
	CycleID     string `json:"cycle_id"`
	ExternalRef string `json:"external_ref"`
	Status      string `json:"status"`
	Provider    string `json:"provider"`
	Reason      string `json:"reason,omitempty"`
}

func (p MobileMoneyPayoutStatusPayload) validate() error {
	switch {
	case strings.TrimSpace(p.CycleID) == "":
		return &callbackValidationError{Field: "cycle_id"}
	case strings.TrimSpace(p.ExternalRef) == "":
		return &callbackValidationError{Field: "external_ref"}
	case strings.TrimSpace(p.Status) == "":
		return &callbackValidationError{Field: "status"}
	case strings.TrimSpace(p.Provider) == "":
		return &callbackValidationError{Field: "provider"}
	default:
		return nil
	}
}

type PayoutCallbackHandler struct {
	HmacSecret string
	adapters   *PayoutCallbackAdapterRegistry
	kernel     KernelClient
	log        *zap.Logger
}

func NewPayoutCallbackHandler(
	hmacSecret string,
	kernel KernelClient,
	log *zap.Logger,
) *PayoutCallbackHandler {
	return &PayoutCallbackHandler{
		HmacSecret: hmacSecret,
		adapters:   NewPayoutCallbackAdapterRegistry(),
		kernel:     kernel,
		log:        log,
	}
}

func (h *PayoutCallbackHandler) HandlePayoutStatusCallback(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 512*1024))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if !verifyHMAC(h.HmacSecret, r.Header.Get("X-Signature"), body, h.log, r.RemoteAddr) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	adapter, err := h.adapters.Resolve(chi.URLParam(r, "provider"))
	if err != nil {
		h.log.Warn("Unsupported Mobile Money payout callback provider", zap.Error(err))
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	payload, err := adapter.Parse(body)
	if err != nil {
		h.log.Error("Failed to parse payout status callback", zap.Error(err))
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
	go h.processPayoutStatus(payload)
}

func (h *PayoutCallbackHandler) processPayoutStatus(payload MobileMoneyPayoutStatusPayload) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	switch payload.Status {
	case "success":
		err := h.kernel.ForwardPayoutConfirmation(ctx, InternalPayoutConfirmationPayload{
			CycleID:     payload.CycleID,
			ExternalRef: payload.ExternalRef,
		})
		if err != nil {
			h.log.Error("Failed to forward payout confirmation to kernel",
				zap.String("cycle_id", payload.CycleID),
				zap.String("external_ref", payload.ExternalRef),
				zap.Error(err),
			)
			return
		}

		h.log.Info("Payout confirmation callback forwarded to kernel",
			zap.String("cycle_id", payload.CycleID),
			zap.String("external_ref", payload.ExternalRef),
			zap.String("provider", payload.Provider),
		)
	case "failed":
		err := h.kernel.ForwardPayoutFailure(ctx, InternalPayoutFailurePayload{
			CycleID:     payload.CycleID,
			ExternalRef: payload.ExternalRef,
			Reason:      payload.Reason,
		})
		if err != nil {
			h.log.Error("Failed to forward payout failure to kernel",
				zap.String("cycle_id", payload.CycleID),
				zap.String("external_ref", payload.ExternalRef),
				zap.Error(err),
			)
			return
		}

		h.log.Warn("Payout failure callback forwarded to kernel",
			zap.String("cycle_id", payload.CycleID),
			zap.String("external_ref", payload.ExternalRef),
			zap.String("provider", payload.Provider),
			zap.String("reason", payload.Reason),
		)
	default:
		h.log.Info("Unknown payout callback status ignored",
			zap.String("cycle_id", payload.CycleID),
			zap.String("external_ref", payload.ExternalRef),
			zap.String("status", payload.Status),
			zap.String("provider", payload.Provider),
		)
	}
}
