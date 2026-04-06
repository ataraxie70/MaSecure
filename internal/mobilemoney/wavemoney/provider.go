// Package wavemoney implémente l'adaptateur Wave Burkina Faso.
//
// Wave utilise une API REST moderne avec authentification OAuth2 Bearer.
// Ce package traduit le contrat interne MaSecure vers le format Wave.
//
// INTÉGRATION RÉELLE : obtenir un compte Business Wave et les credentials
// auprès de Wave Mobile Money. L'API Wave est documentée sur developer.wave.com.
package wavemoney

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
	providerName   = "wave"
	defaultTimeout = 30 * time.Second
)

// WavePayoutRequest — payload Wave Business Disbursement.
// Référence : Wave Business API v1 (https://developer.wave.com — à confirmer).
type WavePayoutRequest struct {
	// UUID unique de la transaction côté marchand
	ClientReference string `json:"client_reference"`
	// Numéro Wave du destinataire
	RecipientMobile string `json:"recipient_mobile"`
	// Montant en XOF
	Amount int64 `json:"amount"`
	// Devise (toujours XOF pour le Burkina Faso)
	Currency string `json:"currency"`
	// Description
	Description string `json:"description"`
	// URL de callback Wave
	WebhookURL string `json:"webhook_url"`
}

type WavePayoutResponse struct {
	// Identifiant Wave de la transaction
	WaveRef    string `json:"wave_ref"`
	Status     string `json:"status"` // pending | processing | completed | failed
	CreatedAt  string `json:"created_at"`
	ErrorCode  string `json:"error_code,omitempty"`
	ErrorMsg   string `json:"error_message,omitempty"`
}

// WaveCallbackPayload — notification de résultat Wave.
type WaveCallbackPayload struct {
	WaveRef         string `json:"wave_ref"`
	ClientReference string `json:"client_reference"`
	RecipientMobile string `json:"recipient_mobile"`
	Amount          int64  `json:"amount"`
	Status          string `json:"status"` // completed | failed | reversed
	Timestamp       string `json:"timestamp"`
	FailureReason   string `json:"failure_reason,omitempty"`
}

// Provider implémente mobilemoney.Provider pour Wave.
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
	webhookURL := p.config.CallbackBaseURL + "/callbacks/wave/payout"

	payload := WavePayoutRequest{
		ClientReference: cmd.IdempotencyKey,
		RecipientMobile: cmd.BeneficiaryMsisdn,
		Amount:          cmd.AmountMinor,
		Currency:        "XOF",
		Description:     fmt.Sprintf("Tontine payout cycle %.8s", cmd.CycleID),
		WebhookURL:      webhookURL,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return mobilemoney.DispatchResult{}, fmt.Errorf("marshal wave payout: %w", err)
	}

	endpoint := p.config.BaseURL + "/v1/business/disbursements"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return mobilemoney.DispatchResult{}, fmt.Errorf("build wave request: %w", err)
	}

	timestamp := time.Now().UTC().Format(time.RFC3339)
	req.Header.Set("Content-Type", "application/json")
	// Wave utilise Bearer token (OAuth2) — l'API Key joue le rôle de token
	req.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	req.Header.Set("X-Idempotency-Key", cmd.IdempotencyKey)
	req.Header.Set("X-Provider", providerName)
	req.Header.Set("X-Timestamp", timestamp)
	req.Header.Set("X-Signature", p.sign(body, timestamp))

	resp, err := p.client.Do(req)
	if err != nil {
		return mobilemoney.DispatchResult{}, fmt.Errorf("wave http: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32*1024))

	if resp.StatusCode == http.StatusConflict || resp.StatusCode == http.StatusUnprocessableEntity {
		// Wave renvoie 422 si la clé d'idempotence est déjà connue
		var dup WavePayoutResponse
		_ = json.Unmarshal(raw, &dup)
		ref := dup.WaveRef
		if ref == "" {
			ref = cmd.IdempotencyKey
		}
		return mobilemoney.DispatchResult{ExternalRef: ref}, nil
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return mobilemoney.DispatchResult{}, fmt.Errorf(
			"wave API %d: %s", resp.StatusCode, string(raw),
		)
	}

	var waveResp WavePayoutResponse
	if err := json.Unmarshal(raw, &waveResp); err != nil {
		return mobilemoney.DispatchResult{}, fmt.Errorf("parse wave response: %w", err)
	}

	return mobilemoney.DispatchResult{
		ExternalRef:        waveResp.WaveRef,
		ConfirmImmediately: waveResp.Status == "completed",
	}, nil
}

func (p *Provider) sign(body []byte, timestamp string) string {
	mac := hmac.New(sha256.New, []byte(p.config.HMACSecret))
	mac.Write([]byte(timestamp + ":"))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyCallbackSignature vérifie la signature Wave sur un callback entrant.
// Wave utilise généralement le header "X-Wave-Signature".
func VerifyCallbackSignature(body []byte, signature, secret string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal([]byte(hex.EncodeToString(mac.Sum(nil))), []byte(signature))
}
