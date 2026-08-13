package telegram

import (
	"os"
	"testing"
)

func TestConfigFromEnvParsesAllowlist(t *testing.T) {
	os.Setenv("WAGGLE_TELEGRAM_TOKEN", "tok123")
	os.Setenv("WAGGLE_TELEGRAM_ALLOWED_CHATS", "111, 222 ,bad,333")
	os.Setenv("WAGGLE_PORT", "4999")
	defer func() {
		os.Unsetenv("WAGGLE_TELEGRAM_TOKEN")
		os.Unsetenv("WAGGLE_TELEGRAM_ALLOWED_CHATS")
		os.Unsetenv("WAGGLE_PORT")
	}()
	c := ConfigFromEnv()
	if c.Token != "tok123" {
		t.Errorf("token = %q", c.Token)
	}
	if len(c.AllowedChats) != 3 {
		t.Fatalf("expected 3 chats, got %d (%v)", len(c.AllowedChats), c.AllowedChats)
	}
	if c.APIBaseURL != "http://localhost:4999" {
		t.Errorf("APIBaseURL = %q", c.APIBaseURL)
	}
	if !c.ChatAllowed(222) || c.ChatAllowed(999) {
		t.Errorf("allowlist check wrong")
	}
}

func TestChatAllowedEmptyDeniesAll(t *testing.T) {
	c := Config{}
	if c.ChatAllowed(1) {
		t.Error("empty allowlist must deny")
	}
}
