# Waggle Agile Task Engine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add sprints, backlog, story points, a kanban board, advisory WIP limits, and burndown to Waggle by extending the existing task/project model.

**Architecture:** Additive-only. The existing `tasks.status` enum (backlog/ready/in_progress/review/done/blocked) becomes the fixed kanban columns. New `sprints` table plus three new `tasks` columns (`sprint_id`, `story_points`, `board_order`). Store → REST API (emitting existing SSE events) → single-file dashboard board view → 4 new MCP tools. No new runtime dependencies; single Go binary + embedded SQLite preserved.

**Tech Stack:** Go, `modernc.org/sqlite`, standard `net/http`, HTML5 drag-and-drop in `mission-control.html`, MCP JSON-RPC adapter.

## Global Constraints

- Migrations are additive only: new table via `CREATE TABLE IF NOT EXISTS`; new columns via the existing `pragma_table_info` check + `ALTER TABLE ADD COLUMN` loop in `store.migrate()`. No destructive changes, no data migration.
- TDD mandatory (repo standard): failing test first, minimal code to pass, refactor while green. Commit after each green step.
- Never sign commits: `git commit --no-gpg-sign`. No Co-Authored-By / attribution footers.
- Store test helper: `tempStore(t)` (in `internal/store/store_test.go`).
- API test helper: `setup(t) (*API, *httptest.Server)` (in `internal/api/api_test.go`).
- MCP test helpers: `setupMCP(t)` and `callMCP(t, adapter, method, id, params)` (in `internal/mcp/mcp_test.go`).
- Timestamps stored as RFC3339 strings, UTC. Empty string means "unset".
- WIP limits are **advisory** in A: the store never hard-blocks a move; the UI warns. Stored in the existing `settings` table.
- Board columns are the existing status enum, fixed. Do NOT add configurable columns (that is sub-project C).

---

### Task 1: Model — Sprint type and new Task fields

**Files:**
- Modify: `internal/model/model.go`
- Test: `internal/model/model_test.go` (create if absent)

**Interfaces:**
- Produces: `model.Sprint` struct; `model.SprintState` type with consts `SprintPlanned="planned"`, `SprintActive="active"`, `SprintClosed="closed"` and method `Valid() bool`; new `Task` fields `SprintID string`, `StoryPoints int`, `BoardOrder float64`.

- [ ] **Step 1: Write the failing test**

Create/append `internal/model/model_test.go`:

```go
package model

import "testing"

func TestSprintStateValid(t *testing.T) {
	for _, s := range []SprintState{SprintPlanned, SprintActive, SprintClosed} {
		if !s.Valid() {
			t.Errorf("expected %q valid", s)
		}
	}
	if SprintState("bogus").Valid() {
		t.Error("expected bogus invalid")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/model/ -run TestSprintStateValid -v`
Expected: FAIL (undefined: SprintPlanned / SprintState).

- [ ] **Step 3: Write minimal implementation**

In `internal/model/model.go` add near the other enums:

```go
type SprintState string

const (
	SprintPlanned SprintState = "planned"
	SprintActive  SprintState = "active"
	SprintClosed  SprintState = "closed"
)

func (s SprintState) Valid() bool {
	switch s {
	case SprintPlanned, SprintActive, SprintClosed:
		return true
	}
	return false
}

type Sprint struct {
	ID        string      `json:"id"`
	ProjectID string      `json:"project_id"`
	Name      string      `json:"name"`
	Goal      string      `json:"goal,omitempty"`
	State     SprintState `json:"state"`
	StartDate string      `json:"start_date,omitempty"`
	EndDate   string      `json:"end_date,omitempty"`
	CreatedAt time.Time   `json:"created_at"`
}
```

Add the three fields to the `Task` struct (after `ProjectID`):

```go
	SprintID    string  `json:"sprint_id,omitempty"`
	StoryPoints int     `json:"story_points,omitempty"`
	BoardOrder  float64 `json:"board_order"`
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/model/ -run TestSprintStateValid -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/model/model.go internal/model/model_test.go
git commit --no-gpg-sign -m "feat(model): add Sprint type and agile Task fields"
```

---

### Task 2: Store — migrations for sprints table and task columns

**Files:**
- Modify: `internal/store/store.go` (the `migrate()` method; the `CreateTask`/`GetTask`/`ListTasks`/`scanTask` task column list)
- Test: `internal/store/store_test.go`

**Interfaces:**
- Consumes: `model.Task` fields from Task 1.
- Produces: `tasks` rows now round-trip `sprint_id`, `story_points`, `board_order`; `sprints` table exists.

Note: the task SELECT/INSERT column lists and `scanTask` must be extended together or scans break. Append the three columns to the END of every task column list and to `scanTask`'s `Scan(...)` in the same order: `sprint_id, story_points, board_order`.

- [ ] **Step 1: Write the failing test**

Append to `internal/store/store_test.go`:

```go
func TestTaskAgileFieldsRoundTrip(t *testing.T) {
	s := tempStore(t)
	task := &model.Task{Title: "pointed", SprintID: "sp1", StoryPoints: 5, BoardOrder: 1.5}
	if err := s.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.SprintID != "sp1" || got.StoryPoints != 5 || got.BoardOrder != 1.5 {
		t.Errorf("got sprint=%q points=%d order=%v", got.SprintID, got.StoryPoints, got.BoardOrder)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestTaskAgileFieldsRoundTrip -v`
Expected: FAIL (fields not persisted / scan mismatch).

- [ ] **Step 3: Write minimal implementation**

In `migrate()`, add to the ALTER-column slice (the first `for _, col := range []struct{...}` loop):

```go
		{"tasks", "sprint_id", "TEXT DEFAULT ''"},
		{"tasks", "story_points", "INTEGER DEFAULT 0"},
		{"tasks", "board_order", "REAL DEFAULT 0"},
```

After the context-manager `CREATE TABLE` block, add:

