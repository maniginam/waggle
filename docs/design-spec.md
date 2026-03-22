# Waggle Design Spec

> Model-agnostic AI agent orchestration. How your agents talk.

## Overview

Waggle is a standalone Go binary that provides SMART task orchestration for AI coding agents. It runs as a local server with WebSocket-first real-time coordination, a REST API for tooling/CLI access, and an MCP adapter for Claude Code and other MCP-compatible agents.

Named after the honeybee waggle dance — the communication protocol bees use to tell other bees where to go and what to do.

## Target User

Solo developers using AI coding agents (Claude Code, Cursor, Aider, Copilot, etc.) who want structured task coordination across multiple agents.

## Design Goals

1. **60-second setup**: `brew install waggle && waggle start && waggle connect`
2. **Model-agnostic**: any AI agent can connect via WebSocket, REST, or MCP
3. **Zero external dependencies**: single binary, embedded SQLite, no Docker/DB/config
4. **Real-time coordination**: WebSocket event stream, no polling
5. **SMART task discipline**: every task is Specific, Measurable, Achievable, Relevant, Time-bound

## Non-Goals (MVP)

- Kanban board UI (future: web frontend on REST API)
- SQDIC dashboard (future: analytics over event log)
- Authentication / multi-user (future: team features)
- Remote access / daemon mode (future: split into client + daemon)

---

## Core Data Model

### SMART Task

The fundamental unit of work.

```
Task {
  id:          string          // auto-generated short hash, e.g. "wg-a3f"
  title:       string
  description: string
  criteria:    []string        // acceptance criteria (the "Measurable" in SMART)
  status:      enum            // backlog | ready | in_progress | review | done | blocked
  priority:    enum            // critical | high | medium | low
  assignee:    string          // agent name or empty
  tags:        []string        // project, goal, category (the "Relevant" in SMART)
  estimate:    duration        // optional time estimate (the "Time-bound" in SMART)
  deadline:    timestamp       // optional
  created_at:  timestamp
  updated_at:  timestamp
  parent_id:   string          // optional, for subtasks/epics
  depends_on:  []string        // task IDs this blocks on
}
```

### Agent

```
Agent {
  id:           string          // auto-generated
  name:         string          // "backend-agent", "test-runner", etc.
  type:         string          // "claude-code", "cursor", "aider", "custom"
  status:       enum            // connected | working | idle | blocked | disconnected
  current_task: string          // task ID or empty
  connected_at: timestamp
  last_seen:    timestamp
}
```

### Event

```
Event {
  type:      enum              // task_created | task_updated | task_claimed |
                               // task_completed | agent_joined | agent_left |
                               // agent_status_changed | message
  agent_id:  string            // who triggered it
  task_id:   string            // if task-related
  payload:   object            // event-specific data
  timestamp: timestamp
}
```

### Message

```
Message {
  id:         string
  from:       string           // agent name
  to:         string           // agent name or "" for broadcast
  body:       string
  read:       bool
  created_at: timestamp
}
```

---

## Communication Architecture

### WebSocket (Primary)

Agents connect to `ws://localhost:4740/ws` and join a shared event stream.

Key behaviors:
- Agent connects -> receives all events in real-time
- Agent claims a task -> other agents see it immediately (atomic claim via SQLite transaction, rejects if already claimed)
- Agent completes a task -> dependent tasks auto-transition from `blocked` to `ready`
- WebSocket heartbeat every 30 seconds; agent marked `disconnected` after 3 missed heartbeats (~90s)
- On disconnect, agent's in_progress tasks are unassigned and returned to `ready`
- Direct messaging between agents via `message` event type

### REST API

HTTP endpoints on `http://localhost:4740/api` for CLI commands and external tools.

```
POST   /api/tasks          # create task
GET    /api/tasks           # list tasks (query params for filters)
GET    /api/tasks/:id       # show task detail
PATCH  /api/tasks/:id       # update task
DELETE /api/tasks/:id       # delete task (rejects if in_progress)
GET    /api/agents          # list agents
GET    /api/agents/:id      # show agent detail
GET    /api/events          # SSE stream (read-only mirror of WebSocket events)
WS     /ws                  # WebSocket endpoint
```

