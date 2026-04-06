package whatsapp

import (
	"testing"
)

func TestFormatResponse_Balance(t *testing.T) {
	response := BotResponse{
		CommandExecuted: "SOLDE",
		Success:         true,
		Data: GroupInfo{
			ID:                 "group-1",
			Name:               "Test Group",
			MembersCount:       5,
			Balance:            "100,000",
			NextCollectionDate: "2026-04-15",
		},
	}

	msg := FormatResponse(response, "+221771234567")

	if msg.To != "+221771234567" {
		t.Errorf("Expected To: +221771234567, got %s", msg.To)
	}

	if msg.Text == "" {
		t.Error("Expected non-empty response text")
	}

	if len(msg.Text) > 1024 {
		t.Errorf("Response exceeds WhatsApp limit: %d chars", len(msg.Text))
	}
}

func TestFormatResponse_Error(t *testing.T) {
	response := BotResponse{
		CommandExecuted: "SOLDE",
		Success:         false,
		Error:           "User not found",
	}

	msg := FormatResponse(response, "+221771234567")

	if msg.To != "+221771234567" {
		t.Errorf("Expected To: +221771234567, got %s", msg.To)
	}

	if msg.Text == "" {
		t.Error("Expected non-empty error message")
	}
}

func TestFormatErrorResponse(t *testing.T) {
	msg := FormatErrorResponse("Test error", "+221771234567")

	if msg.To != "+221771234567" {
		t.Errorf("Expected To: +221771234567, got %s", msg.To)
	}

	if msg.Text == "" {
		t.Error("Expected non-empty error message")
	}
}

func TestFormatAcknowledgement(t *testing.T) {
	msg := FormatAcknowledgement("+221771234567")

	if msg.To != "+221771234567" {
		t.Errorf("Expected To: +221771234567, got %s", msg.To)
	}

	if msg.Text == "" {
		t.Error("Expected non-empty acknowledgement message")
	}
}

func TestTruncateToWhatsAppLimit(t *testing.T) {
	longText := ""
	for i := 0; i < 200; i++ {
		longText += "This is a very long string. "
	}

	truncated := truncateToWhatsAppLimit(longText)

	if len(truncated) > 1024 {
		t.Errorf("Truncated text exceeds limit: %d chars", len(truncated))
	}
}

func TestGetHelpText(t *testing.T) {
	help := getHelpText()

	if help == "" {
		t.Error("Help text is empty")
	}

	if len(help) > 1024 {
		t.Errorf("Help text exceeds WhatsApp limit: %d chars", len(help))
	}
}
