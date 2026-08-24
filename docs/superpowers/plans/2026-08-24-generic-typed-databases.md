# Generic Typed Databases (C2) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add user-defined typed databases (Notion-style) to Waggle: define databases, typed columns, rows, and table/kanban/calendar views, usable over REST, MCP, and the dashboard.

**Architecture:** New `udb` domain in the existing store using JSON-per-row storage (one SQLite row per record, cell values in a `data_json` blob keyed by column id). Layered exactly like the existing task/page features: store -> REST API -> MCP tools -> single-file dashboard. Views filter/sort/group client-side over fetched rows.

**Tech Stack:** Go, modernc.org/sqlite, standard `net/http`, existing `pkg/id`, embedded single-file HTML/JS dashboard.

## Global Constraints

- Go single-binary + embedded SQLite only; no new external dependencies.
- IDs via `id.New()` (prefix `wg-`, 15 chars). Timestamps stored as `time.RFC3339` UTC strings, dates as `YYYY-MM-DD`.
- Migrations are additive and idempotent (`CREATE TABLE IF NOT EXISTS`), added inside `internal/store/store.go`'s `migrate()`.
- Store returns typed sentinel errors; the API maps them to HTTP codes (`ErrNotFound`->404, validation errors->400, else 500).
- Column type set is exactly: `text`, `number`, `date`, `select`, `checkbox`.
- View kind set is exactly: `table`, `kanban`, `calendar`.
- All user-supplied strings rendered in the dashboard MUST be escaped with the existing `esc()` before insertion (XSS lesson from pages/C1).
- Dashboard SSE refresh MUST NOT clobber an open editor (dirty-guard lesson from pages).
- Follow existing code conventions: `writeError`/`writeJSON`, method guards, `a.emit(...)`, `scanner` interface, `const xCols` column lists.

## File Structure

- `internal/model/model.go` (modify): add `Database`, `Column`, `SelectOption`, `Row`, `View`, `ViewConfig`, `ViewFilter`, `ViewSort` types; column-type + view-kind consts; `udb` event consts.
- `internal/store/udb.go` (create): all `udb` store methods + validation + sentinel errors. (Kept in its own file so `store.go` does not grow further; `migrate()` in `store.go` gains the table DDL.)
- `internal/store/store.go` (modify): add `udb` table DDL to `migrate()`.
- `internal/store/udb_test.go` (create): store-layer tests.
- `internal/api/udb.go` (create): all `udb` HTTP handlers.
- `internal/api/api.go` (modify): register `udb` routes in `Handler()`.
- `internal/api/udb_test.go` (create): API-layer tests.
- `internal/mcp/mcp.go` (modify): 7 `udb` tool defs + call handlers.
- `internal/mcp/mcp_test.go` (modify): MCP tool tests.
- `internal/dashboard/static/mission-control.html` (modify): Databases view.

---

### Task 1: Model types, consts, events

**Files:**
- Modify: `internal/model/model.go`
- Test: none (pure type declarations; exercised by later tasks)

**Interfaces:**
- Produces: the types and consts every later task consumes.

- [ ] **Step 1: Add types and consts** to `internal/model/model.go` (near the `Page` type and the `Event*` const block):

```go
// --- User-defined databases (udb) ---

const (
	ColumnTypeText     = "text"
	ColumnTypeNumber   = "number"
	ColumnTypeDate     = "date"
	ColumnTypeSelect   = "select"
	ColumnTypeCheckbox = "checkbox"

	ViewKindTable    = "table"
	ViewKindKanban   = "kanban"
	ViewKindCalendar = "calendar"
)

type Database struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id,omitempty"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type SelectOption struct {
	Value string `json:"value"`
	Color string `json:"color,omitempty"`
}

type Column struct {
	ID        string         `json:"id"`
	DBID      string         `json:"db_id"`
	Name      string         `json:"name"`
	Type      string         `json:"type"`
	Position  int            `json:"position"`
	Options   []SelectOption `json:"options,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

