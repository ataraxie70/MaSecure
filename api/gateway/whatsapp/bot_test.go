package whatsapp

import (
	"testing"
)

func TestNewBotService(t *testing.T) {
	bot := NewBotService()
	if bot == nil {
		t.Error("Expected non-nil BotService")
	}
}

func TestHandleIntent_Balance(t *testing.T) {
	bot := NewBotService()

	intent := UserIntent{
		PhoneNumber: "+221771234567",
		Command:     IntentBalance,
		RawMessage:  "SOLDE",
	}

	response := bot.HandleIntent(intent)

	if !response.Success {
		t.Errorf("Expected success, got error: %s", response.Error)
	}

	if response.CommandExecuted != "SOLDE" {
		t.Errorf("Expected SOLDE, got %s", response.CommandExecuted)
	}

	// Check data is GroupInfo
	if _, ok := response.Data.(GroupInfo); !ok {
		t.Error("Expected GroupInfo data")
	}
}

func TestHandleIntent_History(t *testing.T) {
	bot := NewBotService()

	intent := UserIntent{
		PhoneNumber: "+221771234567",
		Command:     IntentHistory,
		RawMessage:  "HISTO",
	}

	response := bot.HandleIntent(intent)

	if !response.Success {
		t.Errorf("Expected success, got error: %s", response.Error)
	}

	if response.CommandExecuted != "HISTO" {
		t.Errorf("Expected HISTO, got %s", response.CommandExecuted)
	}
}

func TestHandleIntent_Cycle(t *testing.T) {
	bot := NewBotService()

	intent := UserIntent{
		PhoneNumber: "+221771234567",
		Command:     IntentCycle,
		RawMessage:  "CYCLE",
	}

	response := bot.HandleIntent(intent)

	if !response.Success {
		t.Errorf("Expected success, got error: %s", response.Error)
	}

	if response.CommandExecuted != "CYCLE" {
		t.Errorf("Expected CYCLE, got %s", response.CommandExecuted)
	}
}

func TestHandleIntent_Help(t *testing.T) {
	bot := NewBotService()

	intent := UserIntent{
		PhoneNumber: "+221771234567",
		Command:     IntentHelp,
		RawMessage:  "AIDE",
	}

	response := bot.HandleIntent(intent)

	if !response.Success {
		t.Errorf("Expected success, got error: %s", response.Error)
	}

	if response.CommandExecuted != "AIDE" {
		t.Errorf("Expected AIDE, got %s", response.CommandExecuted)
	}
}

func TestHandleIntent_Info(t *testing.T) {
	bot := NewBotService()

	intent := UserIntent{
		PhoneNumber: "+221771234567",
		Command:     IntentInfo,
		RawMessage:  "INFO",
	}

	response := bot.HandleIntent(intent)

	if !response.Success {
		t.Errorf("Expected success, got error: %s", response.Error)
	}

	if response.CommandExecuted != "INFO" {
		t.Errorf("Expected INFO, got %s", response.CommandExecuted)
	}
}

func TestHandleIntent_Unknown(t *testing.T) {
	bot := NewBotService()

	intent := UserIntent{
		PhoneNumber: "+221771234567",
		Command:     IntentUnknown,
		RawMessage:  "UNKNOWN",
	}

	response := bot.HandleIntent(intent)

	if response.Success {
		t.Error("Expected failure for unknown command")
	}

	if response.CommandExecuted != "UNKNOWN" {
		t.Errorf("Expected UNKNOWN, got %s", response.CommandExecuted)
	}
}

func TestValidatePhoneNumber(t *testing.T) {
	tests := []struct {
		phone string
		valid bool
	}{
		{"+221771234567", true},
		{"", false},
		{"+1234567890", true},
	}

	for _, test := range tests {
		result := ValidatePhoneNumber(test.phone)
		if result != test.valid {
			t.Errorf("ValidatePhoneNumber(%q): expected %v, got %v", test.phone, test.valid, result)
		}
	}
}

func TestValidateMessage(t *testing.T) {
	tests := []struct {
		msg   string
		valid bool
		name  string
	}{
		{"SOLDE", true, "normal command"},
		{"", false, "empty message"},
		{"x" + makeString(256), false, "too long"},
		{"HISTO", true, "valid command"},
	}

	for _, test := range tests {
		result := ValidateMessage(test.msg)
		if result != test.valid {
			t.Errorf("%s: expected %v, got %v", test.name, test.valid, result)
		}
	}
}

// Helper to create strings
func makeString(length int) string {
	result := ""
	for i := 0; i < length; i++ {
		result += "x"
	}
	return result
}

func TestGetUserGroup(t *testing.T) {
	bot := NewBotService()

	group, err := bot.GetUserGroup("+221771234567")

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if group.ID == "" {
		t.Error("Expected non-empty group ID")
	}
}

func TestRateLimitCheck(t *testing.T) {
	bot := NewBotService()

	// Should allow by default
	result := bot.RateLimitCheck("+221771234567")

	if !result {
		t.Error("Expected rate limit check to pass")
	}
}
