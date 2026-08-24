# C2: Generic Typed Databases -- Design Spec

**Date:** 2026-08-24
**Sub-project:** C2 (final piece of the Waggle agile-workspace pivot; C1 task multi-view already shipped)
**Status:** Approved design, pending plan.

## Goal

Let users define their own databases inside Waggle -- Notion-style typed tables -- with typed columns, rows, and multiple views (table, kanban, calendar) with client-side filter/sort/group. Agents can read and write these databases over MCP.

## Scope

**In:**
- User-defined databases (project-scoped or workspace-scoped).
- Typed columns: `text`, `number`, `date`, `select`, `checkbox`.
- Rows with per-column values, app-layer type validation.
- Views: `table`, `kanban`, `calendar`, each with a saved config (filters, sort, group-by, date-field).
- REST CRUD, MCP tools, dashboard "Databases" view.

**Out (explicitly deferred):**
- Formulas / computed columns.
- Relations between databases.
- Rollups / aggregations.
- Server-side pagination (client-side over fetched rows, matching C1).
- MCP tools for views (views are dashboard config; agents do not need them in MVP).

## Architecture

New `udb` (user-defined database) domain, added alongside the existing 12-table store. Storage model: **JSON-per-row** (one SQLite row per record; all cell values in a `data_json` blob keyed by column id). Mirrors the existing task/page feature structure: store layer -> REST API -> MCP tools -> single-file dashboard. No new external dependencies. Stays Go single-binary + embedded SQLite.

Rationale for JSON-per-row over EAV: a record maps to one row (natural read/write), inserts and single-row reads are trivial, and `json_extract` covers sort/filter if ever pushed server-side. EAV's per-cell normalization buys little without relations or formulas, both out of scope, and it costs a self-join per filtered/sorted column.

## Data Model

Four new tables. IDs are the project's existing string-id style (see `pkg/id`).

```
udb(
  id          TEXT PRIMARY KEY,
  project_id  TEXT NULL,          -- NULL = workspace-scoped (same convention as pages)
  name        TEXT NOT NULL,
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL
)

udb_column(
  id           TEXT PRIMARY KEY,
  db_id        TEXT NOT NULL,      -- FK udb.id
  name         TEXT NOT NULL,
  type         TEXT NOT NULL,      -- text | number | date | select | checkbox
  position     INTEGER NOT NULL,   -- display order
  options_json TEXT,               -- select: [{"value":"Todo","color":"#aabbcc"}]; else NULL/empty
  created_at   TEXT NOT NULL
)

udb_row(
  id         TEXT PRIMARY KEY,
  db_id      TEXT NOT NULL,        -- FK udb.id
  data_json  TEXT NOT NULL,        -- {"<column_id>": <value>, ...}
  position   INTEGER NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
)

udb_view(
  id          TEXT PRIMARY KEY,
  db_id       TEXT NOT NULL,       -- FK udb.id
  name        TEXT NOT NULL,
  kind        TEXT NOT NULL,       -- table | kanban | calendar
  config_json TEXT NOT NULL,       -- see View Config below
  position    INTEGER NOT NULL
)
```

