package telegram

import (
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/maniginam/waggle/internal/api"
	"github.com/maniginam/waggle/internal/event"
	"github.com/maniginam/waggle/internal/store"
)

func newAPIServer(t *testing.T) (*store.Store, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	eh := event.NewHub()
	a := api.New(s, eh)
	ts := httptest.NewServer(a.Handler())
	t.Cleanup(ts.Close)
	return s, ts
}

func TestWaggleClientCreateListMove(t *testing.T) {
	_, ts := newAPIServer(t)
	w := NewWaggleClient(ts.URL)

	created, err := w.CreateTask("bot task", "p1")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("no task id returned")
	}
	tasks, err := w.ListTasks("p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if err := w.MoveTask(id, "in_progress"); err != nil {
		t.Fatal(err)
	}
	tasks, _ = w.ListTasks("p1")
	if tasks[0]["status"] != "in_progress" {
		t.Errorf("status = %v", tasks[0]["status"])
	}
}
