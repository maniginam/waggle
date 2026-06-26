# Waggle Voice — Design (Sub-project #1)

**Date:** 2026-06-25
**Status:** Approved design, pre-implementation
**Author:** Gina + Claude

## Origin

Inspired by a realtime voice-avatar demo ("Adjutant"): you speak, an AI answers
in voice, in character, in real time. Gina wants Waggle to do "this kind of
automation" — but with Waggle's own persona (not Adjutant's military framing),
Claude as the brain, and reaching into Waggle's context and (later) dev work.

## Full Vision (for context — NOT all in this sub-project)

Three subsystems, built in order:

1. **Voice loop + persona + read-only briefings** ← THIS SPEC (#1)
2. Waggle write-actions by voice (create/complete tasks, log, park, send messages)
3. Drive dev work by voice (kick off Claude Code sessions, run commands)

Entry points, in eventual priority order: dashboard browser (first), terminal,
phone + phone messaging, always-on wake-word. This spec covers **dashboard
browser only**.

## Scope of Sub-project #1

Talk into Mission Control (localhost:4740). Waggle answers in voice, in its own
persona, speaking back **existing** context: briefing, what's-next, project
status, messages. **Read-only.** No write-actions. No dev-driving. No avatar
face (text + voice only).

Everything lives in the existing Go daemon. No new app, no new process.

## Stack (decided)

**B — STT → Claude → TTS pipeline.** Claude is the brain (matches Gina's
all-Claude setup and the later dev-driving goal). Providers swap behind
interfaces.

- STT: Deepgram streaming (primary); whisper.cpp local (fallback, already installed)
- Brain: Claude (Anthropic API)
- TTS: ElevenLabs or Cartesia streaming

Rejected: OpenAI Realtime (brain = GPT-4o, ~$90–150/mo); Local-only (weaker
persona/quality — kept as the whisper fallback only).

## Architecture

```
Browser (mission-control.html)
  mic capture ──▶ ┐
                  │  websocket  /ws/voice  (binary audio frames + json control)
  speaker  ◀──────┘
        │
   Go daemon
        ├─ voicews     transport: frame audio in/out over ws
        ├─ stt         interface → Deepgram (streaming); whisper.cpp local fallback
        ├─ voiceagent  STT text → Claude (persona prompt + read-only Waggle tools) → reply text
        └─ tts         interface → ElevenLabs/Cartesia → audio frames back
```

### Units (each one job, swappable behind interfaces — DIP)

| Unit | Does | Depends on |
|------|------|------------|
| `voicews` | ws transport: frame audio in/out, json control msgs, reconnect | stt, voiceagent, tts |
| `stt` | `Transcribe(audioStream) -> textStream`. Interface. Deepgram + whisper impls | provider API / local binary |
| `tts` | `Synthesize(text) -> audioStream`. Interface. ElevenLabs/Cartesia impls | provider API |
| `voiceagent` | transcript → Claude (persona + read-only tools) → reply text | Claude API, store (read funcs only) |
| browser mic client | capture mic, stream frames, play audio, show transcript | Web Audio / MediaRecorder, ws |

## Data Flow (one turn)

1. Browser captures mic → streams ~20ms PCM/Opus frames over ws while speaking.
2. `voicews` forwards frames to `stt`; Deepgram streams partial + final transcript.
3. On final transcript (silence/endpointing), `voiceagent` calls Claude with:
   persona system prompt + recent turn history + read-only tool defs.
4. Claude answers directly OR calls a read tool (`briefing`, `whats_next`,
   `project_status`, `read_messages`) → `voiceagent` runs the store func → feeds
   result back → Claude composes spoken reply.
5. Reply text → `tts` streams audio frames → `voicews` → browser plays.
   Transcript + reply also rendered as text in the dashboard (works muted).

## Persona

System prompt defines "Waggle" — Gina's/Waggle's voice, NOT Adjutant's military
shtick. Starter direction: warm, dry, concise, knows the projects, talks like a
sharp dev partner.

First-draft prompt (tune at review):

> You are Waggle, Gina's context manager and dev partner. You speak briefly and
> plainly, with dry warmth. You know her projects, tasks, and where she left
> off. When she asks, you check your tools and tell her the real state — no
> filler, no hype. You sound like a sharp colleague, not an assistant reading a
> manual.

## Read-only Tools (this sub-project)

Map 1:1 to existing store reads. NO write funcs reachable, even though MCP write
tools exist elsewhere — hard boundary for #1, enforced by test.

- `briefing` → `waggle_ctx_briefing` equivalent
- `whats_next` → `waggle_whats_next` equivalent
- `project_status` / project list
- `read_messages` → `waggle_read_messages` equivalent

## Error Handling / Degradation

- Deepgram down → fall back to whisper.cpp local.
- TTS down → return text only; dashboard shows it; no crash.
- Claude API / key missing → clear dashboard error, voice disabled, daemon keeps running.
- ws drop → browser reconnects (mirror existing SSE reconnect pattern).
- API keys via env vars only. Never logged. Never in ~/.secrets.

## Testing (TDD — mandatory)

- `stt`/`tts`/Claude are interfaces → fakes in tests.
- `voiceagent`: fake STT feeds text → assert correct tool dispatched → fake tool
  result → assert reply. Persona prompt assembly tested.
- Tool dispatch: assert each maps to the right read-only store func; assert NO
  write func is reachable.
- `voicews`: frame in → frame out round-trip with fakes.
- Browser mic client: thin; manual-verified.

## Cost

- **Build:** ~Gina labor: design done; impl est. 1–2 focused sessions. Token spend: moderate (3-service glue + browser audio).
- **Recurring (usage):** ~$0.10–0.25 per active voice minute.
  - Deepgram STT ~$0.0043/min
  - Claude tokens ~pennies/turn
  - ElevenLabs ~$0.30/1k chars (Cartesia cheaper)
  - At 10 min/day ≈ **$30–75/mo**. Zero when not talking.
- **Fallback path (whisper.cpp local STT):** drops STT cost to $0.
- **Maintenance:** low — provider SDKs behind interfaces; swap if pricing shifts.

## Out of Scope (explicit)

Write-actions, dev-driving, terminal/phone/wake-word entry points, avatar face,
phone messaging. All deferred to later sub-projects.
