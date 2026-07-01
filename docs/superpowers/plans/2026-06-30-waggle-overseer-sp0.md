# Waggle Overseer (SP-0) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stream live Colony + Gas Town state into Waggle's existing SSE event stream via read-only, non-invasive adapters.

**Architecture:** A new `internal/overseer` package defines a `Source` interface; a Colony adapter (read-only SQLite) and a Gas Town adapter (exec `gt ... --json`) produce normalized `model.Event`s; a poller dedups them and emits through the existing store + event hub. Off by default behind `OVERSEER_ENABLED`. Nothing in Colony or Gas Town changes.

**Tech Stack:** Go 1.25, `modernc.org/sqlite` (already in go.mod), stdlib `os/exec`, `encoding/json`, existing `internal/model`, `internal/store`, `internal/event`.

## Global Constraints

- **Read-only, always.** Colony DB opened `file:<path>?mode=ro&_busy_timeout=2000`. `gt` argv allowlist = `{trail, agents}` only; any other subcommand is rejected in code.
- **Fire-and-forget, but not silent.** Every adapter error degrades to an empty result and the poller never stops; a panicking source must not kill other sources or the server. On each swallowed failure emit a **non-fatal** `log.Printf("overseer: ...")` line (via stdlib `log`, the repo convention) so a missing engine is diagnosable. Logging must never change control flow.
- **Opt-in.** `OVERSEER_ENABLED` unset/`!= "true"` → Waggle behaves exactly as today (no goroutine started).
- **No engine coupling.** Zero changes to `internal/store`, `internal/mcp`, Colony, or Gas Town. Only new files under `internal/overseer/` + a small wiring block in `internal/server/server.go`.
- **Live Colony DB path:** `~/.colony/colony.db` (the repo-root `colony.db` is a 0-byte placeholder — never use it).
- **Generic event types** (dotted, distinct from existing snake_case consts): `task.<status>`, `worker.running`, `brain.cycle`, `agent.commit`. `payload.source` is `"colony"` or `"gastown"`.
- Test command: `go test ./internal/overseer/ -count=1 -v`. Full suite: `make test`.
- Existing emit path (reuse, do not duplicate): `store.RecordEvent(*model.Event) error` then `eventHub.Publish(*model.Event)`. `model.Event{ID,Type,AgentID,TaskID,Payload,Timestamp}`; the store assigns `ID` in `RecordEvent`, so adapters leave `ID` empty and dedup on a **separate stable key**.

---

## File Structure

- Create `internal/overseer/source.go` — `Source` interface, `Snapshot`, `Item` (key + event).
- Create `internal/overseer/dedupe.go` — bounded seen-set, `filter([]Item) []Item`.
- Create `internal/overseer/colony.go` — read-only Colony SQLite adapter.
- Create `internal/overseer/gastown.go` — `gt ... --json` adapter with injectable runner + argv allowlist.
- Create `internal/overseer/overseer.go` — poller: registers sources, ticks, dedups, emits.
- Create tests alongside each (`*_test.go`).
- Modify `internal/server/server.go` — opt-in wiring + `~` expansion helper.

---

### Task 1: Source contract (`source.go`)

**Files:**
- Create: `internal/overseer/source.go`
- Test: `internal/overseer/source_test.go`

**Interfaces:**
- Produces: `type Source interface { Name() string; Poll(ctx context.Context) (Snapshot, error) }`; `type Item struct { Key string; Event *model.Event }`; `type Snapshot struct { Items []Item }`.

- [ ] **Step 1: Write the failing test**

```go
package overseer

import (
	"context"
	"testing"

	"github.com/maniginam/waggle/internal/model"
)

type fakeSource struct {
	name  string
	items []Item
}

func (f *fakeSource) Name() string { return f.name }
func (f *fakeSource) Poll(ctx context.Context) (Snapshot, error) {
	return Snapshot{Items: f.items}, nil
}

func TestSourcePollReturnsItems(t *testing.T) {
	var s Source = &fakeSource{
		name:  "fake",
		items: []Item{{Key: "k1", Event: &model.Event{Type: "task.queued", TaskID: "t1"}}},
	}
	snap, err := s.Poll(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(snap.Items) != 1 || snap.Items[0].Key != "k1" {
		t.Fatalf("got %+v", snap.Items)
	}
	if s.Name() != "fake" {
		t.Fatalf("name = %q", s.Name())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/overseer/ -run TestSourcePollReturnsItems -count=1`
