package whatsapp

import (
	"log"
)

// BotService handles WhatsApp bot logic
type BotService struct {
	// You'll wire these in from main app
	// socialServiceClient *http.Client
	// dbConnection *sql.DB
	// logger *zap.Logger
}

// NewBotService creates new bot service
func NewBotService() *BotService {
	return &BotService{}
}

// HandleIntent processes user intent and returns response
func (b *BotService) HandleIntent(intent UserIntent) BotResponse {
	log.Printf("Processing intent: %s from %s", intent.Command, intent.PhoneNumber)

	// Route by command
	switch intent.Command {
	case IntentBalance:
		return b.handleBalance(intent.PhoneNumber)
	case IntentHistory:
		return b.handleHistory(intent.PhoneNumber)
	case IntentCycle:
		return b.handleCycle(intent.PhoneNumber)
	case IntentInfo:
		return b.handleInfo(intent.PhoneNumber)
	case IntentHelp:
		return b.handleHelp()
	case IntentUnknown:
		return BotResponse{
			CommandExecuted: "UNKNOWN",
			Success:         false,
			Error:           "Commande non reconnue",
		}
	default:
		return BotResponse{
			CommandExecuted: "DEFAULT",
			Success:         false,
			Error:           "Erreur interne",
		}
	}
}

// handleBalance retrieves user's balance
func (b *BotService) handleBalance(phoneNumber string) BotResponse {
	log.Printf("Handling SOLDE for %s", phoneNumber)

	// TODO: Replace with actual Social Service API call
	// For now, return mock data
	info := GroupInfo{
		ID:                 "group-1",
		Name:               "Les Mamans du Quartier",
		MembersCount:       8,
		Balance:            "150,000",
		ActiveCycle:        "Cycle 2 en cours",
		NextCollectionDate: "15 Avril 2026",
	}

	return BotResponse{
		CommandExecuted: "SOLDE",
		Success:         true,
		Data:            info,
		Error:           "",
	}
}

// handleHistory retrieves transaction history
func (b *BotService) handleHistory(phoneNumber string) BotResponse {
	log.Printf("Handling HISTO for %s", phoneNumber)

	// TODO: Replace with actual Social Service API call
	// For now, return mock data
	transactions := []Transaction{
		{
			Timestamp:   "2026-04-05 14:30",
			From:        "Fatoumata",
			To:          "Collective",
			Amount:      "15,000",
			Description: "Contribution cyclique",
		},
		{
			Timestamp:   "2026-04-04 09:15",
			From:        "Fatou",
			To:          "Mariam",
			Amount:      "50,000",
			Description: "Payout cycle 1",
		},
		{
			Timestamp:   "2026-04-03 16:45",
			From:        "Aminata",
			To:          "Collective",
			Amount:      "15,000",
			Description: "Contribution cyclique",
		},
		{
			Timestamp:   "2026-04-02 11:20",
			From:        "Hawa",
			To:          "Collective",
			Amount:      "15,000",
			Description: "Contribution cyclique",
		},
		{
			Timestamp:   "2026-04-01 10:05",
			From:        "Aissatou",
			To:          "Collective",
			Amount:      "15,000",
			Description: "Contribution cyclique",
		},
	}

	return BotResponse{
		CommandExecuted: "HISTO",
		Success:         true,
		Data:            transactions,
		Error:           "",
	}
}

// handleCycle retrieves cycle information
func (b *BotService) handleCycle(phoneNumber string) BotResponse {
	log.Printf("Handling CYCLE for %s", phoneNumber)

	// TODO: Replace with actual Social Service API call
	// For now, return mock data
	cycle := CycleInfo{
		ID:        "cycle-2",
		Name:      "Cycle 2",
		Status:    "En cours",
		StartDate: "1 Avril 2026",
		EndDate:   "30 Avril 2026",
		Progress:  "40% (6/8 contributions)",
	}

	return BotResponse{
		CommandExecuted: "CYCLE",
		Success:         true,
		Data:            cycle,
		Error:           "",
	}
}

