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

func TestCleanupStaleTasks(t *testing.T) {
	s := tempStore(t)

	now := time.Now().UTC()
	old := now.Add(-30 * 24 * time.Hour).Format(time.RFC3339)
	recent := now.Add(-3 * 24 * time.Hour).Format(time.RFC3339)

	// Old backlog task, unassigned — should be closed
	s.db.Exec(`INSERT INTO tasks (id, title, description, criteria, status, priority, assignee, tags, estimate, deadline, created_at, updated_at, parent_id, depends_on, task_type, project_id, issue_number, issue_url)
		VALUES (?, 'stale-backlog', '', '[]', 'backlog', 'medium', '', '[]', '', '', ?, ?, '', '[]', 'task', '', 0, '')`,
		"stale-1", old, old)

	// Old ready task, unassigned — should be closed
	s.db.Exec(`INSERT INTO tasks (id, title, description, criteria, status, priority, assignee, tags, estimate, deadline, created_at, updated_at, parent_id, depends_on, task_type, project_id, issue_number, issue_url)
		VALUES (?, 'stale-ready', '', '[]', 'ready', 'low', '', '[]', '', '', ?, ?, '', '[]', 'task', '', 0, '')`,
		"stale-2", old, old)

	// Old in_progress task — should NOT be closed (active work)
	s.db.Exec(`INSERT INTO tasks (id, title, description, criteria, status, priority, assignee, tags, estimate, deadline, created_at, updated_at, parent_id, depends_on, task_type, project_id, issue_number, issue_url)
		VALUES (?, 'active-old', '', '[]', 'in_progress', 'high', 'agent-1', '[]', '', '', ?, ?, '', '[]', 'task', '', 0, '')`,
		"active-1", old, old)

	// Old ready task with assignee — should NOT be closed (someone owns it)
	s.db.Exec(`INSERT INTO tasks (id, title, description, criteria, status, priority, assignee, tags, estimate, deadline, created_at, updated_at, parent_id, depends_on, task_type, project_id, issue_number, issue_url)
		VALUES (?, 'assigned-old', '', '[]', 'ready', 'medium', 'agent-1', '[]', '', '', ?, ?, '', '[]', 'task', '', 0, '')`,
		"assigned-1", old, old)

	// Recent backlog task — should NOT be closed (too new)
	s.db.Exec(`INSERT INTO tasks (id, title, description, criteria, status, priority, assignee, tags, estimate, deadline, created_at, updated_at, parent_id, depends_on, task_type, project_id, issue_number, issue_url)
		VALUES (?, 'fresh-backlog', '', '[]', 'backlog', 'medium', '', '[]', '', '', ?, ?, '', '[]', 'task', '', 0, '')`,
		"fresh-1", recent, recent)

	n, err := s.CleanupStaleTasks(14)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("expected 2 stale tasks closed, got %d", n)
	}

	// Verify stale tasks are now done
	task, _ := s.GetTask("stale-1")
	if task.Status != model.TaskDone {
		t.Errorf("expected stale-1 to be done, got %s", task.Status)
	}

	// Verify others unchanged
	task, _ = s.GetTask("active-1")
	if task.Status != model.TaskInProgress {
		t.Errorf("expected active-1 to stay in_progress, got %s", task.Status)
	}
	task, _ = s.GetTask("assigned-1")
	if task.Assignee != "agent-1" {
		t.Errorf("expected assigned-1 to keep assignee, got %s", task.Assignee)
	}
	task, _ = s.GetTask("fresh-1")
	if task.Status != model.TaskBacklog {
		t.Errorf("expected fresh-1 to stay backlog, got %s", task.Status)
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
