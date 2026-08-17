# Waggle Pages / Docs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a nested markdown pages/docs system (project + workspace scope) that is a shared human+AI knowledge store — dashboard editor with live preview, FTS5 search, and MCP read/write tools.

**Architecture:** Additive. New `pages` table + an FTS5 virtual table kept in sync from the Go store write methods (no triggers). Layers: model → store (CRUD + FTS + reparent-on-delete + search) → REST API (emitting new SSE events) → single-file dashboard Docs view → 4 MCP tools. No new Go dependencies (FTS5 ships in `modernc.org/sqlite`; the markdown preview renderer is inline JS).

**Tech Stack:** Go, `modernc.org/sqlite` (FTS5), `net/http`, the existing `internal/event` hub, inline JS in `mission-control.html`.

## Global Constraints

- Additive migrations only: new tables via `CREATE TABLE IF NOT EXISTS` / `CREATE VIRTUAL TABLE IF NOT EXISTS`; no destructive changes.
- FTS index sync is manual in the store write methods (delete-then-insert the row's FTS entry by `id` on create/update; delete FTS entry on delete). No SQLite triggers.
- FTS5 is probed at migrate time; on create failure set `Store.ftsEnabled=false` and `SearchPages` falls back to `LIKE`. FTS5 is confirmed available in this project's `modernc.org/sqlite`, so the fallback is defensive.
- `DeletePage` re-parents direct children up to the deleted page's `parent_id` before deleting the row (no subtree loss).
- **Security:** page content is markdown, NOT HTML. The dashboard preview renderer MUST escape HTML in page content before applying markdown formatting, so stored `<script>` cannot execute (no stored XSS).
- TDD mandatory: failing test first, minimal code, refactor while green; commit after each green step.
- Commits MUST use `git commit --no-gpg-sign`. No Co-Authored-By / attribution footers.
- Timestamps RFC3339 UTC. `project_id == ""` means workspace-level; `parent_id == ""` means root of its scope.
- Store test helper: `tempStore(t)` (in `internal/store/store_test.go`). API helper: `setup(t) (*API, *httptest.Server)`. MCP helpers: `setupMCP(t)` + `callMCP(t, adapter, method, id, params)`.
- Follow existing patterns: `id.New()`, the `scanner` interface, `ErrNotFound`, `writeError`/`writeJSON`, sub-path split in handlers, `a.emit(...)` for SSE, `toolDef`/`prop` in mcp.

## File structure

- `internal/model/model.go` — add `Page` struct + `EventPageCreated/Updated/Deleted` event consts (modify).
- `internal/store/store.go` — pages migration + FTS probe; a `// --- Pages ---` section (modify; add `ftsEnabled bool` to `Store`).
- `internal/api/api.go` — pages endpoints + routing + SSE emits (modify).
- `internal/mcp/mcp.go` — 4 page tools (modify).
- `internal/dashboard/static/mission-control.html` — Docs view (modify).

---

### Task 1: Model — Page struct and page event types

**Files:**
- Modify: `internal/model/model.go`
- Test: `internal/model/model_test.go`

**Interfaces:**
- Produces: `model.Page` struct; event consts `EventPageCreated EventType = "page_created"`, `EventPageUpdated = "page_updated"`, `EventPageDeleted = "page_deleted"`.

- [ ] **Step 1: Write the failing test**

Append to `internal/model/model_test.go`:

```go
func TestPageEventConsts(t *testing.T) {
	if EventPageCreated != "page_created" || EventPageUpdated != "page_updated" || EventPageDeleted != "page_deleted" {
		t.Errorf("unexpected page event consts: %q %q %q", EventPageCreated, EventPageUpdated, EventPageDeleted)
	}
}

func TestPageZeroValue(t *testing.T) {
	var p Page
	if p.ID != "" || p.Title != "" {
		t.Error("zero Page should be empty")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/model/ -run 'TestPageEventConsts|TestPageZeroValue' -v`
Expected: FAIL (undefined Page / EventPageCreated).

- [ ] **Step 3: Write minimal implementation**

Add the event consts to the existing `EventType` const block:

```go
	EventPageCreated EventType = "page_created"
	EventPageUpdated EventType = "page_updated"
	EventPageDeleted EventType = "page_deleted"
```

Add the struct (near the other content types):

```go
type Page struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id,omitempty"`
	ParentID  string    `json:"parent_id,omitempty"`
	Title     string    `json:"title"`
	Icon      string    `json:"icon,omitempty"`
	Content   string    `json:"content,omitempty"`
	Position  float64   `json:"position"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/model/ -run 'TestPageEventConsts|TestPageZeroValue' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/model/model.go internal/model/model_test.go
git commit --no-gpg-sign -m "feat(model): add Page type and page event consts"
```

---

### Task 2: Store — pages migration + FTS5 probe

**Files:**
- Modify: `internal/store/store.go` (add `ftsEnabled bool` to `Store`; migrate the table + FTS virtual table)
- Test: `internal/store/store_test.go`

**Interfaces:**
- Produces: `pages` table + `pages_fts` virtual table exist after `New`; `Store.ftsEnabled` reflects whether FTS5 create succeeded.

- [ ] **Step 1: Write the failing test**

```go
func TestPagesMigrationAndFTS(t *testing.T) {
	s := tempStore(t)
	// pages table exists (insert a raw row succeeds)
	_, err := s.Exec(`INSERT INTO pages (id, title, created_at, updated_at) VALUES ('p1','T','2026-08-16T00:00:00Z','2026-08-16T00:00:00Z')`)
	if err != nil {
		t.Fatalf("pages table missing: %v", err)
	}
	if !s.FTSEnabled() {
		t.Error("expected FTS5 enabled in modernc sqlite")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestPagesMigrationAndFTS -v`
Expected: FAIL (no pages table / FTSEnabled undefined).

- [ ] **Step 3: Write minimal implementation**

Add the field to the `Store` struct:

```go
type Store struct {
	db         *sql.DB
	ftsEnabled bool
}
```

In `migrate()`, after the other `CREATE TABLE` blocks, add:

```go
	s.db.Exec(`CREATE TABLE IF NOT EXISTS pages (
		id         TEXT PRIMARY KEY,
		project_id TEXT DEFAULT '',
		parent_id  TEXT DEFAULT '',
		title      TEXT NOT NULL,
		icon       TEXT DEFAULT '',
		content    TEXT DEFAULT '',
		position   REAL DEFAULT 0,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`)
	s.db.Exec("CREATE INDEX IF NOT EXISTS idx_pages_project ON pages(project_id)")
	s.db.Exec("CREATE INDEX IF NOT EXISTS idx_pages_parent ON pages(parent_id)")
	if _, err := s.db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS pages_fts USING fts5(id UNINDEXED, title, content)`); err == nil {
		s.ftsEnabled = true
	}
```

Add the accessor (near `Close`/`Exec`):

```go
func (s *Store) FTSEnabled() bool { return s.ftsEnabled }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestPagesMigrationAndFTS -v` then `go test ./internal/store/`
Expected: PASS, no regressions.

- [ ] **Step 5: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit --no-gpg-sign -m "feat(store): pages table and FTS5 virtual table with probe"
```

---

### Task 3: Store — Page CRUD with FTS sync

**Files:**
- Modify: `internal/store/store.go`
- Test: `internal/store/store_test.go`

**Interfaces:**
- Produces:
  - `CreatePage(p *model.Page) error` — sets ID/timestamps; defaults position 0; inserts row and (if ftsEnabled) FTS entry.
  - `GetPage(id string) (*model.Page, error)` — `ErrNotFound` if missing.
  - `ListPages(projectID string) ([]*model.Page, error)` — pages where `project_id = projectID`, ordered `position ASC, created_at ASC`.
  - `UpdatePage(id string, updates map[string]any) (*model.Page, error)` — whitelist keys `title, icon, content, position, parent_id`; bumps updated_at; re-syncs FTS.
  - Internal helper `syncPageFTS(id, title, content string)` — delete-then-insert the FTS row (no-op if !ftsEnabled).

- [ ] **Step 1: Write the failing test**

```go
func TestPageCRUD(t *testing.T) {
	s := tempStore(t)
	p := &model.Page{ProjectID: "proj1", Title: "Notes", Content: "hello world"}
	if err := s.CreatePage(p); err != nil {
		t.Fatal(err)
	}
	if p.ID == "" {
		t.Fatal("expected id set")
	}
	got, err := s.GetPage(p.ID)
	if err != nil || got.Title != "Notes" || got.Content != "hello world" {
		t.Fatalf("get failed: %v %+v", err, got)
	}
	// workspace-level page not returned for a project scope
	ws := &model.Page{Title: "Global"}
	s.CreatePage(ws)
	inProj, _ := s.ListPages("proj1")
	if len(inProj) != 1 || inProj[0].ID != p.ID {
		t.Errorf("scope filter wrong: %d", len(inProj))
	}
	inWs, _ := s.ListPages("")
	if len(inWs) != 1 || inWs[0].ID != ws.ID {
		t.Errorf("workspace scope wrong: %d", len(inWs))
	}
	if _, err := s.UpdatePage(p.ID, map[string]any{"content": "updated body"}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetPage(p.ID)
	if got.Content != "updated body" {
		t.Errorf("update failed: %q", got.Content)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestPageCRUD -v`
Expected: FAIL (undefined CreatePage).

- [ ] **Step 3: Write minimal implementation**

Add a `// --- Pages ---` section:

```go
func (s *Store) syncPageFTS(id, title, content string) {
	if !s.ftsEnabled {
		return
	}
	s.db.Exec("DELETE FROM pages_fts WHERE id = ?", id)
	s.db.Exec("INSERT INTO pages_fts (id, title, content) VALUES (?, ?, ?)", id, title, content)
}

func scanPage(row scanner) (*model.Page, error) {
	var p model.Page
	var createdStr, updatedStr string
	err := row.Scan(&p.ID, &p.ProjectID, &p.ParentID, &p.Title, &p.Icon, &p.Content, &p.Position, &createdStr, &updatedStr)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
	p.UpdatedAt, _ = time.Parse(time.RFC3339, updatedStr)
	return &p, nil
}

const pageCols = "id, project_id, parent_id, title, icon, content, position, created_at, updated_at"

func (s *Store) CreatePage(p *model.Page) error {
	if p.ID == "" {
		p.ID = id.New()
	}
	now := time.Now().UTC()
	p.CreatedAt = now
	p.UpdatedAt = now
	_, err := s.db.Exec("INSERT INTO pages ("+pageCols+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		p.ID, p.ProjectID, p.ParentID, p.Title, p.Icon, p.Content, p.Position,
		p.CreatedAt.Format(time.RFC3339), p.UpdatedAt.Format(time.RFC3339))
	if err != nil {
		return err
	}
	s.syncPageFTS(p.ID, p.Title, p.Content)
	return nil
}

func (s *Store) GetPage(pageID string) (*model.Page, error) {
	row := s.db.QueryRow("SELECT "+pageCols+" FROM pages WHERE id = ?", pageID)
	return scanPage(row)
}

func (s *Store) ListPages(projectID string) ([]*model.Page, error) {
	rows, err := s.db.Query("SELECT "+pageCols+" FROM pages WHERE project_id = ? ORDER BY position ASC, created_at ASC", projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var pages []*model.Page
	for rows.Next() {
		p, err := scanPage(rows)
		if err != nil {
			return nil, err
		}
		pages = append(pages, p)
	}
	return pages, rows.Err()
}

func (s *Store) UpdatePage(pageID string, updates map[string]any) (*model.Page, error) {
	if _, err := s.GetPage(pageID); err != nil {
		return nil, err
	}
	var sets []string
	var args []any
	for k, v := range updates {
		switch k {
		case "title", "icon", "content", "position", "parent_id":
			sets = append(sets, k+" = ?")
			args = append(args, v)
		}
	}
	if len(sets) == 0 {
		return s.GetPage(pageID)
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, time.Now().UTC().Format(time.RFC3339))
	args = append(args, pageID)
	if _, err := s.db.Exec("UPDATE pages SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...); err != nil {
		return nil, err
	}
	updated, err := s.GetPage(pageID)
	if err != nil {
		return nil, err
	}
	s.syncPageFTS(updated.ID, updated.Title, updated.Content)
	return updated, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestPageCRUD -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit --no-gpg-sign -m "feat(store): page CRUD with FTS sync"
```

---

### Task 4: Store — DeletePage (reparent children) + MovePage

**Files:**
- Modify: `internal/store/store.go`
- Test: `internal/store/store_test.go`

**Interfaces:**
- Produces:
  - `DeletePage(id string) error` — re-parents direct children to the deleted page's `parent_id`, then deletes the row + FTS entry. `ErrNotFound` if missing.
  - `MovePage(id, parentID string, position float64) error` — sets parent_id + position + updated_at.

- [ ] **Step 1: Write the failing test**

```go
func TestDeletePageReparentsChildren(t *testing.T) {
	s := tempStore(t)
	root := &model.Page{Title: "root"}
	s.CreatePage(root)
	mid := &model.Page{Title: "mid", ParentID: root.ID}
	s.CreatePage(mid)
	leaf := &model.Page{Title: "leaf", ParentID: mid.ID}
	s.CreatePage(leaf)

	if err := s.DeletePage(mid.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetPage(mid.ID); err != ErrNotFound {
		t.Errorf("mid should be gone: %v", err)
	}
	got, _ := s.GetPage(leaf.ID)
	if got.ParentID != root.ID {
		t.Errorf("leaf should reparent to root, got %q", got.ParentID)
	}
}

func TestMovePage(t *testing.T) {
	s := tempStore(t)
	a := &model.Page{Title: "a"}
	b := &model.Page{Title: "b"}
	s.CreatePage(a)
	s.CreatePage(b)
	if err := s.MovePage(b.ID, a.ID, 2.5); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetPage(b.ID)
	if got.ParentID != a.ID || got.Position != 2.5 {
		t.Errorf("move failed: parent=%q pos=%v", got.ParentID, got.Position)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run 'TestDeletePageReparentsChildren|TestMovePage' -v`
Expected: FAIL (undefined DeletePage).

- [ ] **Step 3: Write minimal implementation**

```go
func (s *Store) DeletePage(pageID string) error {
	page, err := s.GetPage(pageID)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	// Re-parent direct children up to this page's parent.
	s.db.Exec("UPDATE pages SET parent_id = ?, updated_at = ? WHERE parent_id = ?", page.ParentID, now, pageID)
	if _, err := s.db.Exec("DELETE FROM pages WHERE id = ?", pageID); err != nil {
		return err
	}
	if s.ftsEnabled {
		s.db.Exec("DELETE FROM pages_fts WHERE id = ?", pageID)
	}
	return nil
}

func (s *Store) MovePage(pageID, parentID string, position float64) error {
	if _, err := s.GetPage(pageID); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec("UPDATE pages SET parent_id = ?, position = ?, updated_at = ? WHERE id = ?",
		parentID, position, now, pageID)
	return err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run 'TestDeletePageReparentsChildren|TestMovePage' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit --no-gpg-sign -m "feat(store): delete page (reparent children) and move page"
```

---

### Task 5: Store — SearchPages (FTS5 ranked, LIKE fallback)

**Files:**
- Modify: `internal/store/store.go`
- Test: `internal/store/store_test.go`

**Interfaces:**
- Produces:
  - `SearchPages(query, projectID string) ([]*model.Page, error)` — when `ftsEnabled`: FTS5 `MATCH` ranked; else `LIKE` on title/content. `projectID == ""` searches all scopes; else restricts to that project. Query is FTS-quoted so special characters don't break the MATCH.
  - Internal helper `ftsQuote(q string) string` — wraps the query as a quoted FTS string with internal `"` doubled.

- [ ] **Step 1: Write the failing test**

```go
func TestSearchPagesFindsContent(t *testing.T) {
	s := tempStore(t)
	a := &model.Page{Title: "Auth design", Content: "the login flow uses tokens"}
	b := &model.Page{Title: "Grocery", Content: "milk and eggs"}
	s.CreatePage(a)
	s.CreatePage(b)

	res, err := s.SearchPages("tokens", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].ID != a.ID {
		t.Fatalf("expected the auth page, got %d results", len(res))
	}
	// after update, search reflects new content and not the old
	s.UpdatePage(a.ID, map[string]any{"content": "rewritten to use sessions"})
	res, _ = s.SearchPages("tokens", "")
	if len(res) != 0 {
		t.Errorf("stale FTS: still matched old content")
	}
	res, _ = s.SearchPages("sessions", "")
	if len(res) != 1 {
		t.Errorf("FTS not updated: %d", len(res))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestSearchPagesFindsContent -v`
Expected: FAIL (undefined SearchPages).

- [ ] **Step 3: Write minimal implementation**

```go
func ftsQuote(q string) string {
	return `"` + strings.ReplaceAll(q, `"`, `""`) + `"`
}

