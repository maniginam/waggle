package id

import (
	"strings"
	"testing"
)

func TestNew(t *testing.T) {
	got := New()
	if !strings.HasPrefix(got, "wg-") {
		t.Errorf("expected prefix wg-, got %s", got)
	}
	if len(got) != 15 { // "wg-" + 12 hex chars
		t.Errorf("expected length 15, got %d (%s)", len(got), got)
	}
}

func TestNewUnique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id := New()
		if seen[id] {
			t.Errorf("duplicate id: %s", id)
		}
		seen[id] = true
	}
}
