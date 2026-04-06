// Package handlers expose les endpoints HTTP publics de MaSecure.
//
// ARCHITECTURE DE SÉCURITÉ :
// - /callbacks/mobile-money  : reçoit les callbacks opérateurs (HMAC vérifié)
// - /webhooks/whatsapp       : délégué au package gateway/whatsapp
//
// Aucun endpoint public ne peut déclencher directement un PayoutCommand.
// Les callbacks validés sont normalisés puis transférés au kernel financier
// via son endpoint interne dédié.
package handlers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

// MobileMoneyCallbackPayload est la structure générique d'un callback opérateur.
// Chaque opérateur a son propre format — un adapter par opérateur est nécessaire.
type MobileMoneyCallbackPayload struct {
	ProviderTxRef string `json:"transaction_id"`
	PayerMsisdn   string `json:"payer_msisdn"`
	AmountMinor   int64  `json:"amount"`
	Status        string `json:"status"`       // "success" | "failed"
	CycleRef      string `json:"external_ref"` // Référence métier injectée lors de l'initiation
	Provider      string `json:"provider"`
}

func (p MobileMoneyCallbackPayload) validate() error {
	switch {
	case strings.TrimSpace(p.ProviderTxRef) == "":
		return &callbackValidationError{Field: "transaction_id"}
	case strings.TrimSpace(p.PayerMsisdn) == "":
		return &callbackValidationError{Field: "payer_msisdn"}
	case p.AmountMinor <= 0:
		return &callbackValidationError{Field: "amount"}
	case strings.TrimSpace(p.CycleRef) == "":
		return &callbackValidationError{Field: "external_ref"}
	case strings.TrimSpace(p.Provider) == "":
		return &callbackValidationError{Field: "provider"}
	default:
		return nil
	}
}

type callbackValidationError struct {
	Field string
}

func (e *callbackValidationError) Error() string {
	return "missing or invalid callback field: " + e.Field
}

type InternalContributionPayload struct {
	ProviderTxRef string `json:"provider_tx_ref"`
	PayerMsisdn   string `json:"payer_msisdn"`
	AmountMinor   int64  `json:"amount_minor"`
	CycleID       string `json:"cycle_id"`
	Provider      string `json:"provider"`
}

type InternalPayoutConfirmationPayload struct {
	CycleID     string `json:"cycle_id"`
	ExternalRef string `json:"external_ref"`
}

type InternalPayoutFailurePayload struct {
	CycleID     string `json:"cycle_id"`
	ExternalRef string `json:"external_ref"`
	Reason      string `json:"reason"`
}

type KernelClient interface {
	ForwardContributionCallback(ctx context.Context, payload InternalContributionPayload) error
	ForwardPayoutConfirmation(ctx context.Context, payload InternalPayoutConfirmationPayload) error
	ForwardPayoutFailure(ctx context.Context, payload InternalPayoutFailurePayload) error
}

type HTTPKernelClient struct {
	baseURL string
	client  *http.Client
}

func NewHTTPKernelClient(baseURL string) *HTTPKernelClient {
	return &HTTPKernelClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

func (c *HTTPKernelClient) ForwardContributionCallback(
	ctx context.Context,
	payload InternalContributionPayload,
) error {
	return c.postJSON(ctx, "/internal/callbacks/mobile-money", payload)
}

func (c *HTTPKernelClient) ForwardPayoutConfirmation(
	ctx context.Context,
	payload InternalPayoutConfirmationPayload,
) error {
	return c.postJSON(ctx, "/internal/payouts/confirmations", payload)
}

func (c *HTTPKernelClient) ForwardPayoutFailure(
	ctx context.Context,
	payload InternalPayoutFailurePayload,
) error {
	return c.postJSON(ctx, "/internal/payouts/failures", payload)
}

func (c *HTTPKernelClient) postJSON(
	ctx context.Context,
	path string,
	payload any,
) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL+path,
		bytes.NewReader(body),
	)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusMultipleChoices {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &kernelHTTPError{
			StatusCode: resp.StatusCode,
			Body:       string(raw),
		}
	}
	return nil
}

