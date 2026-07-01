package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpandHomeReplacesTilde(t *testing.T) {
	home, _ := os.UserHomeDir()
	got := expandHome("~/.colony/colony.db")
	want := filepath.Join(home, ".colony", "colony.db")
	if got != want {
		t.Fatalf("expandHome = %q, want %q", got, want)
	}
	if expandHome("/abs/path") != "/abs/path" {
		t.Fatalf("absolute path must be unchanged")
	}
}
