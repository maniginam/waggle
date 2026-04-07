# Command Center Gaps — Implementation Plan

## Architecture Overview

All changes touch two layers:
1. **Backend** (`internal/api/api.go`, `internal/store/`) — new endpoints, auto-dispatch logic
2. **Frontend** (`internal/dashboard/static/index.html`) — UI additions

No new packages or dependencies. No schema migrations (auto-dispatch setting uses existing project metadata or settings table).

## Phase 1: Auto-Dispatch Engine (US1)

The core behavioral change. When an agent transitions to idle:

**Backend:**
- `internal/api/api.go`: In the agent status update handler (`handleAgent` PATCH), after setting status to `idle`/`connected`, call a new `tryAutoDispatch(agentName, projectID)` method
- `tryAutoDispatch`: queries ready tasks for the project (sorted by priority), filters out tasks with unsatisfied deps, claims the first match, sends a message to the agent
- Add a project-level setting `auto_dispatch` (bool, default false) — stored in project metadata
- `internal/store/store.go`: Add `GetProjectSetting(projectID, key)` / `SetProjectSetting(projectID, key, value)` if not already present

**Frontend:**
- Project edit panel: add "Auto-dispatch" toggle
- Task cards: show a small lightning bolt icon when `auto_dispatched: true`

**Files:**
- `internal/api/api.go` — add `tryAutoDispatch()`, modify agent status handler
- `internal/store/store.go` — project settings helpers (if needed)
- `internal/dashboard/static/index.html` — project edit toggle, card indicator

## Phase 2: Alert Respawn + Bulk Assign (US2, US3)

**US2 — Respawn from alert:**
- `internal/api/api.go`: New endpoint `POST /api/agents/{name}/respawn` that looks up the agent's last project_id, finds the project's work_dir from settings/project_paths, and calls the existing spawn logic
- Frontend: In `renderAlerts()`, add a respawn button to stale_agent alert chips

**US3 — Bulk assign:**
- Frontend only: In the bulk action bar, add an "Assign" button with agent dropdown
- On select, PATCH each task's assignee via existing `/api/tasks/{id}` endpoint

**Files:**
- `internal/api/api.go` — respawn endpoint
- `internal/dashboard/static/index.html` — alert respawn button, bulk assign UI

## Phase 3: Dependency Editing + Send to Agent (US4, US5)

**US4 — Dep editing:**
- Frontend: In `openTaskPanel()`, add "+ Add Dep" button below deps list
- Opens a type-ahead search (fetches `/api/tasks?q=...` as user types)
- Selecting adds via PATCH to `depends_on` array
- "x" button on each dep row removes it via PATCH
- Server already validates cycles in the PATCH handler

**US5 — Send to Agent:**
- Frontend: Add "Send to Agent" button in task detail panel
- Dropdown of connected agents
- On select: POST message via `/api/messages`, PATCH task assignee, optionally POST to `/api/sessions/{name}/input` for live prompt injection

**Files:**
- `internal/dashboard/static/index.html` — dep editor, send-to-agent button

## Phase 4: Project View + /assign Command (US6, US7)

**US6 — Project dashboard:**
- Frontend: New `renderProjectDashboard(projectId)` view
- Reuses existing `renderBoard()` with project filter pre-applied
- Adds a stats header and agent roster for the project
- Sidebar click on project navigates to this view instead of just filtering

**US7 — /assign command:**
- Frontend: Add to `SLASH_COMMANDS` array
- Handler: parse args, PATCH task assignee, show toast
- Add `/unassign` as well

**Files:**
- `internal/dashboard/static/index.html` — project view, slash commands

## Parallel Opportunities

- [P] Phase 1 backend + Phase 2 frontend (bulk assign is frontend-only)
- [P] US4 and US5 are independent of each other
- [P] US6 and US7 are independent of each other
- Phases 1-2 should complete before 3-4 (auto-dispatch informs how send-to-agent works)

## Testing Strategy

- Backend: Add tests in `internal/api/api_test.go` for auto-dispatch and respawn
- Frontend: Manual testing via dashboard at localhost:4740
- Integration: Spawn a test agent, verify auto-dispatch claims and messages
