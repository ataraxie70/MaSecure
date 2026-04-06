// Package notification implémente le service de notifications MaSecure.
//
// Ce service est le composant manquant qui transforme les DomainEvent de
// l'outbox en messages WhatsApp et SMS effectifs.
//
// Il reçoit les événements du dispatcher de l'Outbox Worker et les route
// vers WhatsApp Business API (Meta) ou SMS (pour les utilisateurs sans WhatsApp).
//
// PHASE 5 : ce service remplace le stub NotificationDispatcher de l'outbox.
package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

// Config contient les paramètres pour les deux canaux de notification.
type Config struct {
	// WhatsApp Business API
	WhatsAppPhoneNumberID string
	WhatsAppAccessToken   string
	WhatsAppAPIVersion    string // ex: "v19.0"

	// SMS Fallback (ex: Orange SMS API, ou un agrégateur SMS BF)
	SMSGatewayURL    string
	SMSAPIKey        string
	SMSSenderID      string // Identifiant expéditeur affiché (ex: "MaSecure")

	// Comportement
	FallbackToSMS bool // Si WhatsApp échoue, envoyer SMS
}

// DefaultConfig retourne une configuration avec les valeurs d'environnement.
func DefaultConfigFromEnv() Config {
	return Config{
		WhatsAppPhoneNumberID: "",
		WhatsAppAccessToken:   "",
		WhatsAppAPIVersion:    "v19.0",
		SMSGatewayURL:         "",
		SMSAPIKey:             "",
		SMSSenderID:           "MaSecure",
		FallbackToSMS:         true,
	}
}

// TemplateID identifie un template WhatsApp pré-approuvé par Meta.
// Ces templates doivent être créés et approuvés dans le Meta Business Suite.
type TemplateID string

const (
	// Templates Phase 1 (notifications basiques)
	TemplatePeContributionConfirmed TemplateID = "contribution_confirmed"   // "Votre cotisation de {{1}} XOF a été reçue pour le cycle {{2}}."
	TemplatePePayoutSent            TemplateID = "payout_sent"              // "Votre versement de {{1}} XOF est en cours de traitement."
	TemplatePePayoutConfirmed       TemplateID = "payout_confirmed"         // "Vous avez reçu {{1}} XOF de votre tontine {{2}}. Félicitations !"
	TemplatePePayoutFailed          TemplateID = "payout_failed"            // "Le versement de votre cycle {{1}} a échoué. Votre groupe a été notifié."
	TemplatePeContributionReminder  TemplateID = "contribution_reminder"    // "Rappel : votre cotisation de {{1}} XOF est due dans {{2}} jours."
	TemplatePeQuarantineAlert       TemplateID = "quarantine_alert"         // "Un paiement de {{1}} XOF a été reçu d'un numéro non lié ({{2}}). Vérification requise."

	// Templates Phase 3 Gouvernance
	TemplatePeProposalCreated      TemplateID = "proposal_created"   // "{{1}} propose une modification de votre tontine. Votez avant le {{2}}."
	TemplatePeVoteReminder         TemplateID = "vote_reminder"      // "Rappel : vous n'avez pas encore voté sur la proposition de {{1}}."
	TemplatePeProposalApproved     TemplateID = "proposal_approved"  // "La modification de configuration a été approuvée et s'appliquera au prochain cycle."
	TemplatePeProposalRejected     TemplateID = "proposal_rejected"  // "La modification de configuration a été rejetée par le vote des membres."

	// Templates Phase 4 Résilience
	TemplatePeDebtCreated          TemplateID = "debt_created"       // "Votre cotisation du cycle {{1}} n'a pas été reçue. Une avance de {{2}} XOF a été effectuée. Vous devez rembourser au prochain cycle."
	TemplatePeDebtRepaid           TemplateID = "debt_repaid"        // "Votre dette de {{1}} XOF a été remboursée. Merci !"
	TemplatePeProRataNotice        TemplateID = "pro_rata_notice"    // "Versement partiel de {{1}} XOF ({{2}}% du montant attendu). Solde à régulariser au prochain cycle."
)

// NotificationRequest représente une demande de notification.
type NotificationRequest struct {
	// Numéro de téléphone du destinataire avec indicatif pays
	RecipientMsisdn string
	// Template WhatsApp à utiliser
	Template TemplateID
	// Paramètres du template (dans l'ordre des {{N}})
	Params []string
	// Fallback SMS si WhatsApp échoue ou n'est pas disponible
	SMSBody string
	// Métadonnées pour le logging
	EventType string
	GroupID   string
}

