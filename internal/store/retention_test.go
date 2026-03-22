package store

import (
	"testing"
	"time"

	"github.com/maniginam/waggle/internal/model"
)

func TestCleanupEvents(t *testing.T) {
	s := tempStore(t)

	// Create an event with a very old timestamp
	s.db.Exec(`INSERT INTO events (id, type, agent_id, task_id, payload, timestamp) VALUES (?, ?, '', '', '{}', ?)`,
		"old-1", string(model.EventTaskCreated), time.Now().UTC().Add(-60*24*time.Hour).Format(time.RFC3339))
	s.db.Exec(`INSERT INTO events (id, type, agent_id, task_id, payload, timestamp) VALUES (?, ?, '', '', '{}', ?)`,
		"new-1", string(model.EventTaskCreated), time.Now().UTC().Format(time.RFC3339))

	n, err := s.CleanupEvents(30)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("expected 1 event cleaned, got %d", n)
	}

	events, _ := s.ListEvents(10)
	if len(events) != 1 {
		t.Errorf("expected 1 event remaining, got %d", len(events))
	}
}

func TestCleanupMessages(t *testing.T) {
	s := tempStore(t)

	// Insert old read message
	s.db.Exec(`INSERT INTO messages (id, "from", "to", body, read, created_at) VALUES (?, ?, ?, ?, 1, ?)`,
		"old-msg", "agent-1", "agent-2", "old", time.Now().UTC().Add(-14*24*time.Hour).Format(time.RFC3339))

	// Insert recent read message
	s.db.Exec(`INSERT INTO messages (id, "from", "to", body, read, created_at) VALUES (?, ?, ?, ?, 1, ?)`,
		"new-msg", "agent-1", "agent-2", "new", time.Now().UTC().Format(time.RFC3339))

	// Insert old unread message (should NOT be cleaned)
	s.db.Exec(`INSERT INTO messages (id, "from", "to", body, read, created_at) VALUES (?, ?, ?, ?, 0, ?)`,
		"unread-msg", "agent-1", "agent-2", "unread", time.Now().UTC().Add(-14*24*time.Hour).Format(time.RFC3339))

	n, err := s.CleanupMessages(7)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("expected 1 message cleaned, got %d", n)
	}
}