// handleInfo retrieves group information
func (b *BotService) handleInfo(phoneNumber string) BotResponse {
	log.Printf("Handling INFO for %s", phoneNumber)

	// TODO: Replace with actual Social Service API call
	// For now, return mock data
	info := GroupInfo{
		ID:                 "group-1",
		Name:               "Les Mamans du Quartier",
		MembersCount:       8,
		Balance:            "150,000",
		ActiveCycle:        "Cycle 2 en cours",
		NextCollectionDate: "15 Avril 2026",
	}

	return BotResponse{
		CommandExecuted: "INFO",
		Success:         true,
		Data:            info,
		Error:           "",
	}
}

// handleHelp returns help information
func (b *BotService) handleHelp() BotResponse {
	helpText := `🆘 Aide - Commandes Disponibles
━━━━━━━━━━━━━━━━━━━━━━━━

SOLDE  → 💰 Voir votre solde
HISTO  → 📊 Dernières transactions
CYCLE  → 📅 Info cycle en cours
INFO   → ℹ️ Infos du groupe
AIDE   → 🆘 Cette aide

━━━━━━━━━━━━━━━━━━━━━━━━
💬 Tapez juste le mot-clé`

	return BotResponse{
		CommandExecuted: "AIDE",
		Success:         true,
		Data:            helpText,
		Error:           "",
		FormattedText:   helpText,
	}
}

// ValidatePhoneNumber checks if phone number is valid
func ValidatePhoneNumber(phoneNumber string) bool {
	// TODO: Implement proper validation
	// For now, just check if it's not empty
	return phoneNumber != ""
}

// ValidateMessage checks if message is valid
func ValidateMessage(message string) bool {
	// Check not empty
	if message == "" {
		return false
	}

	// Check not too long (arbitrary limit)
	if len(message) > 256 {
		return false
	}

	return true
}

// RateLimitCheck checks if user is rate limited
func (b *BotService) RateLimitCheck(phoneNumber string) bool {
	// TODO: Implement rate limiting (using Redis or in-memory cache)
	// For now, always allow
	return true
}

// LogUserMessage logs message for analytics/debugging
func (b *BotService) LogUserMessage(phoneNumber string, intent Intent, rawMessage string) {
	log.Printf("[USER_MSG] phone=%s intent=%s msg=%q", phoneNumber, intent, rawMessage)

	// TODO: Store in database for analytics
	// - Track common commands
	// - Identify patterns
	// - Monitor error rates
}

// LogBotResponse logs bot response for analytics/debugging
func (b *BotService) LogBotResponse(phoneNumber string, response BotResponse) {
	status := "success"
	if !response.Success {
		status = "error"
	}

	log.Printf("[BOT_RESP] phone=%s command=%s status=%s error=%q",
		phoneNumber, response.CommandExecuted, status, response.Error)

	// TODO: Store in database for analytics
}

// GetUserGroup retrieves user's tontine group (mock)
// TODO: Replace with actual API call to Social Service
func (b *BotService) GetUserGroup(phoneNumber string) (GroupInfo, error) {
	// Mock implementation
	return GroupInfo{
		ID:                 "group-1",
		Name:               "Les Mamans du Quartier",
		MembersCount:       8,
		Balance:            "150,000",
		ActiveCycle:        "Cycle 2",
		NextCollectionDate: "15 Avril 2026",
	}, nil
}

// GetUserBalance retrieves specific user's balance (mock)
// TODO: Replace with actual API call
func (b *BotService) GetUserBalance(phoneNumber string, groupID string) (string, error) {
	// Mock implementation
	return "150,000 FCFA", nil
}

// GetGroupTransactions retrieves group transactions (mock)
// TODO: Replace with actual API call
func (b *BotService) GetGroupTransactions(groupID string, limit int) ([]Transaction, error) {
	// Mock implementation - returns sample transactions
	transactions := []Transaction{
		{
			Timestamp:   "2026-04-05 14:30",
			From:        "Fatoumata",
			To:          "Collective",
			Amount:      "15,000",
			Description: "Contribution",
		},
	}

	return transactions, nil
}

// NotifyAdminOfError notifies admin if critical error occurs
func (b *BotService) NotifyAdminOfError(phoneNumber string, errorMsg string) error {
	log.Printf("[ADMIN_ALERT] Erreur pour %s: %s", phoneNumber, errorMsg)

	// TODO: Send notification to admin
	// - Email
	// - SMS
	// - In-app notification to dashboard

	return nil
}