```go
	s.db.Exec(`CREATE TABLE IF NOT EXISTS sprints (
		id         TEXT PRIMARY KEY,
		project_id TEXT NOT NULL,
		name       TEXT NOT NULL,
		goal       TEXT DEFAULT '',
		state      TEXT DEFAULT 'planned',
		start_date TEXT DEFAULT '',
		end_date   TEXT DEFAULT '',
		created_at TEXT NOT NULL
	)`)
	s.db.Exec("CREATE INDEX IF NOT EXISTS idx_sprints_project ON sprints(project_id)")
	s.db.Exec("CREATE INDEX IF NOT EXISTS idx_sprints_state ON sprints(state)")
	s.db.Exec("CREATE INDEX IF NOT EXISTS idx_tasks_sprint ON tasks(sprint_id)")
```

Extend the task column lists. In `CreateTask`: add `sprint_id, story_points, board_order` to the INSERT column list, add three `?` placeholders, and append `t.SprintID, t.StoryPoints, t.BoardOrder` to the args. In `GetTask` and `ListTasks`: append `, sprint_id, story_points, board_order` to the SELECT list. In `scanTask`: append `&t.SprintID, &t.StoryPoints, &t.BoardOrder` to the `Scan(...)` call (at the end, matching column order).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestTaskAgileFieldsRoundTrip -v`
Then full package to catch scan regressions: `go test ./internal/store/`
Expected: PASS, no regressions.

- [ ] **Step 5: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit --no-gpg-sign -m "feat(store): migrate sprints table and agile task columns"
```

---

### Task 3: Store — Sprint CRUD

**Files:**
- Modify: `internal/store/store.go`
- Test: `internal/store/store_test.go`

**Interfaces:**
- Produces:
  - `CreateSprint(sp *model.Sprint) error` — sets ID (via `id.New()`) and `CreatedAt` if empty; defaults `State` to `SprintPlanned`.
  - `GetSprint(id string) (*model.Sprint, error)` — `ErrNotFound` if missing.
  - `ListSprints(projectID string) ([]*model.Sprint, error)` — all when `projectID == ""`, else filtered; ordered `created_at DESC`.
  - `UpdateSprint(id string, updates map[string]any) (*model.Sprint, error)` — keys: name, goal, state, start_date, end_date.
  - `DeleteSprint(id string) error` — clears `sprint_id` on member tasks, then deletes the sprint.

- [ ] **Step 1: Write the failing test**

Append to `internal/store/store_test.go`:

```go
func TestSprintCRUD(t *testing.T) {
	s := tempStore(t)
	sp := &model.Sprint{ProjectID: "p1", Name: "Sprint 1", Goal: "ship A"}
	if err := s.CreateSprint(sp); err != nil {
		t.Fatal(err)
	}
	if sp.ID == "" {
		t.Fatal("expected ID set")
	}
	if sp.State != model.SprintPlanned {
		t.Errorf("expected default planned, got %q", sp.State)
	}
	got, err := s.GetSprint(sp.ID)
	if err != nil || got.Name != "Sprint 1" {
		t.Fatalf("get failed: %v %+v", err, got)
	}
	if _, err := s.UpdateSprint(sp.ID, map[string]any{"goal": "ship faster"}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetSprint(sp.ID)
	if got.Goal != "ship faster" {
		t.Errorf("update failed: %q", got.Goal)
	}
	list, _ := s.ListSprints("p1")
	if len(list) != 1 {
		t.Errorf("expected 1 sprint, got %d", len(list))
	}
	if err := s.DeleteSprint(sp.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSprint(sp.ID); err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestDeleteSprintClearsTasks(t *testing.T) {
	s := tempStore(t)
	sp := &model.Sprint{ProjectID: "p1", Name: "S"}
	s.CreateSprint(sp)
	task := &model.Task{Title: "t", SprintID: sp.ID}
	s.CreateTask(task)
	if err := s.DeleteSprint(sp.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetTask(task.ID)
	if got.SprintID != "" {
		t.Errorf("expected sprint_id cleared, got %q", got.SprintID)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run 'TestSprintCRUD|TestDeleteSprintClearsTasks' -v`
Expected: FAIL (undefined: CreateSprint).

- [ ] **Step 3: Write minimal implementation**

Add a `// --- Sprints ---` section to `internal/store/store.go`:

```go
func (s *Store) CreateSprint(sp *model.Sprint) error {
	if sp.ID == "" {
		sp.ID = id.New()
	}
	if sp.CreatedAt.IsZero() {
		sp.CreatedAt = time.Now().UTC()
	}
	if sp.State == "" {
		sp.State = model.SprintPlanned
	}
	_, err := s.db.Exec(`INSERT INTO sprints (id, project_id, name, goal, state, start_date, end_date, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		sp.ID, sp.ProjectID, sp.Name, sp.Goal, string(sp.State), sp.StartDate, sp.EndDate,
		sp.CreatedAt.Format(time.RFC3339))
	return err
}

func scanSprint(row scanner) (*model.Sprint, error) {
	var sp model.Sprint
	var state, createdStr string
	err := row.Scan(&sp.ID, &sp.ProjectID, &sp.Name, &sp.Goal, &state, &sp.StartDate, &sp.EndDate, &createdStr)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	sp.State = model.SprintState(state)
	sp.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
	return &sp, nil
}

func (s *Store) GetSprint(sprintID string) (*model.Sprint, error) {
	row := s.db.QueryRow(`SELECT id, project_id, name, goal, state, start_date, end_date, created_at FROM sprints WHERE id = ?`, sprintID)
	return scanSprint(row)
}

func (s *Store) ListSprints(projectID string) ([]*model.Sprint, error) {
	query := `SELECT id, project_id, name, goal, state, start_date, end_date, created_at FROM sprints`
	var args []any
	if projectID != "" {
		query += " WHERE project_id = ?"
		args = append(args, projectID)
	}
	query += " ORDER BY created_at DESC"
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sprints []*model.Sprint
	for rows.Next() {
		sp, err := scanSprint(rows)
		if err != nil {
			return nil, err
		}
		sprints = append(sprints, sp)
	}
	return sprints, rows.Err()
}

