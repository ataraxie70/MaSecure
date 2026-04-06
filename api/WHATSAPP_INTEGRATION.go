package main

import (
	"fmt"
	"log"
	"net/http"

	"masecure/api/gateway/whatsapp"
	"masecure/api/handlers"
)

// InitWhatsAppRoutes initializes WhatsApp webhook routes
// Call this from your main.go router setup
func InitWhatsAppRoutes(mux *http.ServeMux) {
	// Create bot service
	botService := whatsapp.NewBotService()

	// Create handler
	whatsappHandler := handlers.NewWhatsAppHandler(botService)

	// Register routes
	mux.HandleFunc("POST /webhooks/whatsapp/incoming", whatsappHandler.HandleWebhook)
	mux.HandleFunc("GET /webhooks/whatsapp/verify", whatsappHandler.HandleVerification)
	mux.HandleFunc("GET /webhooks/whatsapp/status", whatsappHandler.HandleStatus)

	log.Println("✅ WhatsApp routes initialized")
}

// Example integration in main.go:
/*
func main() {
	// ... existing setup ...

	mux := http.NewServeMux()

	// Register existing routes
	// mux.HandleFunc("POST /callbacks/mobile-money", callbackHandler)
	// ... etc ...

	// Initialize WhatsApp routes
	InitWhatsAppRoutes(mux)

	// ... start server ...
	log.Println("Starting server on :8000")
	http.ListenAndServe(":8000", mux)
}
*/

// LocalTestBot shows how to test the bot locally without WhatsApp API
func LocalTestBot() {
	fmt.Println("\n" + "="*60)
	fmt.Println("🤖 WhatsApp Bot v0.1 - Local Test")
	fmt.Println("=" * 60)

	botService := whatsapp.NewBotService()

	// Test cases
	testCases := []struct {
		phone   string
		message string
		name    string
	}{
		{"+221771234567", "SOLDE", "Check Balance"},
		{"+221771234567", "solde", "Balance (lowercase)"},
		{"+221771111111", "HISTO", "Check History"},
		{"+221772222222", "CYCLE", "Check Cycle"},
		{"+221773333333", "INFO", "Check Info"},
		{"+221774444444", "AIDE", "Get Help"},
		{"+221775555555", "RANDOM", "Unknown Command"},
	}

	for _, test := range testCases {
		fmt.Println("\n" + "-"*60)
		fmt.Printf("📱 Input: %s from %s\n", test.message, test.phone)
		fmt.Printf("   (%s)\n", test.name)

		// Parse intent
		intent := whatsapp.ParseIntent(test.phone, test.message)
		fmt.Printf("   Intent: %s\n", intent.Command)

		// Process
		response := botService.HandleIntent(intent)

		// Format
		formatted := whatsapp.FormatResponse(response, test.phone)

		// Display
		fmt.Printf("   Status: ")
		if response.Success {
			fmt.Print("✅ Success\n")
		} else {
			fmt.Printf("❌ Error: %s\n", response.Error)
		}

		fmt.Println("   Response:")
		for _, line := range []rune(formatted.Text) {
			fmt.Printf("   %c", line)
		}
		fmt.Println("\n")
	}

	fmt.Println("=" * 60)
	fmt.Println("✅ Local test complete")
	fmt.Println("="*60 + "\n")
}
