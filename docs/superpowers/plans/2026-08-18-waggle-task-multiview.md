# Waggle Task Multi-View Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a sortable/filterable table view and a deadline calendar view over existing tasks to the dashboard.

**Architecture:** Dashboard-only, zero backend. Two new views in `internal/dashboard/static/mission-control.html`, added alongside the existing Dashboard/Board/Docs views, reusing the existing `switchView()` mechanism, the single existing `connectSSE()` EventSource, the `esc()` helper, the project selector, and `GET /api/tasks?project_id=`. All sort/filter/date-bucketing is client-side.

**Tech Stack:** Inline JS/CSS in `mission-control.html`. No Go changes.

## Global Constraints

- No Go source changes. `go test ./...` and `go build ./...` must stay green (verify after each task — green by construction since no Go files change).
- Single file: `internal/dashboard/static/mission-control.html`. Follow its existing patterns exactly. Do NOT open a second EventSource; hook the existing `connectSSE()` `es.onmessage` handler (around line 1841). Do NOT add a framework.
- Existing hooks to reuse: `switchView(view)` (~line 1789) + the `currentView` global (~1787) + the view-button/`<div id="...-view">` pattern (buttons ~994-996, e.g. `board-view`/`docs-view`); `esc(s)` (~1722); `connectSSE()` (~1839); task fetch `GET /api/tasks` (an existing loader is around ~1947).
- Security: any task-supplied string (title, assignee, description) inserted into the DOM must go through `esc()` — never raw innerHTML of user content.
- Scope: both views respect the dashboard's existing project selector — show the selected project's tasks (`?project_id=<id>`) or all tasks when none selected (omit the param), consistent with the Board.
- Commits MUST use `git commit --no-gpg-sign`. No Co-Authored-By / attribution footers.
- Manual browser verification is the acceptance gate (single HTML file, no automated UI test). Run `node --check` on the extracted `<script>` to catch JS syntax errors; if `node` is unavailable, do a careful manual syntax review.

## File structure

- `internal/dashboard/static/mission-control.html` — add a Table view (Task 1) and a Calendar view (Task 2). Both are self-contained view sections + their render/sort/filter JS.

---

### Task 1: Table view

**Files:**
- Modify: `internal/dashboard/static/mission-control.html`
- Verify: manual (browser) + `node --check`.

This is one deliverable (a working Table view), not a TDD cycle. Build the steps, then verify in the browser.

- [ ] **Step 1: Add the nav button + view container**

Next to the existing view buttons (~line 995-996, `Board`/`Docs`), add:
```html
<button class="view-btn" id="view-table-btn" onclick="switchView('table')">Table</button>
```
Add a hidden container alongside `board-view`/`docs-view`:
```html
<div id="table-view" style="display:none"></div>
```
Confirm `switchView('table')` shows `#table-view` and hides the others — follow exactly how `switchView` already toggles `board`/`docs` (inspect the function ~1789 and add a `table` branch the same way, including setting the active button class).

- [ ] **Step 2: Fetch tasks for the current scope**

Add `async function loadTableView()` that reads the currently-selected project id the same way the Board does (find how the Board/project selector exposes the selected project — reuse that exact accessor; empty = all), fetches `GET /api/tasks` + `?project_id=<id>` when a project is selected, and stores the result array in a module-level `let tableTasks = []`. Call `loadTableView()` when `switchView('table')` activates the view.

- [ ] **Step 3: Render the table**

Render `tableTasks` into `#table-view` as an HTML `<table>` with columns: **title, status, priority, assignee, sprint, points, deadline, project**. Rules:
- `title`, `assignee` → wrap in `esc()`.
- `points` → `task.story_points` (blank if 0/absent).
- `deadline` → the date portion of `task.deadline` (blank if empty).
- `sprint` → the sprint name: fetch sprints once (`GET /api/sprints?project_id=<id>`) into a `sprintNameById` map and show `sprintNameById[task.sprint_id] || ''`.
- `project` → the project name if a project map is already available in the page (reuse it); else `task.project_id` or blank.
- Each `<tr>` has an `onclick` that opens the task (Step 5).

- [ ] **Step 4: Client-side sort + filter**

- Sort: clicking a column header sorts `tableTasks` by that column; a second click on the same header toggles ascending/descending; show a ▲/▼ indicator on the active column. Sort in JS (string compare for text columns, numeric for points, date compare for deadline, priority by rank critical>high>medium>low). Re-render after sorting.
- Filter: add a status `<select>`, a priority `<select>`, and a text `<input>` above the table. On change, compute a filtered view of `tableTasks` (status match, priority match, title/description case-insensitive contains) and render that. Empty controls = no filter. Keep the current sort applied to the filtered set.

- [ ] **Step 5: Row click opens the task**

On row click, open the task using whatever task-open path already exists in the page (inspect the Board for a task-open/detail function and reuse it). If no reusable open path exists, implement a minimal read-only detail panel: a `<div>` (or reuse an existing modal container) showing the task's title, description, status, priority, assignee, sprint, points, deadline — all user strings via `esc()`. Keep it simple; C1 does not require inline editing.

- [ ] **Step 6: Live-refresh on SSE**

