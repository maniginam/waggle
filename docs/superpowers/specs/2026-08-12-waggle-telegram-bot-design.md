# Waggle Telegram Bot — Design

**Date:** 2026-08-12
**Status:** Approved design, pre-implementation
**Context:** Two-way Telegram interface to Waggle, covering the agile board, the context manager, overseer alerts, and revenue/health. Built as a new in-process adapter (`internal/telegram`), sibling to `internal/mcp`. Opt-in, default off, like the overseer. Single Go binary + embedded SQLite unchanged.

## Goal

Wire Waggle into a Telegram bot Gina created, both directions:
- **Outbound:** Waggle pushes to Telegram — task/sprint/message events, overseer alerts, and scheduled digests (briefing / what's-next, revenue/health).
- **Inbound:** Gina drives Waggle from Telegram — slash commands, inline-keyboard buttons, and a natural-language fallback (Claude Haiku) for free-form messages.

## Decisions (locked)

1. **Transport: long-polling** (`getUpdates`). Waggle is a localhost daemon behind a firewall; no public webhook/HTTPS.
2. **In-process adapter that calls the local REST API** over `http://localhost:<port>` (same reuse pattern as the MCP adapter). Inherits every handler's validation and SSE emission; no duplicated store logic.
3. **Opt-in, default off:** `WAGGLE_TELEGRAM_ENABLED=true` to start it (mirrors `OVERSEER_ENABLED` in `internal/server/server.go`).
4. **NL fallback in v1** using **Claude Haiku 4.5** (`claude-haiku-4-5-20251001`), behind a swappable `NLParser` interface. Requires `ANTHROPIC_API_KEY`; if absent, NL degrades gracefully (slash/buttons still work).
5. **No new heavy dependencies.** Telegram Bot API client is a thin `net/http` + `encoding/json` wrapper. Anthropic call is a thin HTTP client too (per the claude-api skill at implementation time).

## Security (binding)

- **Token** read only from `WAGGLE_TELEGRAM_TOKEN` (env). Never hardcoded, never logged (not even masked — see global secret rule).
- **Chat-ID allowlist** from `WAGGLE_TELEGRAM_ALLOWED_CHATS` (comma-separated integer chat IDs). Every inbound update is checked against the allowlist *before* any handler runs; non-allowed chats are ignored (optionally a single "not authorized" reply, no state change).
- `ANTHROPIC_API_KEY` read from env for the NL parser only.
- All features inert until `WAGGLE_TELEGRAM_ENABLED=true`, so this changes nothing by default.

## Architecture

New package `internal/telegram`, decomposed into independently-testable units:

- **`client.go` — Telegram Bot API client.** Thin wrapper: `GetUpdates(offset, timeoutSec)`, `SendMessage(chatID, text, opts)` (opts carry an optional inline keyboard), `EditMessageText(...)`, `AnswerCallbackQuery(id, text)`. Base URL injectable (`https://api.telegram.org/bot<token>`) so tests point it at an httptest server. Never logs the token.
- **`waggle.go` — Waggle API client.** Thin wrapper over the local REST API (`http://localhost:<port>`), reused for reads and writes: list/create/move tasks, sprints + burndown, briefing, whats-next, park, revenue, health. Same style as `internal/mcp` `get`/`postJSON`/`patchJSON`.
- **`bot.go` — orchestrator.** Owns config (token, allowlist, port, enabled), constructs the two clients, starts the inbound long-poll loop and the outbound subscriber + scheduler as goroutines under a `context.Context`. Entry point `New(store, hub, cfg)` and `Run(ctx)`.
- **`inbound.go` — update handling.** Long-poll loop; allowlist gate; router that dispatches `/command` messages, `callback_query` button taps, and (fallback) free-form text through the NL parser.
- **`commands.go` — command handlers.** One function per command; each calls the Waggle API client and formats a reply (with inline keyboards where actions apply).
- **`outbound.go` — push.** Event subscriber (subscribes to `event.Hub`) + digest scheduler. Formats and sends to the allowlisted chat(s).
- **`nl.go` — NL parser.** `NLParser` interface (`Parse(ctx, text) (Intent, error)`); `ClaudeNLParser` implementation using Claude Haiku with a tool/JSON-schema-constrained output mapping to the known action set. A `fakeNLParser` is used in tests.
- **Server wiring** in `internal/server/server.go`: `if os.Getenv("WAGGLE_TELEGRAM_ENABLED") == "true" { tg := telegram.New(s.store, s.eventHub, telegram.ConfigFromEnv()); go tg.Run(ctx) }`.

## Inbound: commands

Slash commands (final names/set finalized in the plan) mapped to Waggle API calls:

| Command | Action |
|---|---|
| `/next` | what's-next across projects (urgency-sorted digest) |
| `/tasks [project]` | list tasks; each card carries inline buttons |
| `/sprint [project]` | active sprint summary + burndown tail |
| `/move` | via inline button on a task card → `POST /api/tasks/{id}/move` |
| `/create <title>` | create a task (project via follow-up or default) |
| `/park <project> <note>` | park a project |
| `/health` | project health roll-up |
| `/revenue` | revenue digest |
| `/help` | list commands |

**Inline keyboards:** a task card renders buttons (e.g. `Doing`, `Review`, `Done`, `Complete`). A tap sends a `callback_query` with compact `callback_data` (e.g. `mv:<taskID>:<status>`); the handler calls the API, edits the message to reflect the new state, and `answerCallbackQuery` acks. `callback_data` is ≤64 bytes (Telegram limit) — use short task-id prefixes if needed.

**NL fallback:** any allowlisted message that is not a slash command and not a button goes to `NLParser.Parse`, which returns `Intent{Action string, Args map[string]string}` constrained to the known action set. The intent routes into the same command handlers. Unrecognized/low-confidence → a friendly "didn't catch that, try /help" reply. If `ANTHROPIC_API_KEY` is unset, NL is disabled and the same fallback reply is returned.

## Outbound: events + digests

- **Event subscriber:** subscribes in-process to `event.Hub` (the hub the API publishes to, and which overseer already publishes its change events into). A curated formatter turns relevant events into messages: task created/completed, sprint state changes, direct messages, and overseer alerts. Noise control: only a whitelisted subset of event types is pushed (not every `task_updated`).
- **Loop suppression:** actions the bot itself initiates (a `/move`, a button tap) would otherwise echo back as an event. The bot keeps a short-TTL set of `(taskID, action)` it just issued and the subscriber skips a matching event once, so Gina isn't pinged for her own Telegram action.
- **Digest scheduler:** time-based sends (configurable via env, sensible defaults) for the daily briefing / what's-next and a revenue/health roll-up. The clock is injected behind an interface so tests are deterministic.
- All outbound goes only to allowlisted chat IDs.

## Error handling

- Long-poll `getUpdates` failures: log and backoff-retry; never crash the daemon (goroutine is panic-isolated like the overseer poller).
- Telegram send failures: logged, dropped (best-effort); a failed notification never blocks Waggle.
- NL parser errors / missing key: degrade to the fallback reply.
- Allowlist rejects are silent (no state change), optionally one canned reply.

## Testing (TDD)

- **client_test.go:** httptest stand-in for `api.telegram.org`; assert `getUpdates`/`sendMessage`/inline-keyboard payloads are well-formed; token never appears in logs.
- **inbound/commands tests:** point the Waggle API client at a real in-process `api` httptest server (as `mcp_test.go` does); feed synthetic updates; assert the correct API calls fire and replies/keyboards are correct.
- **allowlist test:** update from a non-allowlisted chat produces no API call.
- **outbound tests:** publish events to a hub; assert the subscriber formats + sends the whitelisted subset and suppresses self-initiated echoes; digest formatting tested with an injected clock.
- **nl_test.go:** `fakeNLParser` returns canned intents; assert routing. The live `ClaudeNLParser` is exercised behind a mocked HTTP transport (no live Anthropic calls in tests).

## Out of scope (v1)

- Webhook transport (long-poll only).
- Multi-user / per-user permissions beyond the flat allowlist.
- Rich media (charts/images) — text + inline keyboards only; burndown stays textual.
- Editing pages/docs (sub-project B) or generic DB (C) over Telegram — those features don't exist yet.

## Rollout / safety

- Additive: new package + one opt-in wiring block in `server.go`. Nothing runs unless `WAGGLE_TELEGRAM_ENABLED=true` and a token is present.
- No schema changes, no new persistent tables in v1 (allowlist/token are env; if per-chat state is later needed it reuses the existing `settings` table).
- Reversible by unsetting the env var.
