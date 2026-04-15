# Waggle

Model-agnostic AI agent orchestration. Coordinate multiple AI coding agents with real-time task management, messaging, and a live dashboard.

Named after the [honeybee waggle dance](https://en.wikipedia.org/wiki/Waggle_dance) -- the communication protocol bees use to coordinate their hive.

## Features

- **Real-time dashboard** -- Kanban board, agent monitoring, messaging, code review
- **Multi-agent coordination** -- WebSocket-based task claiming, heartbeat, auto-dispatch
- **SMART task management** -- priorities, dependencies, acceptance criteria, deadlines
- **MCP integration** -- 27 tools for Claude Code (works with any MCP-compatible client)
- **CLI** -- Full-featured command line for task, agent, and project management
- **Desktop app** -- Native macOS app via Tauri (system tray, notifications)
- **Zero dependencies** -- Single Go binary, embedded SQLite, no external services

## Installation

### Homebrew (macOS)

```bash
brew tap maniginam/waggle
brew install waggle
```

### Quick Install (macOS / Linux)

```bash
curl -fsSL https://raw.githubusercontent.com/maniginam/waggle/master/install.sh | sh
```

### From Source

```bash
go install github.com/maniginam/waggle/cmd/waggle@latest
```

### Prebuilt Binaries

Download from [GitHub Releases](https://github.com/maniginam/waggle/releases).

## Quick Start

```bash
go install github.com/maniginam/waggle/cmd/waggle@latest

waggle start                                    # start server on :4740
open http://localhost:4740                       # open the dashboard
waggle task add "Build auth module" --priority high
waggle connect                                  # generate .mcp.json for Claude Code
```

## Architecture

Single Go binary, embedded SQLite, zero external dependencies.

```
Agents --WebSocket---+
                     +-- Waggle Server -- SQLite (~/.waggle/waggle.db)
CLI ----REST API-----+
                     |
Claude Code --MCP----+
```

- **WebSocket** (`ws://localhost:4740/ws`) -- real-time event coordination, heartbeat, task claiming
- **REST API** (`http://localhost:4740/api`) -- CRUD operations, stats, usage tracking
- **MCP Adapter** (`waggle mcp`) -- stdio transport for Claude Code and compatible clients
- **Dashboard** (`http://localhost:4740`) -- Kanban board, agent monitoring, messaging

## MCP Integration

Connect any MCP-compatible AI coding agent to Waggle:

```bash
waggle connect    # generates .mcp.json in current directory
```

This exposes 27+ tools including task management, messaging, project tracking, and agent coordination. Agents can register, claim tasks, report status, send messages, and coordinate with other agents.

## SMART Tasks

Every task supports the SMART framework:

- **Specific**: title + description
- **Measurable**: acceptance criteria
- **Achievable**: priority + dependency tracking
- **Relevant**: tags for categorization
- **Time-bound**: estimates + deadlines

```bash
waggle task add "Implement OAuth2" \
  --priority high \
  --criteria "all tests pass" \
  --tag auth \
  --estimate 2h \
  --deadline 2026-04-20
```

## CLI Reference

```
SERVER
  waggle start [--port 4740]       Start the server
  waggle stop                      Stop the server
  waggle status                    Server status + connected agents
  waggle mcp                       Start MCP stdio adapter
  waggle connect                   Generate .mcp.json for Claude Code

TASKS
  waggle task add "title" [flags]  Create a task
  waggle task list [--status X]    List tasks (also --priority, --tag, --search/-q)
  waggle task next [--tag X]       Show highest-priority ready task
  waggle task show <id>            Show task detail
  waggle task update <id> [flags]  Update a task
  waggle task claim <id>           Claim a task
  waggle task done <id>            Mark task complete
  waggle task rm <id>              Delete a task

AGENTS
  waggle agent show <name>         Show agent detail
  waggle agents                    List connected agents
  waggle watch [--agent X]         Tail event stream

MESSAGES
  waggle msg send <agent> "msg"    Send a message
  waggle msg list [agent]          List messages

PROJECTS
  waggle project add "name"        Create a project
  waggle project list              List projects

CONFIG
  waggle config [key] [value]      Get/set configuration
  waggle backup                    Backup database
  waggle reset                     Wipe database
```

## REST API

```
POST   /api/tasks                Create task
GET    /api/tasks                List tasks (?status=&assignee=&priority=&tag=&q=&project_id=)
GET    /api/tasks/:id            Get task
PATCH  /api/tasks/:id            Update task
DELETE /api/tasks/:id            Delete task
POST   /api/tasks/:id/claim      Claim task
POST   /api/tasks/:id/unclaim    Unclaim task
POST   /api/tasks/:id/complete   Complete task (auto-unblocks dependents)
GET    /api/tasks/:id/comments   List comments
POST   /api/tasks/:id/comments   Add comment
GET    /api/tasks/:id/history    Task event history
GET    /api/tasks/:id/deps       Dependency graph

POST   /api/projects             Create project
GET    /api/projects             List projects
GET    /api/projects/:id         Get project
PATCH  /api/projects/:id         Update project
DELETE /api/projects/:id         Delete project

POST   /api/agents/register      Register agent
GET    /api/agents               List agents
POST   /api/agents/:name/status  Update status

POST   /api/messages             Send message
GET    /api/messages?to=<name>   Read messages

GET    /api/stats                Dashboard stats
POST   /api/usage                Report token usage
GET    /api/usage                Token usage summary
GET    /health                   Health check
WS     /ws                       WebSocket endpoint
```

## Desktop App

Waggle includes a native macOS desktop app built with Tauri:

```bash
make app    # build the .app bundle
make dmg    # create a .dmg installer
```

Features: system tray, native notifications, auto-starts the server.

See [desktop app docs](docs/superpowers/specs/2026-04-14-desktop-app-design.md) for details.

## Development

```bash
git clone https://github.com/maniginam/waggle.git
cd waggle
make build          # build binary
make test           # run tests
make test-cover     # test with coverage report
make run            # build and start server
make install        # install to $GOPATH/bin
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for contribution guidelines.

## License

[MIT](LICENSE)