In the existing `connectSSE()` `es.onmessage` handler (~1841), where other views already react (Board/Docs do), add: if `currentView === 'table'` and the event type is one of `task_created`, `task_updated`, `task_completed`, `task_deleted`, call `loadTableView()` to re-fetch + re-render. Do not open a new EventSource.

- [ ] **Step 7: Verify**

Run `go build ./...` (clean) and `go test ./internal/dashboard/` (pass). Run `node --check` on the extracted script (or careful manual review). Then in the browser: open Table view; confirm columns render, headers sort (and toggle direction), the three filters narrow the list, a row click opens the task, the project selector scopes the list, and creating/moving a task elsewhere live-updates the table via SSE.

- [ ] **Step 8: Commit**

```bash
git add internal/dashboard/static/mission-control.html
git commit --no-gpg-sign -m "feat(dashboard): task table view with client-side sort and filter"
```

---

### Task 2: Calendar view

**Files:**
- Modify: `internal/dashboard/static/mission-control.html`
- Verify: manual (browser) + `node --check`.

One deliverable (a working Calendar view). Build then verify.

- [ ] **Step 1: Add the nav button + view container**

Add next to the Table button:
```html
<button class="view-btn" id="view-calendar-btn" onclick="switchView('calendar')">Calendar</button>
```
And a hidden container:
```html
<div id="calendar-view" style="display:none"></div>
```
Add the `calendar` branch to `switchView` exactly like the others (show `#calendar-view`, hide others, set active button).

- [ ] **Step 2: Month state + task fetch**

Add module-level state: `let calMonth` = current year+month (initialize to today's year/month — compute from `new Date()` at render time, not module load). Add `async function loadCalendarView()` that fetches tasks for the current scope (same accessor as Table's Step 2) into `let calTasks = []`, then renders. Call it when `switchView('calendar')` activates.

- [ ] **Step 3: Render the month grid**

Render `#calendar-view` as: a header row with `‹` prev / month-year label / `›` next buttons and a "Today" button; then a 7-column grid (Sun–Sat header + up to 6 week rows) for `calMonth`. Compute the grid days in JS from the first-of-month weekday and days-in-month. Mark today's cell (compare to `new Date()`). Prev/next buttons adjust `calMonth` (handle year rollover) and re-render (no re-fetch needed; re-bucket `calTasks`). "Today" resets `calMonth` to the current month.

- [ ] **Step 4: Place tasks on deadline days**

For each task with a non-empty `deadline`, parse its date (YYYY-MM-DD portion) and, if it falls in `calMonth`, render a chip in that day cell: the task title via `esc()`, colored by priority (reuse existing priority color classes/vars if present, else a small inline style). Stack multiple chips in a cell; if a cell overflows, show "+N more" (optional). Each chip `onclick` opens the task (reuse Task 1 Step 5's open path).

- [ ] **Step 5: Unscheduled strip**

Below (or beside) the grid, render an "Unscheduled" strip listing tasks with no `deadline` (title via `esc()`, click opens the task). If none, show "No unscheduled tasks" or hide the strip.

- [ ] **Step 6: Live-refresh on SSE**

In the same `es.onmessage` handler, add: if `currentView === 'calendar'` and the event is `task_created/updated/completed/deleted`, call `loadCalendarView()`.

- [ ] **Step 7: Verify**

`go build ./...` clean, `go test ./internal/dashboard/` pass, `node --check` clean. In the browser: Calendar renders the current month with tasks on their deadline days; prev/next/today navigation works (including year rollover); the unscheduled strip lists deadline-less tasks; clicking a chip opens the task; the project selector scopes it; SSE live-updates it.

- [ ] **Step 8: Commit**

```bash
git add internal/dashboard/static/mission-control.html
git commit --no-gpg-sign -m "feat(dashboard): task calendar view by deadline"
```

---

## Final verification

- [ ] **Build:** `go build ./...` — clean.
- [ ] **Full suite:** `go test ./...` — green (unchanged; no Go files touched).
- [ ] **Manual smoke:** Table (sort/filter/open/scope/SSE) and Calendar (month nav/deadline placement/unscheduled/open/scope/SSE) both work in the browser.
- [ ] **Review before merge:** dispatch a frontend/security review of the dashboard diff (esc() coverage on all task strings, no second EventSource, switchView reused not duplicated) per repo standards; fix critical/high findings before merging.

## Self-review notes (author)

- Spec coverage: table view sortable/filterable (T1), calendar by deadline + unscheduled + month nav (T2), both scoped by project selector + live SSE + reusing switchView/connectSSE/esc (both tasks, Global Constraints). Zero-backend and single-file constraints honored.
- No Go changes → test suite green by construction; acceptance is manual browser verify + node --check, consistent with prior dashboard tasks (agile Board, pages Docs).
- Consistency: both tasks reuse the same scope accessor (Step 2) and the same task-open path (T1 Step 5, referenced by T2 Step 4). `loadTableView`/`loadCalendarView` are the two entry points wired into `switchView` and `connectSSE`.
- Out of scope (per spec): generic EAV DB (C2), server-side sort/filter, inline editing, drag-to-reschedule, saved views.
