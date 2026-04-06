// Package moovmoney implémente l'adaptateur Moov Money Burkina Faso.
//
// Moov Money (anciennement Tigo Money) utilise une API REST propriétaire.
// Ce package traduit le contrat interne MaSecure vers le format Moov.
//
// INTÉGRATION RÉELLE : obtenir les credentials auprès de Moov Africa BF
// et renseigner les constantes dans .env.
package moovmoney

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"masecure/internal/mobilemoney"
)

const (
	providerName   = "moov_money"
	defaultTimeout = 30 * time.Second
)

// MoovPayoutRequest — format payload Moov Money.
// Structure à valider avec la documentation officielle Moov Africa BF.
type MoovPayoutRequest struct {
	// Identifiant de la transaction côté marchand
	MerchantRef string `json:"merchant_ref"`
	// MSISDN destinataire (+226XXXXXXXX)
	ReceiverMsisdn string `json:"receiver_msisdn"`
	// Montant en XOF
	Amount int64 `json:"amount"`
	// Description / motif du transfert
	Motif string `json:"motif"`
	// Clé d'idempotence
	IdempotencyKey string `json:"idempotency_key"`
	// URL de notification de résultat
	NotifyURL string `json:"notify_url"`
}

type MoovPayoutResponse struct {
	// Référence Moov de la transaction
	MoovRef   string `json:"moov_ref"`
	Status    string `json:"status"` // INITIATED | COMPLETED | FAILED
	Message   string `json:"message,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

// MoovCallbackPayload — format callback Moov entrant.
type MoovCallbackPayload struct {
	MoovRef        string `json:"moov_ref"`
	MerchantRef    string `json:"merchant_ref"`
	ReceiverMsisdn string `json:"receiver_msisdn"`
	Amount         int64  `json:"amount"`
	Status         string `json:"status"` // COMPLETED | FAILED
	ErrorCode      string `json:"error_code,omitempty"`
	Timestamp      string `json:"timestamp"`
}

// Provider implémente mobilemoney.Provider pour Moov Money Burkina Faso.
type Provider struct {
	config mobilemoney.LiveConfig
	client *http.Client
}

func New(config mobilemoney.LiveConfig) *Provider {
	return &Provider{
		config: config,
		client: &http.Client{Timeout: defaultTimeout},
	}
}

func (p *Provider) Name() string { return providerName }

func (p *Provider) SendPayout(ctx context.Context, cmd mobilemoney.PayoutCommand) (mobilemoney.DispatchResult, error) {
	notifyURL := p.config.CallbackBaseURL + "/callbacks/moov-money/payout"

	payload := MoovPayoutRequest{
		MerchantRef:    cmd.IdempotencyKey,
		ReceiverMsisdn: cmd.BeneficiaryMsisdn,
		Amount:         cmd.AmountMinor,
		Motif:          fmt.Sprintf("Versement tontine %s", cmd.CycleID[:8]),
		IdempotencyKey: cmd.IdempotencyKey,
		NotifyURL:      notifyURL,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return mobilemoney.DispatchResult{}, fmt.Errorf("marshal moov payout: %w", err)
	}

	endpoint := p.config.BaseURL + "/api/v1/disbursements"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return mobilemoney.DispatchResult{}, fmt.Errorf("build moov request: %w", err)
	}

	timestamp := time.Now().UTC().Format(time.RFC3339)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", p.config.APIKey)
	req.Header.Set("X-Signature", p.sign(body, timestamp))
	req.Header.Set("X-Idempotency-Key", cmd.IdempotencyKey)
	req.Header.Set("X-Provider", providerName)
	req.Header.Set("X-Timestamp", timestamp)

	resp, err := p.client.Do(req)
	if err != nil {
		return mobilemoney.DispatchResult{}, fmt.Errorf("moov http: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32*1024))

	// 409 = idempotence déjà traité
	if resp.StatusCode == http.StatusConflict {
		var dup MoovPayoutResponse
		_ = json.Unmarshal(raw, &dup)
		ref := dup.MoovRef
		if ref == "" {
			ref = cmd.IdempotencyKey
		}
		return mobilemoney.DispatchResult{ExternalRef: ref}, nil
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return mobilemoney.DispatchResult{}, fmt.Errorf(
			"moov API %d: %s", resp.StatusCode, string(raw),
		)
	}

	var moovResp MoovPayoutResponse
	if err := json.Unmarshal(raw, &moovResp); err != nil {
		return mobilemoney.DispatchResult{}, fmt.Errorf("parse moov response: %w", err)
	}

	return mobilemoney.DispatchResult{
		ExternalRef:        moovResp.MoovRef,
		ConfirmImmediately: moovResp.Status == "COMPLETED",
	}, nil
}

func (p *Provider) sign(body []byte, timestamp string) string {
	mac := hmac.New(sha256.New, []byte(p.config.HMACSecret))
	mac.Write([]byte(p.config.APIKey + ":" + timestamp + ":"))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyCallbackSignature vérifie la signature d'un callback entrant Moov.
func VerifyCallbackSignature(body []byte, signature, secret string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal([]byte(hex.EncodeToString(mac.Sum(nil))), []byte(signature))
}
