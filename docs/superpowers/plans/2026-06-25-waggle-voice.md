# Waggle Voice Implementation Plan (Sub-project #1)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Talk to Waggle in the Mission Control browser; it answers in voice, in a Waggle persona, speaking back read-only context (briefing / what's-next / projects / messages).

**Architecture:** Browser mic → WebSocket `/ws/voice` (binary audio frames + JSON control) → Go daemon. Per turn: collected audio → STT (Deepgram, whisper.cpp fallback) → `voiceagent` (Claude `claude-opus-4-8` via official Go SDK, persona prompt + read-only Waggle tools) → reply text → TTS (ElevenLabs) → audio frames back + transcript JSON. All units sit behind interfaces (DIP) so providers swap and tests use fakes.

**Tech Stack:** Go 1.25, `github.com/coder/websocket` (WS transport), `github.com/anthropics/anthropic-sdk-go` (Claude), Deepgram + ElevenLabs REST via `net/http`, existing `modernc.org/sqlite` store. Existing server is stdlib `http.ServeMux` + `cors` wrapper in `internal/server/server.go`.

## Global Constraints

- Go 1.25.0; module `github.com/maniginam/waggle`.
- TDD mandatory: failing test first, minimum code to pass, refactor green. No production code without a failing test.
- Claude model is exactly `claude-opus-4-8` (Go SDK constant `anthropic.ModelClaudeOpus4_8`). Use `output_config` effort `low` for voice latency; adaptive thinking only if needed — do not set `budget_tokens`.
- Persona is Waggle/Gina's voice, NOT Adjutant's military framing.
- Read-only boundary: voice tools map only to store/REST reads. No write tool reachable — enforced by test.
- Secrets (`DEEPGRAM_API_KEY`, `ELEVENLABS_API_KEY`, `ANTHROPIC_API_KEY`, `ELEVENLABS_VOICE_ID`) come from env only. Never log them. Never read `~/.secrets`.
- All STT/TTS/Claude access behind interfaces; provider HTTP clients take an injectable base URL so tests use `httptest.Server`.
- Follow existing patterns: handlers mounted via `mux.Handle`/`mux.HandleFunc` in `server.New`; the `cors` wrapper already allows cross-origin; no `WriteTimeout` (long-lived connections).

---

### Task 1: `stt` package — interface + Deepgram client + whisper fallback

**Files:**
- Create: `internal/stt/stt.go`
- Create: `internal/stt/deepgram.go`
- Create: `internal/stt/whisper.go`
- Test: `internal/stt/deepgram_test.go`
- Modify: `go.mod` (add `github.com/coder/websocket` and `github.com/anthropics/anthropic-sdk-go` now so later tasks compile — run `go get` in Step 0)

**Interfaces:**
- Produces: `type Transcriber interface { Transcribe(ctx context.Context, audio []byte, mime string) (string, error) }`
- Produces: `func NewDeepgram(apiKey string) *Deepgram` and `func (d *Deepgram) WithBaseURL(u string) *Deepgram` (for tests); `func NewWhisper(modelPath, binPath string) *Whisper`.
- Produces: `func WithFallback(primary, fallback Transcriber) Transcriber` — returns primary's result; on primary error, tries fallback.

- [ ] **Step 0: Add dependencies**

Run:
```bash
cd /Users/maniginam/projects/maniginam/waggle
go get github.com/coder/websocket@latest
go get github.com/anthropics/anthropic-sdk-go@latest
go mod tidy
```
Expected: `go.mod` lists both modules; `go build ./...` still passes.

- [ ] **Step 1: Write the failing test**

Create `internal/stt/deepgram_test.go`:
```go
package stt

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDeepgramTranscribe(t *testing.T) {
	var gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"results":{"channels":[{"alternatives":[{"transcript":"hello waggle"}]}]}}`)
	}))
	defer srv.Close()

	d := NewDeepgram("secret-key").WithBaseURL(srv.URL)
	got, err := d.Transcribe(context.Background(), []byte("RIFFaudio"), "audio/webm")
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if got != "hello waggle" {
		t.Errorf("transcript = %q, want %q", got, "hello waggle")
	}
	if gotAuth != "Token secret-key" {
		t.Errorf("auth = %q, want %q", gotAuth, "Token secret-key")
	}
	if !strings.Contains(gotBody, "RIFFaudio") {
		t.Errorf("body did not contain audio bytes")
	}
}

