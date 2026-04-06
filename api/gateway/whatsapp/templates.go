package whatsapp

// ResponseTemplates contains all predefined message templates
type ResponseTemplates struct {
	// Command responses
	BalanceTemplate string
	HistoryTemplate string
	CycleTemplate   string
	HelpTemplate    string
	InfoTemplate    string

	// Errors
	ErrorTemplate        string
	UnknownTemplate      string
	UnauthorizedTemplate string

	// System
	WelcomeTemplate  string
	GreetingTemplate string
}

// GetTemplates returns all response templates
func GetTemplates() ResponseTemplates {
	return ResponseTemplates{
		BalanceTemplate: `💰 Votre Solde
━━━━━━━━━━━━━━━
Solde: %s FCFA
Groupe: %s
Membres: %d

📅 Prochaine collecte: %s

👉 Utilisez HISTO pour voir plus`,

		HistoryTemplate: `📊 Historique (5 dernières)
━━━━━━━━━━━━━━━
%s

👉 Tapez SOLDE pour voir votre solde`,

		CycleTemplate: `📅 Cycle en Cours
━━━━━━━━━━━━━━━
Cycle: %s
Status: %s
Début: %s
Fin: %s
Progression: %s

👉 Tapez INFO pour les détails`,

		HelpTemplate: `🆘 Aide - Commandes Disponibles
━━━━━━━━━━━━━━━━━━━━━━━━

SOLDE  → 💰 Voir votre solde
HISTO  → 📊 Dernières transactions
CYCLE  → 📅 Info cycle en cours
INFO   → ℹ️ Infos du groupe
AIDE   → 🆘 Cette aide

━━━━━━━━━━━━━━━━━━━━━━━━
💬 Tapez juste le mot-clé`,

		InfoTemplate: `ℹ️ Infos Groupe
━━━━━━━━━━━━━━━
Groupe: %s
Membres: %d
Solde: %s FCFA
Cycle: %s

👉 Tapez HISTO pour l'historique`,

		ErrorTemplate: `❌ Erreur
━━━━━━━━━━━━━━━
%s

Tapez AIDE pour l'aide.`,

		UnknownTemplate: `❓ Commande non reconnue
━━━━━━━━━━━━━━━
Tapez AIDE pour voir les commandes disponibles.`,

		UnauthorizedTemplate: `🚫 Accès refusé
━━━━━━━━━━━━━━━
Vous n'êtes pas membre de ce groupe.

Tapez AIDE pour les commandes.`,

		WelcomeTemplate: `👋 Bienvenue sur MaSecure!
━━━━━━━━━━━━━━━━━━━━
Gestion simple et sécurisée de vos tontines.

Tapez AIDE pour commencer.`,

		GreetingTemplate: `👋 Bonjour %s!
━━━━━━━━━━━━━━━
Que désirez-vous?

SOLDE  → Voir votre solde
HISTO  → Transactions
AIDE   → Aide`,
	}
}

// GetCommandEmoji returns emoji for command
func GetCommandEmoji(command Intent) string {
	emojis := map[Intent]string{
		IntentBalance: "💰",
		IntentHistory: "📊",
		IntentCycle:   "📅",
		IntentHelp:    "🆘",
		IntentInfo:    "ℹ️",
		IntentUnknown: "❓",
	}

	if emoji, ok := emojis[command]; ok {
		return emoji
	}

	return "✓"
}

// FormatTransactionLine formats a single transaction for display
func FormatTransactionLine(index int, from, to, amount string) string {
	return "%d. %s → %s\n   💳 %s FCFA\n"
}

// ErrorMessages contains predefined error messages
var ErrorMessages = map[string]string{
	"not_found":     "Données non trouvées. Vérifiez votre numéro.",
	"api_error":     "Erreur serveur. Réessayez dans quelques instants.",
	"unauthorized":  "Vous n'êtes pas autorisé. Contactez l'administrateur.",
	"invalid_input": "Entrée invalide. Tapez AIDE pour l'aide.",
	"service_down":  "Service indisponible. Réessayez plus tard.",
	"timeout":       "Requête expirée. Réessayez.",
}

// GetErrorMessage returns user-friendly error message
func GetErrorMessage(errorCode string) string {
	if msg, ok := ErrorMessages[errorCode]; ok {
		return msg
	}
	return "Une erreur s'est produite. Réessayez."
}

// SuccessMessages contains predefined success messages
var SuccessMessages = map[string]string{
	"balance_retrieved": "✓ Solde récupéré avec succès",
	"history_retrieved": "✓ Historique récupéré",
	"cycle_retrieved":   "✓ Infos cycle récupérées",
	"info_retrieved":    "✓ Infos du groupe récupérées",
}

// ValidationMessages contains input validation messages
var ValidationMessages = map[string]string{
	"empty_input":    "Message vide. Tapez AIDE.",
	"too_long":       "Message trop long.",
	"invalid_format": "Format invalide.",
}

// SystemMessages contains system-level messages
var SystemMessages = map[string]string{
	"welcome":      "Bienvenue! Tapez AIDE pour commencer.",
	"goodbye":      "À bientôt!",
	"maintenance":  "Maintenance en cours. Réessayez dans quelques instants.",
	"rate_limited": "Trop de requêtes. Attendez un moment.",
}
