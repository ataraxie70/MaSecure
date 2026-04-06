package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"masecure/api/gateway/whatsapp"
)

// WhatsAppHandler handles WhatsApp webhook requests
type WhatsAppHandler struct {
	botService *whatsapp.BotService
	// In production: inject logger, DB, etc.
}

// NewWhatsAppHandler creates new WhatsApp handler
func NewWhatsAppHandler(botService *whatsapp.BotService) *WhatsAppHandler {
	return &WhatsAppHandler{
		botService: botService,
	}
}

// HandleWebhook handles incoming WhatsApp webhook
func (h *WhatsAppHandler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	// Only accept POST
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("Error reading request body: %v", err)
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Log incoming request (for debugging)
	log.Printf("[WEBHOOK] Received: %s", string(body))

	// Verify signature
	if !h.verifySignature(r, body) {
		log.Printf("Invalid signature")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Parse webhook
	var webhook whatsapp.WebhookRequest
	if err := json.Unmarshal(body, &webhook); err != nil {
		log.Printf("Error parsing webhook: %v", err)
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	// Validate webhook structure
	if webhook.Object != "whatsapp_business_account" {
		log.Printf("Unknown object type: %s", webhook.Object)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{}"))
		return
	}

	// Process entries
	for _, entry := range webhook.Entry {
		for _, change := range entry.Changes {
			h.processChange(change.Value)
		}
	}

	// Return 200 OK to acknowledge webhook
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

// HandleVerification handles webhook verification from Meta
func (h *WhatsAppHandler) HandleVerification(w http.ResponseWriter, r *http.Request) {
	// Only accept GET
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	mode := r.URL.Query().Get("hub.mode")
	token := r.URL.Query().Get("hub.verify_token")
	challenge := r.URL.Query().Get("hub.challenge")

	// Get verification token from env
	verifyToken := os.Getenv("WHATSAPP_VERIFY_TOKEN")
	if verifyToken == "" {
		verifyToken = "masecure_verify_2026" // Default for local testing
	}

	// Verify
	if mode == "subscribe" && token == verifyToken {
		log.Printf("Webhook verified successfully")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(challenge))
		return
	}

	log.Printf("Invalid verification token: %s", token)
	http.Error(w, "Forbidden", http.StatusForbidden)
}

// processChange processes a single webhook change
func (h *WhatsAppHandler) processChange(value whatsapp.WebhookValue) {
	// Process messages
	for _, message := range value.Messages {
		h.processMessage(message)
	}

	// Process statuses (delivery confirmations)
	for _, status := range value.Statuses {
		h.processStatus(status)
	}
}

// processMessage processes a single incoming message
func (h *WhatsAppHandler) processMessage(message whatsapp.Message) {
	log.Printf("[MSG] From: %s, Text: %s", message.From, message.Text.Body)

	// Validate
	if message.From == "" || message.Text.Body == "" {
		log.Printf("Invalid message: missing phone or text")
		return
	}

	// Parse intent
	intent := whatsapp.ParseIntent(message.From, message.Text.Body)

	// Log user message
	h.botService.LogUserMessage(message.From, intent.Command, message.Text.Body)

	// Rate limit check
	if !h.botService.RateLimitCheck(message.From) {
		log.Printf("Rate limited: %s", message.From)
		response := whatsapp.FormatResponse(
			whatsapp.BotResponse{
				CommandExecuted: intent.Command,
				Success:         false,
				Error:           "Trop de requêtes. Attendez un moment.",
			},
			message.From,
		)
		h.sendMessage(response)
		return
	}

	// Handle intent
	botResponse := h.botService.HandleIntent(intent)

	// Log response
	h.botService.LogBotResponse(message.From, botResponse)

	// Format and send response
	if botResponse.CommandExecuted == "AIDE" {
		// Special case: help is pre-formatted
		msg := whatsapp.WhatsAppMessage{
			To:   message.From,
			Text: botResponse.FormattedText,
		}
		h.sendMessage(msg)
	} else {
		// Normal case: format response
		msg := whatsapp.FormatResponse(botResponse, message.From)
		h.sendMessage(msg)
	}
}

// processStatus processes delivery status update
func (h *WhatsAppHandler) processStatus(status whatsapp.Status) {
	log.Printf("[STATUS] MessageID: %s, Status: %s, To: %s",
		status.ID, status.Status, status.RecipientID)

	// TODO: Update message delivery status in database
	// - Mark as delivered/read/failed
	// - Log for analytics
	// - Alert on multiple failures
}

// sendMessage sends message back to user (mock for now)
func (h *WhatsAppHandler) sendMessage(msg whatsapp.WhatsAppMessage) error {
	log.Printf("[SEND] To: %s, Text: %s", msg.To, msg.Text)

	// TODO: Call Meta WhatsApp API to actually send message
	// POST https://graph.instagram.com/v18.0/PHONE_NUMBER_ID/messages
	// Headers: Authorization: Bearer {access_token}
	// Body: {messaging_product: "whatsapp", recipient_type: "individual", to: "...", type: "text", text: {body: "..."}}

	// For now, just log to stdout
	fmt.Printf("\n━━━━━━━━━━━━━━━━━━━━━━━━\n📱 Bot Response to %s:\n%s\n━━━━━━━━━━━━━━━━━━━━━━━━\n", msg.To, msg.Text)

	return nil
}

// verifySignature verifies WhatsApp webhook signature
func (h *WhatsAppHandler) verifySignature(r *http.Request, body []byte) bool {
	// Get signature from header
	signature := r.Header.Get("X-Hub-Signature-256")
	if signature == "" {
		// In production, always require signature
		// For local testing, allow unsigned
		log.Printf("No signature header (OK for local testing)")
		return true
	}

	// Get app secret from env
	appSecret := os.Getenv("WHATSAPP_APP_SECRET")
	if appSecret == "" {
		log.Printf("Warning: WHATSAPP_APP_SECRET not set, cannot verify signature")
		return true // Allow for local testing
	}

	// Compute expected signature
	hash := hmac.New(sha256.New, []byte(appSecret))
	hash.Write(body)
	expectedSig := "sha256=" + hex.EncodeToString(hash.Sum(nil))

	// Compare
	if signature != expectedSig {
		log.Printf("Signature mismatch. Got: %s, Expected: %s", signature, expectedSig)
		return false
	}

	log.Printf("Signature verified")
	return true
}

// HandleStatus returns current bot status
func (h *WhatsAppHandler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	status := map[string]interface{}{
		"status": "healthy",
		"bot":    "WhatsApp Bot v0.1",
		"time":   "2026-04-06T09:00:00Z",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}