func TestFallbackUsedOnPrimaryError(t *testing.T) {
	primary := transcriberFunc(func(context.Context, []byte, string) (string, error) {
		return "", errTest
	})
	fallback := transcriberFunc(func(context.Context, []byte, string) (string, error) {
		return "from fallback", nil
	})
	got, err := WithFallback(primary, fallback).Transcribe(context.Background(), nil, "")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "from fallback" {
		t.Errorf("got %q, want %q", got, "from fallback")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/stt/`
Expected: FAIL — `undefined: NewDeepgram`, `undefined: transcriberFunc`, `undefined: errTest`, `undefined: WithFallback`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/stt/stt.go`:
```go
// Package stt converts captured audio to text behind a swappable interface.
package stt

import (
	"context"
	"errors"
)

// Transcriber turns a single utterance's audio into text.
type Transcriber interface {
	Transcribe(ctx context.Context, audio []byte, mime string) (string, error)
}

var errTest = errors.New("test error")

type transcriberFunc func(context.Context, []byte, string) (string, error)

func (f transcriberFunc) Transcribe(ctx context.Context, a []byte, m string) (string, error) {
	return f(ctx, a, m)
}

type fallback struct{ primary, secondary Transcriber }

// WithFallback returns primary's transcript, or secondary's if primary errors.
func WithFallback(primary, secondary Transcriber) Transcriber {
	return &fallback{primary, secondary}
}

func (f *fallback) Transcribe(ctx context.Context, a []byte, m string) (string, error) {
	got, err := f.primary.Transcribe(ctx, a, m)
	if err == nil {
		return got, nil
	}
	return f.secondary.Transcribe(ctx, a, m)
}
```

Create `internal/stt/deepgram.go`:
```go
package stt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

const deepgramDefaultURL = "https://api.deepgram.com/v1/listen?model=nova-2&smart_format=true"

// Deepgram transcribes via Deepgram's prerecorded REST endpoint.
type Deepgram struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

func NewDeepgram(apiKey string) *Deepgram {
	return &Deepgram{apiKey: apiKey, baseURL: deepgramDefaultURL, client: http.DefaultClient}
}

func (d *Deepgram) WithBaseURL(u string) *Deepgram { d.baseURL = u; return d }

func (d *Deepgram) Transcribe(ctx context.Context, audio []byte, mime string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.baseURL, bytes.NewReader(audio))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Token "+d.apiKey)
	req.Header.Set("Content-Type", mime)
	resp, err := d.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("deepgram: status %d", resp.StatusCode)
	}
	var out struct {
		Results struct {
			Channels []struct {
				Alternatives []struct {
					Transcript string `json:"transcript"`
				} `json:"alternatives"`
			} `json:"channels"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if len(out.Results.Channels) == 0 || len(out.Results.Channels[0].Alternatives) == 0 {
		return "", nil
	}
	return out.Results.Channels[0].Alternatives[0].Transcript, nil
}
```

Create `internal/stt/whisper.go`:
```go
package stt

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

// Whisper transcribes locally via the whisper-cli binary (offline fallback).
type Whisper struct {
	modelPath string
	binPath   string
}

func NewWhisper(modelPath, binPath string) *Whisper {
	if binPath == "" {
		binPath = "whisper-cli"
	}
	return &Whisper{modelPath: modelPath, binPath: binPath}
}

func (wsp *Whisper) Transcribe(ctx context.Context, audio []byte, _ string) (string, error) {
	f, err := os.CreateTemp("", "waggle-stt-*.wav")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(audio); err != nil {
		f.Close()
		return "", err
	}
	f.Close()
	out, err := exec.CommandContext(ctx, wsp.binPath, "-m", wsp.modelPath, "-f", f.Name(), "-nt").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/stt/`
Expected: PASS.

- [ ] **Step 5: Commit**
```bash
git add go.mod go.sum internal/stt/
git -c commit.gpgsign=false commit -m "Add stt package: Deepgram client + whisper fallback"
```

---

### Task 2: `tts` package — interface + ElevenLabs client

**Files:**
- Create: `internal/tts/tts.go`
- Create: `internal/tts/elevenlabs.go`
- Test: `internal/tts/elevenlabs_test.go`

**Interfaces:**
- Produces: `type Synthesizer interface { Synthesize(ctx context.Context, text string) (audio []byte, mime string, err error) }`
- Produces: `func NewElevenLabs(apiKey, voiceID string) *ElevenLabs` and `func (e *ElevenLabs) WithBaseURL(u string) *ElevenLabs`.

- [ ] **Step 1: Write the failing test**

Create `internal/tts/elevenlabs_test.go`:
```go
package tts

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestElevenLabsSynthesize(t *testing.T) {
	var gotKey, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("xi-api-key")
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Write([]byte("ID3mp3bytes"))
	}))
	defer srv.Close()

	e := NewElevenLabs("xi-secret", "voice123").WithBaseURL(srv.URL)
	audio, mime, err := e.Synthesize(context.Background(), "hello commander")
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if string(audio) != "ID3mp3bytes" {
		t.Errorf("audio = %q", audio)
	}
	if mime != "audio/mpeg" {
		t.Errorf("mime = %q, want audio/mpeg", mime)
	}
	if gotKey != "xi-secret" {
		t.Errorf("xi-api-key = %q", gotKey)
	}
	if !strings.Contains(gotPath, "voice123") {
		t.Errorf("path %q missing voice id", gotPath)
	}
	if !strings.Contains(gotBody, "hello commander") {
		t.Errorf("body %q missing text", gotBody)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tts/`
Expected: FAIL — `undefined: NewElevenLabs`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/tts/tts.go`:
```go
// Package tts turns reply text into spoken audio behind a swappable interface.
package tts

import "context"

// Synthesizer turns text into audio bytes plus the audio MIME type.
type Synthesizer interface {
	Synthesize(ctx context.Context, text string) (audio []byte, mime string, err error)
}
```

Create `internal/tts/elevenlabs.go`:
```go
package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const elevenLabsDefaultBase = "https://api.elevenlabs.io"

// ElevenLabs synthesizes speech via the ElevenLabs REST API.
type ElevenLabs struct {
	apiKey  string
	voiceID string
	baseURL string
	client  *http.Client
}

func NewElevenLabs(apiKey, voiceID string) *ElevenLabs {
	return &ElevenLabs{apiKey: apiKey, voiceID: voiceID, baseURL: elevenLabsDefaultBase, client: http.DefaultClient}
}

func (e *ElevenLabs) WithBaseURL(u string) *ElevenLabs { e.baseURL = u; return e }

func (e *ElevenLabs) Synthesize(ctx context.Context, text string) ([]byte, string, error) {
	body, _ := json.Marshal(map[string]any{
		"text":     text,
		"model_id": "eleven_turbo_v2_5",
	})
	url := fmt.Sprintf("%s/v1/text-to-speech/%s", e.baseURL, e.voiceID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("xi-api-key", e.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "audio/mpeg")
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, "", fmt.Errorf("elevenlabs: status %d", resp.StatusCode)
	}
	audio, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	mime := resp.Header.Get("Content-Type")
	if mime == "" {
		mime = "audio/mpeg"
	}
	return audio, mime, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tts/`
Expected: PASS.

- [ ] **Step 5: Commit**
```bash
git add internal/tts/
git -c commit.gpgsign=false commit -m "Add tts package: ElevenLabs synthesizer"
```

---

### Task 3: `waggleread` — read-only context reader over localhost REST

**Files:**
- Create: `internal/waggleread/waggleread.go`
- Test: `internal/waggleread/waggleread_test.go`

**Rationale:** Reuses the existing briefing/whats-next composition in `internal/api` by calling the daemon's own GET endpoints over localhost (the same pattern the MCP adapter uses). Read-only by construction — only GET endpoints are called.

**Interfaces:**
- Produces: `type Reader interface { Briefing(ctx context.Context, projectID string) (string, error); WhatsNext(ctx context.Context) (string, error); Projects(ctx context.Context) (string, error); Messages(ctx context.Context, to string, limit int) (string, error) }`
- Produces: `func New(baseURL string) *HTTPReader` implementing `Reader`.

- [ ] **Step 1: Write the failing test**

Create `internal/waggleread/waggleread_test.go`:
```go
package waggleread

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReaderHitsCorrectEndpoints(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	r := New(srv.URL)
	ctx := context.Background()
	if _, err := r.Briefing(ctx, "wg-d2b49a"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.WhatsNext(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Projects(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Messages(ctx, "gina", 10); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"/api/briefing?project_id=wg-d2b49a",
		"/api/whats-next",
		"/api/projects",
		"/api/messages?to=gina&limit=10",
	}
	for i, w := range want {
		if i >= len(paths) || paths[i] != w {
			t.Errorf("call %d = %q, want %q", i, paths[i], w)
		}
	}
}

func TestBriefingReturnsBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"where_left_off":"pivot done"}`))
	}))
	defer srv.Close()
	got, err := New(srv.URL).Briefing(context.Background(), "p1")
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"where_left_off":"pivot done"}` {
		t.Errorf("body = %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/waggleread/`