Error response format:
```json
{ "error": { "code": "task_not_found", "message": "Task wg-xyz not found" } }
```

### MCP Adapter

Stdio transport for Claude Code and MCP-compatible agents.

Tools exposed:
```
waggle_create_task     { title, description, criteria, priority, tags, estimate, deadline, parent_id, depends_on }
waggle_list_tasks      { status, assignee, tag, priority }
waggle_show_task       { id }
waggle_update_task     { id, status, priority, assignee, description, criteria }
waggle_complete_task   { id }
waggle_claim_task      { id }
waggle_unclaim_task    { id }              // self-only: can only unclaim your own tasks
waggle_register_agent  { name, type }      // required as first call; identity used for all subsequent calls
waggle_list_agents     { status }
waggle_set_status      { status, current_task }  // sets status of the calling agent
waggle_send_message    { to, body }
waggle_read_messages   { limit, from }
```

`waggle mcp` launches the stdio adapter, which internally connects to the running server over WebSocket. If the server isn't running, it auto-starts it. Agent identity is established via `waggle_register_agent` (required first call).

---

## CLI

```
waggle start [--port 4740]                # start the server
waggle stop                               # stop the server
waggle status                             # server status + connected agents

waggle task add "title" [flags]           # create a task
  --criteria "criterion"                  # repeatable
  --priority high
  --tag backend                           # repeatable
  --estimate 2h
  --deadline 2026-03-20
  --parent wg-b2c
  --depends wg-a3f                        # repeatable

waggle task list                          # list all tasks
waggle task list --status ready           # filter by status
waggle task show wg-a3f                   # task detail
waggle task update wg-a3f --status in_progress
waggle task claim wg-a3f                  # claim a task
waggle task done wg-a3f                   # mark complete
waggle task rm wg-a3f                     # delete (rejects if in_progress)
waggle tasks                              # shorthand for task list

waggle agents                             # list connected agents
waggle agent show backend-agent           # agent detail + current task

waggle msg send backend-agent "message"   # send message to agent
waggle msg list                           # list messages

waggle watch                              # tail event stream
waggle watch --agent backend-agent        # filter by agent
waggle watch --task wg-a3f                # filter by task

waggle connect                            # generate/merge .mcp.json in current directory
waggle config [key] [value]               # get/set config (retention, port, etc.)
waggle backup                             # backup database
waggle reset                              # wipe database (with confirmation)
```

---

## Storage

Embedded SQLite at `~/.waggle/waggle.db`. Created on first `waggle start`.

Tables: tasks, agents, events, messages

Event log is append-only. Every state change writes an event.

Data lifecycle:
- Tasks persist until explicitly deleted
- Agent records persist, marked `disconnected` on timeout
- Events retained 30 days by default (`waggle config retention 90d`)
- Messages retained until read + 7 days

Backup: `waggle backup` copies to `~/.waggle/backups/waggle-YYYY-MM-DD-HHMMSS.db`

---

## Installation

```
brew install waggle
go install github.com/cleancoders-studio/waggle@latest
curl -fsSL waggle.dev/install | sh
```

Default port: 4740

---

## Technical Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Language | Go | Single binary, no runtime deps, easy distribution |
| Storage | SQLite (embedded) | Zero-config, portable, copy to move |
| Primary protocol | WebSocket | Real-time event-driven coordination |
| Secondary protocol | REST | Universal access, CLI commands |
| Agent integration | MCP adapter | Native to Claude Code ecosystem |
| Port | 4740 | Waggle dance frequency reference |

## Constraints

- Task claiming uses SQLite transactions for atomic compare-and-swap (no race conditions)
- `depends_on` validated for cycle detection on create/update
- Unclaim is self-only (agent can only unclaim tasks assigned to itself)
- SMART fields (criteria, tags, estimate) are advisory, not enforced at DB level
- `.mcp.json` generation merges with existing file if present
