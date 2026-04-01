# Drop tmux Dependency

## Problem

Waggle spawns agents via tmux sessions and uses tmux for terminal I/O. Agents that self-register via MCP (without being spawned by Waggle) show "No tmux session found" errors. tmux is an unnecessary dependency — MCP messaging handles all agent communication.

## Design

### Spawn: Child Process + Log File

Replace `tmux new-session` with `exec.Command` running claude as a child process. Stdout/stderr pipe to `~/.waggle/logs/<agent-name>.log`.

- Server holds an in-memory `map[string]*exec.Cmd` tracking spawned processes
- Log directory: `~/.waggle/logs/` (created on first spawn)
- Log path: `~/.waggle/logs/<agent-name>.log` (deterministic, no DB storage)
- Launch script written to temp file (same as today), executed directly
- `CLAUDECODE` env var filtered out (same as today)
- On spawn: old log file truncated, process started, cmd stored in map

### Reaper: Heartbeat-Only

Remove `tmuxChecker` and `tmuxSessionAlive`. The reaper logic becomes:

```
for each non-disconnected agent:
  if last_seen < cutoff:
    disconnect agent (sets status=disconnected, unassigns tasks)
```

No tmux probing. 90s stale timeout unchanged. 24h purge of disconnected agents unchanged.

### Terminal Tab: Read-Only Log Viewer

- `GET /api/agents/<name>/log?lines=80` tails the log file and returns content
- Terminal tab displays log output, auto-refreshes every 3s
- No "Send" input — terminal is read-only
- If no log file exists (self-registered agent): shows "Use Comms tab" message

### Sessions Endpoints

- `GET /api/sessions` — returns list of spawned agents from the in-memory map (name, started_at, pid, has_log)
- `DELETE /api/sessions/<name>` — kills process via `cmd.Process.Kill()`, removes from map
- `GET /api/sessions/<name>/output?lines=N` — reads last N lines from log file
- Remove: `POST /api/sessions/<name>/send` (no more stdin injection)

### Sessions View (Dashboard)

Shows all agents grouped by status instead of tmux sessions. Spawned agents show log output preview. Self-registered agents show last message. "Spawn Agent" button unchanged.

### Kill Flow

1. Look up process in `map[string]*exec.Cmd`
2. Call `cmd.Process.Kill()`
3. Remove from map
4. Agent's heartbeat stops, reaper disconnects it after 90s (or immediate disconnect via API)

### What Gets Removed

- All `exec.Command("tmux", ...)` calls
- `tmuxChecker` field on Server struct
- `tmuxSessionAlive()` function
- `tmuxSessions` JS array and all tmux session polling
- Terminal tab "Send" input bar
- `send-keys` injection from sendAgentPrompt, sendChat, broadcast

### What Stays

- `/api/spawn` endpoint (different backend)
- Terminal tab (read-only log viewer)
- Comms tab (all messaging)
- Reaper (simplified)
- Sessions view (adapted)
- Prompt tab quick actions

### Edge Cases

- **Waggle restart**: In-memory process map is lost. Orphaned claude processes eventually stop heartbeating and get reaped after 90s. Log files persist and remain viewable.
- **Agent crash**: Process exits, heartbeats stop, reaper disconnects after 90s. Log file shows what happened.
- **Concurrent spawn with same name**: Kill existing process first (same as today's tmux kill-session).

### Files Changed

| File | Changes |
|------|---------|
| `internal/server/server.go` | Remove tmuxChecker, add process map, simplify reaper |
| `internal/api/api.go` | Rewrite spawn (child process + log), rewrite sessions (process map + log file), add log endpoint, remove send endpoint |
| `internal/dashboard/static/index.html` | Terminal read-only, remove tmuxSessions, adapt sessions view, remove send-keys injection |
| `internal/ws/hub.go` | Remove tmux disconnect check if present |
| `internal/server/server_test.go` | Update reaper tests (no tmux mock) |
| `internal/api/api_test.go` | Update spawn/session tests |