Expected: FAIL — `undefined: Source` / `undefined: Item` / package does not compile.

- [ ] **Step 3: Write minimal implementation**

```go
package overseer

import (
	"context"

	"github.com/maniginam/waggle/internal/model"
)

// Item is a normalized event plus a stable key used for change-dedup.
// Event.ID is left empty; the store assigns it on RecordEvent.
type Item struct {
	Key   string
	Event *model.Event
}

// Snapshot is one read of a source's current state.
type Snapshot struct {
	Items []Item
}

// Source is a read-only producer of normalized events from an engine.
// Poll must be non-blocking-ish and must never mutate the engine.
type Source interface {
	Name() string
	Poll(ctx context.Context) (Snapshot, error)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/overseer/ -run TestSourcePollReturnsItems -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/overseer/source.go internal/overseer/source_test.go
git commit -m "feat(overseer): add Source contract"
```

---

### Task 2: Dedupe (`dedupe.go`)

**Files:**
- Create: `internal/overseer/dedupe.go`
- Test: `internal/overseer/dedupe_test.go`

**Interfaces:**
- Consumes: `Item` (Task 1).
- Produces: `func newDeduper(capacity int) *deduper`; `func (d *deduper) filter(items []Item) []Item`.

- [ ] **Step 1: Write the failing test**

```go
package overseer

import (
	"testing"

	"github.com/maniginam/waggle/internal/model"
)

func item(key string) Item { return Item{Key: key, Event: &model.Event{TaskID: key}} }

func TestDeduperEmitsOnlyUnseen(t *testing.T) {
	d := newDeduper(10)
	first := d.filter([]Item{item("a"), item("b")})
	if len(first) != 2 {
		t.Fatalf("first pass = %d, want 2", len(first))
	}
	second := d.filter([]Item{item("a"), item("b"), item("c")})
	if len(second) != 1 || second[0].Key != "c" {
		t.Fatalf("second pass = %+v, want only c", second)
	}
}

func TestDeduperEvictsOldestPastCapacity(t *testing.T) {
	d := newDeduper(2)
	d.filter([]Item{item("a"), item("b")}) // seen: a,b
	d.filter([]Item{item("c")})            // adds c, evicts a; seen: b,c
	again := d.filter([]Item{item("a")})   // a was evicted -> emits again
	if len(again) != 1 || again[0].Key != "a" {
		t.Fatalf("got %+v, want a re-emitted", again)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/overseer/ -run TestDeduper -count=1`
Expected: FAIL — `undefined: newDeduper`.

- [ ] **Step 3: Write minimal implementation**

```go
package overseer

// deduper tracks recently-seen stable keys so unchanged state is not re-emitted.
// Bounded so it cannot grow without limit; evicts oldest first.
type deduper struct {
	seen  map[string]struct{}
	order []string
	cap   int
}

func newDeduper(capacity int) *deduper {
	if capacity < 1 {
		capacity = 1
	}
	return &deduper{seen: make(map[string]struct{}), cap: capacity}
}

func (d *deduper) filter(items []Item) []Item {
	out := make([]Item, 0, len(items))
	for _, it := range items {
		if _, ok := d.seen[it.Key]; ok {
			continue
		}
		d.seen[it.Key] = struct{}{}
		d.order = append(d.order, it.Key)
		out = append(out, it)
		if len(d.order) > d.cap {
			oldest := d.order[0]
			d.order = d.order[1:]
			delete(d.seen, oldest)
		}
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/overseer/ -run TestDeduper -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/overseer/dedupe.go internal/overseer/dedupe_test.go
git commit -m "feat(overseer): add bounded change-deduper"
```

---

### Task 3: Colony adapter (`colony.go`)

**Files:**
- Create: `internal/overseer/colony.go`
- Test: `internal/overseer/colony_test.go`

**Interfaces:**
- Consumes: `Item`, `Snapshot`, `Source` (Task 1).
- Produces: `func NewColonySource(dbPath string) *ColonySource` (implements `Source`).

**Real schema** (verified against live `~/.colony/colony.db`):
`tasks(id TEXT, type TEXT, priority TEXT, status TEXT, payload TEXT, created_at TEXT, started_at TEXT, worker_pid INTEGER, retries INTEGER, result TEXT, parent_id TEXT)`. `payload` is JSON; `project` may live inside it. Statuses: `queued|pending|running|completed|failed`.

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/overseer/ -run TestColony -count=1`
Expected: FAIL — `undefined: NewColonySource`.

- [ ] **Step 3: Write minimal implementation**

```go
package overseer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/maniginam/waggle/internal/model"
	_ "modernc.org/sqlite"
)

