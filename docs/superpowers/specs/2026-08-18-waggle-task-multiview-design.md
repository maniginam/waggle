# Waggle Task Multi-View — Design (Sub-project C1)

**Date:** 2026-08-18
**Status:** Approved design, pre-implementation
**Context:** First half of sub-project C (multi-view databases). C1 adds **table** and **calendar** views over the tasks Waggle already has. C2 (generic user-defined SQLite-EAV databases, Core-typed) is a separate later cycle. A (agile engine), the Telegram bot, and B (pages/docs) are already on master.

## Goal

Two new dashboard views over existing tasks:
- **Table view:** a sortable, filterable grid of tasks.
- **Calendar view:** tasks placed on a month grid by their deadline.

Both complement the existing kanban Board (from A) as alternate views of the same tasks.

## Decisions (locked)

1. **Dashboard-only, zero backend.** No new store/API/MCP. Reuse `GET /api/tasks?project_id=` (already returns status, priority, assignee, sprint_id, story_points, deadline, project_id) and the existing SSE stream. All sort/filter/bucketing is client-side (per-project task counts are small).
2. Two new views in `internal/dashboard/static/mission-control.html`, added alongside Board and Docs, reusing the existing view-switch mechanism and the single existing EventSource (no second connection).
3. Respect the dashboard's existing project selector: views show the selected project's tasks (or all tasks when no project is selected), consistent with the Board.
4. Client-side markdown/HTML safety: any task field placed into the DOM as text uses the existing `esc()` helper (task titles, assignee, etc.), never raw innerHTML of user content.

## Table view

- Columns: **title, status, priority, assignee, sprint, points, deadline, project**.
- Fetch tasks for the current scope (`GET /api/tasks?project_id=<id>`; omit param for all), hold them in a JS array, render as an HTML table.
- **Sort:** click a column header to sort by that column client-side; a second click toggles asc/desc; a sort indicator shows the active column/direction. Sorting covers all columns including points/sprint/assignee (which the store's `sort` param does not support — hence client-side).
- **Filter:** status dropdown, priority dropdown, and a text search box (matches title/description) — all applied client-side over the fetched array. Empty filters = show all.
- Sprint column shows the sprint name (resolve `sprint_id` against a sprints fetch, or show the id/blank if unresolved) — keep simple: show sprint name when resolvable, else blank.
- Click a row → open the task using the existing task-open path the Board uses (a task detail/edit panel/modal). If no reusable open path exists, a minimal read-only detail panel is acceptable for C1.
- Live-refresh: on `task_created/updated/completed/deleted` SSE events, if the Table view is active, re-fetch the scope and re-render.

## Calendar view

- A month grid (weeks x days) for the current month, with prev/next month navigation and a "today" marker.
- Each task with a `deadline` is rendered as a chip on its deadline day (title, colored by priority or status). Multiple tasks stack in the day cell.
- Tasks **without** a deadline appear in a small "Unscheduled" strip below/beside the grid (not on the grid).
- Click a task chip → open the task (same path as the Table view).
- Live-refresh: same SSE hook as Table.

## Testing

- No Go changes, so `go test ./...` and `go build ./...` stay green by construction (verify both after the edit).
- Manual browser verification (run skill): Table renders + sorts + filters; row-click opens a task; Calendar renders tasks on the right days, month nav works, unscheduled strip shows deadline-less tasks; both live-update via SSE; project selector scopes both.
- `node --check` on the extracted script for JS syntax.

## Out of scope for C1

- Generic user-defined databases / typed columns / EAV (→ C2).
- Server-side sort/filter additions (client-side only for C1).
- Editing tasks inline in the table/calendar beyond opening the existing task panel (drag-to-reschedule on the calendar is a nice later enhancement, not C1).
- Saved view configurations / custom column selection.

## Rollout / safety

- Single-file dashboard change, additive. No schema, API, or behavior changes elsewhere. Reversible by not opening the new views.