// NotificationResult résume le résultat d'envoi.
type NotificationResult struct {
	Channel   string    // "whatsapp" | "sms" | "failed"
	MessageID string
	SentAt    time.Time
	Error     error
}

// Sender envoie des notifications sur WhatsApp ou SMS.
type Sender struct {
	config Config
	client *http.Client
	log    *zap.Logger
}

func NewSender(config Config, log *zap.Logger) *Sender {
	return &Sender{
		config: config,
		client: &http.Client{Timeout: 15 * time.Second},
		log:    log,
	}
}

// Send envoie une notification sur le meilleur canal disponible.
func (s *Sender) Send(ctx context.Context, req NotificationRequest) NotificationResult {
	// Tentative WhatsApp en premier
	if s.config.WhatsAppAccessToken != "" {
		result, err := s.sendWhatsApp(ctx, req)
		if err == nil {
			return result
		}
		s.log.Warn("WhatsApp send failed, trying SMS fallback",
			zap.String("msisdn", req.RecipientMsisdn),
			zap.Error(err),
		)
	}

	// Fallback SMS
	if s.config.FallbackToSMS && s.config.SMSGatewayURL != "" && req.SMSBody != "" {
		result, err := s.sendSMS(ctx, req)
		if err == nil {
			return result
		}
		s.log.Error("SMS fallback also failed",
			zap.String("msisdn", req.RecipientMsisdn),
			zap.Error(err),
		)
		return NotificationResult{Channel: "failed", Error: err}
	}

	s.log.Info("Notification logged (no channel configured)",
		zap.String("event_type", req.EventType),
		zap.String("msisdn", req.RecipientMsisdn),
		zap.String("template", string(req.Template)),
	)
	return NotificationResult{Channel: "logged", SentAt: time.Now().UTC()}
}