type Row struct {
	ID        string         `json:"id"`
	DBID      string         `json:"db_id"`
	Data      map[string]any `json:"data"`
	Position  int            `json:"position"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type ViewFilter struct {
	Col   string `json:"col"`
	Op    string `json:"op"`
	Value any    `json:"value"`
}

type ViewSort struct {
	Col string `json:"col"`
	Dir string `json:"dir"`
}

type ViewConfig struct {
	Filters   []ViewFilter `json:"filters,omitempty"`
	Sort      []ViewSort   `json:"sort,omitempty"`
	GroupBy   string       `json:"group_by,omitempty"`
	DateField string       `json:"date_field,omitempty"`
}

type View struct {
	ID       string     `json:"id"`
	DBID     string     `json:"db_id"`
	Name     string     `json:"name"`
	Kind     string     `json:"kind"`
	Config   ViewConfig `json:"config"`
	Position int        `json:"position"`
}
```

- [ ] **Step 2: Add event consts** to the `Event*` const block:

```go
	EventDatabaseCreated EventType = "database_created"
	EventDatabaseUpdated EventType = "database_updated"
	EventDatabaseDeleted EventType = "database_deleted"
	EventDatabaseChanged EventType = "database_changed" // columns/rows/views mutated; Payload {db_id}
```

- [ ] **Step 3: Build**

Run: `go build ./...`
Expected: compiles.

- [ ] **Step 4: Commit**

```bash
git add internal/model/model.go
git commit --no-gpg-sign -m "feat(model): udb types, column/view consts, events"
```

---

### Task 2: Store -- database table + Database CRUD

**Files:**
- Modify: `internal/store/store.go` (add DDL to `migrate()`)
- Create: `internal/store/udb.go`
- Test: `internal/store/udb_test.go`

**Interfaces:**
- Consumes: `model.Database`, `id.New()`, `ErrNotFound`, `scanner`.
- Produces: `CreateDatabase(*model.Database) error`, `GetDatabase(id string) (*model.Database, error)`, `ListDatabases(projectID string) ([]*model.Database, error)`, `RenameDatabase(id, name string) (*model.Database, error)`, `DeleteDatabase(id string) error`. Sentinel errors `ErrUnknownColumn`, `ErrInvalidCell`, `ErrInvalidColumnType`, `ErrInvalidView`, `ErrInvalidViewConfig` (declared now, used in Tasks 3-5).

- [ ] **Step 1: Add table DDL** inside `migrate()` in `internal/store/store.go`, after the pages block:

```go
	s.db.Exec(`CREATE TABLE IF NOT EXISTS udb (
		id         TEXT PRIMARY KEY,
		project_id TEXT DEFAULT '',
		name       TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`)
	s.db.Exec("CREATE INDEX IF NOT EXISTS idx_udb_project ON udb(project_id)")
	s.db.Exec(`CREATE TABLE IF NOT EXISTS udb_column (
		id           TEXT PRIMARY KEY,
		db_id        TEXT NOT NULL,
		name         TEXT NOT NULL,
		type         TEXT NOT NULL,
		position     INTEGER NOT NULL DEFAULT 0,
		options_json TEXT DEFAULT '',
		created_at   TEXT NOT NULL
	)`)
	s.db.Exec("CREATE INDEX IF NOT EXISTS idx_udb_column_db ON udb_column(db_id)")
	s.db.Exec(`CREATE TABLE IF NOT EXISTS udb_row (
		id         TEXT PRIMARY KEY,
		db_id      TEXT NOT NULL,
		data_json  TEXT NOT NULL DEFAULT '{}',
		position   INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`)
	s.db.Exec("CREATE INDEX IF NOT EXISTS idx_udb_row_db ON udb_row(db_id)")
	s.db.Exec(`CREATE TABLE IF NOT EXISTS udb_view (
		id          TEXT PRIMARY KEY,
		db_id       TEXT NOT NULL,
		name        TEXT NOT NULL,
		kind        TEXT NOT NULL,
		config_json TEXT NOT NULL DEFAULT '{}',
		position    INTEGER NOT NULL DEFAULT 0
	)`)
	s.db.Exec("CREATE INDEX IF NOT EXISTS idx_udb_view_db ON udb_view(db_id)")
```

- [ ] **Step 2: Write the failing test** `internal/store/udb_test.go`:

```go
package store

import "testing"

func TestDatabaseCRUD(t *testing.T) {
	s := newTestStore(t)
	db := &model.Database{ProjectID: "p1", Name: "Contacts"}
	if err := s.CreateDatabase(db); err != nil {
		t.Fatalf("create: %v", err)
	}
	if db.ID == "" {
		t.Fatal("expected generated id")
	}
	got, err := s.GetDatabase(db.ID)
	if err != nil || got.Name != "Contacts" {
		t.Fatalf("get: %v name=%q", err, got.Name)
	}
	list, err := s.ListDatabases("p1")
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v len=%d", err, len(list))
	}
	if _, err := s.RenameDatabase(db.ID, "People"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	got, _ = s.GetDatabase(db.ID)
	if got.Name != "People" {
		t.Fatalf("rename not applied: %q", got.Name)
	}
	if err := s.DeleteDatabase(db.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetDatabase(db.ID); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
```

> Check the existing `internal/store/store_test.go` for the real test-store constructor name. If it differs from `newTestStore(t)`, use the actual one and add `import "github.com/maniginam/waggle/internal/model"`.

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestDatabaseCRUD`
Expected: FAIL (undefined methods).

- [ ] **Step 4: Implement** in `internal/store/udb.go`:

```go
package store

import (
	"database/sql"
	"errors"
	"time"

	"github.com/maniginam/waggle/internal/model"
	"github.com/maniginam/waggle/pkg/id"
)

var (
	ErrUnknownColumn     = errors.New("unknown column")
	ErrInvalidCell       = errors.New("invalid cell value for column type")
	ErrInvalidColumnType = errors.New("invalid column type")
	ErrInvalidView       = errors.New("invalid view kind")
	ErrInvalidViewConfig = errors.New("invalid view config")
)

const dbCols = "id, project_id, name, created_at, updated_at"

func scanDatabase(row scanner) (*model.Database, error) {
	var d model.Database
	var c, u string
	err := row.Scan(&d.ID, &d.ProjectID, &d.Name, &c, &u)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	d.CreatedAt, _ = time.Parse(time.RFC3339, c)
	d.UpdatedAt, _ = time.Parse(time.RFC3339, u)
	return &d, nil
}

func (s *Store) CreateDatabase(d *model.Database) error {
	if d.ID == "" {
		d.ID = id.New()
	}
	now := time.Now().UTC()
	d.CreatedAt, d.UpdatedAt = now, now
	_, err := s.db.Exec("INSERT INTO udb ("+dbCols+") VALUES (?,?,?,?,?)",
		d.ID, d.ProjectID, d.Name,
		now.Format(time.RFC3339), now.Format(time.RFC3339))
	return err
}

func (s *Store) GetDatabase(dbID string) (*model.Database, error) {
	return scanDatabase(s.db.QueryRow("SELECT "+dbCols+" FROM udb WHERE id = ?", dbID))
}

func (s *Store) ListDatabases(projectID string) ([]*model.Database, error) {
	rows, err := s.db.Query("SELECT "+dbCols+" FROM udb WHERE project_id = ? ORDER BY created_at ASC", projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Database
	for rows.Next() {
		d, err := scanDatabase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) RenameDatabase(dbID, name string) (*model.Database, error) {
	res, err := s.db.Exec("UPDATE udb SET name = ?, updated_at = ? WHERE id = ?",
		name, time.Now().UTC().Format(time.RFC3339), dbID)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return s.GetDatabase(dbID)
}

func (s *Store) DeleteDatabase(dbID string) error {
	if _, err := s.GetDatabase(dbID); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	for _, q := range []string{
		"DELETE FROM udb_view WHERE db_id = ?",
		"DELETE FROM udb_row WHERE db_id = ?",
		"DELETE FROM udb_column WHERE db_id = ?",
		"DELETE FROM udb WHERE id = ?",
	} {
		if _, err := tx.Exec(q, dbID); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestDatabaseCRUD`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/store/udb.go internal/store/udb_test.go internal/store/store.go
git commit --no-gpg-sign -m "feat(store): udb tables + Database CRUD with cascade delete"
```

---

### Task 3: Store -- Column CRUD

**Files:**
- Modify: `internal/store/udb.go`
- Test: `internal/store/udb_test.go`

**Interfaces:**
- Consumes: `model.Column`, `model.SelectOption`.
- Produces: `AddColumn(*model.Column) error`, `ListColumns(dbID string) ([]*model.Column, error)`, `GetColumn(id string) (*model.Column, error)`, `UpdateColumn(id string, updates map[string]any) (*model.Column, error)`, `DeleteColumn(id string) error`. `AddColumn` rejects a type not in the column-type set with `ErrInvalidColumnType`.

- [ ] **Step 1: Write the failing test** (append to `udb_test.go`):

```go
func TestColumnCRUD(t *testing.T) {
	s := newTestStore(t)
	db := &model.Database{Name: "T"}
	s.CreateDatabase(db)

	col := &model.Column{DBID: db.ID, Name: "Status", Type: model.ColumnTypeSelect,
		Options: []model.SelectOption{{Value: "Todo"}, {Value: "Done"}}}
	if err := s.AddColumn(col); err != nil {
		t.Fatalf("add: %v", err)
	}
	if col.ID == "" {
		t.Fatal("expected id")
	}
	cols, _ := s.ListColumns(db.ID)
	if len(cols) != 1 || cols[0].Type != model.ColumnTypeSelect || len(cols[0].Options) != 2 {
		t.Fatalf("list roundtrip wrong: %+v", cols)
	}

	bad := &model.Column{DBID: db.ID, Name: "X", Type: "bogus"}
	if err := s.AddColumn(bad); err != ErrInvalidColumnType {
		t.Fatalf("expected ErrInvalidColumnType, got %v", err)
	}

	if _, err := s.UpdateColumn(col.ID, map[string]any{"name": "State"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := s.GetColumn(col.ID)
	if got.Name != "State" {
		t.Fatalf("update not applied: %q", got.Name)
	}
	if err := s.DeleteColumn(col.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if cols, _ := s.ListColumns(db.ID); len(cols) != 0 {
		t.Fatalf("expected 0 columns, got %d", len(cols))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestColumnCRUD`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement** (append to `udb.go`; add `"encoding/json"` to imports):

```go
func validColumnType(t string) bool {
	switch t {
	case model.ColumnTypeText, model.ColumnTypeNumber, model.ColumnTypeDate,
		model.ColumnTypeSelect, model.ColumnTypeCheckbox:
		return true
	}
	return false
}

const colCols = "id, db_id, name, type, position, options_json, created_at"

func scanColumn(row scanner) (*model.Column, error) {
	var c model.Column
	var opts, created string
	err := row.Scan(&c.ID, &c.DBID, &c.Name, &c.Type, &c.Position, &opts, &created)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if opts != "" {
		json.Unmarshal([]byte(opts), &c.Options)
	}
	c.CreatedAt, _ = time.Parse(time.RFC3339, created)
	return &c, nil
}

func (s *Store) AddColumn(c *model.Column) error {
	if !validColumnType(c.Type) {
		return ErrInvalidColumnType
	}
	if c.ID == "" {
		c.ID = id.New()
	}
	c.CreatedAt = time.Now().UTC()
	optsJSON := ""
	if len(c.Options) > 0 {
		b, _ := json.Marshal(c.Options)
		optsJSON = string(b)
	}
	_, err := s.db.Exec("INSERT INTO udb_column ("+colCols+") VALUES (?,?,?,?,?,?,?)",
		c.ID, c.DBID, c.Name, c.Type, c.Position, optsJSON, c.CreatedAt.Format(time.RFC3339))
	return err
}

func (s *Store) GetColumn(colID string) (*model.Column, error) {
	return scanColumn(s.db.QueryRow("SELECT "+colCols+" FROM udb_column WHERE id = ?", colID))
}

func (s *Store) ListColumns(dbID string) ([]*model.Column, error) {
	rows, err := s.db.Query("SELECT "+colCols+" FROM udb_column WHERE db_id = ? ORDER BY position ASC, created_at ASC", dbID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Column
	for rows.Next() {
		c, err := scanColumn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) UpdateColumn(colID string, updates map[string]any) (*model.Column, error) {
	if _, err := s.GetColumn(colID); err != nil {
		return nil, err
	}
	var sets []string
	var args []any
	for k, v := range updates {
		switch k {
		case "name", "position":
			sets = append(sets, k+" = ?")
			args = append(args, v)
		case "options":
			b, _ := json.Marshal(v)
			sets = append(sets, "options_json = ?")
			args = append(args, string(b))
		}
	}
	if len(sets) == 0 {
		return s.GetColumn(colID)
	}
	args = append(args, colID)
	if _, err := s.db.Exec("UPDATE udb_column SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...); err != nil {
		return nil, err
	}
	return s.GetColumn(colID)
}

func (s *Store) DeleteColumn(colID string) error {
	res, err := s.db.Exec("DELETE FROM udb_column WHERE id = ?", colID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
```

> Add `"strings"` to the `udb.go` import block (used by `UpdateColumn`).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestColumnCRUD`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/udb.go internal/store/udb_test.go
git commit --no-gpg-sign -m "feat(store): udb column CRUD with type validation"
```

---

### Task 4: Store -- Row CRUD + cell validation

**Files:**
- Modify: `internal/store/udb.go`
- Test: `internal/store/udb_test.go`

**Interfaces:**
- Consumes: `model.Row`, `ListColumns`, `model.Column`.
- Produces: `CreateRow(*model.Row) error`, `ListRows(dbID string) ([]*model.Row, error)`, `GetRow(id string) (*model.Row, error)`, `UpdateRow(id string, data map[string]any) (*model.Row, error)`, `DeleteRow(id string) error`. Writes validate every key against the DB's columns: unknown key -> `ErrUnknownColumn`; type mismatch -> `ErrInvalidCell`. A `nil` value is an allowed unset cell.

- [ ] **Step 1: Write the failing test** (append):

```go
func TestRowCRUDAndValidation(t *testing.T) {
	s := newTestStore(t)
	db := &model.Database{Name: "T"}
	s.CreateDatabase(db)
	name := &model.Column{DBID: db.ID, Name: "Name", Type: model.ColumnTypeText}
	age := &model.Column{DBID: db.ID, Name: "Age", Type: model.ColumnTypeNumber}
	done := &model.Column{DBID: db.ID, Name: "Done", Type: model.ColumnTypeCheckbox}
	s.AddColumn(name)
	s.AddColumn(age)
	s.AddColumn(done)

	row := &model.Row{DBID: db.ID, Data: map[string]any{name.ID: "Ada", age.ID: 36.0, done.ID: true}}
	if err := s.CreateRow(row); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, _ := s.GetRow(row.ID)
	if got.Data[name.ID] != "Ada" || got.Data[age.ID].(float64) != 36 {
		t.Fatalf("roundtrip wrong: %+v", got.Data)
	}

	// unknown column key
	if err := s.CreateRow(&model.Row{DBID: db.ID, Data: map[string]any{"nope": "x"}}); err != ErrUnknownColumn {
		t.Fatalf("expected ErrUnknownColumn, got %v", err)
	}
	// wrong type: number column gets a string
	if err := s.CreateRow(&model.Row{DBID: db.ID, Data: map[string]any{age.ID: "old"}}); err != ErrInvalidCell {
		t.Fatalf("expected ErrInvalidCell, got %v", err)
	}

	if _, err := s.UpdateRow(row.ID, map[string]any{name.ID: "Grace"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ = s.GetRow(row.ID)
	if got.Data[name.ID] != "Grace" {
		t.Fatalf("update not applied: %+v", got.Data)
	}
	if err := s.DeleteRow(row.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if rows, _ := s.ListRows(db.ID); len(rows) != 0 {
		t.Fatalf("expected 0 rows, got %d", len(rows))
	}
}

func TestDeleteColumnLeavesRowDataInert(t *testing.T) {
	s := newTestStore(t)
	db := &model.Database{Name: "T"}
	s.CreateDatabase(db)
	c := &model.Column{DBID: db.ID, Name: "Name", Type: model.ColumnTypeText}
	s.AddColumn(c)
	s.CreateRow(&model.Row{DBID: db.ID, Data: map[string]any{c.ID: "keep"}})
	if err := s.DeleteColumn(c.ID); err != nil {
		t.Fatalf("delete col: %v", err)
	}
	// Row still loads; the stale key is simply ignored by callers.
	rows, err := s.ListRows(db.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("row should survive column delete: %v len=%d", err, len(rows))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run 'TestRowCRUDAndValidation|TestDeleteColumnLeavesRowDataInert'`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement** (append to `udb.go`):

```go
func validateCellValue(c *model.Column, v any) error {
	if v == nil {
		return nil // unset cell allowed
	}
	switch c.Type {
	case model.ColumnTypeText:
		if _, ok := v.(string); !ok {
			return ErrInvalidCell
		}
	case model.ColumnTypeNumber:
		switch v.(type) {
		case float64, float32, int, int64:
		default:
			return ErrInvalidCell
		}
	case model.ColumnTypeCheckbox:
		if _, ok := v.(bool); !ok {
			return ErrInvalidCell
		}
	case model.ColumnTypeDate:
		s, ok := v.(string)
		if !ok {
			return ErrInvalidCell
		}
		if _, err := time.Parse("2006-01-02", s); err != nil {
			return ErrInvalidCell
		}
	case model.ColumnTypeSelect:
		s, ok := v.(string)
		if !ok {
			return ErrInvalidCell
		}
		for _, o := range c.Options {
			if o.Value == s {
				return nil
			}
		}
		return ErrInvalidCell
	default:
		return ErrInvalidColumnType
	}
	return nil
}

func (s *Store) validateRowData(dbID string, data map[string]any) error {
	cols, err := s.ListColumns(dbID)
	if err != nil {
		return err
	}
	byID := make(map[string]*model.Column, len(cols))
	for _, c := range cols {
		byID[c.ID] = c
	}
	for k, v := range data {
		c, ok := byID[k]
		if !ok {
			return ErrUnknownColumn
		}
		if err := validateCellValue(c, v); err != nil {
			return err
		}
	}
	return nil
}

const rowCols = "id, db_id, data_json, position, created_at, updated_at"

func scanRow(row scanner) (*model.Row, error) {
	var r model.Row
	var data, c, u string
	err := row.Scan(&r.ID, &r.DBID, &data, &r.Position, &c, &u)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r.Data = map[string]any{}
	if data != "" {
		json.Unmarshal([]byte(data), &r.Data)
	}
	r.CreatedAt, _ = time.Parse(time.RFC3339, c)
	r.UpdatedAt, _ = time.Parse(time.RFC3339, u)
	return &r, nil
}

func (s *Store) CreateRow(r *model.Row) error {
	if err := s.validateRowData(r.DBID, r.Data); err != nil {
		return err
	}
	if r.ID == "" {
		r.ID = id.New()
	}
	now := time.Now().UTC()
	r.CreatedAt, r.UpdatedAt = now, now
	if r.Data == nil {
		r.Data = map[string]any{}
	}
	b, _ := json.Marshal(r.Data)
	_, err := s.db.Exec("INSERT INTO udb_row ("+rowCols+") VALUES (?,?,?,?,?,?)",
		r.ID, r.DBID, string(b), r.Position,
		now.Format(time.RFC3339), now.Format(time.RFC3339))
	return err
}

func (s *Store) GetRow(rowID string) (*model.Row, error) {
	return scanRow(s.db.QueryRow("SELECT "+rowCols+" FROM udb_row WHERE id = ?", rowID))
}

func (s *Store) ListRows(dbID string) ([]*model.Row, error) {
	rows, err := s.db.Query("SELECT "+rowCols+" FROM udb_row WHERE db_id = ? ORDER BY position ASC, created_at ASC", dbID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Row
	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpdateRow merges the provided keys into the row's data (partial update).
func (s *Store) UpdateRow(rowID string, data map[string]any) (*model.Row, error) {
	existing, err := s.GetRow(rowID)
	if err != nil {
		return nil, err
	}
	if err := s.validateRowData(existing.DBID, data); err != nil {
		return nil, err
	}
	for k, v := range data {
		existing.Data[k] = v
	}
	b, _ := json.Marshal(existing.Data)
	if _, err := s.db.Exec("UPDATE udb_row SET data_json = ?, updated_at = ? WHERE id = ?",
		string(b), time.Now().UTC().Format(time.RFC3339), rowID); err != nil {
		return nil, err
	}
	return s.GetRow(rowID)
}

func (s *Store) DeleteRow(rowID string) error {
	res, err := s.db.Exec("DELETE FROM udb_row WHERE id = ?", rowID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run 'TestRowCRUDAndValidation|TestDeleteColumnLeavesRowDataInert'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/udb.go internal/store/udb_test.go
git commit --no-gpg-sign -m "feat(store): udb row CRUD with cell type validation"
```

---

### Task 5: Store -- View CRUD + config validation

**Files:**
- Modify: `internal/store/udb.go`
- Test: `internal/store/udb_test.go`

**Interfaces:**
- Consumes: `model.View`, `model.ViewConfig`, `ListColumns`.
- Produces: `CreateView(*model.View) error`, `ListViews(dbID string) ([]*model.View, error)`, `GetView(id string) (*model.View, error)`, `UpdateView(id string, updates map[string]any) (*model.View, error)`, `DeleteView(id string) error`. Validation: unknown kind -> `ErrInvalidView`; kanban whose `group_by` is missing or not a `select` column, or calendar whose `date_field` is missing or not a `date` column -> `ErrInvalidViewConfig`.

- [ ] **Step 1: Write the failing test** (append):

```go
func TestViewCRUDAndValidation(t *testing.T) {
	s := newTestStore(t)
	db := &model.Database{Name: "T"}
	s.CreateDatabase(db)
	sel := &model.Column{DBID: db.ID, Name: "Stage", Type: model.ColumnTypeSelect,
		Options: []model.SelectOption{{Value: "A"}}}
	dt := &model.Column{DBID: db.ID, Name: "Due", Type: model.ColumnTypeDate}
	s.AddColumn(sel)
	s.AddColumn(dt)

	tbl := &model.View{DBID: db.ID, Name: "All", Kind: model.ViewKindTable}
	if err := s.CreateView(tbl); err != nil {
		t.Fatalf("table view: %v", err)
	}
	kan := &model.View{DBID: db.ID, Name: "Board", Kind: model.ViewKindKanban,
		Config: model.ViewConfig{GroupBy: sel.ID}}
	if err := s.CreateView(kan); err != nil {
		t.Fatalf("kanban view: %v", err)
	}
	cal := &model.View{DBID: db.ID, Name: "Cal", Kind: model.ViewKindCalendar,
		Config: model.ViewConfig{DateField: dt.ID}}
	if err := s.CreateView(cal); err != nil {
		t.Fatalf("calendar view: %v", err)
	}

	if err := s.CreateView(&model.View{DBID: db.ID, Name: "X", Kind: "grid"}); err != ErrInvalidView {
		t.Fatalf("expected ErrInvalidView, got %v", err)
	}
	// kanban grouped on a non-select column
	if err := s.CreateView(&model.View{DBID: db.ID, Name: "Y", Kind: model.ViewKindKanban,
		Config: model.ViewConfig{GroupBy: dt.ID}}); err != ErrInvalidViewConfig {
		t.Fatalf("expected ErrInvalidViewConfig, got %v", err)
	}
	// calendar on a non-date column
	if err := s.CreateView(&model.View{DBID: db.ID, Name: "Z", Kind: model.ViewKindCalendar,
		Config: model.ViewConfig{DateField: sel.ID}}); err != ErrInvalidViewConfig {
		t.Fatalf("expected ErrInvalidViewConfig, got %v", err)
	}

	views, _ := s.ListViews(db.ID)
	if len(views) != 3 {
		t.Fatalf("expected 3 views, got %d", len(views))
	}
	if _, err := s.UpdateView(kan.ID, map[string]any{"name": "Pipeline"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := s.DeleteView(tbl.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if views, _ := s.ListViews(db.ID); len(views) != 2 {
		t.Fatalf("expected 2 views, got %d", len(views))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestViewCRUDAndValidation`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement** (append to `udb.go`):

```go
func (s *Store) columnByID(dbID, colID string) *model.Column {
	cols, err := s.ListColumns(dbID)
	if err != nil {
		return nil
	}
	for _, c := range cols {
		if c.ID == colID {
			return c
		}
	}
	return nil
}

func (s *Store) validateView(v *model.View) error {
	switch v.Kind {
	case model.ViewKindTable:
		return nil
	case model.ViewKindKanban:
		c := s.columnByID(v.DBID, v.Config.GroupBy)
		if c == nil || c.Type != model.ColumnTypeSelect {
			return ErrInvalidViewConfig
		}
		return nil
	case model.ViewKindCalendar:
		c := s.columnByID(v.DBID, v.Config.DateField)
		if c == nil || c.Type != model.ColumnTypeDate {
			return ErrInvalidViewConfig
		}
		return nil
	default:
		return ErrInvalidView
	}
}

const viewCols = "id, db_id, name, kind, config_json, position"

func scanView(row scanner) (*model.View, error) {
	var v model.View
	var cfg string
	err := row.Scan(&v.ID, &v.DBID, &v.Name, &v.Kind, &cfg, &v.Position)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if cfg != "" {
		json.Unmarshal([]byte(cfg), &v.Config)
	}
	return &v, nil
}

func (s *Store) CreateView(v *model.View) error {
	if err := s.validateView(v); err != nil {
		return err
	}
	if v.ID == "" {
		v.ID = id.New()
	}
	b, _ := json.Marshal(v.Config)
	_, err := s.db.Exec("INSERT INTO udb_view ("+viewCols+") VALUES (?,?,?,?,?,?)",
		v.ID, v.DBID, v.Name, v.Kind, string(b), v.Position)
	return err
}

func (s *Store) GetView(viewID string) (*model.View, error) {
	return scanView(s.db.QueryRow("SELECT "+viewCols+" FROM udb_view WHERE id = ?", viewID))
}

func (s *Store) ListViews(dbID string) ([]*model.View, error) {
	rows, err := s.db.Query("SELECT "+viewCols+" FROM udb_view WHERE db_id = ? ORDER BY position ASC", dbID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.View
	for rows.Next() {
		v, err := scanView(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) UpdateView(viewID string, updates map[string]any) (*model.View, error) {
	existing, err := s.GetView(viewID)
	if err != nil {
		return nil, err
	}
	if name, ok := updates["name"].(string); ok {
		existing.Name = name
	}
	if pos, ok := updates["position"].(float64); ok {
		existing.Position = int(pos)
	}
	if cfg, ok := updates["config"]; ok {
		b, _ := json.Marshal(cfg)
		json.Unmarshal(b, &existing.Config)
	}
	if err := s.validateView(existing); err != nil {
		return nil, err
	}
	cfgB, _ := json.Marshal(existing.Config)
	if _, err := s.db.Exec("UPDATE udb_view SET name = ?, config_json = ?, position = ? WHERE id = ?",
		existing.Name, string(cfgB), existing.Position, viewID); err != nil {
		return nil, err
	}
	return s.GetView(viewID)
}

func (s *Store) DeleteView(viewID string) error {
	res, err := s.db.Exec("DELETE FROM udb_view WHERE id = ?", viewID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestViewCRUDAndValidation`
Expected: PASS.

- [ ] **Step 5: Full store suite + commit**

Run: `go test ./internal/store/`
Expected: PASS.

```bash
git add internal/store/udb.go internal/store/udb_test.go
git commit --no-gpg-sign -m "feat(store): udb view CRUD with config validation"
```

---

### Task 6: API -- database endpoints

**Files:**
- Create: `internal/api/udb.go`
- Modify: `internal/api/api.go` (register routes in `Handler()`)
- Test: `internal/api/udb_test.go`

**Interfaces:**
- Consumes: store `*Database` methods, `writeJSON`, `writeError`, `a.emit`, `store.ErrNotFound`.
- Produces: handler `handleUDB` (collection `/api/udb`) and `handleUDBItem` (`/api/udb/`), the router entry point for all later udb API tasks. Later tasks extend `handleUDBItem`'s sub-path routing (`{id}/columns`, `{id}/rows`, `{id}/views`).
- A shared helper `udbMapErr(w, err) bool` maps store sentinel errors to HTTP status and returns true if it wrote a response.

- [ ] **Step 1: Register routes.** In `internal/api/api.go` `Handler()`, next to the pages routes, add:

```go
	mux.HandleFunc("/api/udb", a.handleUDB)
	mux.HandleFunc("/api/udb/", a.handleUDBItem)
```

- [ ] **Step 2: Write the failing test** `internal/api/udb_test.go`:

```go
package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestAPI must match the existing helper in api_test.go; if the name/signature
// differs, use the real one. It should return an *API wired to an in-memory store.
func TestUDBDatabaseEndpoints(t *testing.T) {
	a := newTestAPI(t)
	srv := httptest.NewServer(a.Handler())
	defer srv.Close()

	// create
	body, _ := json.Marshal(map[string]any{"name": "Contacts", "project_id": "p1"})
	res, _ := http.Post(srv.URL+"/api/udb", "application/json", bytes.NewReader(body))
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create status %d", res.StatusCode)
	}
	var db map[string]any
	json.NewDecoder(res.Body).Decode(&db)
	id, _ := db["id"].(string)
	if id == "" {
		t.Fatal("no id returned")
	}

	// list
	res, _ = http.Get(srv.URL + "/api/udb?project_id=p1")
	var list []map[string]any
	json.NewDecoder(res.Body).Decode(&list)
	if len(list) != 1 {
		t.Fatalf("list len %d", len(list))
	}

	// delete
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/udb/"+id, nil)
	res, _ = http.DefaultClient.Do(req)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status %d", res.StatusCode)
	}

	// get 404
	res, _ = http.Get(srv.URL + "/api/udb/" + id)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", res.StatusCode)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/api/ -run TestUDBDatabaseEndpoints`
Expected: FAIL (undefined handlers).

- [ ] **Step 4: Implement** `internal/api/udb.go`:

```go
package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/maniginam/waggle/internal/model"
	"github.com/maniginam/waggle/internal/store"
)

// udbMapErr maps store sentinel errors to HTTP responses.
// Returns true if it wrote a response.
func udbMapErr(w http.ResponseWriter, err error) bool {
	switch err {
	case nil:
		return false
	case store.ErrNotFound:
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	case store.ErrUnknownColumn:
		writeError(w, http.StatusBadRequest, "unknown_column", err.Error())
	case store.ErrInvalidCell:
		writeError(w, http.StatusBadRequest, "invalid_cell", err.Error())
	case store.ErrInvalidColumnType:
		writeError(w, http.StatusBadRequest, "invalid_column_type", err.Error())
	case store.ErrInvalidView:
		writeError(w, http.StatusBadRequest, "invalid_view", err.Error())
	case store.ErrInvalidViewConfig:
		writeError(w, http.StatusBadRequest, "invalid_view_config", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
	}
	return true
}

func (a *API) handleUDB(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		dbs, err := a.store.ListDatabases(r.URL.Query().Get("project_id"))
		if udbMapErr(w, err) {
			return
		}
		if dbs == nil {
			dbs = []*model.Database{}
		}
		writeJSON(w, http.StatusOK, dbs)
	case http.MethodPost:
		var d model.Database
		if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		if d.Name == "" {
			writeError(w, http.StatusBadRequest, "missing_name", "name is required")
			return
		}
		if err := a.store.CreateDatabase(&d); udbMapErr(w, err) {
			return
		}
		a.emit(&model.Event{Type: model.EventDatabaseCreated, Payload: d})
		writeJSON(w, http.StatusCreated, d)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleUDBItem routes /api/udb/{id} and /api/udb/{id}/{sub}...
// Sub-resources (columns, rows, views) are added in Tasks 7-9.
func (a *API) handleUDBItem(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/udb/")
	parts := strings.Split(rest, "/")
	dbID := parts[0]
	if dbID == "" {
		writeError(w, http.StatusBadRequest, "missing_id", "database ID required")
		return
	}
	if len(parts) > 1 {
		switch parts[1] {
		case "columns":
			a.handleUDBColumns(w, r, dbID, parts[2:])
			return
		case "rows":
			a.handleUDBRows(w, r, dbID, parts[2:])
			return
		case "views":
			a.handleUDBViews(w, r, dbID, parts[2:])
			return
		}
		writeError(w, http.StatusNotFound, "not_found", "unknown sub-resource")
		return
	}
	switch r.Method {
	case http.MethodGet:
		d, err := a.store.GetDatabase(dbID)
		if udbMapErr(w, err) {
			return
		}
		writeJSON(w, http.StatusOK, d)
	case http.MethodPatch:
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		d, err := a.store.RenameDatabase(dbID, body.Name)
		if udbMapErr(w, err) {
			return
		}
		a.emit(&model.Event{Type: model.EventDatabaseUpdated, Payload: d})
		writeJSON(w, http.StatusOK, d)
	case http.MethodDelete:
		if err := a.store.DeleteDatabase(dbID); udbMapErr(w, err) {
			return
		}
		a.emit(&model.Event{Type: model.EventDatabaseDeleted, Payload: map[string]string{"id": dbID}})
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
```

> This task references `handleUDBColumns`, `handleUDBRows`, `handleUDBViews`, which Tasks 7-9 implement. To keep Task 6 compiling and testable on its own, add temporary stubs at the bottom of `udb.go` now and replace them in the later tasks:
> ```go
> func (a *API) handleUDBColumns(w http.ResponseWriter, r *http.Request, dbID string, rest []string) {
> 	w.WriteHeader(http.StatusNotImplemented)
> }
> func (a *API) handleUDBRows(w http.ResponseWriter, r *http.Request, dbID string, rest []string) {
> 	w.WriteHeader(http.StatusNotImplemented)
> }
> func (a *API) handleUDBViews(w http.ResponseWriter, r *http.Request, dbID string, rest []string) {
> 	w.WriteHeader(http.StatusNotImplemented)
> }
> ```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/api/ -run TestUDBDatabaseEndpoints`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/udb.go internal/api/api.go internal/api/udb_test.go
git commit --no-gpg-sign -m "feat(api): udb database endpoints + sub-resource router"
```

---

### Task 7: API -- column endpoints

**Files:**
- Modify: `internal/api/udb.go` (replace the `handleUDBColumns` stub)
- Test: `internal/api/udb_test.go`

**Interfaces:**
- Consumes: store column methods, `udbMapErr`.
- Produces: `handleUDBColumns(w, r, dbID string, rest []string)` handling `GET/POST /api/udb/{id}/columns` and `PATCH/DELETE /api/udb/{id}/columns/{cid}`. Emits `EventDatabaseChanged` with `{db_id}` on mutation.

- [ ] **Step 1: Write the failing test** (append to `udb_test.go`):

```go
func TestUDBColumnEndpoints(t *testing.T) {
	a := newTestAPI(t)
	srv := httptest.NewServer(a.Handler())
	defer srv.Close()
	dbID := createTestDB(t, srv.URL, "T")

	// add column
	body, _ := json.Marshal(map[string]any{"name": "Status", "type": "select",
		"options": []map[string]any{{"value": "Todo"}}})
	res, _ := http.Post(srv.URL+"/api/udb/"+dbID+"/columns", "application/json", bytes.NewReader(body))
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("add column status %d", res.StatusCode)
	}

	// bad type -> 400 invalid_column_type
	bad, _ := json.Marshal(map[string]any{"name": "X", "type": "bogus"})
	res, _ = http.Post(srv.URL+"/api/udb/"+dbID+"/columns", "application/json", bytes.NewReader(bad))
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad type status %d", res.StatusCode)
	}

	// list
	res, _ = http.Get(srv.URL + "/api/udb/" + dbID + "/columns")
	var cols []map[string]any
	json.NewDecoder(res.Body).Decode(&cols)
	if len(cols) != 1 {
		t.Fatalf("expected 1 column, got %d", len(cols))
	}
}
```

Add these test helpers to `udb_test.go` (reused by later tasks):

```go
func createTestDB(t *testing.T, base, name string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"name": name})
	res, _ := http.Post(base+"/api/udb", "application/json", bytes.NewReader(body))
	var db map[string]any
	json.NewDecoder(res.Body).Decode(&db)
	return db["id"].(string)
}

func addTestColumn(t *testing.T, base, dbID string, m map[string]any) string {
	t.Helper()
	body, _ := json.Marshal(m)
	res, _ := http.Post(base+"/api/udb/"+dbID+"/columns", "application/json", bytes.NewReader(body))
	var c map[string]any
	json.NewDecoder(res.Body).Decode(&c)
	return c["id"].(string)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/ -run TestUDBColumnEndpoints`
Expected: FAIL (stub returns 501).

- [ ] **Step 3: Implement** -- replace the `handleUDBColumns` stub in `udb.go`:

```go
func (a *API) handleUDBColumns(w http.ResponseWriter, r *http.Request, dbID string, rest []string) {
	if len(rest) == 0 || rest[0] == "" {
		switch r.Method {
		case http.MethodGet:
			cols, err := a.store.ListColumns(dbID)
			if udbMapErr(w, err) {
				return
			}
			if cols == nil {
				cols = []*model.Column{}
			}
			writeJSON(w, http.StatusOK, cols)
		case http.MethodPost:
			var c model.Column
			if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
				writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
				return
			}
			if c.Name == "" {
				writeError(w, http.StatusBadRequest, "missing_name", "column name is required")
				return
			}
			c.DBID = dbID
			if err := a.store.AddColumn(&c); udbMapErr(w, err) {
				return
			}
			a.emit(&model.Event{Type: model.EventDatabaseChanged, Payload: map[string]string{"db_id": dbID}})
			writeJSON(w, http.StatusCreated, c)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	colID := rest[0]
	switch r.Method {
	case http.MethodPatch:
		var updates map[string]any
		if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		c, err := a.store.UpdateColumn(colID, updates)
		if udbMapErr(w, err) {
			return
		}
		a.emit(&model.Event{Type: model.EventDatabaseChanged, Payload: map[string]string{"db_id": dbID}})
		writeJSON(w, http.StatusOK, c)
	case http.MethodDelete:
		if err := a.store.DeleteColumn(colID); udbMapErr(w, err) {
			return
		}
		a.emit(&model.Event{Type: model.EventDatabaseChanged, Payload: map[string]string{"db_id": dbID}})
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/api/ -run TestUDBColumnEndpoints`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/udb.go internal/api/udb_test.go
git commit --no-gpg-sign -m "feat(api): udb column endpoints"
```

---

### Task 8: API -- row endpoints

**Files:**
- Modify: `internal/api/udb.go` (replace the `handleUDBRows` stub)
- Test: `internal/api/udb_test.go`

**Interfaces:**
- Consumes: store row methods, `udbMapErr`.
- Produces: `handleUDBRows(w, r, dbID string, rest []string)` handling `GET/POST /api/udb/{id}/rows` and `PATCH/DELETE /api/udb/{id}/rows/{rid}`. POST/PATCH bodies carry `{"data": {colID: value}}`. Validation errors surface as 400 via `udbMapErr`.

- [ ] **Step 1: Write the failing test** (append):

```go
func TestUDBRowEndpoints(t *testing.T) {
	a := newTestAPI(t)
	srv := httptest.NewServer(a.Handler())
	defer srv.Close()
	dbID := createTestDB(t, srv.URL, "T")
	nameCol := addTestColumn(t, srv.URL, dbID, map[string]any{"name": "Name", "type": "text"})
	ageCol := addTestColumn(t, srv.URL, dbID, map[string]any{"name": "Age", "type": "number"})

	// create row
	body, _ := json.Marshal(map[string]any{"data": map[string]any{nameCol: "Ada", ageCol: 36}})
	res, _ := http.Post(srv.URL+"/api/udb/"+dbID+"/rows", "application/json", bytes.NewReader(body))
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create row status %d", res.StatusCode)
	}
	var row map[string]any
	json.NewDecoder(res.Body).Decode(&row)
	rowID := row["id"].(string)

	// wrong type -> 400 invalid_cell
	badBody, _ := json.Marshal(map[string]any{"data": map[string]any{ageCol: "old"}})
	res, _ = http.Post(srv.URL+"/api/udb/"+dbID+"/rows", "application/json", bytes.NewReader(badBody))
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad cell, got %d", res.StatusCode)
	}

	// list
	res, _ = http.Get(srv.URL + "/api/udb/" + dbID + "/rows")
	var rows []map[string]any
	json.NewDecoder(res.Body).Decode(&rows)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}

	// patch
	patch, _ := json.Marshal(map[string]any{"data": map[string]any{nameCol: "Grace"}})
	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/api/udb/"+dbID+"/rows/"+rowID, bytes.NewReader(patch))
	res, _ = http.DefaultClient.Do(req)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("patch status %d", res.StatusCode)
	}

	// delete
	req, _ = http.NewRequest(http.MethodDelete, srv.URL+"/api/udb/"+dbID+"/rows/"+rowID, nil)
	res, _ = http.DefaultClient.Do(req)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status %d", res.StatusCode)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/ -run TestUDBRowEndpoints`
Expected: FAIL (stub 501).

- [ ] **Step 3: Implement** -- replace the `handleUDBRows` stub:

```go
func (a *API) handleUDBRows(w http.ResponseWriter, r *http.Request, dbID string, rest []string) {
	if len(rest) == 0 || rest[0] == "" {
		switch r.Method {
		case http.MethodGet:
			rows, err := a.store.ListRows(dbID)
			if udbMapErr(w, err) {
				return
			}
			if rows == nil {
				rows = []*model.Row{}
			}
			writeJSON(w, http.StatusOK, rows)
		case http.MethodPost:
			var body struct {
				Data map[string]any `json:"data"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
				return
			}
			row := model.Row{DBID: dbID, Data: body.Data}
			if err := a.store.CreateRow(&row); udbMapErr(w, err) {
				return
			}
			a.emit(&model.Event{Type: model.EventDatabaseChanged, Payload: map[string]string{"db_id": dbID}})
			writeJSON(w, http.StatusCreated, row)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	rowID := rest[0]
	switch r.Method {
	case http.MethodPatch:
		var body struct {
			Data map[string]any `json:"data"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		row, err := a.store.UpdateRow(rowID, body.Data)
		if udbMapErr(w, err) {
			return
		}
		a.emit(&model.Event{Type: model.EventDatabaseChanged, Payload: map[string]string{"db_id": dbID}})
		writeJSON(w, http.StatusOK, row)
	case http.MethodDelete:
		if err := a.store.DeleteRow(rowID); udbMapErr(w, err) {
			return
		}
		a.emit(&model.Event{Type: model.EventDatabaseChanged, Payload: map[string]string{"db_id": dbID}})
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/api/ -run TestUDBRowEndpoints`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/udb.go internal/api/udb_test.go
git commit --no-gpg-sign -m "feat(api): udb row endpoints with validation"
```

---

### Task 9: API -- view endpoints

**Files:**
- Modify: `internal/api/udb.go` (replace the `handleUDBViews` stub)
- Test: `internal/api/udb_test.go`

**Interfaces:**
- Consumes: store view methods, `udbMapErr`.
- Produces: `handleUDBViews(w, r, dbID string, rest []string)` handling `GET/POST /api/udb/{id}/views` and `PATCH/DELETE /api/udb/{id}/views/{vid}`. Invalid kind/config -> 400.

- [ ] **Step 1: Write the failing test** (append):

```go
func TestUDBViewEndpoints(t *testing.T) {
	a := newTestAPI(t)
	srv := httptest.NewServer(a.Handler())
	defer srv.Close()
	dbID := createTestDB(t, srv.URL, "T")
	selCol := addTestColumn(t, srv.URL, dbID, map[string]any{"name": "Stage", "type": "select",
		"options": []map[string]any{{"value": "A"}}})

	// table view
	body, _ := json.Marshal(map[string]any{"name": "All", "kind": "table"})
	res, _ := http.Post(srv.URL+"/api/udb/"+dbID+"/views", "application/json", bytes.NewReader(body))
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("table view status %d", res.StatusCode)
	}

	// kanban with valid select group -> 201
	kb, _ := json.Marshal(map[string]any{"name": "Board", "kind": "kanban",
		"config": map[string]any{"group_by": selCol}})
	res, _ = http.Post(srv.URL+"/api/udb/"+dbID+"/views", "application/json", bytes.NewReader(kb))
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("kanban view status %d", res.StatusCode)
	}

	// calendar without a date field -> 400 invalid_view_config
	cal, _ := json.Marshal(map[string]any{"name": "Cal", "kind": "calendar",
		"config": map[string]any{"date_field": selCol}})
	res, _ = http.Post(srv.URL+"/api/udb/"+dbID+"/views", "application/json", bytes.NewReader(cal))
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad calendar, got %d", res.StatusCode)
	}

	// list
	res, _ = http.Get(srv.URL + "/api/udb/" + dbID + "/views")
	var views []map[string]any
	json.NewDecoder(res.Body).Decode(&views)
	if len(views) != 2 {
		t.Fatalf("expected 2 views, got %d", len(views))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/ -run TestUDBViewEndpoints`
Expected: FAIL (stub 501).

- [ ] **Step 3: Implement** -- replace the `handleUDBViews` stub:

```go
func (a *API) handleUDBViews(w http.ResponseWriter, r *http.Request, dbID string, rest []string) {
	if len(rest) == 0 || rest[0] == "" {
		switch r.Method {
		case http.MethodGet:
			views, err := a.store.ListViews(dbID)
			if udbMapErr(w, err) {
				return
			}
			if views == nil {
				views = []*model.View{}
			}
			writeJSON(w, http.StatusOK, views)
		case http.MethodPost:
			var v model.View
			if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
				writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
				return
			}
			if v.Name == "" {
				writeError(w, http.StatusBadRequest, "missing_name", "view name is required")
				return
			}
			v.DBID = dbID
			if err := a.store.CreateView(&v); udbMapErr(w, err) {
				return
			}
			a.emit(&model.Event{Type: model.EventDatabaseChanged, Payload: map[string]string{"db_id": dbID}})
			writeJSON(w, http.StatusCreated, v)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	viewID := rest[0]
	switch r.Method {
	case http.MethodPatch:
		var updates map[string]any
		if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		v, err := a.store.UpdateView(viewID, updates)
		if udbMapErr(w, err) {
			return
		}
		a.emit(&model.Event{Type: model.EventDatabaseChanged, Payload: map[string]string{"db_id": dbID}})
		writeJSON(w, http.StatusOK, v)
	case http.MethodDelete:
		if err := a.store.DeleteView(viewID); udbMapErr(w, err) {
			return
		}
		a.emit(&model.Event{Type: model.EventDatabaseChanged, Payload: map[string]string{"db_id": dbID}})
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
```

- [ ] **Step 4: Run test to verify it passes + full api suite**

Run: `go test ./internal/api/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/udb.go internal/api/udb_test.go
git commit --no-gpg-sign -m "feat(api): udb view endpoints with config validation"
```

---

### Task 10: MCP tools (7)

**Files:**
- Modify: `internal/mcp/mcp.go` (tool defs in `handleToolsList`, cases in `handleToolsCall`)
- Test: `internal/mcp/mcp_test.go`

**Interfaces:**
- Consumes: store udb methods, existing `toolDef`, `prop`, and the tool-result helpers already used by page tools.
- Produces: MCP tools `waggle_list_databases`, `waggle_create_database`, `waggle_add_column`, `waggle_list_rows`, `waggle_create_row`, `waggle_update_row`, `waggle_delete_row`.

> Before writing, read how an existing page tool returns results in `handleToolsCall` (the JSON-result / error helper it calls) and mirror it exactly. The snippets below assume a helper that marshals a value to a tool result; match the real one.

- [ ] **Step 1: Write the failing test** in `internal/mcp/mcp_test.go` (follow the existing MCP test style for building an adapter + calling a tool; adapt names to the real helpers):

```go
func TestUDBTools_CreateAndListRows(t *testing.T) {
	a := newTestAdapter(t) // use the real MCP test constructor

	// create database
	out := callTool(t, a, "waggle_create_database", map[string]any{"name": "T"})
	dbID := out["id"].(string)
	if dbID == "" {
		t.Fatal("no db id")
	}
	// add a text column
	col := callTool(t, a, "waggle_add_column", map[string]any{
		"db_id": dbID, "name": "Name", "type": "text"})
	colID := col["id"].(string)
	// create a row
	callTool(t, a, "waggle_create_row", map[string]any{
		"db_id": dbID, "data": map[string]any{colID: "Ada"}})
	// list rows
	list := callTool(t, a, "waggle_list_rows", map[string]any{"db_id": dbID})
	rows := list["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
}
```

> `callTool`/`newTestAdapter` are placeholders for whatever the existing MCP tests use. If none exist, drive `handleToolsCall` directly the way the current page-tool tests do and assert on the returned result payload.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcp/ -run TestUDBTools`
Expected: FAIL (unknown tool).

- [ ] **Step 3: Add tool defs** in `handleToolsList` (in the `tools` slice, after the page tools):

```go
		toolDef("waggle_list_databases", "List user-defined databases. project_id empty = workspace-level.", map[string]any{
			"type":       "object",
			"properties": map[string]any{"project_id": prop("string", "Project ID; empty for workspace-level")},
		}),
		toolDef("waggle_create_database", "Create a user-defined database.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":       prop("string", "Database name"),
				"project_id": prop("string", "Project ID (empty = workspace-level)"),
			},
			"required": []string{"name"},
		}),
		toolDef("waggle_add_column", "Add a typed column to a database. type ∈ text|number|date|select|checkbox.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"db_id":   prop("string", "Database ID"),
				"name":    prop("string", "Column name"),
				"type":    prop("string", "text|number|date|select|checkbox"),
				"options": prop("array", "For select columns: list of {value,color}"),
			},
			"required": []string{"db_id", "name", "type"},
		}),
		toolDef("waggle_list_rows", "List a database's columns and rows.", map[string]any{
			"type":       "object",
			"properties": map[string]any{"db_id": prop("string", "Database ID")},
			"required":   []string{"db_id"},
		}),
		toolDef("waggle_create_row", "Add a row. data maps column IDs to values (validated against column types).", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"db_id": prop("string", "Database ID"),
				"data":  prop("object", "Map of column_id -> value"),
			},
			"required": []string{"db_id", "data"},
		}),
		toolDef("waggle_update_row", "Update a row's cells (partial merge).", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"row_id": prop("string", "Row ID"),
				"data":   prop("object", "Map of column_id -> value to merge"),
			},
			"required": []string{"row_id", "data"},
		}),
		toolDef("waggle_delete_row", "Delete a row by ID.", map[string]any{
			"type":       "object",
			"properties": map[string]any{"row_id": prop("string", "Row ID")},
			"required":   []string{"row_id"},
		}),
```

- [ ] **Step 4: Add call cases** in `handleToolsCall` (mirror the page tools' argument-decoding and result-返回 helper; the shape below shows intent -- match the real helpers):

```go
	case "waggle_list_databases":
		var args struct{ ProjectID string `json:"project_id"` }
		json.Unmarshal(call.Arguments, &args)
		dbs, err := a.store.ListDatabases(args.ProjectID)
		if err != nil {
			a.sendToolError(req.ID, err.Error()) // use the real error helper
			return
		}
		a.sendToolJSON(req.ID, map[string]any{"databases": dbs}) // use the real result helper

	case "waggle_create_database":
		var args struct {
			Name      string `json:"name"`
			ProjectID string `json:"project_id"`
		}
		json.Unmarshal(call.Arguments, &args)
		d := model.Database{Name: args.Name, ProjectID: args.ProjectID}
		if err := a.store.CreateDatabase(&d); err != nil {
			a.sendToolError(req.ID, err.Error())
			return
		}
		a.sendToolJSON(req.ID, d)

	case "waggle_add_column":
		var args struct {
			DBID    string                `json:"db_id"`
			Name    string                `json:"name"`
			Type    string                `json:"type"`
			Options []model.SelectOption  `json:"options"`
		}
		json.Unmarshal(call.Arguments, &args)
		c := model.Column{DBID: args.DBID, Name: args.Name, Type: args.Type, Options: args.Options}
		if err := a.store.AddColumn(&c); err != nil {
			a.sendToolError(req.ID, err.Error())
			return
		}
		a.sendToolJSON(req.ID, c)

	case "waggle_list_rows":
		var args struct{ DBID string `json:"db_id"` }
		json.Unmarshal(call.Arguments, &args)
		cols, err := a.store.ListColumns(args.DBID)
		if err != nil {
			a.sendToolError(req.ID, err.Error())
			return
		}
		rows, err := a.store.ListRows(args.DBID)
		if err != nil {
			a.sendToolError(req.ID, err.Error())
			return
		}
		a.sendToolJSON(req.ID, map[string]any{"columns": cols, "rows": rows})

	case "waggle_create_row":
		var args struct {
			DBID string         `json:"db_id"`
			Data map[string]any `json:"data"`
		}
		json.Unmarshal(call.Arguments, &args)
		r := model.Row{DBID: args.DBID, Data: args.Data}
		if err := a.store.CreateRow(&r); err != nil {
			a.sendToolError(req.ID, err.Error())
			return
		}
		a.sendToolJSON(req.ID, r)

	case "waggle_update_row":
		var args struct {
			RowID string         `json:"row_id"`
			Data  map[string]any `json:"data"`
		}
		json.Unmarshal(call.Arguments, &args)
		r, err := a.store.UpdateRow(args.RowID, args.Data)
		if err != nil {
			a.sendToolError(req.ID, err.Error())
			return
		}
		a.sendToolJSON(req.ID, r)

	case "waggle_delete_row":
		var args struct{ RowID string `json:"row_id"` }
		json.Unmarshal(call.Arguments, &args)
		if err := a.store.DeleteRow(args.RowID); err != nil {
			a.sendToolError(req.ID, err.Error())
			return
		}
		a.sendToolJSON(req.ID, map[string]any{"deleted": args.RowID})
```

> `sendToolJSON`/`sendToolError` are placeholders -- replace with the exact result/error helpers the existing page tools use so the MCP envelope matches. Add `"encoding/json"` and the `model` import if not already present.

- [ ] **Step 5: Run test to verify it passes + full mcp suite**

Run: `go test ./internal/mcp/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/mcp/mcp.go internal/mcp/mcp_test.go
git commit --no-gpg-sign -m "feat(mcp): 7 udb tools (databases, columns, rows)"
```

---

### Task 11: Dashboard -- Databases view scaffold, DB picker, columns, rows

**Files:**
- Modify: `internal/dashboard/static/mission-control.html`
- Test: manual (no JS harness in this project)

**Interfaces:**
- Consumes: existing `switchView`/`currentView`, the view-button row, `connectSSE`/`es.onmessage`, `esc(s)`, the project selector, and the REST endpoints from Tasks 6-9.
- Produces: a `databases` view with DB list, column editor, and a rows table; a module-level `udbState`; SSE handling for `database_*` events. Table/kanban/calendar rendering is Task 12 (this task ships the table body as the default render).

- [ ] **Step 1: Add the view button and container.** Next to the existing view buttons, add:

```html
<button class="view-btn" onclick="switchView('databases')">Databases</button>
```
And a container alongside the other view sections:
```html
<div id="view-databases" class="view" style="display:none">
  <div id="udb-sidebar"></div>
  <div id="udb-main"></div>
</div>
```

- [ ] **Step 2: Add state + switchView hook.** In the script, near the other view globals:

```js
let udbState = { databases: [], currentDBId: null, columns: [], rows: [], views: [], currentViewId: null, editingRowId: null };
```
In `switchView(view)`, add a branch: when `view === 'databases'`, call `loadDatabases()`.

- [ ] **Step 3: Implement data loads + DB picker.**

```js
async function loadDatabases() {
  const pid = currentProjectId || '';
  const res = await fetch('/api/udb?project_id=' + encodeURIComponent(pid));
  udbState.databases = await res.json();
  renderUDBSidebar();
  if (udbState.databases.length && !udbState.currentDBId) selectDatabase(udbState.databases[0].id);
}

function renderUDBSidebar() {
  const el = document.getElementById('udb-sidebar');
  el.innerHTML =
    udbState.databases.map(d =>
      `<div class="udb-db-item ${d.id === udbState.currentDBId ? 'active' : ''}" onclick="selectDatabase('${esc(d.id)}')">${esc(d.name)}</div>`
    ).join('') +
    `<button onclick="createDatabase()">+ New database</button>`;
}

async function createDatabase() {
  const name = prompt('Database name');
  if (!name) return;
  const res = await fetch('/api/udb', {method:'POST', headers:{'Content-Type':'application/json'},
    body: JSON.stringify({name, project_id: currentProjectId || ''})});
  const db = await res.json();
  await loadDatabases();
  selectDatabase(db.id);
}

async function selectDatabase(id) {
  udbState.currentDBId = id;
  const [cols, rows, views] = await Promise.all([
    fetch(`/api/udb/${id}/columns`).then(r=>r.json()),
    fetch(`/api/udb/${id}/rows`).then(r=>r.json()),
    fetch(`/api/udb/${id}/views`).then(r=>r.json()),
  ]);
  udbState.columns = cols; udbState.rows = rows; udbState.views = views;
  udbState.currentViewId = views[0] ? views[0].id : null;
  renderUDBSidebar();
  renderUDBMain();
}
```

- [ ] **Step 4: Implement column editor + row table (default render).**

```js
function renderUDBMain() {
  const el = document.getElementById('udb-main');
  if (!udbState.currentDBId) { el.innerHTML = '<p>Select or create a database.</p>'; return; }
  el.innerHTML =
    renderColumnBar() +
    renderViewTabs() +   // Task 12 fills this in; stub returns '' for now
    `<div id="udb-view-body">${renderTableView()}</div>`;
}

function renderColumnBar() {
  return `<div class="udb-columns">` +
    udbState.columns.map(c => `<span class="udb-col">${esc(c.name)} <em>${esc(c.type)}</em>
       <button onclick="deleteColumn('${esc(c.id)}')">x</button></span>`).join('') +
    `<button onclick="addColumn()">+ column</button></div>`;
}

function renderViewTabs() { return ''; } // replaced in Task 12

function renderTableView() {
  const cols = udbState.columns;
  const head = cols.map(c => `<th>${esc(c.name)}</th>`).join('') + '<th></th>';
  const body = udbState.rows.map(r => {
    const cells = cols.map(c => `<td>${esc(formatCell(r.data[c.id], c))}</td>`).join('');
    return `<tr>${cells}<td><button onclick="editRow('${esc(r.id)}')">edit</button>
      <button onclick="deleteRow('${esc(r.id)}')">x</button></td></tr>`;
  }).join('');
  return `<table class="udb-table"><thead><tr>${head}</tr></thead><tbody>${body}</tbody></table>
    <button onclick="addRow()">+ row</button>`;
}

function formatCell(v, col) {
  if (v === undefined || v === null) return '';
  if (col.type === 'checkbox') return v ? '✓' : '';
  return String(v);
}

async function addColumn() {
  const name = prompt('Column name'); if (!name) return;
  const type = prompt('Type: text, number, date, select, checkbox', 'text'); if (!type) return;
  let options;
  if (type === 'select') {
    const raw = prompt('Options (comma-separated)', 'Todo,Doing,Done') || '';
    options = raw.split(',').map(s => ({value: s.trim()})).filter(o => o.value);
  }
  const res = await fetch(`/api/udb/${udbState.currentDBId}/columns`, {method:'POST',
    headers:{'Content-Type':'application/json'}, body: JSON.stringify({name, type, options})});
  if (!res.ok) { alert('Add column failed'); return; }
  selectDatabase(udbState.currentDBId);
}

async function deleteColumn(id) {
  if (!confirm('Delete column?')) return;
  await fetch(`/api/udb/${udbState.currentDBId}/columns/${id}`, {method:'DELETE'});
  selectDatabase(udbState.currentDBId);
}

function buildRowInputs(existing) {
  return udbState.columns.map(c => {
    const v = existing ? existing.data[c.id] : undefined;
    if (c.type === 'checkbox')
      return `<label>${esc(c.name)} <input type="checkbox" data-col="${esc(c.id)}" ${v ? 'checked':''}></label>`;
    if (c.type === 'select')
      return `<label>${esc(c.name)} <select data-col="${esc(c.id)}">
        <option value=""></option>` +
        (c.options||[]).map(o=>`<option ${v===o.value?'selected':''}>${esc(o.value)}</option>`).join('') +
        `</select></label>`;
    const t = c.type === 'number' ? 'number' : (c.type === 'date' ? 'date' : 'text');
    return `<label>${esc(c.name)} <input type="${t}" data-col="${esc(c.id)}" value="${esc(v==null?'':String(v))}"></label>`;
  }).join('');
}

function collectRowData() {
  const data = {};
  document.querySelectorAll('#udb-row-form [data-col]').forEach(el => {
    const c = udbState.columns.find(x => x.id === el.dataset.col);
    if (c.type === 'checkbox') data[c.id] = el.checked;
    else if (el.value === '') { /* leave unset */ }
    else if (c.type === 'number') data[c.id] = Number(el.value);
    else data[c.id] = el.value;
  });
  return data;
}

function addRow() { openRowForm(null); }
function editRow(id) { udbState.editingRowId = id; openRowForm(udbState.rows.find(r => r.id === id)); }

function openRowForm(existing) {
  const body = document.getElementById('udb-view-body');
  body.innerHTML = `<div id="udb-row-form">${buildRowInputs(existing)}
    <button onclick="saveRow()">Save</button>
    <button onclick="renderUDBMain()">Cancel</button></div>`;
}

async function saveRow() {
  const data = collectRowData();
  const dbId = udbState.currentDBId;
  let res;
  if (udbState.editingRowId) {
    res = await fetch(`/api/udb/${dbId}/rows/${udbState.editingRowId}`, {method:'PATCH',
      headers:{'Content-Type':'application/json'}, body: JSON.stringify({data})});
  } else {
    res = await fetch(`/api/udb/${dbId}/rows`, {method:'POST',
      headers:{'Content-Type':'application/json'}, body: JSON.stringify({data})});
  }
  udbState.editingRowId = null;
  if (!res.ok) { alert('Save failed (check cell types)'); return; }
  selectDatabase(dbId);
}

async function deleteRow(id) {
  if (!confirm('Delete row?')) return;
  await fetch(`/api/udb/${udbState.currentDBId}/rows/${id}`, {method:'DELETE'});
  selectDatabase(udbState.currentDBId);
}
```

- [ ] **Step 5: SSE handling with dirty-guard.** In the existing `es.onmessage` handler, add: when the event type starts with `database_` and the databases view is active, refresh -- but NOT while a row form is open (dirty-guard):

```js
if (currentView === 'databases' && ev.type && ev.type.indexOf('database_') === 0) {
  const formOpen = !!document.getElementById('udb-row-form');
  if (!formOpen) {
    if (ev.type === 'database_created' || ev.type === 'database_deleted') loadDatabases();
    else if (udbState.currentDBId) selectDatabase(udbState.currentDBId);
  }
}
```

> Match the real event object shape used in the existing `es.onmessage` (it may be `data.type` after `JSON.parse`). Follow the surrounding code.

- [ ] **Step 6: Manual verification.**

Build + run locally, open the dashboard, switch to Databases:
```bash
go build -o /tmp/waggle-dev ./cmd/waggle && echo built
```
Verify: create a database; add columns of each type; add a row with typed inputs; a wrong-typed value is rejected (alert); edit and delete rows; delete a column and confirm existing rows still render (stale cell just disappears).

- [ ] **Step 7: Commit**

```bash
git add internal/dashboard/static/mission-control.html
git commit --no-gpg-sign -m "feat(dashboard): databases view with DB picker, columns, row table"
```

---

### Task 12: Dashboard -- table/kanban/calendar view tabs

**Files:**
- Modify: `internal/dashboard/static/mission-control.html`
- Test: manual

**Interfaces:**
- Consumes: `udbState`, the C1 calendar/table rendering already in the file, the `/views` endpoints.
- Produces: `renderViewTabs()` (real), per-kind rendering (`renderTableView` reused, `renderKanbanView`, `renderCalendarView`), view create/delete, and client-side filter/sort/group applied to `udbState.rows`.

- [ ] **Step 1: Replace `renderViewTabs()`** to list views + a current-view selector + "+ view":

```js
function currentView_() { return udbState.views.find(v => v.id === udbState.currentViewId); }

function renderViewTabs() {
  const tabs = udbState.views.map(v =>
    `<button class="udb-view-tab ${v.id===udbState.currentViewId?'active':''}"
       onclick="selectView('${esc(v.id)}')">${esc(v.name)} <small>${esc(v.kind)}</small>
       <span onclick="event.stopPropagation();deleteView('${esc(v.id)}')">x</span></button>`).join('');
  return `<div class="udb-view-tabs">${tabs}
    <button onclick="createView()">+ view</button></div>`;
}

function selectView(id) { udbState.currentViewId = id; renderViewBody(); }

function renderViewBody() {
  const body = document.getElementById('udb-view-body');
  const v = currentView_();
  if (!v || v.kind === 'table') { body.innerHTML = renderTableView(); return; }
  if (v.kind === 'kanban') { body.innerHTML = renderKanbanView(v); return; }
  if (v.kind === 'calendar') { body.innerHTML = renderCalendarView(v); return; }
}
```
Update `renderUDBMain()` so its `#udb-view-body` initial content calls `renderViewBody()` instead of `renderTableView()` directly (guard when there are no views -> default table).

- [ ] **Step 2: Client-side filter/sort** helper over `udbState.rows`:

```js
function applyViewConfig(rows, cfg) {
  let out = rows.slice();
  (cfg.filters||[]).forEach(f => {
    out = out.filter(r => {
      const v = r.data[f.col];
      switch (f.op) {
        case 'eq': return v === f.value;
        case 'neq': return v !== f.value;
        case 'contains': return String(v??'').toLowerCase().includes(String(f.value??'').toLowerCase());
        case 'gt': return Number(v) > Number(f.value);
        case 'lt': return Number(v) < Number(f.value);
        default: return true;
      }
    });
  });
  (cfg.sort||[]).slice().reverse().forEach(s => {
    out.sort((a,b) => {
      const av=a.data[s.col], bv=b.data[s.col];
      if (av===bv) return 0;
      const r = (av>bv?1:-1);
      return s.dir==='desc' ? -r : r;
    });
  });
  return out;
}
```
Make `renderTableView()` render `applyViewConfig(udbState.rows, (currentView_()||{config:{}}).config)` instead of raw `udbState.rows`.

- [ ] **Step 3: Kanban render** (lanes from the group-by select column's options):

```js
function renderKanbanView(v) {
  const groupCol = udbState.columns.find(c => c.id === v.config.group_by);
  if (!groupCol) return '<p>Invalid kanban config.</p>';
  const rows = applyViewConfig(udbState.rows, v.config);
  const lanes = (groupCol.options||[]).map(o => o.value).concat(['(empty)']);
  return `<div class="udb-kanban">` + lanes.map(lane => {
    const laneRows = rows.filter(r => (r.data[groupCol.id] || '(empty)') === lane);
    const cards = laneRows.map(r => {
      const title = udbState.columns[0] ? esc(String(r.data[udbState.columns[0].id] ?? '')) : esc(r.id);
      return `<div class="udb-card" onclick="editRow('${esc(r.id)}')">${title}</div>`;
    }).join('');
    return `<div class="udb-lane"><h4>${esc(lane)}</h4>${cards}</div>`;
  }).join('') + `</div>`;
}
```

- [ ] **Step 4: Calendar render** -- reuse C1's calendar structure keyed on `v.config.date_field`. Read the existing C1 calendar functions in the file and build a udb variant that reads each row's date from `r.data[v.config.date_field]` and labels the cell with the first column's value. Keep month-nav + unscheduled strip parity.

```js
function renderCalendarView(v) {
  const dateCol = v.config.date_field;
  if (!dateCol) return '<p>Invalid calendar config.</p>';
  const rows = applyViewConfig(udbState.rows, v.config);
  const items = rows.map(r => ({
    id: r.id,
    date: r.data[dateCol] || '',
    label: udbState.columns[0] ? String(r.data[udbState.columns[0].id] ?? '') : r.id,
  }));
  // Reuse the existing C1 month-grid renderer if it accepts {date,label} items;
  // otherwise mirror its structure here. onclick -> editRow(item.id).
  return renderMonthGrid(items, (id) => `editRow('${id}')`);
}
```
> If the C1 calendar isn't factored as a reusable `renderMonthGrid(items, onClick)`, refactor the C1 calendar minimally to expose that helper and have both C1 and udb call it (DRY). Keep C1's behavior identical -- verify the task board calendar still works after the refactor.

- [ ] **Step 5: View create/delete.**

```js
async function createView() {
  const name = prompt('View name'); if (!name) return;
  const kind = prompt('Kind: table, kanban, calendar', 'table'); if (!kind) return;
  const config = {};
  if (kind === 'kanban') {
    const sel = udbState.columns.find(c => c.type === 'select');
    if (!sel) { alert('Add a select column first for kanban.'); return; }
    config.group_by = sel.id;
  } else if (kind === 'calendar') {
    const dt = udbState.columns.find(c => c.type === 'date');
    if (!dt) { alert('Add a date column first for calendar.'); return; }
    config.date_field = dt.id;
  }
  const res = await fetch(`/api/udb/${udbState.currentDBId}/views`, {method:'POST',
    headers:{'Content-Type':'application/json'}, body: JSON.stringify({name, kind, config})});
  if (!res.ok) { alert('Create view failed'); return; }
  selectDatabase(udbState.currentDBId);
}

async function deleteView(id) {
  if (!confirm('Delete view?')) return;
  await fetch(`/api/udb/${udbState.currentDBId}/views/${id}`, {method:'DELETE'});
  selectDatabase(udbState.currentDBId);
}
```

- [ ] **Step 6: Manual verification.**

Build + run; for a database with a select column and a date column: create a table view (sort/filter), a kanban view (cards land in the right lanes, `(empty)` catches unset), and a calendar view (rows appear on their date, month-nav works). Re-verify the C1 task-board calendar still renders correctly (regression check for the Step-4 refactor).

- [ ] **Step 7: Commit**

```bash
git add internal/dashboard/static/mission-control.html
git commit --no-gpg-sign -m "feat(dashboard): udb table/kanban/calendar views with client-side filter/sort"
```

---

## Final Steps (after all tasks)

1. Full suite: `go test ./...` -- all green.
2. `go vet ./...` -- clean.
3. Whole-branch review (most capable model) over the full diff vs `origin/master`; fix findings.
4. Merge `c2-generic-typed-databases` to master (fast-forward), push.
5. Redeploy: back up DB, rebuild from master, reinstall `/Users/maniginam/go/bin/waggle`, restart the launchd daemon, verify `GET /api/udb` returns 200 and the Databases view loads. (Additive migration -- new tables only.)
6. Update memory: mark C2 done in `workspace-pivot.md`; the workspace pivot (A + telegram + B + C1 + C2) is then complete.

## Self-Review Notes

- **Spec coverage:** databases/columns/rows/views CRUD (Tasks 2-9), typed cells + validation (Task 4/8), view config validation kanban->select / calendar->date (Task 5/9), 7 MCP tools (Task 10), dashboard 3 views + inline editing + escape-first + dirty-guard (Tasks 11-12), additive migration + redeploy (Final). JSON-per-row storage as chosen.
- **Type consistency:** store methods, their signatures in the Interfaces blocks, the API handlers that call them, and the MCP cases all use the same names (`CreateDatabase`, `AddColumn`, `CreateRow`, `UpdateRow`, `CreateView`, etc.). Row write bodies use `{"data": {...}}` consistently across API (Task 8) and MCP (Task 10). Event `database_changed` carries `{db_id}` consistently.
- **Out of scope confirmed absent:** no formulas, relations, rollups, server-side pagination, or MCP view tools.
- **Known adaptation points flagged for implementers:** test-helper constructor names (`newTestStore`/`newTestAPI`/MCP test harness), the exact MCP result/error helpers, and the C1 calendar `renderMonthGrid` refactor -- each called out inline where it occurs.
