# Model-Agnostic Agent Bridges

Date: 2026-04-17

## Problem

Waggle's agent coordination (tasks, messaging, heartbeats, lifecycle) is model-agnostic by design, but the only way to connect an agent today is through Claude Code's MCP stdio adapter. Other models (GPT, Gemini, Grok, etc.) can't participate in the swarm without a custom integration.

## Solution

Add bridge agents as a first-class agent type within Waggle. A bridge is an in-process goroutine that connects a model provider's chat API to Waggle's REST API, letting any LLM register, message, claim tasks, and participate alongside Claude Code agents.

No new CLI commands. Bridges integrate with the existing spawn/team system — `type: "openai"` instead of `type: "claude-code"`.

## Providers

Six providers, four distinct implementations:

| Provider | Type String | Env Var | Default Model | Capabilities | Implementation |
|----------|------------|---------|---------------|-------------|----------------|
| OpenAI | `openai` | `OPENAI_API_KEY` | gpt-4o | chat, code, vision | OpenAI-compatible |
| Gemini | `gemini` | `GOOGLE_API_KEY` | gemini-2.5-pro | chat, code | Gemini |
| Grok | `grok` | `XAI_API_KEY` | grok-3 | chat, image_gen | OpenAI-compatible |
| Claude API | `claude-api` | `ANTHROPIC_API_KEY` | claude-sonnet-4-6 | chat, code | Claude |
| Ollama | `ollama` | (none) | llama3 | chat, code | OpenAI-compatible |
| Bedrock | `bedrock` | AWS credentials | claude-sonnet-4-6 | chat, code | Bedrock |

OpenAI, Grok, and Ollama share the OpenAI-compatible chat completions format (different base URLs and auth). This cuts six providers down to four implementations.

## Architecture

```
+---------------------------------------------+
|                Waggle Server                 |
|  REST API - WebSocket - Event Hub - Store    |
+------+----------+----------+----------+------+
       |          |          |          |
  Claude Code   Bridge    Bridge     Bridge
  (MCP/stdio)  (OpenAI)  (Gemini)   (Grok)
       |          |          |          |
  tmux/shell   goroutine  goroutine  goroutine
                  |          |          |
             openai API  google API  xai API
```

Bridge agents run as goroutines managed by the spawn system. They use the same REST API as any other client — register, heartbeat, message, claim/complete tasks.

## Core Interface

```go
type Bridge interface {
    Chat(ctx context.Context, messages []Message, opts ChatOpts) (string, error)
    Capabilities() []Capability
    Provider() string
}

type Capability string
const (
    CapChat     Capability = "chat"
    CapCode     Capability = "code"
    CapImageGen Capability = "image_gen"
    CapVision   Capability = "vision"
)

type ChatOpts struct {
    Model        string
    MaxTokens    int
    Temperature  float64
    SystemPrompt string
}

type Message struct {
    Role    string // "system", "user", "assistant"
    Content string
}
```

## Participation Modes

Bridge agents operate in one of two modes:

- **message_only** (default): Agent responds when messaged by another agent. Does not claim tasks. Use case: "Hey grok, generate a hero image" -> Grok responds with the result.

- **full_participant**: Agent also claims tasks from the pool, filtered by capability match. A task tagged `image_gen` could be auto-claimed by the Grok bridge. Use case: Gemini 2.5 Pro driving implementation tasks.

Mode is set at spawn time and can be changed via the REST API.

## Runner Event Loop

```go
type Runner struct {
    bridge     Bridge
    agentName  string
    baseURL    string
    mode       Mode
    projectID  string
    httpClient *http.Client
}

type Mode string
const (
    ModeMessageOnly     Mode = "message_only"
    ModeFullParticipant Mode = "full_participant"
)
```

Loop cadence:
- Every 1s: poll for new messages, translate to `Chat()`, post response
- Every 5s: (full_participant only) check for available tasks, claim if capabilities match
- Every 30s: send heartbeat
- On message: call `Chat()` with conversation history, reply via Waggle messaging
- On task claim: build prompt from task title + description + criteria, call `Chat()`, post result as task comment, complete task

## Spawn Integration

The spawn API accepts bridge agents:

```
POST /api/spawn
{
  "name": "gpt-coder",
  "type": "openai",
  "project_id": "wg-d2b49a",
  "mode": "full_participant",
  "model": "gpt-4o",
  "system_prompt": "You are a senior Go developer"
}
```

Spawn handler routing:
- `type == "claude-code"` -> existing tmux launch path (unchanged)
- any other type -> look up provider in registry, validate env var, start Runner goroutine

Dashboard spawn UI gets a model-type dropdown. When a non-Claude type is selected, it shows mode, model override, and system prompt fields.

## Leader Role

Any agent type can be a leader. Claude is default, but spawning with `role: "leader"` assigns leadership to that bridge agent. The runner adds leader behaviors (task dispatch, status checks) when the role is leader or alpha.

## Agent Context

Bridge agents operate with conversation context only — no file access, no shell. They receive:
- Task descriptions and acceptance criteria
- Messages from other agents
- Their own conversation history

This covers the primary use cases (image gen, code review, answering questions, writing docs) without the complexity of remote code execution.

## Shutdown

When a bridge agent is stopped (dashboard, CLI, or context cancellation):
1. Runner context is cancelled
2. Current task is unclaimed or completed
3. `disconnect` is called on the REST API
4. Goroutine exits

Same lifecycle as Claude Code agents, just no tmux to kill.

## API Key Management

Environment variables only. Standard convention, no config files:
- `OPENAI_API_KEY`, `GOOGLE_API_KEY`, `XAI_API_KEY`, `ANTHROPIC_API_KEY`
- Ollama needs no key (local)
- Bedrock uses `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_REGION`

Spawn fails fast with a clear error if the required env var is missing.

## Package Structure

```
internal/bridge/
  bridge.go          # Bridge interface, Message, ChatOpts, Capability types
  runner.go          # Runner event loop, lifecycle
  runner_test.go
  registry.go        # Provider registry: type string -> constructor
  registry_test.go
  openai/
    openai.go        # OpenAI-compatible (OpenAI, Grok, Ollama)
    openai_test.go
  gemini/
    gemini.go
    gemini_test.go
  claude/
    claude.go        # Anthropic native messages API
    claude_test.go
  bedrock/
    bedrock.go       # Anthropic format + Sigv4 signing
    sigv4.go         # AWS Sigv4 request signing
    bedrock_test.go
```

Registry maps type strings to constructors:
- `"openai"` -> `openai.New("https://api.openai.com/v1", $OPENAI_API_KEY)`
- `"grok"` -> `openai.New("https://api.x.ai/v1", $XAI_API_KEY)`
- `"ollama"` -> `openai.New("http://localhost:11434/v1", "")`
- `"gemini"` -> `gemini.New($GOOGLE_API_KEY)`
- `"claude-api"` -> `claude.New($ANTHROPIC_API_KEY)`
- `"bedrock"` -> `bedrock.New(region, accessKey, secretKey)`

## Out of Scope (Future)

- File/shell access for bridge agents (remote code execution)
- Streaming responses
- Multi-modal responses (image bytes in messages)
- Bridge-specific MCP adapters
- Cost tracking per provider (model pricing already exists in model.go)
