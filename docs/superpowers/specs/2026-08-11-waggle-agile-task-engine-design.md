# Waggle Agile Task Engine — Design (Sub-project A)

**Date:** 2026-08-11
**Status:** Approved design, pre-implementation
**Context:** First of three sub-projects turning Waggle into a "full agile workspace" (inspired by remnus-app). Order: **A. Agile task engine → B. Pages/docs → C. Multi-view databases (SQLite EAV, generic user-defined tables)**. Waggle stays a Go single-binary + embedded SQLite app; no Datomic, no Next.js rewrite.

This spec covers **only sub-project A**. B and C get their own spec → plan → implementation cycles.

## Goal

Make Waggle "more agile" in the methodology sense: sprints, kanban board, backlog, story points, WIP limits, burndown. Deliver it by *extending* the existing task/project model rather than rebuilding.

## What already exists (do not rebuild)

- `tasks` table with `status` enum: `backlog, ready, in_progress, review, done, blocked` — these ARE the kanban columns.
- Task fields: priority, assignee, tags, estimate (free text), deadline, parent_id, depends_on, `task_type` (task/epic/story/issue), project_id.
- `projects` table, SSE event stream, single-file dashboard `internal/dashboard/static/mission-control.html`.
- `Stats()` already computes 7-day completion velocity.
- Additive-migration pattern in `store.migrate()` (pragma_table_info check + ALTER TABLE ADD COLUMN).

## Design decisions (locked)

1. **Board columns = existing `status` enum, fixed.** No configurable per-project columns in A. Generalization deferred to sub-project C (generic DB). Reason: avoids doing C's work twice and a disruptive enum migration.
2. **Add numeric `story_points`.** Keep `estimate` free-text field untouched (nothing breaks). Points drive burndown/velocity math.
3. **Fractional `board_order` (REAL)** for drag-drop insert-between without renumbering the column.
4. **WIP limits live in the existing `settings` table**, not a new table. Key format `wip:<projectID>:<status>` → integer string. Absent = no limit.

## Data model (additive migrations only)

New table:

```sql
CREATE TABLE IF NOT EXISTS sprints (
    id         TEXT PRIMARY KEY,
    project_id TEXT NOT NULL,
    name       TEXT NOT NULL,
    goal       TEXT DEFAULT '',
    state      TEXT DEFAULT 'planned',   -- planned | active | closed
    start_date TEXT DEFAULT '',          -- RFC3339 or ''
    end_date   TEXT DEFAULT '',
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sprints_project ON sprints(project_id);
CREATE INDEX IF NOT EXISTS idx_sprints_state ON sprints(state);
```

New columns on `tasks` (via existing ALTER-column loop):

| column        | type    | default | meaning                                  |
|---------------|---------|---------|------------------------------------------|
| `sprint_id`   | TEXT    | `''`    | empty = backlog; else member of a sprint |
| `story_points`| INTEGER | `0`     | 0 = unpointed                            |
| `board_order` | REAL    | `0`     | fractional rank within its status column |

New index: `idx_tasks_sprint ON tasks(sprint_id)`.

**Model (`internal/model/model.go`):**
- New `Sprint` struct + `SprintState` type with `Valid()` (planned/active/closed), matching the existing enum idiom.
- `Task` gains `SprintID string`, `StoryPoints int`, `BoardOrder float64` with json tags (`omitempty` where sensible; `board_order` always emitted for the UI).

## Store layer (`internal/store/store.go`)

Sprints:
- `CreateSprint(*model.Sprint) error`
- `GetSprint(id) (*model.Sprint, error)`
- `ListSprints(projectID string) ([]*model.Sprint, error)` — all when projectID == "".
- `UpdateSprint(id, updates map[string]any) (*model.Sprint, error)` — mirrors UpdateTask/UpdateProject style.
- `SetSprintState(id, state)` — convenience; enforces single active sprint per project (activating one closes/deactivates the currently-active one in that project, in a tx).
- `DeleteSprint(id)` — clears `sprint_id` on member tasks (back to backlog), does not delete tasks.