// sendWhatsApp envoie un message via WhatsApp Business API (Meta Graph API).
func (s *Sender) sendWhatsApp(ctx context.Context, req NotificationRequest) (NotificationResult, error) {
	components := make([]any, len(req.Params))
	for i, p := range req.Params {
		components[i] = map[string]any{
			"type": "text",
			"text": p,
		}
	}

	payload := map[string]any{
		"messaging_product": "whatsapp",
		"to":                req.RecipientMsisdn,
		"type":              "template",
		"template": map[string]any{
			"name": string(req.Template),
			"language": map[string]string{
				"code": "fr", // Français pour le Burkina Faso
			},
			"components": []any{
				map[string]any{
					"type":       "body",
					"parameters": components,
				},
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return NotificationResult{}, err
	}

	url := fmt.Sprintf(
		"https://graph.facebook.com/%s/%s/messages",
		s.config.WhatsAppAPIVersion,
		s.config.WhatsAppPhoneNumberID,
	)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return NotificationResult{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+s.config.WhatsAppAccessToken)

	resp, err := s.client.Do(httpReq)
	if err != nil {
		return NotificationResult{}, fmt.Errorf("whatsapp http: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return NotificationResult{}, fmt.Errorf("whatsapp API %d: %s", resp.StatusCode, string(raw))
	}

	var result struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	var msgID string
	if err := json.Unmarshal(raw, &result); err == nil && len(result.Messages) > 0 {
		msgID = result.Messages[0].ID
	}

	s.log.Info("WhatsApp notification sent",
		zap.String("msisdn", req.RecipientMsisdn),
		zap.String("template", string(req.Template)),
		zap.String("message_id", msgID),
	)

	return NotificationResult{
		Channel:   "whatsapp",
		MessageID: msgID,
		SentAt:    time.Now().UTC(),
	}, nil
}

// sendSMS envoie un SMS via l'agrégateur SMS configuré.
// Format générique — adapter selon l'API Orange SMS, Twilio, ou autre.
func (s *Sender) sendSMS(ctx context.Context, req NotificationRequest) (NotificationResult, error) {
	payload := map[string]string{
		"to":        req.RecipientMsisdn,
		"message":   req.SMSBody,
		"sender_id": s.config.SMSSenderID,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return NotificationResult{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.config.SMSGatewayURL+"/send", bytes.NewReader(body))
	if err != nil {
		return NotificationResult{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-API-Key", s.config.SMSAPIKey)

	resp, err := s.client.Do(httpReq)
	if err != nil {
		return NotificationResult{}, fmt.Errorf("sms http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return NotificationResult{}, fmt.Errorf("sms API %d: %s", resp.StatusCode, string(raw))
	}

	s.log.Info("SMS notification sent",
		zap.String("msisdn", req.RecipientMsisdn),
	)

	return NotificationResult{Channel: "sms", SentAt: time.Now().UTC()}, nil
}

// ── Builder de messages ───────────────────────────────────────────────────────

// BuildContributionConfirmed crée une notification de confirmation de cotisation.
func BuildContributionConfirmed(msisdn string, amountMinor int64, cycleNumber int) NotificationRequest {
	amount := fmt.Sprintf("%d", amountMinor/100)
	return NotificationRequest{
		RecipientMsisdn: msisdn,
		Template:        TemplatePeContributionConfirmed,
		Params:          []string{amount, fmt.Sprintf("%d", cycleNumber)},
		SMSBody:         fmt.Sprintf("MaSecure : votre cotisation de %s XOF a été reçue pour le cycle %d. Merci.", amount, cycleNumber),
		EventType:       "contribution.confirmed",
	}
}

// BuildPayoutConfirmed crée une notification de versement reçu.
func BuildPayoutConfirmed(msisdn, groupName string, amountMinor int64) NotificationRequest {
	amount := fmt.Sprintf("%d", amountMinor/100)
	return NotificationRequest{
		RecipientMsisdn: msisdn,
		Template:        TemplatePePayoutConfirmed,
		Params:          []string{amount, groupName},
		SMSBody:         fmt.Sprintf("MaSecure : vous avez reçu %s XOF de votre tontine %s. Félicitations !", amount, groupName),
		EventType:       "payout.confirmed",
	}
}

// BuildPayoutFailed crée une notification d'échec de versement.
func BuildPayoutFailed(msisdn string, cycleNumber int) NotificationRequest {
	return NotificationRequest{
		RecipientMsisdn: msisdn,
		Template:        TemplatePePayoutFailed,
		Params:          []string{fmt.Sprintf("%d", cycleNumber)},
		SMSBody:         fmt.Sprintf("MaSecure : le versement du cycle %d a échoué. Votre groupe a été notifié. Contact : votre fondateur.", cycleNumber),
		EventType:       "payout.failed",
	}
}

// BuildContributionReminder crée un rappel de cotisation avant échéance.
func BuildContributionReminder(msisdn string, amountMinor int64, daysLeft int) NotificationRequest {
	amount := fmt.Sprintf("%d", amountMinor/100)
	return NotificationRequest{
		RecipientMsisdn: msisdn,
		Template:        TemplatePeContributionReminder,
		Params:          []string{amount, fmt.Sprintf("%d", daysLeft)},
		SMSBody:         fmt.Sprintf("MaSecure : rappel - votre cotisation de %s XOF est due dans %d jour(s). Payez via votre Mobile Money.", amount, daysLeft),
		EventType:       "contribution.reminder",
	}
}

// BuildDebtCreated notifie un membre qu'une créance a été créée pour lui.
func BuildDebtCreated(msisdn string, cycleNumber int, advanceMinor int64) NotificationRequest {
	advance := fmt.Sprintf("%d", advanceMinor/100)
	return NotificationRequest{
		RecipientMsisdn: msisdn,
		Template:        TemplatePeDebtCreated,
		Params:          []string{fmt.Sprintf("%d", cycleNumber), advance},
		SMSBody: fmt.Sprintf(
			"MaSecure : votre cotisation du cycle %d n'a pas été reçue. Une avance de %s XOF a été effectuée. Remboursement requis au prochain cycle.",
			cycleNumber, advance),
		EventType: "debt.created",
	}
}

// BuildProposalCreated notifie les membres d'une nouvelle proposition de vote.
func BuildProposalCreated(msisdn, proposerName string, expiresAt time.Time) NotificationRequest {
	deadline := expiresAt.Format("02/01 à 15h")
	return NotificationRequest{
		RecipientMsisdn: msisdn,
		Template:        TemplatePeProposalCreated,
		Params:          []string{proposerName, deadline},
		SMSBody: fmt.Sprintf(
			"MaSecure : %s propose une modification de votre tontine. Votez avant le %s.",
			proposerName, deadline),
		EventType: "governance.proposal_created",
	}
}

// FormatXOF formate un montant en centimes XOF en string lisible.
func FormatXOF(minor int64) string {
	xof := minor / 100
	if xof >= 1_000_000 {
		return fmt.Sprintf("%.1f M XOF", float64(xof)/1_000_000)
	}
	if xof >= 1_000 {
		return strings.ReplaceAll(fmt.Sprintf("%d XOF", xof), "000", " 000")
	}
	return fmt.Sprintf("%d XOF", xof)
}
