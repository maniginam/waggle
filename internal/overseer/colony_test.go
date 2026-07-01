package overseer

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func newFixtureDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "colony.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE tasks (
		id TEXT PRIMARY KEY, type TEXT NOT NULL, priority TEXT NOT NULL DEFAULT 'normal',
		status TEXT NOT NULL DEFAULT 'queued', payload TEXT, created_at TEXT NOT NULL,
		started_at TEXT, worker_pid INTEGER, retries INTEGER DEFAULT 0, result TEXT,
		parent_id TEXT)`); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO tasks (id,type,priority,status,payload,created_at,started_at,worker_pid) VALUES
		('t1','dev','normal','queued','{"project":"passive-income"}','2026-06-30T00:00:00Z',NULL,NULL),
		('t2','dev','high','running','{}','2026-06-30T00:00:01Z','2026-06-30T00:00:05Z',4242),
		('t3','roi-brain','critical','queued','{}','2026-06-30T00:00:02Z',NULL,NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestColonyPollMapsRows(t *testing.T) {
	src := NewColonySource(newFixtureDB(t))
	snap, err := src.Poll(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	byTask := map[string]Item{}
	for _, it := range snap.Items {
		byTask[it.Event.TaskID] = it
	}
	if got := byTask["t1"].Event.Type; got != "task.queued" {
		t.Errorf("t1 type = %q, want task.queued", got)
	}
	if got := byTask["t1"].Event.Payload.(map[string]any)["project"]; got != "passive-income" {
		t.Errorf("t1 project = %v", got)
	}
	if got := byTask["t2"].Event.Type; got != "worker.running" {
		t.Errorf("t2 type = %q, want worker.running", got)
	}
	if got := byTask["t3"].Event.Type; got != "brain.cycle" {
		t.Errorf("t3 type = %q, want brain.cycle", got)
	}
	if byTask["t1"].Key != "colony:t1:queued" {
		t.Errorf("t1 key = %q", byTask["t1"].Key)
	}
}

func TestColonyPollMissingDBDegradesEmpty(t *testing.T) {
	src := NewColonySource("/nonexistent/colony.db")
	snap, err := src.Poll(context.Background())
	if err != nil {
		t.Fatalf("missing db should not error, got %v", err)
	}
	if len(snap.Items) != 0 {
		t.Fatalf("want empty, got %d", len(snap.Items))
	}
}