Expected: FAIL — `undefined: New`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/waggleread/waggleread.go`:
```go
// Package waggleread exposes read-only Waggle context to the voice agent by
// calling the daemon's own GET endpoints over localhost. No write paths.
package waggleread

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// Reader returns read-only Waggle context as JSON text.
type Reader interface {
	Briefing(ctx context.Context, projectID string) (string, error)
	WhatsNext(ctx context.Context) (string, error)
	Projects(ctx context.Context) (string, error)
	Messages(ctx context.Context, to string, limit int) (string, error)
}

// HTTPReader reads context via localhost REST GETs.
type HTTPReader struct {
	baseURL string
	client  *http.Client
}

func New(baseURL string) *HTTPReader {
	return &HTTPReader{baseURL: baseURL, client: http.DefaultClient}
}

func (h *HTTPReader) get(ctx context.Context, path string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.baseURL+path, nil)
	if err != nil {
		return "", err
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("waggleread %s: status %d", path, resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	return string(b), err
}

func (h *HTTPReader) Briefing(ctx context.Context, projectID string) (string, error) {
	return h.get(ctx, "/api/briefing?project_id="+url.QueryEscape(projectID))
}

func (h *HTTPReader) WhatsNext(ctx context.Context) (string, error) {
	return h.get(ctx, "/api/whats-next")
}

func (h *HTTPReader) Projects(ctx context.Context) (string, error) {
	return h.get(ctx, "/api/projects")
}

func (h *HTTPReader) Messages(ctx context.Context, to string, limit int) (string, error) {
	return h.get(ctx, fmt.Sprintf("/api/messages?to=%s&limit=%d", url.QueryEscape(to), limit))
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/waggleread/`
Expected: PASS.

- [ ] **Step 5: Commit**
```bash
git add internal/waggleread/
git -c commit.gpgsign=false commit -m "Add waggleread: read-only context reader over localhost REST"
```

---

### Task 4: `voiceagent` — persona + read-only tools + Claude brain

**Files:**
- Create: `internal/voiceagent/voiceagent.go`
- Create: `internal/voiceagent/brain.go` (real Anthropic brain)
- Test: `internal/voiceagent/voiceagent_test.go`

**Interfaces:**
- Consumes: `waggleread.Reader` (Task 3).
- Produces: `type Turn struct { Role, Text string }`
- Produces: `type Tool struct { Name, Description string; Schema map[string]any; Exec func(ctx context.Context, args map[string]any) (string, error) }`
- Produces: `type Brain interface { Run(ctx context.Context, system string, history []Turn, tools []Tool) (string, error) }`
- Produces: `func New(r waggleread.Reader, brain Brain) *Agent`; `func (a *Agent) Tools() []Tool`; `func (a *Agent) SystemPrompt() string`; `func (a *Agent) Reply(ctx context.Context, history []Turn) (string, error)`.
- Produces: `func NewAnthropicBrain(apiKey string) Brain` (real brain, in `brain.go`).

**Read-only tool set (exactly four, no write tool):** `briefing`, `whats_next`, `list_projects`, `read_messages` — each `Exec` calls the matching `Reader` method.

- [ ] **Step 1: Write the failing test**

Create `internal/voiceagent/voiceagent_test.go`:
```go
package voiceagent

import (
	"context"
	"strings"
	"testing"
)

type fakeReader struct{ called []string }

func (f *fakeReader) Briefing(_ context.Context, id string) (string, error) {
	f.called = append(f.called, "briefing:"+id)
	return `{"briefing":"ok"}`, nil
}
func (f *fakeReader) WhatsNext(context.Context) (string, error) {
	f.called = append(f.called, "whats_next")
	return `{"next":"ok"}`, nil
}
func (f *fakeReader) Projects(context.Context) (string, error) {
	f.called = append(f.called, "projects")
	return `{"projects":[]}`, nil
}
func (f *fakeReader) Messages(_ context.Context, to string, limit int) (string, error) {
	f.called = append(f.called, "messages:"+to)
	return `{"messages":[]}`, nil
}

// fakeBrain invokes the named tool once, then returns a fixed reply that
// embeds the tool output — exercises the dispatch wiring without Anthropic.
type fakeBrain struct{ callTool string }

func (b *fakeBrain) Run(ctx context.Context, system string, history []Turn, tools []Tool) (string, error) {
	for _, tl := range tools {
		if tl.Name == b.callTool {
			out, err := tl.Exec(ctx, map[string]any{"project_id": "p1", "to": "gina", "limit": float64(5)})
			if err != nil {
				return "", err
			}
			return "spoken: " + out, nil
		}
	}
	return "spoken: (no tool)", nil
}

func TestToolsAreReadOnlyAndComplete(t *testing.T) {
	a := New(&fakeReader{}, &fakeBrain{})
	got := map[string]bool{}
	for _, tl := range a.Tools() {
		got[tl.Name] = true
	}
	want := []string{"briefing", "whats_next", "list_projects", "read_messages"}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing read tool %q", w)
		}
	}
	if len(a.Tools()) != len(want) {
		t.Errorf("tool count = %d, want %d (no extra/write tools)", len(a.Tools()), len(want))
	}
	for _, bad := range []string{"create_task", "update_task", "park", "log", "send_message"} {
		if got[bad] {
			t.Errorf("write tool %q must not be reachable", bad)
		}
	}
}