func (s *Store) UpdateSprint(sprintID string, updates map[string]any) (*model.Sprint, error) {
	if _, err := s.GetSprint(sprintID); err != nil {
		return nil, err
	}
	var sets []string
	var args []any
	for k, v := range updates {
		switch k {
		case "name", "goal", "state", "start_date", "end_date":
			sets = append(sets, k+" = ?")
			args = append(args, v)
		}
	}
	if len(sets) == 0 {
		return s.GetSprint(sprintID)
	}
	args = append(args, sprintID)
	_, err := s.db.Exec("UPDATE sprints SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...)
	if err != nil {
		return nil, err
	}
	return s.GetSprint(sprintID)
}

func (s *Store) DeleteSprint(sprintID string) error {
	if _, err := s.GetSprint(sprintID); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	s.db.Exec("UPDATE tasks SET sprint_id = '', updated_at = ? WHERE sprint_id = ?", now, sprintID)
	_, err := s.db.Exec("DELETE FROM sprints WHERE id = ?", sprintID)
	return err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run 'TestSprintCRUD|TestDeleteSprintClearsTasks' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit --no-gpg-sign -m "feat(store): sprint CRUD"
```

---

### Task 4: Store — SetSprintState with single-active invariant

**Files:**
- Modify: `internal/store/store.go`
- Test: `internal/store/store_test.go`

**Interfaces:**
- Produces: `SetSprintState(sprintID string, state model.SprintState) error`. Activating a sprint closes any other currently-active sprint in the same project (one active sprint per project), in a transaction. `ErrNotFound` if the sprint is missing.

- [ ] **Step 1: Write the failing test**

```go
func TestSetSprintStateSingleActive(t *testing.T) {
	s := tempStore(t)
	a := &model.Sprint{ProjectID: "p1", Name: "A"}
	b := &model.Sprint{ProjectID: "p1", Name: "B"}
	s.CreateSprint(a)
	s.CreateSprint(b)
	if err := s.SetSprintState(a.ID, model.SprintActive); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSprintState(b.ID, model.SprintActive); err != nil {
		t.Fatal(err)
	}
	ga, _ := s.GetSprint(a.ID)
	gb, _ := s.GetSprint(b.ID)
	if ga.State != model.SprintClosed {
		t.Errorf("expected A closed after B activated, got %q", ga.State)
	}
	if gb.State != model.SprintActive {
		t.Errorf("expected B active, got %q", gb.State)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestSetSprintStateSingleActive -v`
Expected: FAIL (undefined: SetSprintState).

- [ ] **Step 3: Write minimal implementation**

```go
func (s *Store) SetSprintState(sprintID string, state model.SprintState) error {
	sp, err := s.GetSprint(sprintID)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if state == model.SprintActive {
		if _, err := tx.Exec(
			"UPDATE sprints SET state = 'closed' WHERE project_id = ? AND state = 'active' AND id != ?",
			sp.ProjectID, sprintID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec("UPDATE sprints SET state = ? WHERE id = ?", string(state), sprintID); err != nil {
		return err
	}
	return tx.Commit()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestSetSprintStateSingleActive -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit --no-gpg-sign -m "feat(store): single active sprint per project"
```

---

### Task 5: Store — MoveTask, AssignToSprint, and sprint_id filter

**Files:**
- Modify: `internal/store/store.go` (add methods; extend `ListTasks` filters)
- Test: `internal/store/store_test.go`

**Interfaces:**
- Produces:
  - `MoveTask(taskID, status string, boardOrder float64) (*model.Task, error)` — validates status via `model.TaskStatus(status).Valid()`, returns error `ErrInvalidStatus` if bad; sets status + board_order + updated_at.
  - `AssignToSprint(taskID, sprintID string) error` — sets `sprint_id` ("" = backlog) + updated_at; `ErrNotFound` if task missing.
  - `ListTasks` supports filter key `sprint_id`; the sentinel value `"__backlog__"` matches tasks with empty `sprint_id`.
- New sentinel error `ErrInvalidStatus = errors.New("invalid status")`.

- [ ] **Step 1: Write the failing test**

```go
func TestMoveTask(t *testing.T) {
	s := tempStore(t)
	task := &model.Task{Title: "move me"}
	s.CreateTask(task)
	got, err := s.MoveTask(task.ID, "in_progress", 2.5)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.TaskInProgress || got.BoardOrder != 2.5 {
		t.Errorf("got status=%q order=%v", got.Status, got.BoardOrder)
	}
	if _, err := s.MoveTask(task.ID, "bogus", 1); err != ErrInvalidStatus {
		t.Errorf("expected ErrInvalidStatus, got %v", err)
	}
}

func TestAssignToSprintAndBacklogFilter(t *testing.T) {
	s := tempStore(t)
	sp := &model.Sprint{ProjectID: "p1", Name: "S"}
	s.CreateSprint(sp)
	inSprint := &model.Task{Title: "in", ProjectID: "p1"}
	backlog := &model.Task{Title: "back", ProjectID: "p1"}
	s.CreateTask(inSprint)
	s.CreateTask(backlog)
	if err := s.AssignToSprint(inSprint.ID, sp.ID); err != nil {
		t.Fatal(err)
	}
	inList, _ := s.ListTasks(map[string]string{"sprint_id": sp.ID})
	if len(inList) != 1 || inList[0].ID != inSprint.ID {
		t.Errorf("expected only in-sprint task, got %d", len(inList))
	}
	backList, _ := s.ListTasks(map[string]string{"sprint_id": "__backlog__", "project_id": "p1"})
	if len(backList) != 1 || backList[0].ID != backlog.ID {
		t.Errorf("expected only backlog task, got %d", len(backList))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run 'TestMoveTask|TestAssignToSprintAndBacklogFilter' -v`
Expected: FAIL (undefined: MoveTask / ErrInvalidStatus).

- [ ] **Step 3: Write minimal implementation**

Add to the `var (...)` error block near the top of `store.go`:

```go
	ErrInvalidStatus = errors.New("invalid status")
```

Add methods:

```go
func (s *Store) MoveTask(taskID, status string, boardOrder float64) (*model.Task, error) {
	if !model.TaskStatus(status).Valid() {
		return nil, ErrInvalidStatus
	}
	if _, err := s.GetTask(taskID); err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec("UPDATE tasks SET status = ?, board_order = ?, updated_at = ? WHERE id = ?",
		status, boardOrder, now, taskID)
	if err != nil {
		return nil, err
	}
	return s.GetTask(taskID)
}

func (s *Store) AssignToSprint(taskID, sprintID string) error {
	if _, err := s.GetTask(taskID); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec("UPDATE tasks SET sprint_id = ?, updated_at = ? WHERE id = ?", sprintID, now, taskID)
	return err
}
```

In `ListTasks`, add a filter branch (alongside the other `if v, ok := filters[...]` blocks):

```go
	if v, ok := filters["sprint_id"]; ok {
		if v == "__backlog__" {
			conditions = append(conditions, "COALESCE(sprint_id,'') = ''")
		} else {
			conditions = append(conditions, "sprint_id = ?")
			args = append(args, v)
		}
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run 'TestMoveTask|TestAssignToSprintAndBacklogFilter' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit --no-gpg-sign -m "feat(store): move task, assign to sprint, backlog filter"
```

---

### Task 6: Store — SprintBurndown

**Files:**
- Modify: `internal/store/store.go`
- Test: `internal/store/store_test.go`

**Interfaces:**
- Produces:
  - Type `PointDay struct { Date string \`json:"date"\`; Remaining int \`json:"remaining"\` }`.
  - `SprintBurndown(sprintID string) ([]PointDay, error)` — for each calendar day from the sprint `start_date` (or, if empty, the sprint `created_at`) through `min(today, end_date-or-today)`, `Remaining` = sum of `story_points` for the sprint's tasks that were NOT yet done at end of that day. "Done at day D" means status is `done` AND `DATE(updated_at) <= D` (same completion approximation as `Stats().Velocity`). Days are UTC, `YYYY-MM-DD`.

- [ ] **Step 1: Write the failing test**

```go
func TestSprintBurndown(t *testing.T) {
	s := tempStore(t)
	sp := &model.Sprint{ProjectID: "p1", Name: "S", StartDate: time.Now().UTC().Format(time.RFC3339)}
	s.CreateSprint(sp)
	// Two tasks, 3 + 2 points, both in sprint.
	a := &model.Task{Title: "a", StoryPoints: 3, SprintID: sp.ID}
	b := &model.Task{Title: "b", StoryPoints: 2, SprintID: sp.ID}
	s.CreateTask(a)
	s.CreateTask(b)
	// Complete one today.
	s.MoveTask(a.ID, "done", 0)
	days, err := s.SprintBurndown(sp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(days) == 0 {
		t.Fatal("expected at least one day")
	}
	last := days[len(days)-1]
	if last.Remaining != 2 {
		t.Errorf("expected 2 points remaining today, got %d", last.Remaining)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestSprintBurndown -v`
Expected: FAIL (undefined: SprintBurndown).

- [ ] **Step 3: Write minimal implementation**

```go
type PointDay struct {
	Date      string `json:"date"`
	Remaining int    `json:"remaining"`
}

func (s *Store) SprintBurndown(sprintID string) ([]PointDay, error) {
	sp, err := s.GetSprint(sprintID)
	if err != nil {
		return nil, err
	}
	startStr := sp.StartDate
	if startStr == "" {
		startStr = sp.CreatedAt.Format(time.RFC3339)
	}
	start, err := time.Parse(time.RFC3339, startStr)
	if err != nil {
		start = sp.CreatedAt
	}
	start = start.UTC().Truncate(24 * time.Hour)
	end := time.Now().UTC().Truncate(24 * time.Hour)
	if sp.EndDate != "" {
		if e, err := time.Parse(time.RFC3339, sp.EndDate); err == nil {
			ed := e.UTC().Truncate(24 * time.Hour)
			if ed.Before(end) {
				end = ed
			}
		}
	}
	// Load sprint tasks: points + done-date (empty if not done).
	rows, err := s.db.Query(
		`SELECT story_points, status, DATE(updated_at) FROM tasks WHERE sprint_id = ?`, sprintID)
	if err != nil {
		return nil, err
	}
	type tp struct {
		points   int
		doneDate string // "" if not done
	}
	var tasks []tp
	for rows.Next() {
		var pts int
		var status, upd string
		rows.Scan(&pts, &status, &upd)
		d := ""
		if status == string(model.TaskDone) {
			d = upd
		}
		tasks = append(tasks, tp{points: pts, doneDate: d})
	}
	rows.Close()

	var days []PointDay
	for d := start; !d.After(end); d = d.Add(24 * time.Hour) {
		dayStr := d.Format("2006-01-02")
		remaining := 0
		for _, tk := range tasks {
			if tk.doneDate != "" && tk.doneDate <= dayStr {
				continue // completed on or before this day
			}
			remaining += tk.points
		}
		days = append(days, PointDay{Date: dayStr, Remaining: remaining})
	}
	return days, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestSprintBurndown -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit --no-gpg-sign -m "feat(store): sprint burndown"
```

---

### Task 7: Store — WIP limit settings wrappers

**Files:**
- Modify: `internal/store/store.go`
- Test: `internal/store/store_test.go`

**Interfaces:**
- Produces:
  - `SetWIP(projectID, status string, limit int) error` — stores under settings key `wip:<projectID>:<status>`. `limit <= 0` deletes the key (no limit).
  - `GetWIPLimits(projectID string) (map[string]int, error)` — returns `{status: limit}` for the project.

- [ ] **Step 1: Write the failing test**

```go
func TestWIPLimits(t *testing.T) {
	s := tempStore(t)
	if err := s.SetWIP("p1", "in_progress", 3); err != nil {
		t.Fatal(err)
	}
	limits, _ := s.GetWIPLimits("p1")
	if limits["in_progress"] != 3 {
		t.Errorf("expected 3, got %d", limits["in_progress"])
	}
	// 0 clears.
	s.SetWIP("p1", "in_progress", 0)
	limits, _ = s.GetWIPLimits("p1")
	if _, ok := limits["in_progress"]; ok {
		t.Error("expected limit cleared")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestWIPLimits -v`
Expected: FAIL (undefined: SetWIP).

- [ ] **Step 3: Write minimal implementation**

```go
func wipKey(projectID, status string) string {
	return "wip:" + projectID + ":" + status
}

func (s *Store) SetWIP(projectID, status string, limit int) error {
	if limit <= 0 {
		_, err := s.db.Exec("DELETE FROM settings WHERE key = ?", wipKey(projectID, status))
		return err
	}
	return s.SetSetting(wipKey(projectID, status), fmt.Sprintf("%d", limit))
}

func (s *Store) GetWIPLimits(projectID string) (map[string]int, error) {
	prefix := "wip:" + projectID + ":"
	rows, err := s.db.Query("SELECT key, value FROM settings WHERE key LIKE ?", prefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	limits := map[string]int{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		var n int
		fmt.Sscanf(v, "%d", &n)
		limits[strings.TrimPrefix(k, prefix)] = n
	}
	return limits, rows.Err()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestWIPLimits -v` then `go test ./internal/store/`
Expected: PASS, no regressions.

- [ ] **Step 5: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit --no-gpg-sign -m "feat(store): advisory WIP limit settings"
```

---

### Task 8: API — sprint endpoints

**Files:**
- Modify: `internal/api/api.go` (register routes in `Handler()`; add handlers)
- Test: `internal/api/api_test.go`

**Interfaces:**
- Consumes: store `CreateSprint`, `ListSprints`, `GetSprint`, `UpdateSprint`, `SetSprintState`, `DeleteSprint`, `SprintBurndown`.
- Produces routes:
  - `GET /api/sprints?project_id=` → list; `POST /api/sprints` (body `model.Sprint`, requires `name`) → 201 created.
  - `GET /api/sprints/{id}` → sprint; `PATCH /api/sprints/{id}` (updates map; `state` routes through `SetSprintState`) → 200; `DELETE /api/sprints/{id}` → 204.
  - `GET /api/sprints/{id}/burndown` → `[]PointDay`.

- [ ] **Step 1: Write the failing test**

Append to `internal/api/api_test.go`:

```go
func TestSprintEndpoints(t *testing.T) {
	_, ts := setup(t)
	// Create.
	body := `{"project_id":"p1","name":"Sprint 1","goal":"ship"}`
	resp, err := http.Post(ts.URL+"/api/sprints", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var created model.Sprint
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	if created.ID == "" {
		t.Fatal("expected sprint id")
	}
	// List.
	lr := mustGet(t, ts.URL+"/api/sprints?project_id=p1")
	var list []model.Sprint
	json.NewDecoder(lr.Body).Decode(&list)
	lr.Body.Close()
	if len(list) != 1 {
		t.Errorf("expected 1 sprint, got %d", len(list))
	}
	// Burndown.
	br := mustGet(t, ts.URL+"/api/sprints/"+created.ID+"/burndown")
	if br.StatusCode != http.StatusOK {
		t.Errorf("burndown expected 200, got %d", br.StatusCode)
	}
	br.Body.Close()
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/ -run TestSprintEndpoints -v`
Expected: FAIL (404s — routes not registered).

- [ ] **Step 3: Write minimal implementation**

In `Handler()`, register (near the other `mux.HandleFunc` lines):

```go
	mux.HandleFunc("/api/sprints", a.handleSprints)
	mux.HandleFunc("/api/sprints/", a.handleSprint)
```

Add handlers:

```go
func (a *API) handleSprints(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		sprints, err := a.store.ListSprints(r.URL.Query().Get("project_id"))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list_failed", err.Error())
			return
		}
		if sprints == nil {
			sprints = []*model.Sprint{}
		}
		writeJSON(w, http.StatusOK, sprints)
	case http.MethodPost:
		var sp model.Sprint
		if err := json.NewDecoder(r.Body).Decode(&sp); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		if sp.Name == "" {
			writeError(w, http.StatusBadRequest, "missing_name", "name is required")
			return
		}
		if sp.State != "" && !sp.State.Valid() {
			writeError(w, http.StatusBadRequest, "invalid_state", "invalid state: "+string(sp.State))
			return
		}
		if err := a.store.CreateSprint(&sp); err != nil {
			writeError(w, http.StatusInternalServerError, "create_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, sp)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *API) handleSprint(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/sprints/")
	parts := strings.SplitN(id, "/", 2)
	id = parts[0]
	subAction := ""
	if len(parts) > 1 {
		subAction = parts[1]
	}
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing_id", "sprint ID required")
		return
	}
	if subAction == "burndown" {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		days, err := a.store.SprintBurndown(id)
		if err != nil {
			if err == store.ErrNotFound {
				writeError(w, http.StatusNotFound, "sprint_not_found", "Sprint "+id+" not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "burndown_failed", err.Error())
			return
		}
		if days == nil {
			days = []store.PointDay{}
		}
		writeJSON(w, http.StatusOK, days)
		return
	}
	switch r.Method {
	case http.MethodGet:
		sp, err := a.store.GetSprint(id)
		if err != nil {
			if err == store.ErrNotFound {
				writeError(w, http.StatusNotFound, "sprint_not_found", "Sprint "+id+" not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "get_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, sp)
	case http.MethodPatch:
		var updates map[string]any
		if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		if st, ok := updates["state"].(string); ok {
			if !model.SprintState(st).Valid() {
				writeError(w, http.StatusBadRequest, "invalid_state", "invalid state: "+st)
				return
			}
			if err := a.store.SetSprintState(id, model.SprintState(st)); err != nil {
				if err == store.ErrNotFound {
					writeError(w, http.StatusNotFound, "sprint_not_found", "Sprint "+id+" not found")
					return
				}
				writeError(w, http.StatusInternalServerError, "update_failed", err.Error())
				return
			}
			delete(updates, "state")
		}
		sp, err := a.store.UpdateSprint(id, updates)
		if err != nil {
			if err == store.ErrNotFound {
				writeError(w, http.StatusNotFound, "sprint_not_found", "Sprint "+id+" not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "update_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, sp)
	case http.MethodDelete:
		if err := a.store.DeleteSprint(id); err != nil {
			if err == store.ErrNotFound {
				writeError(w, http.StatusNotFound, "sprint_not_found", "Sprint "+id+" not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "delete_failed", err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/api/ -run TestSprintEndpoints -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/api.go internal/api/api_test.go
git commit --no-gpg-sign -m "feat(api): sprint endpoints + burndown"
```

---

### Task 9: API — task move and sprint-assignment sub-actions

**Files:**
- Modify: `internal/api/api.go` (`handleTask` subAction switch)
- Test: `internal/api/api_test.go`

**Interfaces:**
- Consumes: store `MoveTask`, `AssignToSprint`.
- Produces:
  - `POST /api/tasks/{id}/move` body `{"status":"in_progress","board_order":1.5}` → 200 updated task, emits `EventTaskUpdated`. Bad status → 400.
  - `POST /api/tasks/{id}/sprint` body `{"sprint_id":"..."}` ("" = backlog) → 200 updated task, emits `EventTaskUpdated`.

- [ ] **Step 1: Write the failing test**

```go
func TestTaskMoveAndSprintAssign(t *testing.T) {
	a, ts := setup(t)
	task := &model.Task{Title: "t"}
	a.store.CreateTask(task)
	// Move.
	mr, err := http.Post(ts.URL+"/api/tasks/"+task.ID+"/move", "application/json",
		strings.NewReader(`{"status":"in_progress","board_order":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if mr.StatusCode != http.StatusOK {
		t.Fatalf("move expected 200, got %d", mr.StatusCode)
	}
	var moved model.Task
	json.NewDecoder(mr.Body).Decode(&moved)
	mr.Body.Close()
	if moved.Status != model.TaskInProgress || moved.BoardOrder != 2 {
		t.Errorf("move failed: %q %v", moved.Status, moved.BoardOrder)
	}
	// Bad status.
	br, _ := http.Post(ts.URL+"/api/tasks/"+task.ID+"/move", "application/json",
		strings.NewReader(`{"status":"nope","board_order":1}`))
	if br.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for bad status, got %d", br.StatusCode)
	}
	br.Body.Close()
	// Assign to sprint.
	sp := &model.Sprint{ProjectID: "p1", Name: "S"}
	a.store.CreateSprint(sp)
	sr, _ := http.Post(ts.URL+"/api/tasks/"+task.ID+"/sprint", "application/json",
		strings.NewReader(`{"sprint_id":"`+sp.ID+`"}`))
	if sr.StatusCode != http.StatusOK {
		t.Errorf("sprint assign expected 200, got %d", sr.StatusCode)
	}
	sr.Body.Close()
	got, _ := a.store.GetTask(task.ID)
	if got.SprintID != sp.ID {
		t.Errorf("expected sprint assigned, got %q", got.SprintID)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/ -run TestTaskMoveAndSprintAssign -v`
Expected: FAIL (sub-actions fall through to GET/404).

- [ ] **Step 3: Write minimal implementation**

In `handleTask`, add two cases to the `switch subAction` block (alongside `claim`, `complete`, etc.):

```go
	case "move":
		a.handleTaskMove(w, r, id)
		return
	case "sprint":
		a.handleTaskSprint(w, r, id)
		return
```

Add the handlers:

```go
func (a *API) handleTaskMove(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Status     string  `json:"status"`
		BoardOrder float64 `json:"board_order"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	task, err := a.store.MoveTask(taskID, req.Status, req.BoardOrder)
	if err != nil {
		if err == store.ErrNotFound {
			writeError(w, http.StatusNotFound, "task_not_found", "Task "+taskID+" not found")
			return
		}
		if err == store.ErrInvalidStatus {
			writeError(w, http.StatusBadRequest, "invalid_status", "invalid status: "+req.Status)
			return
		}
		writeError(w, http.StatusInternalServerError, "move_failed", err.Error())
		return
	}
	a.emit(&model.Event{Type: model.EventTaskUpdated, TaskID: taskID, Payload: map[string]any{
		"status": req.Status, "board_order": req.BoardOrder,
	}})
	writeJSON(w, http.StatusOK, task)
}

func (a *API) handleTaskSprint(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		SprintID string `json:"sprint_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := a.store.AssignToSprint(taskID, req.SprintID); err != nil {
		if err == store.ErrNotFound {
			writeError(w, http.StatusNotFound, "task_not_found", "Task "+taskID+" not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "assign_failed", err.Error())
		return
	}
	a.emit(&model.Event{Type: model.EventTaskUpdated, TaskID: taskID, Payload: map[string]any{
		"sprint_id": req.SprintID,
	}})
	task, _ := a.store.GetTask(taskID)
	writeJSON(w, http.StatusOK, task)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/api/ -run TestTaskMoveAndSprintAssign -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/api.go internal/api/api_test.go
git commit --no-gpg-sign -m "feat(api): task move and sprint assignment"
```

---

### Task 10: API — WIP limits endpoint

**Files:**
- Modify: `internal/api/api.go` (route + handler)
- Test: `internal/api/api_test.go`

**Interfaces:**
- Consumes: store `GetWIPLimits`, `SetWIP`.
- Produces:
  - `GET /api/wip?project_id=` → `{status: limit}` JSON map.
  - `PUT /api/wip?project_id=` body `{status: limit}` → sets each; 0 clears; returns the resulting map.

- [ ] **Step 1: Write the failing test**

```go
func TestWIPEndpoint(t *testing.T) {
	_, ts := setup(t)
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/wip?project_id=p1",
		strings.NewReader(`{"in_progress":3}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	gr := mustGet(t, ts.URL+"/api/wip?project_id=p1")
	var limits map[string]int
	json.NewDecoder(gr.Body).Decode(&limits)
	gr.Body.Close()
	if limits["in_progress"] != 3 {
		t.Errorf("expected 3, got %d", limits["in_progress"])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/ -run TestWIPEndpoint -v`
Expected: FAIL (404).

- [ ] **Step 3: Write minimal implementation**

Register in `Handler()`:

```go
	mux.HandleFunc("/api/wip", a.handleWIP)
```

Add handler:

```go
func (a *API) handleWIP(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project_id")
	switch r.Method {
	case http.MethodGet:
		limits, err := a.store.GetWIPLimits(projectID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "get_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, limits)
	case http.MethodPut:
		var req map[string]int
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		for status, limit := range req {
			if !model.TaskStatus(status).Valid() {
				writeError(w, http.StatusBadRequest, "invalid_status", "invalid status: "+status)
				return
			}
			if err := a.store.SetWIP(projectID, status, limit); err != nil {
				writeError(w, http.StatusInternalServerError, "set_failed", err.Error())
				return
			}
		}
		limits, _ := a.store.GetWIPLimits(projectID)
		writeJSON(w, http.StatusOK, limits)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/api/ -run TestWIPEndpoint -v` then `go test ./internal/api/`
Expected: PASS, no regressions.

- [ ] **Step 5: Commit**

```bash
git add internal/api/api.go internal/api/api_test.go
git commit --no-gpg-sign -m "feat(api): advisory WIP limits endpoint"
```

---

### Task 11: MCP — board and sprint tools

**Files:**
- Modify: `internal/mcp/mcp.go` (`handleToolsList` tool defs; `executeTool` switch)
- Test: `internal/mcp/mcp_test.go`

**Interfaces:**
- Consumes: API routes from Tasks 8–9.
- Produces four MCP tools:
  - `waggle_move_task` — args `{task_id, status, board_order?}` → `POST /api/tasks/{id}/move`.
  - `waggle_create_sprint` — args `{project_id, name, goal?, start_date?, end_date?}` → `POST /api/sprints`.
  - `waggle_assign_sprint` — args `{task_id, sprint_id}` → `POST /api/tasks/{id}/sprint`.
  - `waggle_sprint_status` — args `{project_id}` → lists sprints, finds active one, fetches its burndown; returns `{active_sprint, burndown}` (or `{active_sprint:null}` if none).

- [ ] **Step 1: Write the failing test**

Append to `internal/mcp/mcp_test.go` (uses `callMCP`; inspects the returned tool result JSON):

```go
func TestMCPAgileTools(t *testing.T) {
	adapter, _ := setupMCP(t)
	// tools/list includes the new tools.
	list := callMCP(t, adapter, "tools/list", 1, nil)
	result, _ := list["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	names := map[string]bool{}
	for _, tv := range tools {
		if m, ok := tv.(map[string]any); ok {
			names[m["name"].(string)] = true
		}
	}
	for _, want := range []string{"waggle_move_task", "waggle_create_sprint", "waggle_assign_sprint", "waggle_sprint_status"} {
		if !names[want] {
			t.Errorf("missing tool %q", want)
		}
	}
	// create_sprint returns a sprint with an id.
	call := callMCP(t, adapter, "tools/call", 2, map[string]any{
		"name":      "waggle_create_sprint",
		"arguments": map[string]any{"project_id": "p1", "name": "S1"},
	})
	cr, _ := call["result"].(map[string]any)
	content, _ := cr["content"].([]any)
	if len(content) == 0 {
		t.Fatal("expected content")
	}
	text := content[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "\"id\"") {
		t.Errorf("expected sprint id in result, got %s", text)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcp/ -run TestMCPAgileTools -v`
Expected: FAIL (tools missing).

- [ ] **Step 3: Write minimal implementation**

In `handleToolsList`, add to the `tools` slice (after the task tools):

```go
		toolDef("waggle_move_task", "Move a task to a board column (status) and optional position. Use to drag a card across the kanban board.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id":     prop("string", "Task ID to move"),
				"status":      propEnum("string", []string{"backlog", "ready", "in_progress", "review", "done", "blocked"}, "Target column"),
				"board_order": map[string]any{"type": "number", "description": "Position within the column (optional; appends if omitted)"},
			},
			"required": []string{"task_id", "status"},
		}),
		toolDef("waggle_create_sprint", "Create a sprint for a project.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project_id": prop("string", "Project ID"),
				"name":       prop("string", "Sprint name"),
				"goal":       prop("string", "Sprint goal (optional)"),
				"start_date": prop("string", "RFC3339 start date (optional)"),
				"end_date":   prop("string", "RFC3339 end date (optional)"),
			},
			"required": []string{"project_id", "name"},
		}),
		toolDef("waggle_assign_sprint", "Assign a task to a sprint, or send it to the backlog with an empty sprint_id.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id":   prop("string", "Task ID"),
				"sprint_id": prop("string", "Sprint ID ('' = backlog)"),
			},
			"required": []string{"task_id"},
		}),
		toolDef("waggle_sprint_status", "Get the active sprint and its burndown for a project.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project_id": prop("string", "Project ID"),
			},
			"required": []string{"project_id"},
		}),
```

In `executeTool`, add cases (after the task tool cases):

```go
	case "waggle_move_task":
		id, _ := args["task_id"].(string)
		status, _ := args["status"].(string)
		if id == "" || status == "" {
			return nil, fmt.Errorf("task_id and status are required")
		}
		body := map[string]any{"status": status}
		if bo, ok := args["board_order"].(float64); ok {
			body["board_order"] = bo
		}
		return a.postJSON("/api/tasks/"+id+"/move", body)

	case "waggle_create_sprint":
		projectID, _ := args["project_id"].(string)
		name, _ := args["name"].(string)
		if projectID == "" || name == "" {
			return nil, fmt.Errorf("project_id and name are required")
		}
		return a.postJSON("/api/sprints", args)

	case "waggle_assign_sprint":
		id, _ := args["task_id"].(string)
		if id == "" {
			return nil, fmt.Errorf("task_id is required")
		}
		sprintID, _ := args["sprint_id"].(string)
		return a.postJSON("/api/tasks/"+id+"/sprint", map[string]string{"sprint_id": sprintID})

	case "waggle_sprint_status":
		projectID, _ := args["project_id"].(string)
		if projectID == "" {
			return nil, fmt.Errorf("project_id is required")
		}
		listed, err := a.get("/api/sprints?project_id=" + url.QueryEscape(projectID))
		if err != nil {
			return nil, err
		}
		sprints, _ := listed.([]any)
		var active map[string]any
		for _, sv := range sprints {
			if m, ok := sv.(map[string]any); ok && m["state"] == "active" {
				active = m
				break
			}
		}
		if active == nil {
			return map[string]any{"active_sprint": nil}, nil
		}
		burndown, _ := a.get("/api/sprints/" + active["id"].(string) + "/burndown")
		return map[string]any{"active_sprint": active, "burndown": burndown}, nil
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mcp/ -run TestMCPAgileTools -v` then `go test ./internal/mcp/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/mcp.go internal/mcp/mcp_test.go
git commit --no-gpg-sign -m "feat(mcp): board and sprint tools"
```

---

### Task 12: Dashboard — kanban board view

**Files:**
- Modify: `internal/dashboard/static/mission-control.html`
- Verify: manual, via the run skill (no Go test; single-file inline JS/CSS as the file already does).

**Interfaces:**
- Consumes: `GET /api/tasks?sprint_id=&project_id=`, `POST /api/tasks/{id}/move`, `GET /api/sprints?project_id=`, `PATCH /api/sprints/{id}`, `GET /api/sprints/{id}/burndown`, `GET/PUT /api/wip?project_id=`, and the existing SSE `/api/events` stream.

This task is one deliverable (a working board view) rather than a TDD cycle. Build it incrementally and verify in the browser.

- [ ] **Step 1: Add a "Board" nav entry / view container**

Follow the file's existing view-switching pattern (inspect how current views are toggled — look for the existing nav/tab markup and the show/hide function). Add a `Board` entry and an empty `<div id="board-view">` container styled to match existing views.

- [ ] **Step 2: Render columns from the status enum**

Render six columns in this fixed order: `backlog, ready, in_progress, review, done, blocked`. Fetch tasks (respecting the current sprint selector, see Step 4), bucket by `status`, sort each bucket ascending by `board_order` then `created_at`. Each card shows title, priority, assignee, and story points (if `story_points > 0`). Each column header shows the card count and the sum of story points.

- [ ] **Step 3: Drag-and-drop between columns**

Use HTML5 drag-and-drop (`draggable="true"`, `dragstart`/`dragover`/`drop`). On drop, compute the new `board_order` as the midpoint of the neighbors at the drop position (top of column: `firstOrder - 1`; bottom: `lastOrder + 1`; between A and B: `(A + B) / 2`), then:

```js
await fetch(`/api/tasks/${taskId}/move`, {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ status: targetStatus, board_order: newOrder }),
});
```

Re-render the affected columns from the response.

- [ ] **Step 4: Sprint selector**

Add a selector with options **Active sprint**, **Backlog**, **All**, plus one entry per sprint from `GET /api/sprints?project_id=`. Map the choice to the tasks query: Active sprint → resolve the sprint whose `state == "active"` and filter `sprint_id=<id>`; Backlog → `sprint_id=__backlog__`; All → no `sprint_id` filter. Include `project_id` when a project is selected. Add a control to set the active sprint via `PATCH /api/sprints/{id}` with `{"state":"active"}`.

- [ ] **Step 5: WIP limit badges**

Fetch `GET /api/wip?project_id=`. For each column with a limit, show `count / limit` in the header. When `count > limit`, add a red class to the header (define a `.wip-over` style, magenta/red per the flags convention). Provide a small input to set a column's limit via `PUT /api/wip?project_id=` with `{status: limit}`.

- [ ] **Step 6: Burndown sparkline**

When a sprint is selected (Active or a specific sprint), fetch `GET /api/sprints/{id}/burndown` and draw a small inline SVG/canvas sparkline of `remaining` over `date`. Keep it minimal; label first/last values.

- [ ] **Step 7: Live updates via existing SSE**

Hook into the page's existing SSE `/api/events` handler. On `task_updated`, `task_created`, `task_completed`, or `task_deleted` events, if the Board view is active, refresh the affected column(s) (simplest correct approach: re-fetch the current task query and re-render). Reuse the existing EventSource, do not open a second one.

- [ ] **Step 8: Manual verification**

Run the app (use the `run` skill or `go run ./cmd/...` per repo convention) and confirm in the browser:
- Board renders six columns; cards appear under the right status.
- Dragging a card to another column persists (reload keeps it there) and updates counts/points.
- Sprint selector switches between Active / Backlog / All.
- Setting a WIP limit below a column's count turns the header red.
- Burndown sparkline renders for the active sprint.
- Moving a card in one browser tab updates a second tab (SSE).

- [ ] **Step 9: Commit**

```bash
git add internal/dashboard/static/mission-control.html
git commit --no-gpg-sign -m "feat(dashboard): kanban board view with sprints, WIP, burndown"
```

---

## Final verification

- [ ] **Run the full suite:** `go test ./...` — all green.
- [ ] **Build:** `go build ./...` — clean.
- [ ] **Manual smoke:** create a sprint, assign a task, move it across the board, activate the sprint, view burndown — all via both the dashboard and the MCP tools.
- [ ] **Review before merge:** dispatch parallel review agents (backend for store/api/mcp, frontend for the dashboard) per repo standards; fix critical/high findings before merging.

## Self-review notes (author)

- Spec coverage: sprints (T3–4, T8), backlog (T5 filter, T12 selector), story points (T1–2 field, T12 display, T6 burndown), kanban board reusing status enum (T12), fractional board_order drag (T2 field, T5 MoveTask, T9 API, T12 DnD), advisory WIP (T7, T10, T12), burndown (T6, T8, T12), MCP tools (T11). All spec sections mapped.
- Types consistent across tasks: `MoveTask(taskID, status string, boardOrder float64)`, `AssignToSprint(taskID, sprintID string)`, `SprintBurndown → []PointDay`, `GetWIPLimits/SetWIP`, sentinel `ErrInvalidStatus`, backlog sentinel `"__backlog__"` — same names used by API (T8–10) and MCP (T11).
- Out of scope (per spec): configurable columns, pages/docs, generic EAV DB, hard WIP enforcement, burndown snapshot table.
