package server

import (
	"os"
	"testing"

	"github.com/maniginam/waggle/internal/telegram"
)

func TestTelegramEnabledGate(t *testing.T) {
	os.Setenv("WAGGLE_TELEGRAM_ENABLED", "true")
	defer os.Unsetenv("WAGGLE_TELEGRAM_ENABLED")

	if telegramEnabled(telegram.Config{Token: ""}) {
		t.Error("must be disabled without a token even when flag is true")
	}
	if !telegramEnabled(telegram.Config{Token: "tok"}) {
		t.Error("must be enabled with flag true and a token present")
	}

	os.Unsetenv("WAGGLE_TELEGRAM_ENABLED")
	if telegramEnabled(telegram.Config{Token: "tok"}) {
		t.Error("must be disabled when flag is unset")
	}
}