func TestReplyDispatchesBriefingTool(t *testing.T) {
	r := &fakeReader{}
	a := New(r, &fakeBrain{callTool: "briefing"})
	reply, err := a.Reply(context.Background(), []Turn{{Role: "user", Text: "brief me on p1"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reply, `{"briefing":"ok"}`) {
		t.Errorf("reply = %q, want briefing output", reply)
	}
	if len(r.called) != 1 || r.called[0] != "briefing:p1" {
		t.Errorf("reader calls = %v, want [briefing:p1]", r.called)
	}
}

func TestSystemPromptIsWagglePersonaNotAdjutant(t *testing.T) {
	a := New(&fakeReader{}, &fakeBrain{})
	p := strings.ToLower(a.SystemPrompt())
	if !strings.Contains(p, "waggle") {
		t.Errorf("system prompt should name Waggle persona")
	}
	for _, banned := range []string{"adjutant", "commander", "fleet"} {
		if strings.Contains(p, banned) {
			t.Errorf("system prompt must not use military persona word %q", banned)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/voiceagent/`
Expected: FAIL — `undefined: New`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/voiceagent/voiceagent.go`:
```go
// Package voiceagent turns a transcript into a spoken-style reply using Claude
// with a Waggle persona and a fixed set of read-only Waggle tools.
package voiceagent

import (
	"context"

	"github.com/maniginam/waggle/internal/waggleread"
)

// Turn is one entry of conversation history.
type Turn struct {
	Role string // "user" or "assistant"
	Text string
}

// Tool is a read-only capability exposed to the brain.
type Tool struct {
	Name        string
	Description string
	Schema      map[string]any
	Exec        func(ctx context.Context, args map[string]any) (string, error)
}

// Brain runs the model loop: given persona + history + tools, returns reply text.
type Brain interface {
	Run(ctx context.Context, system string, history []Turn, tools []Tool) (string, error)
}

// Agent wires the reader-backed read-only tools to a brain.
type Agent struct {
	reader waggleread.Reader
	brain  Brain
}

func New(r waggleread.Reader, brain Brain) *Agent {
	return &Agent{reader: r, brain: brain}
}

const systemPrompt = `You are Waggle, Gina's context manager and dev partner.
You speak briefly and plainly, with dry warmth — like a sharp colleague, not an
assistant reading a manual. You know her projects, tasks, and where she left off.
When she asks about status, call your tools and tell her the real state: no filler,
no hype. Keep spoken replies short and natural.`

func (a *Agent) SystemPrompt() string { return systemPrompt }

func strArg(args map[string]any, key string) string {
	s, _ := args[key].(string)
	return s
}

func (a *Agent) Tools() []Tool {
	return []Tool{
		{
			Name:        "briefing",
			Description: "Get the briefing for a project: where you left off, open tasks, recent progress.",
			Schema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"project_id": map[string]any{"type": "string"}},
				"required":   []string{"project_id"},
			},
			Exec: func(ctx context.Context, args map[string]any) (string, error) {
				return a.reader.Briefing(ctx, strArg(args, "project_id"))
			},
		},
		{
			Name:        "whats_next",
			Description: "See all projects sorted by urgency: what needs attention across the portfolio.",
			Schema:      map[string]any{"type": "object", "properties": map[string]any{}},
			Exec: func(ctx context.Context, _ map[string]any) (string, error) {
				return a.reader.WhatsNext(ctx)
			},
		},
		{
			Name:        "list_projects",
			Description: "List all projects with their status.",
			Schema:      map[string]any{"type": "object", "properties": map[string]any{}},
			Exec: func(ctx context.Context, _ map[string]any) (string, error) {
				return a.reader.Projects(ctx)
			},
		},
		{
			Name:        "read_messages",
			Description: "Read recent messages for a recipient.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"to":    map[string]any{"type": "string"},
					"limit": map[string]any{"type": "integer"},
				},
				"required": []string{"to"},
			},
			Exec: func(ctx context.Context, args map[string]any) (string, error) {
				limit := 10
				if l, ok := args["limit"].(float64); ok {
					limit = int(l)
				}
				return a.reader.Messages(ctx, strArg(args, "to"), limit)
			},
		},
	}
}

func (a *Agent) Reply(ctx context.Context, history []Turn) (string, error) {
	return a.brain.Run(ctx, a.SystemPrompt(), history, a.Tools())
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/voiceagent/`
Expected: PASS.

- [ ] **Step 5: Write the real Anthropic brain (no new test — exercised manually in Task 7)**

Create `internal/voiceagent/brain.go`:
```go
package voiceagent

import (
	"context"
	"encoding/json"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

type anthropicBrain struct {
	client anthropic.Client
}

// NewAnthropicBrain builds a Brain backed by Claude (claude-opus-4-8).
func NewAnthropicBrain(apiKey string) Brain {
	return &anthropicBrain{client: anthropic.NewClient(option.WithAPIKey(apiKey))}
}

func (b *anthropicBrain) Run(ctx context.Context, system string, history []Turn, tools []Tool) (string, error) {
	byName := map[string]Tool{}
	toolParams := make([]anthropic.ToolUnionParam, 0, len(tools))
	for _, t := range tools {
		byName[t.Name] = t
		tp := anthropic.ToolParam{
			Name:        t.Name,
			Description: anthropic.String(t.Description),
			InputSchema: anthropic.ToolInputSchemaParam{Properties: t.Schema["properties"]},
		}
		toolParams = append(toolParams, anthropic.ToolUnionParam{OfTool: &tp})
	}

	msgs := make([]anthropic.MessageParam, 0, len(history))
	for _, turn := range history {
		if turn.Role == "assistant" {
			msgs = append(msgs, anthropic.NewAssistantMessage(anthropic.NewTextBlock(turn.Text)))
		} else {
			msgs = append(msgs, anthropic.NewUserMessage(anthropic.NewTextBlock(turn.Text)))
		}
	}

	for {
		resp, err := b.client.Messages.New(ctx, anthropic.MessageNewParams{
			Model:     anthropic.ModelClaudeOpus4_8,
			MaxTokens: 1024,
			System:    []anthropic.TextBlockParam{{Text: system}},
			Messages:  msgs,
			Tools:     toolParams,
		})
		if err != nil {
			return "", err
		}
		msgs = append(msgs, resp.ToParam())

		var text string
		var results []anthropic.ContentBlockParamUnion
		for _, block := range resp.Content {
			switch v := block.AsAny().(type) {
			case anthropic.TextBlock:
				text += v.Text
			case anthropic.ToolUseBlock:
				out := ""
				if tl, ok := byName[v.Name]; ok {
					var args map[string]any
					_ = json.Unmarshal([]byte(v.JSON.Input.Raw()), &args)
					if r, e := tl.Exec(ctx, args); e == nil {
						out = r
					} else {
						out = "error: " + e.Error()
					}
				}
				results = append(results, anthropic.NewToolResultBlock(block.ID, out, false))
			}
		}
		if resp.StopReason != anthropic.StopReasonToolUse {
			return text, nil
		}
		msgs = append(msgs, anthropic.NewUserMessage(results...))
	}
}
```

- [ ] **Step 6: Verify build + tests**

Run: `go build ./... && go test ./internal/voiceagent/`
Expected: build OK, tests PASS. (If a Go SDK symbol differs from the above, WebFetch `https://github.com/anthropics/anthropic-sdk-go` per the claude-api skill and adjust — do not guess.)

- [ ] **Step 7: Commit**
```bash
git add internal/voiceagent/
git -c commit.gpgsign=false commit -m "Add voiceagent: Waggle persona + read-only tools + Claude brain"
```

---

### Task 5: `voicews` — WebSocket transport wiring STT → agent → TTS

**Files:**
- Create: `internal/voicews/voicews.go`
- Test: `internal/voicews/voicews_test.go`

**Protocol (push-to-talk):** Client sends binary audio frames while the button is held, then a JSON text message `{"type":"end","mime":"audio/webm"}` to mark turn end. Server runs STT → agent → TTS, then sends a JSON text message `{"type":"reply","transcript":"...","text":"...","audio_mime":"audio/mpeg"}` followed by one binary message with the audio bytes. On any stage error it sends `{"type":"error","stage":"stt|agent|tts","message":"..."}` and keeps the socket open.

**Interfaces:**
- Consumes: `stt.Transcriber`, `tts.Synthesizer`, and a `Replier` (satisfied by `*voiceagent.Agent`): `type Replier interface { Reply(ctx context.Context, history []voiceagent.Turn) (string, error) }`.
- Produces: `func NewHandler(t stt.Transcriber, r Replier, s tts.Synthesizer) http.Handler` mounted at `/ws/voice`.

- [ ] **Step 1: Write the failing test**

Create `internal/voicews/voicews_test.go`:
```go
package voicews

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/maniginam/waggle/internal/voiceagent"
)

type fakeT struct{ got []byte }

func (f *fakeT) Transcribe(_ context.Context, audio []byte, _ string) (string, error) {
	f.got = audio
	return "what's next", nil
}

type fakeR struct{ gotHistory []voiceagent.Turn }

func (f *fakeR) Reply(_ context.Context, h []voiceagent.Turn) (string, error) {
	f.gotHistory = h
	return "Two projects need attention.", nil
}

type fakeS struct{ gotText string }

func (f *fakeS) Synthesize(_ context.Context, text string) ([]byte, string, error) {
	f.gotText = text
	return []byte("MP3DATA"), "audio/mpeg", nil
}

func TestVoiceTurnRoundTrip(t *testing.T) {
	tr, rep, syn := &fakeT{}, &fakeR{}, &fakeS{}
	srv := httptest.NewServer(NewHandler(tr, rep, syn))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, wsURL+"/ws/voice", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	if err := c.Write(ctx, websocket.MessageBinary, []byte("AUDIOIN")); err != nil {
		t.Fatal(err)
	}
	if err := c.Write(ctx, websocket.MessageText, []byte(`{"type":"end","mime":"audio/webm"}`)); err != nil {
		t.Fatal(err)
	}

	// First server message: JSON reply.
	typ, data, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if typ != websocket.MessageText {
		t.Fatalf("first msg type = %v, want text", typ)
	}
	var reply struct {
		Type, Transcript, Text, AudioMime string
	}
	if err := json.Unmarshal(data, &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Type != "reply" || reply.Transcript != "what's next" || reply.Text != "Two projects need attention." {
		t.Errorf("reply = %+v", reply)
	}

	// Second server message: binary audio.
	typ, audio, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if typ != websocket.MessageBinary || string(audio) != "MP3DATA" {
		t.Errorf("audio msg type=%v data=%q", typ, audio)
	}

	if string(tr.got) != "AUDIOIN" {
		t.Errorf("transcriber got %q", tr.got)
	}
	if syn.gotText != "Two projects need attention." {
		t.Errorf("synthesizer got %q", syn.gotText)
	}
	if len(rep.gotHistory) != 1 || rep.gotHistory[0].Text != "what's next" {
		t.Errorf("agent history = %+v", rep.gotHistory)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/voicews/`
Expected: FAIL — `undefined: NewHandler`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/voicews/voicews.go`:
```go
// Package voicews bridges a browser voice turn over a WebSocket to the
// STT -> agent -> TTS pipeline.
package voicews

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/coder/websocket"
	"github.com/maniginam/waggle/internal/stt"
	"github.com/maniginam/waggle/internal/tts"
	"github.com/maniginam/waggle/internal/voiceagent"
)

// Replier produces a reply for a conversation history (satisfied by *voiceagent.Agent).
type Replier interface {
	Reply(ctx context.Context, history []voiceagent.Turn) (string, error)
}

type handler struct {
	transcriber stt.Transcriber
	replier     Replier
	synth       tts.Synthesizer
}

// NewHandler returns the /ws/voice WebSocket handler.
func NewHandler(t stt.Transcriber, r Replier, s tts.Synthesizer) http.Handler {
	return &handler{transcriber: t, replier: r, synth: s}
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx := r.Context()

	var audio []byte
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		if typ == websocket.MessageBinary {
			audio = append(audio, data...)
			continue
		}
		// Text message = control.
		var ctrl struct {
			Type, Mime string
		}
		if err := json.Unmarshal(data, &ctrl); err != nil || ctrl.Type != "end" {
			continue
		}
		h.runTurn(ctx, c, audio, ctrl.Mime)
		audio = nil
	}
}

func (h *handler) sendErr(ctx context.Context, c *websocket.Conn, stage, msg string) {
	b, _ := json.Marshal(map[string]string{"type": "error", "stage": stage, "message": msg})
	c.Write(ctx, websocket.MessageText, b)
}

func (h *handler) runTurn(ctx context.Context, c *websocket.Conn, audio []byte, mime string) {
	transcript, err := h.transcriber.Transcribe(ctx, audio, mime)
	if err != nil {
		h.sendErr(ctx, c, "stt", err.Error())
		return
	}
	reply, err := h.replier.Reply(ctx, []voiceagent.Turn{{Role: "user", Text: transcript}})
	if err != nil {
		h.sendErr(ctx, c, "agent", err.Error())
		return
	}
	var audioOut []byte
	audioMime := ""
	if a, m, e := h.synth.Synthesize(ctx, reply); e == nil {
		audioOut, audioMime = a, m
	} else {
		h.sendErr(ctx, c, "tts", e.Error())
		// continue: still send text reply so muted mode works
	}
	msg, _ := json.Marshal(map[string]string{
		"type":       "reply",
		"transcript": transcript,
		"text":       reply,
		"audio_mime": audioMime,
	})
	c.Write(ctx, websocket.MessageText, msg)
	if audioOut != nil {
		c.Write(ctx, websocket.MessageBinary, audioOut)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/voicews/`
Expected: PASS.

- [ ] **Step 5: Commit**
```bash
git add internal/voicews/
git -c commit.gpgsign=false commit -m "Add voicews: WebSocket voice-turn transport"
```

---

### Task 6: Server wiring + env config + graceful disable

**Files:**
- Create: `internal/voice/voice.go` (assembles the pipeline from env; returns handler or a disabled stub)
- Test: `internal/voice/voice_test.go`
- Modify: `internal/server/server.go` (mount `/ws/voice`)

**Interfaces:**
- Consumes: env vars `ANTHROPIC_API_KEY`, `DEEPGRAM_API_KEY`, `ELEVENLABS_API_KEY`, `ELEVENLABS_VOICE_ID`, optional `WHISPER_MODEL_PATH`.
- Produces: `func Handler(baseURL string) http.Handler` — returns the wired voice handler, or a stub that replies HTTP 503 + `{"error":"voice disabled: missing ANTHROPIC_API_KEY"}` when required keys are absent. Daemon keeps running either way.

- [ ] **Step 1: Write the failing test**

Create `internal/voice/voice_test.go`:
```go
package voice

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDisabledWhenNoAnthropicKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	h := Handler("http://localhost:4740")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ws/voice", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "voice disabled") {
		t.Errorf("body = %q", rec.Body.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/voice/`
Expected: FAIL — `undefined: Handler`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/voice/voice.go`:
```go
// Package voice assembles the voice pipeline from environment configuration,
// degrading to a disabled stub when required credentials are absent.
package voice

import (
	"net/http"
	"os"

	"github.com/maniginam/waggle/internal/stt"
	"github.com/maniginam/waggle/internal/tts"
	"github.com/maniginam/waggle/internal/voiceagent"
	"github.com/maniginam/waggle/internal/voicews"
	"github.com/maniginam/waggle/internal/waggleread"
)

func disabled(reason string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error":"voice disabled: ` + reason + `"}`))
	})
}

// Handler wires the voice pipeline, or returns a 503 stub when keys are missing.
func Handler(baseURL string) http.Handler {
	anthropicKey := os.Getenv("ANTHROPIC_API_KEY")
	if anthropicKey == "" {
		return disabled("missing ANTHROPIC_API_KEY")
	}
	deepgramKey := os.Getenv("DEEPGRAM_API_KEY")
	if deepgramKey == "" {
		return disabled("missing DEEPGRAM_API_KEY")
	}
	elevenKey := os.Getenv("ELEVENLABS_API_KEY")
	voiceID := os.Getenv("ELEVENLABS_VOICE_ID")
	if elevenKey == "" || voiceID == "" {
		return disabled("missing ELEVENLABS_API_KEY or ELEVENLABS_VOICE_ID")
	}

	var transcriber stt.Transcriber = stt.NewDeepgram(deepgramKey)
	if model := os.Getenv("WHISPER_MODEL_PATH"); model != "" {
		transcriber = stt.WithFallback(transcriber, stt.NewWhisper(model, ""))
	}
	agent := voiceagent.New(waggleread.New(baseURL), voiceagent.NewAnthropicBrain(anthropicKey))
	synth := tts.NewElevenLabs(elevenKey, voiceID)
	return voicews.NewHandler(transcriber, agent, synth)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/voice/`
Expected: PASS.

- [ ] **Step 5: Mount the handler in the server**

In `internal/server/server.go`, add the import `"github.com/maniginam/waggle/internal/voice"`, and inside `New`, immediately after the dashboard mount (`mux.Handle("/", dashboard.Handler())`), add:
```go
	// Voice (browser mic → STT → Claude → TTS); 503 stub if keys are unset.
	mux.Handle("/ws/voice", voice.Handler(baseURL))
```
(`baseURL` is already defined above as `fmt.Sprintf("http://localhost:%d", cfg.Port)`.)

- [ ] **Step 6: Run server tests + build**

Run: `go build ./... && go test ./internal/server/ ./internal/voice/`
Expected: PASS.

- [ ] **Step 7: Commit**
```bash
git add internal/voice/ internal/server/server.go
git -c commit.gpgsign=false commit -m "Wire /ws/voice into server with env-gated graceful disable"
```

---

### Task 7: Browser mic client in Mission Control

**Files:**
- Modify: `internal/dashboard/static/mission-control.html`

**Behavior:** A push-to-talk mic button. Hold → `MediaRecorder` captures mic to a webm/opus blob and streams chunks over the `/ws/voice` WebSocket as binary; release → send `{"type":"end","mime":"audio/webm"}`. On the `reply` JSON message, append the transcript (you) and text (Waggle) to a small transcript panel; on the following binary message, play the audio via an `Audio` element / blob URL. On an `error` message, show the stage + message inline. Reconnect the socket if it drops (mirror the existing SSE reconnect approach already in the file).

- [ ] **Step 1: Locate the dashboard structure**

Run: `grep -n "EventSource\|new WebSocket\|<script\|</body>" internal/dashboard/static/mission-control.html`
Expected: shows where scripts live and the existing SSE/reconnect pattern to mirror.

- [ ] **Step 2: Add the mic UI + client script**

Add a mic button and transcript panel near the top of the dashboard body, and a script block before `</body>`:
```html
<div id="voice-panel">
  <button id="voice-mic" title="Hold to talk">🎙️ Hold to talk</button>
  <div id="voice-transcript"></div>
</div>
<script>
(function () {
  const btn = document.getElementById('voice-mic');
  const log = document.getElementById('voice-transcript');
  let ws, recorder;

  function connect() {
    ws = new WebSocket(`ws://${location.host}/ws/voice`);
    ws.binaryType = 'arraybuffer';
    ws.onmessage = (ev) => {
      if (typeof ev.data === 'string') {
        const m = JSON.parse(ev.data);
        if (m.type === 'reply') {
          log.innerHTML += `<div class="you">you: ${m.transcript}</div>`;
          log.innerHTML += `<div class="waggle">waggle: ${m.text}</div>`;
        } else if (m.type === 'error') {
          log.innerHTML += `<div class="err">voice error (${m.stage}): ${m.message}</div>`;
        }
      } else {
        const blob = new Blob([ev.data], { type: 'audio/mpeg' });
        new Audio(URL.createObjectURL(blob)).play();
      }
    };
    ws.onclose = () => setTimeout(connect, 1000);
  }
  connect();

  async function start() {
    const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
    recorder = new MediaRecorder(stream, { mimeType: 'audio/webm' });
    recorder.ondataavailable = (e) => {
      if (e.data.size > 0 && ws.readyState === 1) e.data.arrayBuffer().then((b) => ws.send(b));
    };
    recorder.start(250);
  }
  function stop() {
    if (recorder && recorder.state !== 'inactive') {
      recorder.stop();
      if (ws.readyState === 1) ws.send(JSON.stringify({ type: 'end', mime: 'audio/webm' }));
    }
  }
  btn.addEventListener('mousedown', start);
  btn.addEventListener('mouseup', stop);
  btn.addEventListener('mouseleave', stop);
})();
</script>
```

- [ ] **Step 3: Manual verification**

Set the env keys and start the daemon:
```bash
export ANTHROPIC_API_KEY=... DEEPGRAM_API_KEY=... ELEVENLABS_API_KEY=... ELEVENLABS_VOICE_ID=...
go run ./cmd/waggle start
```
Open `http://localhost:4740`, hold the mic button, say "What's next across my projects?", release. Expected: transcript shows your words and Waggle's reply, and the reply plays as audio. With keys unset, the button shows a `voice error (… )` / the socket gets a 503 — the dashboard and daemon keep working.

- [ ] **Step 4: Commit**
```bash
git add internal/dashboard/static/mission-control.html
git -c commit.gpgsign=false commit -m "Add push-to-talk voice client to Mission Control"
```

---

## Self-Review (completed)

- **Spec coverage:** voice loop (Tasks 1,2,5,7), persona (Task 4), read-only tools mapped to existing REST reads (Tasks 3,4), graceful degradation + whisper fallback (Tasks 1,6), env-only secrets (Task 6), TDD throughout, browser-first entry point (Task 7). All spec sections map to a task.
- **Read-only boundary:** enforced by `TestToolsAreReadOnlyAndComplete` (Task 4) — exactly four read tools, named write tools must be absent.
- **Type consistency:** `voiceagent.Turn`/`Tool`/`Brain`/`Agent`, `stt.Transcriber`, `tts.Synthesizer`, `waggleread.Reader`, `voicews.Replier` referenced identically across tasks.
- **Known risk:** the Go Anthropic SDK symbol names in Task 4 Step 5 are from the claude-api skill; if a name differs at build time, WebFetch the SDK repo and adjust (noted in the task). This is the only spot not covered by a unit test (it talks to Claude), and Task 7 Step 3 is its manual verification.