Migrations follow the existing additive migration pattern in `internal/store/store.go` (idempotent `CREATE TABLE IF NOT EXISTS`; the migration is dated per the project's migration rule).

### Value typing (app-layer, enforced on row write)

| Column type | Stored JSON | Validation on write |
|-------------|-------------|---------------------|
| `text`      | string      | must be string |
| `number`    | number (float64) | must be numeric |
| `date`      | string (ISO 8601, `YYYY-MM-DD`) | must parse as date |
| `select`    | string      | must equal one of the column's option values |
| `checkbox`  | bool        | must be bool |

A missing key = empty/unset cell (allowed). An unknown key (no matching column) is rejected on write. A value whose type does not match its column -> `400 invalid_cell`.

### View config (`config_json`)

```
{
  "filters":  [{"col": "<column_id>", "op": "eq|neq|contains|gt|lt", "value": <any>}],
  "sort":     [{"col": "<column_id>", "dir": "asc|desc"}],
  "group_by": "<column_id>",   // kanban only; MUST reference a select column
  "date_field": "<column_id>"  // calendar only; MUST reference a date column
}
```

- `table`: filters + sort apply; `group_by`/`date_field` ignored.
- `kanban`: `group_by` required and must reference a `select` column; each option value becomes a lane; filters + sort apply within lanes.
- `calendar`: `date_field` required and must reference a `date` column; rows placed by that date; filters apply.

Validation on view create/update: unknown `kind` -> `400 invalid_view`; kanban without a valid select `group_by`, or calendar without a valid date `date_field` -> `400 invalid_view_config`.

## Data Flow

1. Create a database (name, optional project_id).
2. Add columns (name, type, options for select).
3. Add rows (data_json validated against columns).
4. Define views (kind + config).
5. Dashboard fetches all columns + rows for a DB; each view renders client-side, applying its filter/sort/group over the cached rows (parity with C1's client-side table/calendar). No server round-trip on view switch or sort/filter change.

## Deletes / cascade

- Delete database: cascade-delete its columns, rows, and views (single transaction).
- Delete column: remove the column def only. Stale keys left in existing rows' `data_json` become inert -- ignored on read and by every view. No mass row rewrite. (A future compaction pass may strip them; not in scope.)
- Delete row / delete view: straightforward single-record delete.

## REST API

Handlers in `internal/api`, following existing conventions (`writeError`, method guards, `safeNameRe`-style name validation, JSON bodies, SSE emit on mutation).

```
GET    /api/udb?project_id=<id>     list databases (project or workspace scope)
POST   /api/udb                     create database
PATCH  /api/udb/{id}                rename database
DELETE /api/udb/{id}               delete database (cascade)

GET    /api/udb/{id}/columns        list columns
POST   /api/udb/{id}/columns        add column
PATCH  /api/udb/{id}/columns/{cid}  edit column (name/options/position)
DELETE /api/udb/{id}/columns/{cid}  delete column

GET    /api/udb/{id}/rows           list rows (all rows for DB)
POST   /api/udb/{id}/rows           add row (validated)
PATCH  /api/udb/{id}/rows/{rid}     update row (validated)
DELETE /api/udb/{id}/rows/{rid}     delete row

GET    /api/udb/{id}/views          list views
POST   /api/udb/{id}/views          create view (validated)
PATCH  /api/udb/{id}/views/{vid}    update view (validated)
DELETE /api/udb/{id}/views/{vid}    delete view
```

Error codes (following existing style): `invalid_json`, `missing_name`, `invalid_name`, `not_found`, `invalid_cell`, `unknown_column`, `invalid_view`, `invalid_view_config`.

## MCP Tools (7)

Added to `internal/mcp/mcp.go`, so agents can read/write user databases:

- `waggle_list_databases` -- list DBs (optional project_id filter)
- `waggle_create_database` -- create a DB
- `waggle_add_column` -- add a typed column
- `waggle_list_rows` -- list a DB's columns + rows
- `waggle_create_row` -- add a validated row
- `waggle_update_row` -- update a validated row
- `waggle_delete_row` -- delete a row

No view tools (views are dashboard config). This brings the MCP surface to ~28 tools.

## Dashboard

New "Databases" view in `internal/dashboard/static/mission-control.html`, added via the existing `switchView`/`currentView` mechanism and view button row, reusing `connectSSE`/`esc`/the project selector.

- **DB picker:** list databases for the selected project (+ workspace-scoped); create/rename/delete.
- **Column editor:** add/edit/delete columns inline; pick type; edit select options.
- **View tabs:** table | kanban | calendar, per DB; create/edit/delete views with their config.
  - Table: reuse C1's client-side sortable/filterable table rendering over the fetched rows.
  - Calendar: reuse C1's calendar (month nav, unscheduled strip) keyed on the view's `date_field`.
  - Kanban: new -- lanes from the `group_by` select column's options; row cards; client-side filter/sort within lanes.
- **Row editing:** add/edit rows inline, typed inputs per column (text box, number, date picker, select dropdown, checkbox).
- **Safety:** escape-first rendering for all user strings (XSS lesson from pages/C1); SSE-driven live refresh with a dirty-guard so an open editor is not clobbered by an incoming event (pages lesson).

## Error Handling Summary

- Row writes validated against column defs; type mismatch or unknown column -> 400.
- View config validated against column types (kanban->select, calendar->date) -> 400.
- Name validation on DB/column/view names.
- Cascade deletes in a transaction.
- All mutations emit SSE events (`udb_*`) for live dashboard refresh.

## Testing Strategy

TDD throughout (red-green-refactor), executed via subagent-driven development, final whole-branch review, merge to master.

- **Store tests:** table creation/migration, DB/column/row/view CRUD, cascade delete, JSON round-trip, type validation at the store boundary, stale-key inertness after column delete.
- **API tests:** each endpoint happy path + validation errors (invalid_cell, unknown_column, invalid_view_config, not_found), cascade behavior, SSE emission.
- **MCP tests:** tool definitions present + shaped correctly; create/list/update/delete row round-trip through a tool call; type validation surfaced through MCP.
- **Dashboard:** manual verification of the three view kinds + inline editing (no JS test harness in this project).

## Deployment

Additive migration (new tables only) -- safe on the live 100MB DB. After merge to master: rebuild binary, back up DB, reinstall to `/Users/maniginam/go/bin/waggle`, restart the launchd daemon, verify `/api/udb` responds. (Same redeploy flow just used for the spawn fix.)
