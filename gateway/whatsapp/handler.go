// Package whatsapp traite les webhooks entrants de WhatsApp Business API.
//
// RÈGLE DE SÉCURITÉ FONDAMENTALE : ce package ne produit que des UserIntent.
// Il ne possède aucune clé API Mobile Money et ne peut jamais émettre
// un PayoutCommand. La séparation est architecturale, pas seulement logique.
package whatsapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"go.uber.org/zap"
)

// UserIntentType représente les intentions possibles d'un utilisateur
// à travers l'interface WhatsApp. Ces événements alimentent le Service Social,
// jamais directement le Kernel Financier.
type UserIntentType string

const (
	IntentConfirmParticipation UserIntentType = "confirm_participation"
	IntentRequestConfigChange  UserIntentType = "request_config_change"
	IntentVoteApprove          UserIntentType = "vote_approve"
	IntentVoteReject           UserIntentType = "vote_reject"
	IntentQueryBalance         UserIntentType = "query_balance"
	IntentQueryAudit           UserIntentType = "query_audit"
	IntentUnknown              UserIntentType = "unknown"
)

// UserIntent est l'événement produit par la gateway.
// C'est tout ce que WhatsApp peut injecter dans le système.
type UserIntent struct {
	Type       UserIntentType
	FromMsisdn string
	GroupRef   string
	RawText    string
	Payload    map[string]string
}

// WebhookHandler traite les événements entrants de Meta/WhatsApp
type WebhookHandler struct {
	AppSecret     string // Pour vérifier la signature HMAC-SHA256
	VerifyToken   string // Token de vérification du webhook Meta
	IntentChannel chan<- UserIntent
	log           *zap.Logger
}

func NewWebhookHandler(secret, verifyToken string, ch chan<- UserIntent, log *zap.Logger) *WebhookHandler {
	return &WebhookHandler{
		AppSecret:     secret,
		VerifyToken:   verifyToken,
		IntentChannel: ch,
		log:           log,
	}
}

// HandleVerification gère la vérification initiale du webhook par Meta.
// GET /webhooks/whatsapp?hub.mode=subscribe&hub.verify_token=...&hub.challenge=...
func (h *WebhookHandler) HandleVerification(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("hub.mode")
	token := r.URL.Query().Get("hub.verify_token")
	challenge := r.URL.Query().Get("hub.challenge")

	if mode == "subscribe" && token == h.VerifyToken {
		h.log.Info("WhatsApp webhook verified by Meta")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, challenge)
		return
	}
	h.log.Warn("WhatsApp webhook verification failed", zap.String("token", token))
	w.WriteHeader(http.StatusForbidden)
}

// HandleEvent traite les événements entrants (messages, statuts).
// POST /webhooks/whatsapp
func (h *WebhookHandler) HandleEvent(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MB max
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Vérification HMAC-SHA256 de la signature Meta
	if !h.verifySignature(r.Header.Get("X-Hub-Signature-256"), body) {
		h.log.Warn("WhatsApp webhook HMAC signature verification failed")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// Répondre 200 immédiatement à Meta (obligatoire sous 20s)
	w.WriteHeader(http.StatusOK)

	// Parser et router en goroutine pour ne pas bloquer
	go h.parseAndRoute(body)
}

func (h *WebhookHandler) verifySignature(sigHeader string, body []byte) bool {
	if h.AppSecret == "" {
		h.log.Warn("AppSecret not configured — skipping HMAC check (dev mode only)")
		return true
	}
	expected := sigHeader
	if !strings.HasPrefix(expected, "sha256=") {
		return false
	}
	expected = strings.TrimPrefix(expected, "sha256=")

	mac := hmac.New(sha256.New, []byte(h.AppSecret))
	mac.Write(body)
	computed := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(computed), []byte(expected))
}

// Payload Meta WhatsApp simplifié
type waEvent struct {
	Object string `json:"object"`
	Entry  []struct {
		Changes []struct {
			Value struct {
				Messages []struct {
					From string `json:"from"`
					Text struct {
						Body string `json:"body"`
					} `json:"text"`
				} `json:"messages"`
			} `json:"value"`
		} `json:"changes"`
	} `json:"entry"`
}

func (h *WebhookHandler) parseAndRoute(body []byte) {
	var ev waEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		h.log.Error("Failed to parse WhatsApp event", zap.Error(err))
		return
	}

	for _, entry := range ev.Entry {
		for _, change := range entry.Changes {
			for _, msg := range change.Value.Messages {
				intent := parseIntent(msg.From, msg.Text.Body)
				h.log.Info("WhatsApp UserIntent parsed",
					zap.String("from", msg.From),
					zap.String("intent", string(intent.Type)),
				)
				// Envoi non-bloquant : si le canal est plein, on log et on abandonne
				select {
				case h.IntentChannel <- intent:
				default:
					h.log.Warn("Intent channel full — dropping message", zap.String("from", msg.From))
				}
			}
		}
	}
}

// parseIntent analyse le texte du message pour déterminer l'intention.
// Cette logique sera enrichie avec NLP ou commandes structurées.
func parseIntent(from, text string) UserIntent {
	text = strings.TrimSpace(strings.ToLower(text))
	intent := UserIntent{FromMsisdn: "+" + from, RawText: text}

	switch {
	case strings.HasPrefix(text, "ok") || strings.HasPrefix(text, "confirme"):
		intent.Type = IntentConfirmParticipation
	case strings.HasPrefix(text, "vote oui") || text == "oui":
		intent.Type = IntentVoteApprove
	case strings.HasPrefix(text, "vote non") || text == "non":
		intent.Type = IntentVoteReject
	case strings.HasPrefix(text, "solde") || strings.HasPrefix(text, "balance"):
		intent.Type = IntentQueryBalance
	case strings.HasPrefix(text, "audit") || strings.HasPrefix(text, "historique"):
		intent.Type = IntentQueryAudit
	default:
		intent.Type = IntentUnknown
	}
	return intent
}