type ColonySource struct {
	dbPath string
	limit  int
}

func NewColonySource(dbPath string) *ColonySource {
	return &ColonySource{dbPath: dbPath, limit: 50}
}

func (c *ColonySource) Name() string { return "colony" }

func (c *ColonySource) Poll(ctx context.Context) (Snapshot, error) {
	db, err := sql.Open("sqlite", "file:"+c.dbPath+"?mode=ro&_busy_timeout=2000")
	if err != nil {
		log.Printf("overseer: colony open %s: %v", c.dbPath, err)
		return Snapshot{}, nil // degrade to empty, never fatal
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, `
		SELECT id, type, priority, status, payload, started_at, worker_pid
		FROM tasks
		ORDER BY coalesce(started_at, created_at) DESC
		LIMIT ?`, c.limit)
	if err != nil {
		log.Printf("overseer: colony query: %v", err)
		return Snapshot{}, nil
	}
	defer rows.Close()

	var items []Item
	for rows.Next() {
		var id, typ, prio, status string
		var payload, startedAt sql.NullString
		var workerPID sql.NullInt64
		if err := rows.Scan(&id, &typ, &prio, &status, &payload, &startedAt, &workerPID); err != nil {
			continue
		}
		items = append(items, colonyItem(id, typ, prio, status, payload.String, startedAt.String, workerPID))
	}
	return Snapshot{Items: items}, nil
}

func colonyItem(id, typ, prio, status, payload, startedAt string, workerPID sql.NullInt64) Item {
	pl := map[string]any{"source": "colony", "type": typ, "priority": prio}
	if payload != "" {
		var p struct {
			Project string `json:"project"`
		}
		if json.Unmarshal([]byte(payload), &p) == nil && p.Project != "" {
			pl["project"] = p.Project
		}
	}

	var et model.EventType
	switch {
	case typ == "roi-brain" && (status == "running" || status == "queued"):
		et = "brain.cycle"
	case status == "running":
		et = "worker.running"
		pl["started_at"] = startedAt
		if workerPID.Valid {
			pl["worker_pid"] = workerPID.Int64
		}
	default:
		et = model.EventType("task." + status)
	}

	return Item{
		Key: fmt.Sprintf("colony:%s:%s", id, status),
		Event: &model.Event{
			Type:      et,
			TaskID:    id,
			Payload:   pl,
			Timestamp: time.Now(),
		},
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/overseer/ -run TestColony -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/overseer/colony.go internal/overseer/colony_test.go
git commit -m "feat(overseer): add read-only Colony SQLite adapter"
```

---

### Task 4: Gas Town adapter (`gastown.go`)

**Files:**
- Create: `internal/overseer/gastown.go`
- Test: `internal/overseer/gastown_test.go`

**Interfaces:**
- Consumes: `Item`, `Snapshot`, `Source` (Task 1).
- Produces: `func NewGasTownSource(bin string) *GasTownSource` (implements `Source`); exported test seam `(*GasTownSource).run runner` where `type runner func(ctx context.Context, name string, args ...string) ([]byte, error)`.

> **Note:** `gt trail --json` currently emits `null` (Gas Town's `bd` is version-mismatched on this machine), so the live JSON field shape is unverified. The fields below (`bead/agent/rig/message`) are provisional; the adapter tolerates `null`/non-JSON by returning empty. Re-verify field names against `gt trail --json` once `gt doctor` is green, before SP-1 renders them.

- [ ] **Step 1: Write the failing test**

```go
package overseer

import (
	"context"
	"errors"
	"testing"
)

func TestGasTownParsesTrail(t *testing.T) {
	src := NewGasTownSource("gt")
	src.run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return []byte(`[{"bead":"b1","agent":"polecat-1","rig":"waggle","message":"fix x"}]`), nil
	}
	snap, err := src.Poll(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(snap.Items) != 1 {
		t.Fatalf("want 1 item, got %d", len(snap.Items))
	}
	it := snap.Items[0]
	if it.Event.Type != "agent.commit" || it.Event.AgentID != "polecat-1" || it.Key != "gastown:commit:b1" {
		t.Fatalf("bad item: %+v / %+v", it.Event, it.Key)
	}
}

func TestGasTownNullDegradesEmpty(t *testing.T) {
	src := NewGasTownSource("gt")
	src.run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return []byte("null\n"), nil
	}
	snap, _ := src.Poll(context.Background())
	if len(snap.Items) != 0 {
		t.Fatalf("null should be empty, got %d", len(snap.Items))
	}
}

func TestGasTownExecErrorDegradesEmpty(t *testing.T) {
	src := NewGasTownSource("gt")
	src.run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return nil, errors.New("gt not found")
	}
	snap, err := src.Poll(context.Background())
	if err != nil || len(snap.Items) != 0 {
		t.Fatalf("exec error should degrade empty, got items=%d err=%v", len(snap.Items), err)
	}
}

