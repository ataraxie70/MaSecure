// Package orangemoney implémente l'adaptateur Orange Money Burkina Faso.
//
// Documentation de référence : Orange Money Business API (OMAPI) v2.
// Ce package traduit le contrat interne MaSecure vers le format
// spécifique d'Orange Money et normalise les callbacks entrants.
//
// INTÉGRATION RÉELLE : remplacer les constantes OMAPI_* par les valeurs
// fournies par le partenaire commercial Orange Burkina Faso.
package orangemoney

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
	providerName   = "orange_money"
	apiVersion     = "v2"
	defaultTimeout = 30 * time.Second
	// En-têtes spécifiques Orange Money — à confirmer avec le partenaire
	headerOrangeAPIKey    = "X-API-Key"
	headerOrangeSignature = "X-Signature"
	headerOrangeIdempKey  = "X-Idempotency-Key"
	headerOrangeProvider  = "X-Provider"
	headerOrangeTimestamp = "X-Timestamp"
)

// OrangePayoutRequest représente le payload attendu par l'API Orange Money.
// Structure à valider avec la documentation officielle du partenaire agréé.
type OrangePayoutRequest struct {
	// Référence unique de la transaction côté MaSecure
	Reference string `json:"reference"`
	// Numéro de téléphone du bénéficiaire avec indicatif pays
	BeneficiaryMsisdn string `json:"beneficiary_msisdn"`
	// Montant en XOF (centimes si l'API l'exige, unités sinon — à confirmer)
	AmountMinor int64 `json:"amount"`
	// Description affichée au bénéficiaire dans son solde
	Description string `json:"description"`
	// Clé d'idempotence transmise à Orange pour éviter les doublons côté opérateur
	IdempotencyKey string `json:"idempotency_key"`
	// URL de callback de l'API MaSecure pour la confirmation
	CallbackURL string `json:"callback_url"`
	// Métadonnées optionnelles pour réconciliation
	Metadata map[string]string `json:"metadata,omitempty"`
}

// OrangePayoutResponse représente la réponse de l'API Orange Money.
type OrangePayoutResponse struct {
	// Référence de la transaction chez Orange (external_ref)
	TransactionID string `json:"transaction_id"`
	// Statut immédiat : PENDING, PROCESSING, SUCCESS, FAILED
	Status string `json:"status"`
	// Message humain (pour le logging)
	Message string `json:"message,omitempty"`
	// Horodatage Orange
	Timestamp string `json:"timestamp,omitempty"`
}

// OrangeCallbackPayload représente le callback entrant d'Orange Money.
// Ce type est utilisé par le callback_adapters.go pour normaliser.
type OrangeCallbackPayload struct {
	TransactionID     string `json:"transaction_id"`
	Reference         string `json:"reference"`
	BeneficiaryMsisdn string `json:"beneficiary_msisdn"`
	Amount            int64  `json:"amount"`
	Status            string `json:"status"` // SUCCESS | FAILED | REVERSED
	FailureReason     string `json:"failure_reason,omitempty"`
	Timestamp         string `json:"timestamp"`
}

// Provider implémente mobilemoney.Provider pour Orange Money Burkina Faso.
type Provider struct {
	config mobilemoney.LiveConfig
	client *http.Client
}

// New crée un provider Orange Money avec la configuration fournie.
// baseURL doit pointer sur le endpoint de l'agrégateur ou directement sur OMAPI.
func New(config mobilemoney.LiveConfig) *Provider {
	return &Provider{
		config: config,
		client: &http.Client{
			Timeout: defaultTimeout,
			// En production, ajouter un Transport avec TLS pinning si requis
		},
	}
}

func (p *Provider) Name() string { return providerName }

// SendPayout envoie un ordre de paiement vers Orange Money.
// La réponse initiale est PENDING — la confirmation arrive par callback.
func (p *Provider) SendPayout(ctx context.Context, cmd mobilemoney.PayoutCommand) (mobilemoney.DispatchResult, error) {
	callbackURL := p.config.CallbackBaseURL + "/callbacks/orange-money/payout"

	payload := OrangePayoutRequest{
		Reference:         cmd.CycleID,
		BeneficiaryMsisdn: cmd.BeneficiaryMsisdn,
		AmountMinor:       cmd.AmountMinor,
		Description:       fmt.Sprintf("Versement tontine cycle %s", cmd.CycleID[:8]),
		IdempotencyKey:    cmd.IdempotencyKey,
		CallbackURL:       callbackURL,
		Metadata: map[string]string{
			"group_id":       cmd.GroupID,
			"beneficiary_id": cmd.BeneficiaryID,
			"cycle_id":       cmd.CycleID,
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return mobilemoney.DispatchResult{}, fmt.Errorf("marshal orange payout: %w", err)
	}

	endpoint := fmt.Sprintf("%s/%s/payouts", p.config.BaseURL, apiVersion)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return mobilemoney.DispatchResult{}, fmt.Errorf("build orange request: %w", err)
	}

	timestamp := time.Now().UTC().Format(time.RFC3339)
	sig := p.sign(body, timestamp)

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerOrangeAPIKey, p.config.APIKey)
	req.Header.Set(headerOrangeSignature, sig)
	req.Header.Set(headerOrangeIdempKey, cmd.IdempotencyKey)
	req.Header.Set(headerOrangeProvider, providerName)
	req.Header.Set(headerOrangeTimestamp, timestamp)

	resp, err := p.client.Do(req)
	if err != nil {
		return mobilemoney.DispatchResult{}, fmt.Errorf("orange money http: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32*1024))

	if resp.StatusCode == http.StatusConflict {
		// 409 Conflict = idempotence : déjà traité côté Orange
		var dup OrangePayoutResponse
		_ = json.Unmarshal(raw, &dup)
		extRef := dup.TransactionID
		if extRef == "" {
			extRef = cmd.IdempotencyKey
		}
		return mobilemoney.DispatchResult{ExternalRef: extRef, ConfirmImmediately: false}, nil
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return mobilemoney.DispatchResult{}, fmt.Errorf(
			"orange money API %d: %s", resp.StatusCode, string(raw),
		)
	}

	var omResp OrangePayoutResponse
	if err := json.Unmarshal(raw, &omResp); err != nil {
		return mobilemoney.DispatchResult{}, fmt.Errorf("parse orange response: %w", err)
	}

	// SUCCESS immédiat rare mais possible (petits montants sur sandbox)
	confirmNow := omResp.Status == "SUCCESS"

	return mobilemoney.DispatchResult{
		ExternalRef:        omResp.TransactionID,
		ConfirmImmediately: confirmNow,
	}, nil
}

// sign calcule la signature HMAC-SHA256 du payload pour Orange Money.
// Format : HMAC-SHA256(apiKey + ":" + timestamp + ":" + bodyHex)
// À ajuster selon la documentation exacte du partenaire.
func (p *Provider) sign(body []byte, timestamp string) string {
	mac := hmac.New(sha256.New, []byte(p.config.HMACSecret))
	mac.Write([]byte(p.config.APIKey))
	mac.Write([]byte(":"))
	mac.Write([]byte(timestamp))
	mac.Write([]byte(":"))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyCallbackSignature vérifie la signature d'un callback entrant Orange.
// Appelée par callback_adapters.go avant tout traitement.
func VerifyCallbackSignature(body []byte, signature, hmacSecret string) bool {
	mac := hmac.New(sha256.New, []byte(hmacSecret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(signature), []byte(expected))
}