func (s *Store) SearchPages(query, projectID string) ([]*model.Page, error) {
	var rows *sql.Rows
	var err error
	if s.ftsEnabled {
		q := "SELECT p." + strings.ReplaceAll(pageCols, ", ", ", p.") +
			" FROM pages_fts f JOIN pages p ON p.id = f.id WHERE pages_fts MATCH ?"
		args := []any{ftsQuote(query)}
		if projectID != "" {
			q += " AND p.project_id = ?"
			args = append(args, projectID)
		}
		q += " ORDER BY rank"
		rows, err = s.db.Query(q, args...)
	} else {
		q := "SELECT " + pageCols + " FROM pages WHERE (title LIKE ? OR content LIKE ?)"
		like := "%" + query + "%"
		args := []any{like, like}
		if projectID != "" {
			q += " AND project_id = ?"
			args = append(args, projectID)
		}
		q += " ORDER BY updated_at DESC"
		rows, err = s.db.Query(q, args...)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var pages []*model.Page
	for rows.Next() {
		p, err := scanPage(rows)
		if err != nil {
			return nil, err
		}
		pages = append(pages, p)
	}
	return pages, rows.Err()
}
```

Note: `strings.ReplaceAll(pageCols, ", ", ", p.")` prefixes every column after the first with `p.`; the first column (`id`) is prefixed by the literal `p.` in the SELECT string. Verify the produced SQL selects `p.id, p.project_id, ...` — if the produced string is wrong for any column, write the `p.`-qualified column list out explicitly instead of transforming `pageCols`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestSearchPagesFindsContent -v` then `go test ./internal/store/`
Expected: PASS, no regressions.

- [ ] **Step 5: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit --no-gpg-sign -m "feat(store): FTS5 page search with LIKE fallback"
```

---

### Task 6: API — pages CRUD endpoints

**Files:**
- Modify: `internal/api/api.go` (register routes in `Handler()`; add handlers)
- Test: `internal/api/api_test.go`

**Interfaces:**
- Consumes: store `CreatePage`, `ListPages`, `GetPage`, `UpdatePage`, `DeletePage`.
- Produces:
  - `GET /api/pages?project_id=` → list (scope). `POST /api/pages` (body `model.Page`, requires `title`) → 201, emits `EventPageCreated`.
  - `GET /api/pages/{id}` → page. `PATCH /api/pages/{id}` (updates map) → 200, emits `EventPageUpdated`. `DELETE /api/pages/{id}` → 204, emits `EventPageDeleted`.
  - `store.ErrNotFound` → 404.

- [ ] **Step 1: Write the failing test**

```go
func TestPageEndpointsCRUD(t *testing.T) {
	_, ts := setup(t)
	resp, err := http.Post(ts.URL+"/api/pages", "application/json",
		strings.NewReader(`{"project_id":"proj1","title":"Spec","content":"# Hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create expected 201, got %d", resp.StatusCode)
	}
	var created model.Page
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	if created.ID == "" {
		t.Fatal("no page id")
	}
	// list scope
	lr := mustGet(t, ts.URL+"/api/pages?project_id=proj1")
	var list []model.Page
	json.NewDecoder(lr.Body).Decode(&list)
	lr.Body.Close()
	if len(list) != 1 {
		t.Errorf("expected 1 page, got %d", len(list))
	}
	// patch
	preq, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/pages/"+created.ID,
		strings.NewReader(`{"content":"# Updated"}`))
	pr, _ := http.DefaultClient.Do(preq)
	if pr.StatusCode != http.StatusOK {
		t.Errorf("patch expected 200, got %d", pr.StatusCode)
	}
	pr.Body.Close()
	// missing title → 400
	br, _ := http.Post(ts.URL+"/api/pages", "application/json", strings.NewReader(`{"content":"x"}`))
	if br.StatusCode != http.StatusBadRequest {
		t.Errorf("missing title expected 400, got %d", br.StatusCode)
	}
	br.Body.Close()
	// delete
	dreq, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/pages/"+created.ID, nil)
	dr, _ := http.DefaultClient.Do(dreq)
	if dr.StatusCode != http.StatusNoContent {
		t.Errorf("delete expected 204, got %d", dr.StatusCode)
	}
	dr.Body.Close()
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/ -run TestPageEndpointsCRUD -v`
Expected: FAIL (404s — routes not registered).

- [ ] **Step 3: Write minimal implementation**

Register in `Handler()`:

```go
	mux.HandleFunc("/api/pages", a.handlePages)
	mux.HandleFunc("/api/pages/", a.handlePage)
```

Add handlers (search + move sub-actions are added in Task 7):

```go
func (a *API) handlePages(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		pages, err := a.store.ListPages(r.URL.Query().Get("project_id"))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list_failed", err.Error())
			return
		}
		if pages == nil {
			pages = []*model.Page{}
		}
		writeJSON(w, http.StatusOK, pages)
	case http.MethodPost:
		var p model.Page
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		if p.Title == "" {
			writeError(w, http.StatusBadRequest, "missing_title", "title is required")
			return
		}
		if err := a.store.CreatePage(&p); err != nil {
			writeError(w, http.StatusInternalServerError, "create_failed", err.Error())
			return
		}
		a.emit(&model.Event{Type: model.EventPageCreated, Payload: p})
		writeJSON(w, http.StatusCreated, p)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *API) handlePage(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/pages/")
	// Task 7 adds the "search" collection route and the "{id}/move" sub-action here.
	parts := strings.SplitN(id, "/", 2)
	id = parts[0]
	subAction := ""
	if len(parts) > 1 {
		subAction = parts[1]
	}
	if subAction == "move" {
		a.handlePageMove(w, r, id) // defined in Task 7
		return
	}
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing_id", "page ID required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		p, err := a.store.GetPage(id)
		if err != nil {
			if err == store.ErrNotFound {
				writeError(w, http.StatusNotFound, "page_not_found", "Page "+id+" not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "get_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, p)
	case http.MethodPatch:
		var updates map[string]any
		if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		p, err := a.store.UpdatePage(id, updates)
		if err != nil {
			if err == store.ErrNotFound {
				writeError(w, http.StatusNotFound, "page_not_found", "Page "+id+" not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "update_failed", err.Error())
			return
		}
		a.emit(&model.Event{Type: model.EventPageUpdated, Payload: p})
		writeJSON(w, http.StatusOK, p)
	case http.MethodDelete:
		if err := a.store.DeletePage(id); err != nil {
			if err == store.ErrNotFound {
				writeError(w, http.StatusNotFound, "page_not_found", "Page "+id+" not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "delete_failed", err.Error())
			return
		}
		a.emit(&model.Event{Type: model.EventPageDeleted, Payload: map[string]string{"id": id}})
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
```

Note: `handlePageMove` is defined in Task 7; if implementing Task 6 alone, temporarily stub the `subAction == "move"` branch to `w.WriteHeader(http.StatusNotFound)` and the test in this task still passes (it exercises no move). Task 7 replaces the stub with the real handler + the `search` route.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/api/ -run TestPageEndpointsCRUD -v` then `go test ./internal/api/`
Expected: PASS, no regressions.

- [ ] **Step 5: Commit**

```bash
git add internal/api/api.go internal/api/api_test.go
git commit --no-gpg-sign -m "feat(api): page CRUD endpoints"
```

---

### Task 7: API — page move + search endpoints

**Files:**
- Modify: `internal/api/api.go` (`handlePages` search route or a dedicated `/api/pages/search`; `handlePageMove`)
- Test: `internal/api/api_test.go`

**Interfaces:**
- Consumes: store `MovePage`, `SearchPages`.
- Produces:
  - `POST /api/pages/{id}/move` body `{parent_id, position}` → 200 updated page, emits `EventPageUpdated`. `store.ErrNotFound` → 404.
  - `GET /api/pages/search?q=&project_id=` → `[]*model.Page`.
- Wiring: register `mux.HandleFunc("/api/pages/search", a.handlePageSearch)` in `Handler()` BEFORE the `/api/pages/` catch-all is not required (Go ServeMux longest-prefix matches `/api/pages/search` to the exact pattern), but register it explicitly so search never falls into `handlePage`.

- [ ] **Step 1: Write the failing test**

```go
func TestPageMoveAndSearch(t *testing.T) {
	a, ts := setup(t)
	parent := &model.Page{Title: "Parent"}
	child := &model.Page{Title: "Child", Content: "searchable widget text"}
	a.store.CreatePage(parent)
	a.store.CreatePage(child)

	mr, err := http.Post(ts.URL+"/api/pages/"+child.ID+"/move", "application/json",
		strings.NewReader(`{"parent_id":"`+parent.ID+`","position":1.5}`))
	if err != nil {
		t.Fatal(err)
	}
	if mr.StatusCode != http.StatusOK {
		t.Fatalf("move expected 200, got %d", mr.StatusCode)
	}
	mr.Body.Close()
	got, _ := a.store.GetPage(child.ID)
	if got.ParentID != parent.ID || got.Position != 1.5 {
		t.Errorf("move not persisted: %q %v", got.ParentID, got.Position)
	}

	sr := mustGet(t, ts.URL+"/api/pages/search?q=widget")
	var res []model.Page
	json.NewDecoder(sr.Body).Decode(&res)
	sr.Body.Close()
	if len(res) != 1 || res[0].ID != child.ID {
		t.Errorf("search expected child, got %d", len(res))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/ -run TestPageMoveAndSearch -v`
Expected: FAIL (move 404 / search route missing).

- [ ] **Step 3: Write minimal implementation**

Register the search route in `Handler()`:

```go
	mux.HandleFunc("/api/pages/search", a.handlePageSearch)
```

Replace the Task 6 `subAction == "move"` stub with the real handler, and add both handlers:

```go
func (a *API) handlePageMove(w http.ResponseWriter, r *http.Request, pageID string) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ParentID string  `json:"parent_id"`
		Position float64 `json:"position"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := a.store.MovePage(pageID, req.ParentID, req.Position); err != nil {
		if err == store.ErrNotFound {
			writeError(w, http.StatusNotFound, "page_not_found", "Page "+pageID+" not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "move_failed", err.Error())
		return
	}
	page, _ := a.store.GetPage(pageID)
	a.emit(&model.Event{Type: model.EventPageUpdated, Payload: page})
	writeJSON(w, http.StatusOK, page)
}

func (a *API) handlePageSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	pages, err := a.store.SearchPages(r.URL.Query().Get("q"), r.URL.Query().Get("project_id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "search_failed", err.Error())
		return
	}
	if pages == nil {
		pages = []*model.Page{}
	}
	writeJSON(w, http.StatusOK, pages)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/api/ -run TestPageMoveAndSearch -v` then `go test ./internal/api/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/api.go internal/api/api_test.go
git commit --no-gpg-sign -m "feat(api): page move and FTS search endpoints"
```

---

### Task 8: MCP — page tools

**Files:**
- Modify: `internal/mcp/mcp.go` (`handleToolsList` defs; `executeTool` cases)
- Test: `internal/mcp/mcp_test.go`

**Interfaces:**
- Consumes: API routes from Tasks 6–7.
- Produces four tools:
  - `waggle_list_pages` — `{project_id?}` → `GET /api/pages?project_id=`.
  - `waggle_get_page` — `{id}` → `GET /api/pages/{id}`.
  - `waggle_write_page` — `{id?, project_id?, parent_id?, title, content}`: with `id` → `PATCH /api/pages/{id}`; without → `POST /api/pages`.
  - `waggle_search_pages` — `{query, project_id?}` → `GET /api/pages/search?q=...`.

- [ ] **Step 1: Write the failing test**

```go
func TestMCPPageTools(t *testing.T) {
	adapter, _ := setupMCP(t)
	list := callMCP(t, adapter, "tools/list", 1, nil)
	result, _ := list["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	names := map[string]bool{}
	for _, tv := range tools {
		if m, ok := tv.(map[string]any); ok {
			names[m["name"].(string)] = true
		}
	}
	for _, want := range []string{"waggle_list_pages", "waggle_get_page", "waggle_write_page", "waggle_search_pages"} {
		if !names[want] {
			t.Errorf("missing tool %q", want)
		}
	}
	// write a page, then search finds it
	callMCP(t, adapter, "tools/call", 2, map[string]any{
		"name":      "waggle_write_page",
		"arguments": map[string]any{"title": "MCP Note", "content": "unicorn payload"},
	})
	call := callMCP(t, adapter, "tools/call", 3, map[string]any{
		"name":      "waggle_search_pages",
		"arguments": map[string]any{"query": "unicorn"},
	})
	cr, _ := call["result"].(map[string]any)
	content, _ := cr["content"].([]any)
	if len(content) == 0 {
		t.Fatal("expected content")
	}
	text := content[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "MCP Note") {
		t.Errorf("search did not find the written page, got: %s", text)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcp/ -run TestMCPPageTools -v`
Expected: FAIL (tools missing).

- [ ] **Step 3: Write minimal implementation**

If `TestToolsList` asserts a hard tool count, bump it by 4. Add to `handleToolsList`:

```go
		toolDef("waggle_list_pages", "List doc pages for a scope. project_id empty = workspace-level pages.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project_id": prop("string", "Project ID; empty for workspace-level pages"),
			},
		}),
		toolDef("waggle_get_page", "Get one doc page's markdown by ID.", map[string]any{
			"type":       "object",
			"properties": map[string]any{"id": prop("string", "Page ID")},
			"required":   []string{"id"},
		}),
		toolDef("waggle_write_page", "Create or update a doc page. With id = update; without id = create.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":         prop("string", "Page ID to update (omit to create)"),
				"project_id": prop("string", "Project ID (empty = workspace-level)"),
				"parent_id":  prop("string", "Parent page ID (empty = root)"),
				"title":      prop("string", "Page title"),
				"content":    prop("string", "Markdown content"),
			},
			"required": []string{"title"},
		}),
		toolDef("waggle_search_pages", "Full-text search doc pages. Returns ranked matches.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":      prop("string", "Search query"),
				"project_id": prop("string", "Optional project scope"),
			},
			"required": []string{"query"},
		}),
```

Add to `executeTool`:

```go
	case "waggle_list_pages":
		pagesURL := "/api/pages"
		if pid, ok := args["project_id"].(string); ok && pid != "" {
			pagesURL += "?project_id=" + url.QueryEscape(pid)
		}
		return a.get(pagesURL)

	case "waggle_get_page":
		id, _ := args["id"].(string)
		if id == "" {
			return nil, fmt.Errorf("id is required")
		}
		return a.get("/api/pages/" + url.PathEscape(id))

	case "waggle_write_page":
		title, _ := args["title"].(string)
		if title == "" {
			return nil, fmt.Errorf("title is required")
		}
		if id, ok := args["id"].(string); ok && id != "" {
			updates := map[string]any{}
			for k, v := range args {
				if k != "id" {
					updates[k] = v
				}
			}
			return a.patchJSON("/api/pages/"+url.PathEscape(id), updates)
		}
		return a.postJSON("/api/pages", args)

	case "waggle_search_pages":
		query, _ := args["query"].(string)
		if query == "" {
			return nil, fmt.Errorf("query is required")
		}
		params := []string{"q=" + url.QueryEscape(query)}
		if pid, ok := args["project_id"].(string); ok && pid != "" {
			params = append(params, "project_id="+url.QueryEscape(pid))
		}
		return a.get("/api/pages/search?" + strings.Join(params, "&"))
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mcp/ -run TestMCPPageTools -v` then `go test ./internal/mcp/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/mcp.go internal/mcp/mcp_test.go
git commit --no-gpg-sign -m "feat(mcp): page list/get/write/search tools"
```

---

### Task 9: Dashboard — Docs view (tree + editor + preview + search)

**Files:**
- Modify: `internal/dashboard/static/mission-control.html`
- Verify: manual, via the run skill (single deliverable, no Go test).

**Interfaces:**
- Consumes: `GET/POST /api/pages`, `GET/PATCH/DELETE /api/pages/{id}`, `POST /api/pages/{id}/move`, `GET /api/pages/search`, and the existing SSE `/api/events` stream.

This is one deliverable (a working Docs view), not a TDD cycle. Build incrementally and verify in the browser. Reuse the existing view-switch mechanism and the single existing EventSource — do not open a second connection or introduce a framework.

- [ ] **Step 1: Add a "Docs" nav entry + view container**

Follow the file's existing view-switch pattern (inspect how views toggle — the same mechanism the Board view from sub-project A uses). Add a `Docs` entry and an empty `<div id="docs-view">` styled to match existing views, hidden by default.

- [ ] **Step 2: Render the page tree**

Fetch workspace pages (`GET /api/pages`) and, when a project is selected, that project's pages (`GET /api/pages?project_id=<id>`). Build a nested tree from `parent_id`, ordered by `position` then `created_at`; render two sections (Workspace, Project) in a sidebar. Show `icon` + `title`. Clicking a node opens it in the editor pane (Step 3).

- [ ] **Step 3: Editor pane with live markdown preview**

Selected page → a `<textarea>` bound to the page `content` plus a preview panel. Write a small inline `renderMarkdown(md)` function that FIRST HTML-escapes the input, THEN applies formatting (headings `#`-`######`, `**bold**`, `*italic*`, inline `` `code` ``, fenced ``` ``` ``` blocks, `- ` / `1. ` lists, `> ` blockquotes, `[text](url)` links). Escaping first is mandatory (Global Constraints security): a page body containing `<script>` must render as text, never execute. Save via `PATCH /api/pages/{id}` (explicit Save button; also update title via the same PATCH). Update the preview live on input.

- [ ] **Step 4: Tree controls — create / rename / delete / nest**

New-page button → `POST /api/pages` (with the current scope's `project_id` and the selected node as `parent_id` when nesting). Rename → `PATCH {title}`. Delete → `DELETE /api/pages/{id}` (confirm first; note children re-parent up). Drag a node onto another → `POST /api/pages/{id}/move` with the new `parent_id` and a `position` (midpoint of neighbors, same fractional approach as the Board's drag-order). Re-render the tree from the response/refetch.

- [ ] **Step 5: Search box**

A search input → `GET /api/pages/search?q=<term>` (include `project_id` when a project is selected) → render clickable results (title + a short snippet from `content`); clicking opens that page in the editor.

- [ ] **Step 6: Live updates via existing SSE**

Hook the page's existing EventSource handler: on `page_created` / `page_updated` / `page_deleted` events, if the Docs view is active, refresh the tree (and reload the open page if it was the one updated/deleted). Reuse the existing connection; do not open a second EventSource.

- [ ] **Step 7: Manual verification**

Run the app (run skill / `go run ./cmd/...`) and confirm in the browser:
- Docs view renders workspace + project trees, nested correctly.
- Create a page, edit markdown, preview updates live, Save persists (reload keeps it).
- A page whose content is `<script>alert(1)</script>` renders as literal text, does not execute (XSS check).
- Drag-to-nest persists; delete re-parents children (they move up, not vanish).
- Search returns and opens a page.
- Editing a page in one tab updates a second tab (SSE).

- [ ] **Step 8: Commit**

```bash
git add internal/dashboard/static/mission-control.html
git commit --no-gpg-sign -m "feat(dashboard): docs view with page tree, markdown editor, preview, search"
```

---

## Final verification

- [ ] **Full suite:** `go test ./...` — all green.
- [ ] **Build:** `go build ./...` — clean.
- [ ] **Manual smoke:** create a page via the dashboard and via `waggle_write_page`; search finds both; open, edit, delete (children reparent); confirm the XSS escape.
- [ ] **Review before merge:** dispatch parallel review agents (backend for store/api/mcp, frontend/security for the dashboard renderer's HTML escaping) per repo standards; fix critical/high findings before merging.

## Self-review notes (author)

- Spec coverage: pages table + FTS (T2), model (T1), CRUD + FTS sync (T3), reparent-on-delete + move (T4), FTS search + LIKE fallback (T5), REST CRUD (T6), move + search endpoints + SSE (T6/T7), MCP 4 tools (T8), dashboard Docs view with escaped markdown preview (T9). Both scopes (project + workspace) handled via `project_id` throughout. XSS-escape requirement is explicit in T9 Step 3 + verification.
- Type consistency: `CreatePage/GetPage/ListPages(projectID)/UpdatePage/DeletePage/MovePage(id,parentID,position)/SearchPages(query,projectID)` — same signatures used by API (T6/T7) and via HTTP by MCP (T8). Event consts `EventPageCreated/Updated/Deleted` from T1 used in T6/T7. `pageCols` constant shared across store methods.
- Out of scope (per spec): generic EAV DB, multi-view, permissions, version history, attachments, BM25 ranking, briefing pinned-page.
- FTS caveat: `SearchPages` builds the `p.`-qualified column list by transforming `pageCols`; T5 notes to write it out explicitly if the transform is wrong. FTS5 availability confirmed by probe; LIKE fallback covered.
