package whatsapp

import (
	"strings"
)

// ParseIntent extracts user intent from raw message
func ParseIntent(phoneNumber, rawMessage string) UserIntent {
	// Normalize message: trim, uppercase
	normalized := strings.ToUpper(strings.TrimSpace(rawMessage))

	// Detect command
	command := detectCommand(normalized)

	return UserIntent{
		PhoneNumber: phoneNumber,
		Command:     command,
		RawMessage:  rawMessage,
	}
}

// detectCommand identifies which command user wants to run
func detectCommand(normalized string) Intent {
	// Check for exact match
	switch normalized {
	case "SOLDE", "SOLDES", "BALANCE":
		return IntentBalance
	case "HISTO", "HISTORIQUE", "HISTORY", "TRANSACTIONS":
		return IntentHistory
	case "CYCLE", "CYCLES", "GROUPE":
		return IntentCycle
	case "AIDE", "HELP", "?", "COMMANDS":
		return IntentHelp
	case "INFO", "INFOS", "INFORMATION":
		return IntentInfo
	}

	// Check for partial match (if user types extra words)
	parts := strings.Fields(normalized)
	if len(parts) > 0 {
		firstWord := parts[0]
		switch firstWord {
		case "SOLDE":
			return IntentBalance
		case "HISTO":
			return IntentHistory
		case "CYCLE":
			return IntentCycle
		case "AIDE":
			return IntentHelp
		case "INFO":
			return IntentInfo
		}
	}

	return IntentUnknown
}

// GetCommandDescription returns user-friendly description of command
func GetCommandDescription(command Intent) string {
	descriptions := map[Intent]string{
		IntentBalance: "Consulter votre solde",
		IntentHistory: "Voir l'historique des transactions",
		IntentCycle:   "Infos sur le cycle en cours",
		IntentHelp:    "Afficher l'aide",
		IntentInfo:    "Infos sur le groupe",
		IntentUnknown: "Commande non reconnue",
	}

	if desc, ok := descriptions[command]; ok {
		return desc
	}

	return "Commande inconnue"
}
