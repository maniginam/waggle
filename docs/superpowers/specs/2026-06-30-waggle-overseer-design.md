# Waggle Overseer (SP-0): Read-Only Adapters for Colony + Gas Town

**Date**: 2026-06-30
**Status**: PROPOSAL
**Author**: waggle session (user-directed)

## Context

The user wanted "Adjutant's autonomy + ambition + integration." Investigation found
the autonomy already exists and is more mature than Adjutant's:

- **Colony** (Clojure daemon, `~/projects/maniginam/colony`) already runs an autonomous
  revenue/ROI operator — daemon tick, priority queue, worker spawning via `claude -p`,
  stall detection, adaptive ROI brain, self-learning memory, budget caps, CPU throttling,
  trust-ladder permissions. It runs headless today.
- **Gas Town** (`gt`, Homebrew binary) is a mature multi-agent *coding-team* orchestrator —
  spawning, `sling` dispatch, Deacon/Witness watchdogs, Refinery merge queue, `gt dashboard`,
  `gt trail`, `gt peek`, multi-account `gt quota`.

The gap is not autonomy. It is **observability + steer + 24/7 phone access** — one pane of
glass over the engines. The user's hard constraint: **do not disrupt Colony, Waggle-core,
or ROI** (Colony makes money; a bug there is expensive).

That constraint dictates the design: the Overseer is **read-only and non-invasive**. It
*consumes* what the engines already emit (databases, status files, CLI JSON) and never
reaches into their internals. A read-mostly aggregator cannot break a money-making daemon,
because it only reads. This also honors the 2026-06-11 pivot
(`2026-06-11-waggle-pivot-context-manager.md`), which already designated Waggle as
"the read-side of all automated work."

## Scope of SP-0 (this spec)

SP-0 is the **data backbone only**: pull Colony and Gas Town state into Waggle's existing
event stream as normalized events. Nothing else.

**In scope:**
- A generic `Source` adapter interface in Waggle.
- A **Colony adapter** (read-only): reads `colony.db` (SQLite) + tails STATUS files.
- A **Gas Town adapter** (read-only): shells `gt trail --json` / `gt agents --json`.
- A **normalizer** mapping engine-specific state into Waggle's `model.Event` + `emit()`.
- A **poller/scheduler** that drives adapters on intervals, with backoff + dedup.

**Explicitly OUT of SP-0 (later sub-projects):**
- SP-1: unified live mission-control UI + mobile-first PWA polish + Tailscale 24/7 access.
- SP-2: steer-out (approve red-tier, nudge) through each engine's existing safe channel.
- SP-3: chat with workers.

SP-0 changes **only Waggle**. Colony and Gas Town are untouched.

## Design Principle

**The Overseer reads; the engines run.** Every adapter is read-only. The Overseer holds
no authority over the engines in SP-0. If the Overseer crashes, the engines do not notice.
If an engine is down, the adapter degrades to empty results, never blocks.

## Architecture / Data Flow

```
COLONY (untouched)                 GAS TOWN (untouched)
  colony.db (SQLite, WAL)            gt trail --json
  STATUS-*.md files                  gt agents --json
        │  (read-only)                     │  (exec, read-only)
        ▼                                  ▼
  ColonyAdapter.Poll()             GasTownAdapter.Poll()
        │                                  │
        └──────────────┬───────────────────┘
                       ▼
                  Normalizer  → model.Event{Type, AgentID, TaskID, Payload, Timestamp}
                       │  (dedup by stable id; only emit on change)
                       ▼
              Waggle a.emit(evt)  ──►  GET /api/events  (SSE, Last-Event-ID replay)
                       │
                       ▼
              (SP-1 dashboard / PWA consume this)
```

## Components (each independently testable)

### 1. `internal/overseer/source.go` — adapter interface
```go
type Snapshot struct {
    Events []*model.Event // normalized, stable-id'd
}

type Source interface {
    Name() string                       // "colony", "gastown"
    Poll(ctx context.Context) (Snapshot, error) // read-only, must never block long
}
```
- Pure contract. No I/O of its own. Dependency: `model`.

### 2. `internal/overseer/colony.go` — Colony adapter (read-only)
- Opens `colony.db` with `sql.Open("sqlite", path+"?mode=ro&_busy_timeout=2000")` — **read-only
  mode**, short busy timeout, so it can never contend with the daemon's writes.
- Reads: `tasks` (id, title, status, priority, project, updated_at), worker registry rows if
  present, recent `progress`/events table rows.
- Tails `STATUS-*.md` only for a `parking_note`-style latest-summary signal (optional, best-effort).
- Maps rows → `model.Event` (see Event Mapping). Path is config-driven
  (`COLONY_DB_PATH`, default `~/projects/maniginam/colony/colony.db`).
- Degrades to empty `Snapshot` if the file is absent/locked. Never errors fatally.
- Dependencies: `database/sql`, `model`. Testable with a fixture SQLite db.

### 3. `internal/overseer/gastown.go` — Gas Town adapter (read-only)
- Shells `gt trail --json --since <interval> --limit N` and `gt agents --json` via
  `exec.CommandContext` with a hard timeout (e.g. 5s).
