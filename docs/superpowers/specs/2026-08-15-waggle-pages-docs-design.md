# Waggle Pages / Docs — Design (Sub-project B)

**Date:** 2026-08-15
**Status:** Approved design, pre-implementation
**Context:** Second of three sub-projects turning Waggle into a full workspace (after A, the agile task engine, now on master). B adds a nested markdown **pages/docs** system: shared knowledge authored and read by both Gina (dashboard editor) and Claude (MCP tools). Stays Go single-binary + embedded SQLite. Order: A (done) → **B (this)** → C (multi-view + generic SQLite-EAV databases).

## Goal

A nested markdown page tree that is a shared human+AI knowledge store:
- **Human:** author/read pages in a new dashboard "Docs" view with a markdown editor + live preview.
- **AI:** Claude reads specs and writes notes into the same store via MCP tools at session time.

Pages exist at two scopes: **per-project** (attached to a project) and **workspace-level** (global). Arbitrary-depth nesting via `parent_id`.

## Decisions (locked)

1. **Editor:** markdown textarea + **live preview** via a small inline JS renderer in the dashboard. No new Go dependency; store raw markdown, render for preview only.
2. **Search:** **SQLite FTS5** full-text over `title` + `content`, ranked; exposed to both the dashboard and MCP. FTS5 confirmed available in `modernc.org/sqlite` (probed). The plan still probes at migration time and logs + falls back to a `LIKE` search only if the virtual-table create fails.
3. **DeletePage re-parents children** up to the deleted page's parent (no accidental subtree loss).
4. Additive migrations only. Markdown text only — no attachments, no rich media, no version history, no permissions, no BM25 "Context Pack" ranking (FTS5 default rank suffices). Generic user-defined databases remain sub-project C.

## Data model (additive)

New `pages` table:

```sql
CREATE TABLE IF NOT EXISTS pages (
    id         TEXT PRIMARY KEY,
    project_id TEXT DEFAULT '',   -- '' = workspace-level (global)
    parent_id  TEXT DEFAULT '',   -- '' = root of its scope
    title      TEXT NOT NULL,
    icon       TEXT DEFAULT '',   -- optional emoji
    content    TEXT DEFAULT '',   -- raw markdown
    position   REAL DEFAULT 0,    -- sibling order within a parent
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_pages_project ON pages(project_id);
CREATE INDEX IF NOT EXISTS idx_pages_parent  ON pages(parent_id);
```

FTS5 index (kept in sync manually from the Go store write methods — no triggers):

```sql
CREATE VIRTUAL TABLE IF NOT EXISTS pages_fts USING fts5(id UNINDEXED, title, content);
```

On CreatePage/UpdatePage: delete-then-insert the row's FTS entry by `id`. On DeletePage: delete the FTS entry. This keeps the index authoritative without trigger complexity. `SearchPages` queries `pages_fts` and joins back to `pages` for full rows, ordered by FTS `rank`.

**Model (`internal/model/model.go`):** a `Page` struct (ID, ProjectID, ParentID, Title, Icon, Content, Position, CreatedAt, UpdatedAt) with json tags.

## Store layer (`internal/store/store.go`)

- `CreatePage(*model.Page) error` — sets ID via `id.New()`, timestamps; inserts row + FTS entry.
- `GetPage(id) (*model.Page, error)` — `ErrNotFound` if missing.
- `ListPages(projectID string) ([]*model.Page, error)` — all pages in a scope (projectID `""` = workspace-level), ordered by `position, created_at`. Callers build the tree from `parent_id`.
- `UpdatePage(id, updates map[string]any) (*model.Page, error)` — whitelist keys title/icon/content/position/parent_id; re-sync FTS on content/title change; bumps updated_at.
- `MovePage(id, parentID string, position float64) error` — change parent + position (drag-to-nest / reorder).
- `DeletePage(id) error` — re-parent direct children to the deleted page's `parent_id`, then delete the row + FTS entry.
- `SearchPages(query, projectID string) ([]*model.Page, error)` — FTS5 MATCH, ranked; optional project scope (empty = all scopes). Escapes the query for FTS. Falls back to `LIKE` only if FTS is unavailable (probed once at migrate time; a store flag records which mode is active).