func TestGasTownRejectsDisallowedSubcommand(t *testing.T) {
	called := false
	src := NewGasTownSource("gt")
	src.run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		called = true
		return nil, nil
	}
	if _, err := src.runGT(context.Background(), "down"); err == nil {
		t.Fatal("expected disallowed subcommand error")
	}
	if called {
		t.Fatal("runner must not be invoked for disallowed subcommand")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/overseer/ -run TestGasTown -count=1`
Expected: FAIL — `undefined: NewGasTownSource`.

- [ ] **Step 3: Write minimal implementation**

```go
package overseer

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"

	"github.com/maniginam/waggle/internal/model"
)

type runner func(ctx context.Context, name string, args ...string) ([]byte, error)

func execRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output() // stdout only
}

var gtAllowed = map[string]bool{"trail": true, "agents": true}

type GasTownSource struct {
	bin string
	run runner
}

func NewGasTownSource(bin string) *GasTownSource {
	return &GasTownSource{bin: bin, run: execRunner}
}

func (g *GasTownSource) Name() string { return "gastown" }

func (g *GasTownSource) Poll(ctx context.Context) (Snapshot, error) {
	out, err := g.runGT(ctx, "trail", "--json", "--limit", "20")
	if err != nil {
		log.Printf("overseer: gastown trail: %v", err)
		return Snapshot{}, nil // gt missing/broken -> empty, never fatal
	}
	return Snapshot{Items: parseTrail(out)}, nil
}

