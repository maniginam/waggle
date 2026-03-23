# Waggle

Model-agnostic AI agent orchestration. SMART task coordination for multiple AI coding agents via WebSocket, REST API, and MCP.

Named after the honeybee waggle dance — the communication protocol bees use to tell other bees where to go and what to do.

## Quick Start

```bash
go install github.com/maniginam/waggle/cmd/waggle@latest

waggle start          # start the server on :4740
waggle task add "Implement auth module" --priority high --criteria "all tests pass"
waggle agents         # list connected agents
```

## Architecture

Single Go binary, embedded SQLite, zero external dependencies.

```
Agents ──WebSocket──┐
                    ├── Waggle Server ── SQLite (~/.waggle/waggle.db)
CLI ────REST API────┤
                    │
Claude Code ──MCP───┘
```

- **WebSocket** (`ws://localhost:4740/ws`) — real-time event coordination
- **REST API** (`http://localhost:4740/api`) — CRUD operations
- **MCP Adapter** (`waggle mcp`) — stdio transport for Claude Code

## MCP Integration

Connect Claude Code to a running Waggle server:

```bash
waggle connect    # generates .mcp.json in current directory
```

This exposes 18 tools: `waggle_register_agent`, `waggle_create_task`, `waggle_list_tasks`, `waggle_show_task`, `waggle_update_task`, `waggle_claim_task`, `waggle_unclaim_task`, `waggle_complete_task`, `waggle_delete_task`, `waggle_get_next_task`, `waggle_task_history`, `waggle_list_subtasks`, `waggle_add_comment`, `waggle_list_comments`, `waggle_list_agents`, `waggle_set_status`, `waggle_send_message`, `waggle_read_messages`.

## SMART Tasks

Every task supports the SMART framework:

- **Specific**: title + description
- **Measurable**: acceptance criteria
- **Achievable**: priority + dependency tracking
- **Relevant**: tags for categorization
- **Time-bound**: estimates + deadlines

## CLI Reference

```
waggle start [--port 4740]       Start the server
waggle stop                      Stop the server
waggle status                    Server status + connected agents
waggle mcp                       Start MCP stdio adapter
waggle connect                   Generate .mcp.json for Claude Code

waggle task add "title" [flags]  Create a task
waggle task list [--status X]    List tasks (also --priority, --tag, --search/-q)
waggle task next [--tag X]       Show highest-priority ready task
waggle task show <id>            Show task detail
waggle task update <id> [flags]  Update a task
waggle task claim <id>           Claim a task
waggle task done <id>            Mark task complete
waggle task rm <id>              Delete a task

waggle agent show <name>         Show agent detail
waggle agents                    List connected agents
waggle watch [--agent X]         Tail event stream
waggle msg send <agent> "msg"    Send a message
waggle msg list [agent]          List messages

waggle config [key] [value]      Get/set configuration
waggle backup                    Backup database
waggle reset                     Wipe database
```

## REST API

```
POST   /api/tasks                Create task
GET    /api/tasks                List tasks (?status=&assignee=&priority=&tag=&q=)
GET    /api/tasks/:id            Get task
PATCH  /api/tasks/:id            Update task
DELETE /api/tasks/:id            Delete task (rejects if in_progress)
POST   /api/tasks/:id/claim      Claim task
POST   /api/tasks/:id/unclaim    Unclaim task (self-only)
POST   /api/tasks/:id/complete   Complete task (auto-unblocks dependents)
GET    /api/tasks/:id/comments  List task comments
POST   /api/tasks/:id/comments  Add comment (author, body)
GET    /api/tasks/:id/history   Task event history
GET    /api/tasks/:id/subtasks  Subtasks with progress (done/total)
POST   /api/agents/register      Register agent
GET    /api/agents               List agents
GET    /api/agents/:id           Get agent
POST   /api/agents/:name/status  Update agent status
POST   /api/messages             Send message
GET    /api/messages?to=<name>   Read messages
GET    /api/events               List events (or SSE with Accept: text/event-stream)
GET    /api/stats                Dashboard stats (tasks/agents/messages summary)
GET    /health                   Health check
WS     /ws                       WebSocket endpoint
```

## Development

```bash
make build          # build binary
make test           # run tests
make test-cover     # test with coverage report
make run            # build and start server
make install        # install to $GOPATH/bin
```

## License

MIT
