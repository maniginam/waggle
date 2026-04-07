# Command Center Gaps — Tasks

## Phase 1: Auto-Dispatch Engine

- T001 [P1] [US1] Add `tryAutoDispatch()` method to API struct
  - `internal/api/api.go`
  - Query ready tasks for agent's project, filter by satisfied deps, claim first match
  - Send message to agent with task details
  - Write test in `internal/api/api_test.go`

- T002 [P1] [US1] Hook auto-dispatch into agent status transitions
  - `internal/api/api.go` — modify `handleAgent` PATCH handler
  - Trigger `tryAutoDispatch` when agent status becomes idle/connected and has a project_id
  - Also trigger on task completion (agent finishes task -> check for next)

- T003 [P1] [US1] Add auto-dispatch project setting
  - `internal/api/api.go` — expose via project PATCH (add `auto_dispatch` field)
  - `internal/store/store.go` — store in project metadata column (JSON)
  - Default: false (opt-in)

- T004 [P1] [US1] Dashboard: auto-dispatch toggle + indicator
  - `internal/dashboard/static/index.html`
  - Add toggle to project edit panel
  - Show lightning bolt on auto-dispatched task cards

## Phase 2: Alert Respawn + Bulk Assign

- T005 [P1] [US2] Add `/api/agents/{name}/respawn` endpoint
  - `internal/api/api.go`
  - Look up agent's last project_id and work_dir
  - Call existing spawn logic with those params
  - Clean up old agent record
  - Write test in `internal/api/api_test.go`

- T006 [P1] [US2] Dashboard: respawn button on stale agent alerts
  - `internal/dashboard/static/index.html`
  - Add respawn button to alert chips where type === 'stale_agent'
  - Call `/api/agents/{name}/respawn` on click

- T007 [P2] [US3] [P] Dashboard: bulk assign action
  - `internal/dashboard/static/index.html`
  - Add "Assign" button to bulk action bar
  - Show dropdown of connected agents
  - PATCH assignee on all selected tasks

## Phase 3: Dependency Editing + Send to Agent

- T008 [P2] [US4] [P] Dashboard: dependency editor in task detail
  - `internal/dashboard/static/index.html`
  - "+ Add Dep" button with type-ahead search (fetch `/api/tasks?q=...`)
  - "x" remove button on each dep row
  - PATCH `depends_on` array on add/remove

- T009 [P2] [US5] [P] Dashboard: "Send to Agent" button on task detail
  - `internal/dashboard/static/index.html`
  - Dropdown of connected agents + live sessions
  - POST message with task details, PATCH assignee
  - Inject prompt via `/api/sessions/{name}/input` if live

## Phase 4: Project View + Slash Commands

- T010 [P3] [US6] [P] Dashboard: per-project dashboard view
  - `internal/dashboard/static/index.html`
  - `renderProjectDashboard(projectId)` — filtered board, stats, agent roster
  - Sidebar project click navigates to this view
  - "Spawn Agent for Project" button with pre-filled fields

- T011 [P3] [US7] [P] Add /assign and /unassign slash commands
  - `internal/dashboard/static/index.html`
  - Parse: `/assign <task-id> <agent>`, `/unassign <task-id>`
  - PATCH task assignee, show toast
  - Add to `SLASH_COMMANDS` array