func (g *GasTownSource) runGT(ctx context.Context, args ...string) ([]byte, error) {
	if len(args) == 0 || !gtAllowed[args[0]] {
		return nil, fmt.Errorf("gt subcommand not allowed: %v", args)
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return g.run(cctx, g.bin, args...)
}

func parseTrail(out []byte) []Item {
	s := strings.TrimSpace(string(out))
	if s == "" || s == "null" {
		return nil
	}
	var commits []struct {
		Bead    string `json:"bead"`
		Agent   string `json:"agent"`
		Rig     string `json:"rig"`
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(s), &commits) != nil {
		return nil // tolerate warnings / non-JSON
	}
	items := make([]Item, 0, len(commits))
	for _, c := range commits {
		items = append(items, Item{
			Key: "gastown:commit:" + c.Bead,
			Event: &model.Event{
				Type:      "agent.commit",
				TaskID:    c.Bead,
				AgentID:   c.Agent,
				Payload:   map[string]any{"source": "gastown", "agent": c.Agent, "rig": c.Rig, "msg": c.Message},
				Timestamp: time.Now(),
			},
		})
	}
	return items
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/overseer/ -run TestGasTown -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/overseer/gastown.go internal/overseer/gastown_test.go
git commit -m "feat(overseer): add read-only Gas Town gt-json adapter"
```

---

### Task 5: Poller (`overseer.go`)

**Files:**
- Create: `internal/overseer/overseer.go`
- Test: `internal/overseer/overseer_test.go`

**Interfaces:**
- Consumes: `Source`, `Item` (Task 1), `newDeduper` (Task 2).
- Produces: `func New(store emitter, hub publisher) *Overseer`; `func (o *Overseer) Register(src Source, interval time.Duration)`; `func (o *Overseer) Run(ctx context.Context)`; internal `pollOnce`. `emitter` = `interface{ RecordEvent(*model.Event) error }` (satisfied by `*store.Store`); `publisher` = `interface{ Publish(*model.Event) }` (satisfied by `*event.Hub`).

- [ ] **Step 1: Write the failing test**

```go
package overseer

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/maniginam/waggle/internal/model"
)

type fakeStore struct {
	mu   sync.Mutex
	recs []*model.Event
}

func (f *fakeStore) RecordEvent(e *model.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recs = append(f.recs, e)
	return nil
}

type fakeHub struct {
	mu   sync.Mutex
	pubs []*model.Event
}

func (f *fakeHub) Publish(e *model.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pubs = append(f.pubs, e)
}

type panicSource struct{}

func (panicSource) Name() string                              { return "boom" }
func (panicSource) Poll(context.Context) (Snapshot, error)    { panic("kaboom") }

func TestPollOnceEmitsDedupedEvents(t *testing.T) {
	fs, fh := &fakeStore{}, &fakeHub{}
	o := New(fs, fh)
	o.Register(&fakeSource{name: "fake", items: []Item{
		{Key: "k1", Event: &model.Event{Type: "task.queued", TaskID: "t1"}},
	}}, time.Hour)

	o.pollOnce(context.Background(), &o.sources[0])
	o.pollOnce(context.Background(), &o.sources[0]) // same state -> no new emit

	if len(fs.recs) != 1 || len(fh.pubs) != 1 {
		t.Fatalf("want 1 record + 1 publish, got recs=%d pubs=%d", len(fs.recs), len(fh.pubs))
	}
}

func TestPollOncePanicIsContained(t *testing.T) {
	o := New(&fakeStore{}, &fakeHub{})
	o.Register(panicSource{}, time.Hour)
	// must not panic out of pollOnce
	o.pollOnce(context.Background(), &o.sources[0])
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/overseer/ -run TestPollOnce -count=1`
Expected: FAIL — `undefined: New`.

- [ ] **Step 3: Write minimal implementation**

```go
package overseer

import (
	"context"
	"log"
	"time"

	"github.com/maniginam/waggle/internal/model"
)

type emitter interface {
	RecordEvent(*model.Event) error
}

type publisher interface {
	Publish(*model.Event)
}

type sourceConfig struct {
	src      Source
	interval time.Duration
	dedup    *deduper
}

type Overseer struct {
	store   emitter
	hub     publisher
	sources []sourceConfig
}

func New(store emitter, hub publisher) *Overseer {
	return &Overseer{store: store, hub: hub}
}

func (o *Overseer) Register(src Source, interval time.Duration) {
	o.sources = append(o.sources, sourceConfig{src: src, interval: interval, dedup: newDeduper(1000)})
}

// Run polls every registered source on its interval until ctx is cancelled.
func (o *Overseer) Run(ctx context.Context) {
	for i := range o.sources {
		go o.runSource(ctx, &o.sources[i])
	}
	<-ctx.Done()
}

func (o *Overseer) runSource(ctx context.Context, sc *sourceConfig) {
	ticker := time.NewTicker(sc.interval)
	defer ticker.Stop()
	o.pollOnce(ctx, sc) // immediate first poll
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			o.pollOnce(ctx, sc)
		}
	}
}