Board / tasks:
- `MoveTask(taskID, status string, boardOrder float64) (*model.Task, error)` — sets status + board_order + updated_at in one update; validates status via `TaskStatus.Valid()`. Reuses SSE event emission.
- `AssignToSprint(taskID, sprintID string) error` — sprintID "" sends back to backlog.
- Extend `ListTasks` filters with `sprint_id` (including a sentinel for "backlog only", e.g. `sprint_id=""`).
- `SprintBurndown(sprintID string) ([]PointDay, error)` — for each day from sprint start to min(today, end): points still not `done`. Uses task `updated_at` transition to done as the completion signal (same approximation `Stats().Velocity` already uses). Struct `PointDay{ Date string; Remaining int }`.
- `WIP limits:` `GetWIP(projectID, status) (int, bool)` and `SetWIP(projectID, status, limit)` thin wrappers over settings. WIP is advisory (UI enforces/warns); store does not hard-block moves in A. Documented as advisory.

## API layer (`internal/api/api.go`)

Mirror store, follow existing handler + JSON conventions, emit existing SSE events so the board live-updates:
- `GET/POST /api/sprints`, `GET/PATCH/DELETE /api/sprints/{id}`
- `GET /api/sprints/{id}/burndown`
- `POST /api/tasks/{id}/move` — body `{status, board_order}`
- `POST /api/tasks/{id}/sprint` — body `{sprint_id}`
- `GET/PUT /api/wip?project_id=` — read/set WIP limits map

## Dashboard (`internal/dashboard/static/mission-control.html`)

New **Board** view (added alongside existing views, not replacing them):
- Columns rendered from the status enum; cards sorted by `board_order`.
- HTML5 drag-and-drop between columns; on drop compute new fractional `board_order` (midpoint of neighbors) and `POST /api/tasks/{id}/move`.
- Sprint selector: **Active sprint** / **Backlog** / **All**. Filters cards.
- Per-column: card count, story-point sum, WIP limit badge; column header turns red when count > WIP limit.
- Burndown sparkline for the active sprint (from `/burndown`).
- Live updates via the existing SSE connection (move/create/complete events re-render affected columns).

Single-file constraint preserved (inline JS/CSS as the file already does).

## MCP tools (`internal/mcp/mcp.go`)

So Claude drives the board from inside a session (adds to the current 12):
- `waggle_move_task` — `{task_id, status, board_order?}`; omitted order appends to column end.
- `waggle_create_sprint` — `{project_id, name, goal?, start_date?, end_date?}`.
- `waggle_assign_sprint` — `{task_id, sprint_id}` (""=backlog).
- `waggle_sprint_status` — `{project_id}` → active sprint, point totals by column, burndown tail. Read-only briefing helper.

## Testing (TDD, mandatory per repo standards)

Red-green-refactor. New/changed behavior gets a failing test first.
- **store_test.go:** sprint CRUD; single-active-sprint invariant; `MoveTask` sets status+order; fractional reorder keeps ordering stable; `AssignToSprint` ""→backlog; `SprintBurndown` math on a seeded timeline; WIP get/set roundtrip; `ListTasks` sprint_id filter incl. backlog sentinel.
- **api_test.go:** each new endpoint happy-path + validation error (bad status, missing sprint), mirroring existing table-driven style.
- **mcp:** definition + invocation tests for the 4 new tools (also closes the existing "no tests for MCP tool definitions" gap).
- Board UI verified manually via the run skill (drag, WIP red state, burndown render).

## Out of scope for A (explicitly)

- Configurable/custom board columns (→ C).
- Pages/docs (→ B).
- Generic user-defined databases / EAV (→ C).
- Hard WIP enforcement in the store (A ships advisory only).
- Historical burndown snapshots table (A derives burndown from `updated_at`; a snapshot table can come later if the approximation proves too coarse).

## Rollout / safety

- All migrations additive; existing rows get column defaults. No destructive changes, no data migration. Reversible by ignoring the new columns.
- Feature is inert until the Board view is opened / new endpoints are called; existing views and MCP tools unchanged.
