package whatsapp

import (
	"fmt"
	"strings"
)

// FormatResponse converts BotResponse to WhatsApp message
func FormatResponse(response BotResponse, phoneNumber string) WhatsAppMessage {
	var text string

	if !response.Success {
		// Error case
		text = fmt.Sprintf("❌ Erreur\n━━━━━━━━━━━━━━━\n%s\n\nTapez AIDE pour l'aide.", response.Error)
	} else {
		// Success case - format based on command
		switch response.CommandExecuted {
		case "SOLDE":
			text = formatBalance(response.Data)
		case "HISTO":
			text = formatHistory(response.Data)
		case "CYCLE":
			text = formatCycle(response.Data)
		case "AIDE":
			text = getHelpText()
		case "INFO":
			text = formatInfo(response.Data)
		default:
			text = "✓ Commande exécutée\n\nTapez AIDE pour l'aide."
		}
	}

	return WhatsAppMessage{
		To:   phoneNumber,
		Text: text,
	}
}

// formatBalance formats balance response
func formatBalance(data interface{}) string {
	info, ok := data.(GroupInfo)
	if !ok {
		return "❌ Erreur: données indisponibles"
	}

	// Truncate if too long (WhatsApp limit ~1024 chars)
	result := fmt.Sprintf(`💰 Votre Solde
━━━━━━━━━━━━━━━
Solde: %s FCFA
Groupe: %s
Membres: %d

📅 Prochaine collecte: %s

👉 Utilisez HISTO pour voir plus`,
		info.Balance,
		info.Name,
		info.MembersCount,
		info.NextCollectionDate,
	)

	return truncateToWhatsAppLimit(result)
}

// formatHistory formats transaction history response
func formatHistory(data interface{}) string {
	transactions, ok := data.([]Transaction)
	if !ok || len(transactions) == 0 {
		return "📊 Historique: Aucune transaction"
	}

	var buf strings.Builder
	buf.WriteString("📊 Historique (5 dernières)\n")
	buf.WriteString("━━━━━━━━━━━━━━━\n")

	for i, tx := range transactions {
		if i >= 5 {
			break
		}
		buf.WriteString(fmt.Sprintf("%d. %s → %s\n   💳 %s FCFA\n",
			i+1, tx.From, tx.To, tx.Amount))
	}

	buf.WriteString("\n👉 Tapez SOLDE pour voir votre solde")

	return truncateToWhatsAppLimit(buf.String())
}

// formatCycle formats active cycle info
func formatCycle(data interface{}) string {
	cycle, ok := data.(CycleInfo)
	if !ok {
		return "❌ Erreur: données de cycle indisponibles"
	}

	result := fmt.Sprintf(`📅 Cycle en Cours
━━━━━━━━━━━━━━━
Cycle: %s
Status: %s
Début: %s
Fin: %s
Progression: %s

👉 Tapez INFO pour les détails du groupe`,
		cycle.Name,
		cycle.Status,
		cycle.StartDate,
		cycle.EndDate,
		cycle.Progress,
	)

	return truncateToWhatsAppLimit(result)
}

// formatInfo formats group info
func formatInfo(data interface{}) string {
	info, ok := data.(GroupInfo)
	if !ok {
		return "❌ Erreur: données du groupe indisponibles"
	}

	result := fmt.Sprintf(`ℹ️ Infos Groupe
━━━━━━━━━━━━━━━
Groupe: %s
Membres: %d
Solde: %s FCFA
Cycle: %s

👉 Tapez HISTO pour l'historique`,
		info.Name,
		info.MembersCount,
		info.Balance,
		info.ActiveCycle,
	)

	return truncateToWhatsAppLimit(result)
}

// getHelpText returns help message
func getHelpText() string {
	return `🆘 Aide - Commandes Disponibles
━━━━━━━━━━━━━━━━━━━━━━━━

SOLDE  → 💰 Voir votre solde
HISTO  → 📊 Dernières transactions
CYCLE  → 📅 Info cycle en cours
INFO   → ℹ️ Infos du groupe
AIDE   → 🆘 Cette aide

━━━━━━━━━━━━━━━━━━━━━━━━
💬 Tapez juste le mot-clé`
}

// truncateToWhatsAppLimit ensures message fits WhatsApp limit
func truncateToWhatsAppLimit(text string) string {
	limit := 1024
	if len(text) <= limit {
		return text
	}

	truncated := text[:limit-3] + "...\n👉 Tapez AIDE"
	return truncated
}

// FormatErrorResponse creates error message
func FormatErrorResponse(errorMsg string, phoneNumber string) WhatsAppMessage {
	text := fmt.Sprintf(`❌ Erreur
━━━━━━━━━━━━━━━
%s

Tapez AIDE pour l'aide.`, errorMsg)

	return WhatsAppMessage{
		To:   phoneNumber,
		Text: truncateToWhatsAppLimit(text),
	}
}

// FormatAcknowledgement creates ack message for unknown commands
func FormatAcknowledgement(phoneNumber string) WhatsAppMessage {
	return WhatsAppMessage{
		To:   phoneNumber,
		Text: `❓ Commande non reconnue\n━━━━━━━━━━━━━━━\nTapez AIDE pour voir les commandes disponibles.`,
	}
}