## API layer (`internal/api/api.go`)

Follow existing handler idioms (`writeError`, `writeJSON`, sub-path split, `store.ErrNotFound`→404, SSE `emit`):
- `GET /api/pages?project_id=` → list (scope). `POST /api/pages` (requires title) → 201.
- `GET /api/pages/{id}`, `PATCH /api/pages/{id}`, `DELETE /api/pages/{id}`.
- `POST /api/pages/{id}/move` body `{parent_id, position}`.
- `GET /api/pages/search?q=&project_id=` → ranked results.
- New event types `EventPageCreated/Updated/Deleted` emitted so the dashboard live-updates via existing SSE.

## Dashboard (`internal/dashboard/static/mission-control.html`)

New **Docs** view (added alongside existing views, reusing the existing view-switch + single EventSource):
- **Tree sidebar:** workspace pages section + a per-project pages section (respecting the currently selected project). Nested by `parent_id`, ordered by `position`. Icons shown. Create / rename / delete controls; drag-to-nest and reorder → `POST /api/pages/{id}/move`.
- **Editor pane:** select a page → markdown `<textarea>` + a **live preview** panel rendered by a small inline JS markdown function (headings, bold/italic, inline code + fenced code, lists, links, blockquotes). Save via `PATCH /api/pages/{id}` (debounced or explicit Save button).
- **Search box:** query → `GET /api/pages/search` → clickable results that open the page.
- Live updates: on `page_*` SSE events, refresh the tree / open page if affected. Reuse the existing EventSource; no second connection. Escape all user content that goes into the DOM as text (the markdown renderer must not enable raw HTML injection — treat page content as markdown, not HTML, and escape before formatting).

## MCP tools (`internal/mcp/mcp.go`)

Four tools so Claude uses the shared store in-session (added to the current set):
- `waggle_list_pages` — `{project_id?}` → page tree/list for a scope.
- `waggle_get_page` — `{id}` → one page's markdown.
- `waggle_write_page` — create-or-update: `{id?, project_id?, parent_id?, title, content}`. With `id` → update; without → create.
- `waggle_search_pages` — `{query, project_id?}` → ranked matches (title + snippet).

## Testing (TDD, mandatory)

- **store_test.go:** page CRUD; ListPages scope filter (project vs workspace); DeletePage re-parents children (assert child's parent_id becomes grandparent); MovePage; tree ordering by position; FTS SearchPages returns the matching page and ranks a title/content hit; FTS stays in sync after UpdatePage (search finds new content, not old).
- **api_test.go:** each endpoint happy-path + validation (missing title → 400, unknown id → 404), mirroring existing style; search endpoint returns results.
- **mcp:** definition + invocation tests for the 4 new tools (write-then-get, search).
- **Dashboard:** manual verification via the run skill (tree render, edit + preview, save persists, search opens a page, SSE cross-tab refresh). Inline JS markdown function gets a `node --check` syntax pass.

## Security

- Page content is **markdown, not HTML** — the dashboard preview renderer must escape HTML in page content before applying markdown formatting, so a page body containing `<script>` cannot execute (no XSS via stored content). This is the one real risk and is called out for the plan.
- No new auth surface; pages inherit the daemon's existing localhost-only exposure.

## Out of scope for B (explicitly)

- Generic user-defined databases / EAV (→ C).
- Multi-view (table/kanban/calendar) over pages (→ C).
- Page permissions / sharing, version history, attachments, rich media.
- BM25 / Context-Pack relevance ranking (FTS5 default rank only).
- Surfacing a "pinned" page in `waggle_ctx_briefing` — a natural later enhancement, not v1.

## Rollout / safety

- Additive: new table + FTS virtual table + new endpoints + new dashboard view + new MCP tools. No changes to existing schema or behavior. Reversible by ignoring the new surface.
- FTS5 probed at migrate time; LIKE fallback keeps search functional if the virtual table can't be created.