type kernelHTTPError struct {
	StatusCode int
	Body       string
}

func (e *kernelHTTPError) Error() string {
	return http.StatusText(e.StatusCode) + ": " + e.Body
}

// CallbackHandler traite les callbacks Mobile Money entrants.
type CallbackHandler struct {
	HmacSecret string
	adapters   *CallbackAdapterRegistry
	kernel     KernelClient
	log        *zap.Logger
}

func NewCallbackHandler(hmacSecret string, kernel KernelClient, log *zap.Logger) *CallbackHandler {
	return NewCallbackHandlerWithAdapters(
		hmacSecret,
		kernel,
		NewCallbackAdapterRegistry(),
		log,
	)
}

func NewCallbackHandlerWithAdapters(
	hmacSecret string,
	kernel KernelClient,
	adapters *CallbackAdapterRegistry,
	log *zap.Logger,
) *CallbackHandler {
	return &CallbackHandler{
		HmacSecret: hmacSecret,
		adapters:   adapters,
		kernel:     kernel,
		log:        log,
	}
}

// HandleMobileMoneyCallback — POST /callbacks/mobile-money
// Vérification HMAC → parsing → transfert vers le kernel interne.
func (h *CallbackHandler) HandleMobileMoneyCallback(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 512*1024))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if !h.verifyHMAC(r.Header.Get("X-Signature"), body, r.RemoteAddr) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	adapter, err := h.adapters.Resolve(chi.URLParam(r, "provider"))
	if err != nil {
		h.log.Warn("Unsupported Mobile Money callback provider",
			zap.Error(err),
		)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	payload, err := adapter.Parse(body)
	if err != nil {
		h.log.Error("Failed to parse Mobile Money callback", zap.Error(err))
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)

	go h.processCallback(payload)
}

func (h *CallbackHandler) processCallback(payload MobileMoneyCallbackPayload) {
	if payload.Status != "success" {
		h.log.Info("Non-success callback ignored",
			zap.String("tx_ref", payload.ProviderTxRef),
			zap.String("status", payload.Status),
		)
		return
	}

	forward := InternalContributionPayload{
		ProviderTxRef: payload.ProviderTxRef,
		PayerMsisdn:   payload.PayerMsisdn,
		AmountMinor:   payload.AmountMinor,
		CycleID:       payload.CycleRef,
		Provider:      payload.Provider,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := h.kernel.ForwardContributionCallback(ctx, forward); err != nil {
		h.log.Error("Failed to forward callback to kernel",
			zap.String("tx_ref", payload.ProviderTxRef),
			zap.Error(err),
		)
		return
	}

	h.log.Info("Mobile Money callback forwarded to kernel",
		zap.String("tx_ref", payload.ProviderTxRef),
		zap.String("payer", payload.PayerMsisdn),
		zap.Int64("amount_minor", payload.AmountMinor),
		zap.String("provider", payload.Provider),
	)
}

func (h *CallbackHandler) verifyHMAC(signatureHeader string, body []byte, remoteAddr string) bool {
	return verifyHMAC(h.HmacSecret, signatureHeader, body, h.log, remoteAddr)
}

func verifyHMAC(
	secret string,
	signatureHeader string,
	body []byte,
	log *zap.Logger,
	remoteAddr string,
) bool {
	if secret == "" {
		log.Warn("HMAC secret not configured — skipping verification (dev only)")
		return true
	}
	sig := strings.TrimPrefix(signatureHeader, "sha256=")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	valid := hmac.Equal([]byte(sig), []byte(expected))
	if !valid {
		log.Warn("Mobile Money callback HMAC verification failed",
			zap.String("remote_addr", remoteAddr),
		)
	}
	return valid
}

// HealthHandler — GET /health
// Retourne le statut du service pour les load balancers et monitoring.
func HealthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"service": "masecure-api",
	})
}