func (o *Overseer) pollOnce(ctx context.Context, sc *sourceConfig) {
	defer func() {
		if r := recover(); r != nil { // one bad source never kills the loop
			log.Printf("overseer: source %s panicked: %v", sc.src.Name(), r)
		}
	}()
	snap, err := sc.src.Poll(ctx)
	if err != nil {
		log.Printf("overseer: source %s poll: %v", sc.src.Name(), err)
		return
	}
	for _, it := range sc.dedup.filter(snap.Items) {
		_ = o.store.RecordEvent(it.Event)
		o.hub.Publish(it.Event)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/overseer/ -count=1`
Expected: PASS (all overseer tests)

- [ ] **Step 5: Commit**

```bash
git add internal/overseer/overseer.go internal/overseer/overseer_test.go
git commit -m "feat(overseer): add poller with dedup + panic isolation"
```

---

### Task 6: Server wiring (opt-in)

**Files:**
- Modify: `internal/server/server.go` (struct + `New` + `Start`/`Stop`)
- Test: `internal/server/overseer_wiring_test.go`

**Interfaces:**
- Consumes: `overseer.New`, `overseer.NewColonySource`, `overseer.NewGasTownSource`, `(*Overseer).Register`, `(*Overseer).Run` (Tasks 1–5).
- Produces: opt-in startup driven by env. Helper `expandHome(path string) string`.

> Verify current field/ctor shape first: `grep -n "type Server struct" -A8 internal/server/server.go` and read `func New(` + `func (s *Server) Start()`. The snippets below assume `Server{ store, eventHub, ... }` (confirmed at server.go:48 `restAPI := api.New(s, eh)`). Adapt field names if they differ.

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/server/ -run TestExpandHome -count=1`
Expected: FAIL — `undefined: expandHome`.

- [ ] **Step 3: Add the helper + opt-in wiring**

Add the helper (new small block in `server.go`):

```go
func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}
```

Add a field to the `Server` struct:

```go
	overseerCancel context.CancelFunc
```

In `Start()` (before `return s.httpServer.ListenAndServe()`), add the opt-in block:

```go
	if os.Getenv("OVERSEER_ENABLED") == "true" {
		ov := overseer.New(s.store, s.eventHub)

		colonyPath := os.Getenv("COLONY_DB_PATH")
		if colonyPath == "" {
			colonyPath = "~/.colony/colony.db"
		}
		ov.Register(overseer.NewColonySource(expandHome(colonyPath)), interval("OVERSEER_COLONY_INTERVAL", 3*time.Second))

		gtBin := os.Getenv("GASTOWN_BIN")
		if gtBin == "" {
			gtBin = "gt"
		}
		ov.Register(overseer.NewGasTownSource(gtBin), interval("OVERSEER_GASTOWN_INTERVAL", 10*time.Second))

		ctx, cancel := context.WithCancel(context.Background())
		s.overseerCancel = cancel
		go ov.Run(ctx)
	}
```

Add the small interval helper:

```go
func interval(env string, def time.Duration) time.Duration {
	if v := os.Getenv(env); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
```

In the existing shutdown path (`Stop`/`shutdown` — find it with `grep -n "func (s \*Server)" internal/server/server.go`), add:

```go
	if s.overseerCancel != nil {
		s.overseerCancel()
	}
```

Ensure imports include: `context`, `os`, `path/filepath`, `strings`, `time`, and `"github.com/maniginam/waggle/internal/overseer"`.

- [ ] **Step 4: Run tests + build**

Run: `go test ./internal/server/ -run TestExpandHome -count=1 && go build ./...`
Expected: PASS + clean build.

- [ ] **Step 5: Full suite + manual smoke**

Run: `make test`
Expected: all pass.

Manual smoke (optional, proves end-to-end):
```bash
OVERSEER_ENABLED=true COLONY_DB_PATH=~/.colony/colony.db go run ./cmd/waggle start &
sleep 5
curl -sN -H 'Accept: text/event-stream' http://localhost:4740/api/events | head -20
# expect events with type task.* / worker.running / brain.cycle and payload.source=colony
```

- [ ] **Step 6: Commit**

```bash
git add internal/server/server.go internal/server/overseer_wiring_test.go
git commit -m "feat(overseer): opt-in server wiring (OVERSEER_ENABLED, default off)"
```

---

## Self-Review

**Spec coverage:**
- Source interface → Task 1. ✓
- Dedup/normalize → Tasks 2 (+ mapping inside Task 3/4). ✓
- Colony read-only adapter (real schema, ro mode, degrade-empty) → Task 3. ✓
- Gas Town adapter (allowlist, null-tolerant, injectable runner) → Task 4. ✓
- Poller (intervals, panic isolation, emit via store+hub) → Task 5. ✓
- Opt-in wiring + default-off invariant + path config → Task 6. ✓
- Event mapping table (`task.<status>`, `worker.running`, `brain.cycle`, `agent.commit`, `source` in payload) → Tasks 3/4. ✓
- Error-handling invariants (read-only, fire-and-forget, no contention, opt-in, degraded=empty) → enforced across Tasks 3–6. ✓
- `permission.pending`/STATUS-tail explicitly deferred in spec → not in plan. ✓

**Placeholder scan:** none — every code step is complete and runnable.

**Type consistency:** `Item{Key,Event}`, `Snapshot{Items}`, `Source{Name,Poll}` consistent across all tasks. `emitter.RecordEvent`/`publisher.Publish` match real `store.Store`/`event.Hub` signatures. `model.Event{Type,AgentID,TaskID,Payload,Timestamp}` matches `internal/model`. Dotted event-type strings consistent (`task.<status>`, `worker.running`, `brain.cycle`, `agent.commit`).

**Open verification (flagged, not blocking SP-0):** `gt trail --json` field names are provisional (gt currently returns `null`); re-confirm before SP-1 renders them.