- Parses JSON → `model.Event`. If `gt` is missing or errors, returns empty `Snapshot`.
- Never writes; never runs a mutating `gt` subcommand. An allowlist of permitted argv is
  enforced in code (only `trail`, `agents`, later `peek`).
- Dependencies: `os/exec`, `encoding/json`, `model`. Testable by injecting a fake exec runner.

### 4. `internal/overseer/normalize.go` — event mapping + dedup
- Assigns **stable ids** so re-polling the same state does not re-emit: e.g.
  `colony:task:<id>:<status>`, `gastown:agent:<name>:<state-hash>`.
- Keeps a small in-memory `seen` set (LRU/size-capped) so only **changes** emit to SSE.
- Pure function given (rows/json, seen) → (events, newSeen). Heavily unit-tested.

### 5. `internal/overseer/overseer.go` — the poller
- Holds the registered `Source`s and per-source poll intervals (Colony fast ~3s, Gas Town
  ~10s; configurable).
- On each tick: `Poll` → normalize/dedup → `a.emit` each new event. Per-source panics/errors
  are caught and logged; one bad source never stops the others or the server.
- Started from `server.go` as a goroutine alongside the existing SSE hub. Off by default
  behind a config flag `OVERSEER_ENABLED` (so existing Waggle behavior is unchanged unless
  opted in).

## Event Mapping (the contract — generic, model-agnostic)

Reuse `model.EventType`. Generic verbs only; no "Claude"/engine-internal leakage:

| Source state | Event Type | AgentID | Payload |
|---|---|---|---|
| Colony task status change | `task.<status>` (`queued/running/completed/failed`) | — | `{source:"colony", project, title, priority}` |
| Colony worker registered/heartbeat/stalled | `worker.<state>` | worker id | `{source:"colony", task_id}` |
| Colony permission row pending | `permission.pending` | — | `{source:"colony", action, project}` |
| Colony ROI brain cycle | `brain.cycle` | — | `{source:"colony", queued, interval}` |
| Gas Town trail commit | `agent.commit` | agent | `{source:"gastown", rig, bead, msg}` |
| Gas Town agent state | `worker.<state>` | polecat | `{source:"gastown", rig}` |

`source` in the payload lets the UI group/filter by engine while the event types stay generic.

## Configuration
```
OVERSEER_ENABLED=true
COLONY_DB_PATH=~/projects/maniginam/colony/colony.db
COLONY_STATUS_GLOB=~/projects/maniginam/colony/STATUS-*.md
GASTOWN_BIN=gt
OVERSEER_COLONY_INTERVAL=3s
OVERSEER_GASTOWN_INTERVAL=10s
```

## Error Handling (non-negotiable invariants)
1. **Read-only, always.** Colony db opened `mode=ro`; `gt` argv allowlist excludes mutations.
2. **Fire-and-forget.** Adapter errors are logged and swallowed; the poller continues.
3. **No contention.** Short busy timeouts on Colony db; the daemon's writes always win.
4. **Opt-in.** `OVERSEER_ENABLED=false` (default) = Waggle behaves exactly as today.
5. **Degraded = empty.** Missing engine → empty Snapshot, never a crash, never a fatal log.

## Testing (TDD, per repo convention)
- `source` contract: a fake Source emits → poller emits to a captured hub.
- `colony` adapter: against a fixture `colony.db` (read shapes from the real schema) →
  asserts correct events; locked/absent db → empty Snapshot.
- `gastown` adapter: injected fake exec returning canned `gt ... --json` → asserts events;
  `gt` missing/error → empty; mutating argv → rejected by allowlist test.
- `normalize`: same state twice → emits once (dedup); changed state → re-emits; stable-id
  format locked by test.
- `overseer` poller: one panicking source does not stop the other; emits land on the hub.

Capture real fixture shapes first (`sqlite3 colony.db .schema`, `gt trail --json`) so tests
exercise reality, not assumed shapes.

## Boundaries / Isolation
- `source.go` — pure contract.
- `colony.go` / `gastown.go` — I/O adapters, swappable, no cross-dependency.
- `normalize.go` — pure, the brain of dedup.
- `overseer.go` — orchestration only; depends on the above + existing `emit`.
- **Zero changes** to `internal/store`, `internal/mcp`, Colony, or Gas Town.

## What This ISN'T
- Not a change to Colony, ROI, or Gas Town (read-only).
- Not the dashboard UI (SP-1) — SP-0 only fills the event stream.
- Not steer/control (SP-2) or chat (SP-3).
- Not a new system — it lives inside Waggle, the already-designated read-side.

## Success Criteria
1. `OVERSEER_ENABLED=true` → `GET /api/events` shows live Colony task/worker/brain events.
2. Gas Town `trail`/`agents` activity appears in the same stream, tagged `source:"gastown"`.
3. With `OVERSEER_ENABLED=false`, Waggle is byte-for-byte unchanged in behavior.
4. Killing/restarting Waggle never affects Colony or Gas Town.
5. Colony db remains uncontended (no new lock errors in the daemon log during polling).

## Roadmap (after SP-0)
- **SP-1**: unified mission-control UI consuming the stream; mobile-first PWA; Tailscale 24/7.
- **SP-2**: steer-out via existing safe channels (Colony permission-queue row, `gt nudge`) + optional push.
- **SP-3**: chat with workers over Colony IPC chat + `gt mail`, surfaced in Waggle.
