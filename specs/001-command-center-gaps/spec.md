# Waggle Command Center: Closing the Gaps

> Goal: Manage everything from the Waggle dashboard — no terminal required.

## Current State

The dashboard already supports: agent spawning, session management, project/task CRUD,
task assignment, swimlane drag-and-drop, bulk operations, command bar with slash commands,
alerts, push notifications, personas, and proposals.

## What's Missing

Seven targeted gaps prevent full terminal-free operation.

---

## US1: Task Auto-Dispatch (Priority: P1)

**As a** user managing multiple agents,
**I want** idle agents to automatically receive the next highest-priority ready task from their project,
**so that** I don't have to manually assign every task.

### Acceptance Criteria
- When an agent's status changes to `idle` or `connected` (no current task), the server checks for ready tasks in that agent's project
- If a matching task exists, the server sends the agent a message: "Auto-assigned task: [title] ([id])"
- The task is claimed for that agent (status -> in_progress, assignee -> agent name)
- Auto-dispatch respects task dependencies (only assigns tasks with all deps satisfied)
- Auto-dispatch can be toggled on/off per-project via a setting
- Dashboard shows a visual indicator when a task was auto-dispatched

---

## US2: One-Click Respawn from Stale Alert (Priority: P1)

**As a** user monitoring agents,
**I want** to respawn a stale/disconnected agent directly from the alert bar,
**so that** I don't have to open the spawn panel and fill in details again.

### Acceptance Criteria
- Stale agent alerts include a "Respawn" button
- Clicking respawn uses the agent's last-known name, project, and working directory
- The old stale agent record is cleaned up (disconnected)
- A new agent process is spawned with the same configuration
- Toast confirms: "Respawned [name]"

---

## US3: Bulk Assign to Agent (Priority: P2)

**As a** user triaging tasks,
**I want** to select multiple tasks and assign them all to a specific agent,
**so that** I can batch-dispatch work without editing tasks one by one.

### Acceptance Criteria
- Bulk action bar includes an "Assign" button (in addition to status moves and delete)
- Clicking "Assign" shows a dropdown of connected agents
- Selected tasks are all assigned to the chosen agent
- Toast confirms: "Assigned N tasks to [agent]"

---

## US4: Task Dependency Editing from UI (Priority: P2)

**As a** user creating task hierarchies,
**I want** to add and remove task dependencies from the task detail panel,
**so that** I don't need CLI to wire up task ordering.

### Acceptance Criteria
- Task detail panel shows a "+ Add Dependency" button below the deps list
- Clicking opens a search/select for existing tasks (type-ahead by title or ID)
- Selected task is added to `depends_on` via PATCH
- Each dep row has a "x" button to remove the dependency
- Cycle detection is enforced (server rejects cycles)

---

## US5: "Send to Agent" from Task Detail (Priority: P2)

**As a** user viewing a task,
**I want** a button that sends the task details as a prompt to a specific agent,
**so that** I can dispatch work with one click from the task view.

### Acceptance Criteria
- Task detail panel has a "Send to Agent" button
- Clicking shows a dropdown of connected agents (+ any live sessions)
- Selecting an agent sends a message: "Please work on task [id]: [title]. Description: [desc]. Criteria: [criteria]"
- If the agent has a live session, the prompt is also injected via the session API
- Task is auto-claimed for that agent

---

## US6: Per-Project Dashboard View (Priority: P3)

**As a** user managing multiple projects,
**I want** a dedicated project view showing its tasks, agents, and progress,
**so that** I can focus on one project at a time.

### Acceptance Criteria
- Clicking a project in the sidebar opens a project-scoped view
- Shows: task board (filtered to project), assigned agents, progress bar (done/total)
- Shows project-specific stats: tasks by status, velocity (tasks completed per day over last 7 days)
- "Spawn Agent for Project" button pre-fills project and work_dir

---

## US7: /assign Slash Command (Priority: P3)

**As a** power user,
**I want** `/assign <task-id> <agent-name>` in the command bar,
**so that** I can quickly dispatch tasks without mouse clicks.

### Acceptance Criteria
- `/assign wg-xxx agent-name` assigns the task and shows confirmation toast
- `/unassign wg-xxx` unassigns the task
- Tab completion for task IDs and agent names
- Error messages for invalid IDs or agent names
