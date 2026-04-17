# Model-Agnostic Agent Bridges Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let any LLM (OpenAI, Gemini, Grok, Claude API, Ollama, Bedrock) participate as a first-class agent in Waggle's swarm via the existing spawn/team system.

**Architecture:** Each bridge is an in-process goroutine that registers with Waggle's REST API, runs an event loop (poll messages, heartbeat, optionally claim tasks), and translates work into the provider's chat completions API. Four distinct HTTP client implementations cover six providers (OpenAI/Grok/Ollama share a format).

**Tech Stack:** Go 1.25, net/http (no SDKs), existing Waggle REST API

---

## File Structure

```
internal/bridge/
  bridge.go              # Bridge interface, Message, ChatOpts, Capability, Mode types
  bridge_test.go         # Interface contract tests with a fake bridge
  registry.go            # Provider registry: type string -> constructor func
  registry_test.go       # Registry lookup, missing key, unknown provider tests
  runner.go              # Runner event loop, REST client, lifecycle
  runner_test.go         # Runner loop tests against httptest server
  openai/
    openai.go            # OpenAI-compatible client (OpenAI, Grok, Ollama)
    openai_test.go       # HTTP round-trip tests with httptest
  gemini/
    gemini.go            # Google Gemini generateContent client
    gemini_test.go
  claude/
    claude.go            # Anthropic messages API client
    claude_test.go
  bedrock/
    bedrock.go           # Bedrock invokeModel client
    bedrock_test.go
    sigv4.go             # AWS Sigv4 request signing
    sigv4_test.go

Modify:
  internal/api/api.go              # Add bridge spawn routing in handleSpawn
  internal/api/api_test.go         # Tests for bridge spawn path
  internal/dashboard/static/index.html  # Agent type dropdown in spawn UI
```

---

### Task 1: Bridge Interface and Types

**Files:**
- Create: `internal/bridge/bridge.go`
- Create: `internal/bridge/bridge_test.go`

- [ ] **Step 1: Write the test for Bridge interface compliance**

Create `internal/bridge/bridge_test.go`:

```go
package bridge

import (
	"context"
	"testing"
)

type fakeBridge struct {
	response string
	caps     []Capability
	provider string
}

func (f *fakeBridge) Chat(_ context.Context, msgs []Message, _ ChatOpts) (string, error) {
	return f.response, nil
}

func (f *fakeBridge) Capabilities() []Capability { return f.caps }
func (f *fakeBridge) Provider() string           { return f.provider }

func TestBridgeInterfaceCompliance(t *testing.T) {
	var b Bridge = &fakeBridge{
		response: "hello",
		caps:     []Capability{CapChat, CapCode},
		provider: "fake",
	}

	resp, err := b.Chat(context.Background(), []Message{
		{Role: "user", Content: "hi"},
	}, ChatOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if resp != "hello" {
		t.Errorf("got %q, want %q", resp, "hello")
	}
	if len(b.Capabilities()) != 2 {
		t.Errorf("got %d capabilities, want 2", len(b.Capabilities()))
	}
	if b.Provider() != "fake" {
		t.Errorf("got provider %q, want %q", b.Provider(), "fake")
	}
}

func TestModeConstants(t *testing.T) {
	if ModeMessageOnly != "message_only" {
		t.Errorf("got %q, want %q", ModeMessageOnly, "message_only")
	}
	if ModeFullParticipant != "full_participant" {
		t.Errorf("got %q, want %q", ModeFullParticipant, "full_participant")
	}
}

func TestChatOptsDefaults(t *testing.T) {
	opts := ChatOpts{}
	if opts.Model != "" || opts.MaxTokens != 0 || opts.Temperature != 0 || opts.SystemPrompt != "" {
		t.Error("zero-value ChatOpts should have empty defaults")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/maniginam/projects/waggle && go test ./internal/bridge/ -v -run TestBridge`
Expected: FAIL — `Bridge`, `Message`, `Capability`, etc. not defined

- [ ] **Step 3: Write the types**

Create `internal/bridge/bridge.go`:

```go
package bridge

import "context"

// Bridge is the interface every model provider implements.
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

type Mode string

const (
	ModeMessageOnly     Mode = "message_only"
	ModeFullParticipant Mode = "full_participant"
)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/maniginam/projects/waggle && go test ./internal/bridge/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd /Users/maniginam/projects/waggle
git add internal/bridge/bridge.go internal/bridge/bridge_test.go
git -c commit.gpgsign=false commit -m "Add Bridge interface and core types for model-agnostic agent bridges"
```

---

### Task 2: Provider Registry

**Files:**
- Create: `internal/bridge/registry.go`
- Create: `internal/bridge/registry_test.go`

- [ ] **Step 1: Write registry tests**

Create `internal/bridge/registry_test.go`:

```go
package bridge

import (
	"fmt"
	"testing"
)

func TestRegistryRegisterAndGet(t *testing.T) {
	r := NewRegistry()
	r.Register("test", func() (Bridge, error) {
		return &fakeBridge{provider: "test"}, nil
	})

	constructor, ok := r.Get("test")
	if !ok {
		t.Fatal("expected provider 'test' to be registered")
	}
	b, err := constructor()
	if err != nil {
		t.Fatal(err)
	}
	if b.Provider() != "test" {
		t.Errorf("got provider %q, want %q", b.Provider(), "test")
	}
}

func TestRegistryGetUnknown(t *testing.T) {
	r := NewRegistry()
	_, ok := r.Get("nonexistent")
	if ok {
		t.Error("expected unknown provider to return false")
	}
}

func TestRegistryProviders(t *testing.T) {
	r := NewRegistry()
	r.Register("alpha", func() (Bridge, error) { return nil, nil })
	r.Register("beta", func() (Bridge, error) { return nil, nil })

	names := r.Providers()
	if len(names) != 2 {
		t.Fatalf("got %d providers, want 2", len(names))
	}
}

func TestRegistryConstructorError(t *testing.T) {
	r := NewRegistry()
	r.Register("bad", func() (Bridge, error) {
		return nil, fmt.Errorf("missing API key")
	})

	constructor, ok := r.Get("bad")
	if !ok {
		t.Fatal("expected provider to be registered")
	}
	_, err := constructor()
	if err == nil || err.Error() != "missing API key" {
		t.Errorf("expected 'missing API key' error, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/maniginam/projects/waggle && go test ./internal/bridge/ -v -run TestRegistry`
Expected: FAIL — `NewRegistry` not defined

- [ ] **Step 3: Write the registry**

Create `internal/bridge/registry.go`:

```go
package bridge

import "sort"

// Constructor creates a Bridge instance, returning an error if config is missing (e.g. API key).
type Constructor func() (Bridge, error)

// Registry maps provider type strings to their constructors.
type Registry struct {
	providers map[string]Constructor
}

func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]Constructor)}
}

func (r *Registry) Register(name string, c Constructor) {
	r.providers[name] = c
}

func (r *Registry) Get(name string) (Constructor, bool) {
	c, ok := r.providers[name]
	return c, ok
}

func (r *Registry) Providers() []string {
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/maniginam/projects/waggle && go test ./internal/bridge/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd /Users/maniginam/projects/waggle
git add internal/bridge/registry.go internal/bridge/registry_test.go
git -c commit.gpgsign=false commit -m "Add provider registry for bridge constructors"
```

---

### Task 3: OpenAI-Compatible Provider (covers OpenAI, Grok, Ollama)

**Files:**
- Create: `internal/bridge/openai/openai.go`
- Create: `internal/bridge/openai/openai_test.go`

- [ ] **Step 1: Write the tests**

Create `internal/bridge/openai/openai_test.go`:

```go
package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/maniginam/waggle/internal/bridge"
)

func TestChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/chat/completions" {
			t.Errorf("expected /chat/completions, got %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("expected Bearer test-key, got %s", r.Header.Get("Authorization"))
		}

		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "gpt-4o" {
			t.Errorf("expected model gpt-4o, got %v", body["model"])
		}

		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": "Hello from GPT"}},
			},
		})
	}))
	defer srv.Close()

	b := New(srv.URL, "test-key", "gpt-4o", []bridge.Capability{bridge.CapChat, bridge.CapCode})

	resp, err := b.Chat(context.Background(), []bridge.Message{
		{Role: "user", Content: "hi"},
	}, bridge.ChatOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if resp != "Hello from GPT" {
		t.Errorf("got %q, want %q", resp, "Hello from GPT")
	}
}

func TestChatWithSystemPrompt(t *testing.T) {
	var receivedMessages []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		msgs, _ := body["messages"].([]any)
		for _, m := range msgs {
			receivedMessages = append(receivedMessages, m.(map[string]any))
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": "ok"}},
			},
		})
	}))
	defer srv.Close()

	b := New(srv.URL, "key", "gpt-4o", nil)
	b.Chat(context.Background(), []bridge.Message{
		{Role: "user", Content: "hi"},
	}, bridge.ChatOpts{SystemPrompt: "You are helpful"})

	if len(receivedMessages) != 2 {
		t.Fatalf("expected 2 messages (system + user), got %d", len(receivedMessages))
	}
	if receivedMessages[0]["role"] != "system" {
		t.Errorf("first message should be system, got %s", receivedMessages[0]["role"])
	}
}

func TestChatModelOverride(t *testing.T) {
	var receivedModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		receivedModel, _ = body["model"].(string)
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": "ok"}},
			},
		})
	}))
	defer srv.Close()

	b := New(srv.URL, "key", "gpt-4o", nil)
	b.Chat(context.Background(), []bridge.Message{
		{Role: "user", Content: "hi"},
	}, bridge.ChatOpts{Model: "o3"})

	if receivedModel != "o3" {
		t.Errorf("expected model override 'o3', got %q", receivedModel)
	}
}

func TestChatNoAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("expected no auth header for keyless provider")
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": "local response"}},
			},
		})
	}))
	defer srv.Close()

	b := New(srv.URL, "", "llama3", nil)
	resp, err := b.Chat(context.Background(), []bridge.Message{
		{Role: "user", Content: "hi"},
	}, bridge.ChatOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if resp != "local response" {
		t.Errorf("got %q, want %q", resp, "local response")
	}
}

func TestChatAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"message": "rate limited"},
		})
	}))
	defer srv.Close()

	b := New(srv.URL, "key", "gpt-4o", nil)
	_, err := b.Chat(context.Background(), []bridge.Message{
		{Role: "user", Content: "hi"},
	}, bridge.ChatOpts{})
	if err == nil {
		t.Fatal("expected error for 429 response")
	}
}

func TestChatEmptyChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{}})
	}))
	defer srv.Close()

	b := New(srv.URL, "key", "gpt-4o", nil)
	_, err := b.Chat(context.Background(), []bridge.Message{
		{Role: "user", Content: "hi"},
	}, bridge.ChatOpts{})
	if err == nil {
		t.Fatal("expected error for empty choices")
	}
}

func TestProviderAndCapabilities(t *testing.T) {
	b := New("http://localhost", "key", "gpt-4o", []bridge.Capability{bridge.CapChat, bridge.CapVision})
	if b.Provider() != "openai" {
		t.Errorf("got provider %q, want %q", b.Provider(), "openai")
	}
	if len(b.Capabilities()) != 2 {
		t.Errorf("got %d capabilities, want 2", len(b.Capabilities()))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/maniginam/projects/waggle && go test ./internal/bridge/openai/ -v`
Expected: FAIL — package doesn't exist

- [ ] **Step 3: Write the implementation**

Create `internal/bridge/openai/openai.go`:

```go
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/maniginam/waggle/internal/bridge"
)

// Client implements the Bridge interface for OpenAI-compatible APIs.
// Works with OpenAI, Grok (api.x.ai), and Ollama (localhost:11434).
type Client struct {
	baseURL      string
	apiKey       string
	defaultModel string
	caps         []bridge.Capability
	http         *http.Client
}

func New(baseURL, apiKey, defaultModel string, caps []bridge.Capability) *Client {
	if caps == nil {
		caps = []bridge.Capability{bridge.CapChat}
	}
	return &Client{
		baseURL:      baseURL,
		apiKey:       apiKey,
		defaultModel: defaultModel,
		caps:         caps,
		http:         &http.Client{},
	}
}

func (c *Client) Chat(ctx context.Context, messages []bridge.Message, opts bridge.ChatOpts) (string, error) {
	model := c.defaultModel
	if opts.Model != "" {
		model = opts.Model
	}

	var apiMsgs []map[string]string
	if opts.SystemPrompt != "" {
		apiMsgs = append(apiMsgs, map[string]string{"role": "system", "content": opts.SystemPrompt})
	}
	for _, m := range messages {
		apiMsgs = append(apiMsgs, map[string]string{"role": m.Role, "content": m.Content})
	}

	reqBody := map[string]any{
		"model":    model,
		"messages": apiMsgs,
	}
	if opts.MaxTokens > 0 {
		reqBody["max_tokens"] = opts.MaxTokens
	}
	if opts.Temperature > 0 {
		reqBody["temperature"] = opts.Temperature
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

	return result.Choices[0].Message.Content, nil
}

func (c *Client) Capabilities() []bridge.Capability { return c.caps }
func (c *Client) Provider() string                  { return "openai" }
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/maniginam/projects/waggle && go test ./internal/bridge/openai/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd /Users/maniginam/projects/waggle
git add internal/bridge/openai/
git -c commit.gpgsign=false commit -m "Add OpenAI-compatible bridge provider (covers OpenAI, Grok, Ollama)"
```

---

### Task 4: Gemini Provider

**Files:**
- Create: `internal/bridge/gemini/gemini.go`
- Create: `internal/bridge/gemini/gemini_test.go`

- [ ] **Step 1: Write the tests**

Create `internal/bridge/gemini/gemini_test.go`:

```go
package gemini

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/maniginam/waggle/internal/bridge"
)

func TestChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Query().Get("key") != "test-key" {
			t.Errorf("expected key=test-key, got %s", r.URL.Query().Get("key"))
		}

		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)

		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{
				{"content": map[string]any{
					"parts": []map[string]any{
						{"text": "Hello from Gemini"},
					},
				}},
			},
		})
	}))
	defer srv.Close()

	b := New("test-key", "gemini-2.5-pro")
	b.baseURL = srv.URL

	resp, err := b.Chat(context.Background(), []bridge.Message{
		{Role: "user", Content: "hi"},
	}, bridge.ChatOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if resp != "Hello from Gemini" {
		t.Errorf("got %q, want %q", resp, "Hello from Gemini")
	}
}

func TestChatWithSystemPrompt(t *testing.T) {
	var receivedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedBody)
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{
				{"content": map[string]any{
					"parts": []map[string]any{{"text": "ok"}},
				}},
			},
		})
	}))
	defer srv.Close()

	b := New("key", "gemini-2.5-pro")
	b.baseURL = srv.URL

	b.Chat(context.Background(), []bridge.Message{
		{Role: "user", Content: "hi"},
	}, bridge.ChatOpts{SystemPrompt: "Be helpful"})

	si, ok := receivedBody["system_instruction"]
	if !ok {
		t.Fatal("expected system_instruction in request body")
	}
	parts := si.(map[string]any)["parts"].([]any)
	text := parts[0].(map[string]any)["text"].(string)
	if text != "Be helpful" {
		t.Errorf("got system prompt %q, want %q", text, "Be helpful")
	}
}

func TestChatAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":{"message":"invalid key"}}`))
	}))
	defer srv.Close()

	b := New("bad-key", "gemini-2.5-pro")
	b.baseURL = srv.URL

	_, err := b.Chat(context.Background(), []bridge.Message{
		{Role: "user", Content: "hi"},
	}, bridge.ChatOpts{})
	if err == nil {
		t.Fatal("expected error for 403 response")
	}
}

func TestChatEmptyCandidates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"candidates": []any{}})
	}))
	defer srv.Close()

	b := New("key", "gemini-2.5-pro")
	b.baseURL = srv.URL

	_, err := b.Chat(context.Background(), []bridge.Message{
		{Role: "user", Content: "hi"},
	}, bridge.ChatOpts{})
	if err == nil {
		t.Fatal("expected error for empty candidates")
	}
}

func TestProviderAndCapabilities(t *testing.T) {
	b := New("key", "gemini-2.5-pro")
	if b.Provider() != "gemini" {
		t.Errorf("got provider %q, want %q", b.Provider(), "gemini")
	}
	caps := b.Capabilities()
	if len(caps) != 2 {
		t.Errorf("got %d capabilities, want 2", len(caps))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/maniginam/projects/waggle && go test ./internal/bridge/gemini/ -v`
Expected: FAIL — package doesn't exist

- [ ] **Step 3: Write the implementation**

Create `internal/bridge/gemini/gemini.go`:

```go
package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/maniginam/waggle/internal/bridge"
)

const defaultBaseURL = "https://generativelanguage.googleapis.com/v1beta"

type Client struct {
	baseURL      string
	apiKey       string
	defaultModel string
	http         *http.Client
}

func New(apiKey, defaultModel string) *Client {
	return &Client{
		baseURL:      defaultBaseURL,
		apiKey:       apiKey,
		defaultModel: defaultModel,
		http:         &http.Client{},
	}
}

func (c *Client) Chat(ctx context.Context, messages []bridge.Message, opts bridge.ChatOpts) (string, error) {
	model := c.defaultModel
	if opts.Model != "" {
		model = opts.Model
	}

	var contents []map[string]any
	for _, m := range messages {
		role := m.Role
		if role == "assistant" {
			role = "model"
		}
		if role == "system" {
			continue // handled via system_instruction
		}
		contents = append(contents, map[string]any{
			"role":  role,
			"parts": []map[string]any{{"text": m.Content}},
		})
	}

	reqBody := map[string]any{
		"contents": contents,
	}

	if opts.SystemPrompt != "" {
		reqBody["system_instruction"] = map[string]any{
			"parts": []map[string]any{{"text": opts.SystemPrompt}},
		}
	}

	if opts.MaxTokens > 0 || opts.Temperature > 0 {
		genConfig := map[string]any{}
		if opts.MaxTokens > 0 {
			genConfig["maxOutputTokens"] = opts.MaxTokens
		}
		if opts.Temperature > 0 {
			genConfig["temperature"] = opts.Temperature
		}
		reqBody["generationConfig"] = genConfig
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/models/%s:generateContent?key=%s", c.baseURL, model, c.apiKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}
	if len(result.Candidates) == 0 || len(result.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("no candidates in response")
	}

	return result.Candidates[0].Content.Parts[0].Text, nil
}

func (c *Client) Capabilities() []bridge.Capability {
	return []bridge.Capability{bridge.CapChat, bridge.CapCode}
}

func (c *Client) Provider() string { return "gemini" }
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/maniginam/projects/waggle && go test ./internal/bridge/gemini/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd /Users/maniginam/projects/waggle
git add internal/bridge/gemini/
git -c commit.gpgsign=false commit -m "Add Gemini bridge provider"
```

---

### Task 5: Claude API Provider

**Files:**
- Create: `internal/bridge/claude/claude.go`
- Create: `internal/bridge/claude/claude_test.go`

- [ ] **Step 1: Write the tests**

Create `internal/bridge/claude/claude_test.go`:

```go
package claude

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/maniginam/waggle/internal/bridge"
)

func TestChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/v1/messages" {
			t.Errorf("expected /v1/messages, got %s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("expected x-api-key test-key, got %s", r.Header.Get("x-api-key"))
		}
		if r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("expected anthropic-version 2023-06-01, got %s", r.Header.Get("anthropic-version"))
		}

		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "claude-sonnet-4-6" {
			t.Errorf("expected model claude-sonnet-4-6, got %v", body["model"])
		}

		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "Hello from Claude"},
			},
		})
	}))
	defer srv.Close()

	b := New("test-key", "claude-sonnet-4-6")
	b.baseURL = srv.URL

	resp, err := b.Chat(context.Background(), []bridge.Message{
		{Role: "user", Content: "hi"},
	}, bridge.ChatOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if resp != "Hello from Claude" {
		t.Errorf("got %q, want %q", resp, "Hello from Claude")
	}
}

func TestChatWithSystemPrompt(t *testing.T) {
	var receivedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedBody)
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "ok"}},
		})
	}))
	defer srv.Close()

	b := New("key", "claude-sonnet-4-6")
	b.baseURL = srv.URL

	b.Chat(context.Background(), []bridge.Message{
		{Role: "user", Content: "hi"},
	}, bridge.ChatOpts{SystemPrompt: "Be helpful"})

	system, ok := receivedBody["system"].(string)
	if !ok || system != "Be helpful" {
		t.Errorf("expected system 'Be helpful', got %v", receivedBody["system"])
	}
}

func TestChatAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"invalid key"}}`))
	}))
	defer srv.Close()

	b := New("bad-key", "claude-sonnet-4-6")
	b.baseURL = srv.URL

	_, err := b.Chat(context.Background(), []bridge.Message{
		{Role: "user", Content: "hi"},
	}, bridge.ChatOpts{})
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
}

func TestChatEmptyContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"content": []any{}})
	}))
	defer srv.Close()

	b := New("key", "claude-sonnet-4-6")
	b.baseURL = srv.URL

	_, err := b.Chat(context.Background(), []bridge.Message{
		{Role: "user", Content: "hi"},
	}, bridge.ChatOpts{})
	if err == nil {
		t.Fatal("expected error for empty content")
	}
}

func TestProviderAndCapabilities(t *testing.T) {
	b := New("key", "claude-sonnet-4-6")
	if b.Provider() != "claude-api" {
		t.Errorf("got provider %q, want %q", b.Provider(), "claude-api")
	}
	caps := b.Capabilities()
	if len(caps) != 2 {
		t.Errorf("got %d capabilities, want 2", len(caps))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/maniginam/projects/waggle && go test ./internal/bridge/claude/ -v`
Expected: FAIL — package doesn't exist

- [ ] **Step 3: Write the implementation**

Create `internal/bridge/claude/claude.go`:

```go
package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/maniginam/waggle/internal/bridge"
)

const defaultBaseURL = "https://api.anthropic.com"

type Client struct {
	baseURL      string
	apiKey       string
	defaultModel string
	http         *http.Client
}

func New(apiKey, defaultModel string) *Client {
	return &Client{
		baseURL:      defaultBaseURL,
		apiKey:       apiKey,
		defaultModel: defaultModel,
		http:         &http.Client{},
	}
}

func (c *Client) Chat(ctx context.Context, messages []bridge.Message, opts bridge.ChatOpts) (string, error) {
	model := c.defaultModel
	if opts.Model != "" {
		model = opts.Model
	}

	var apiMsgs []map[string]string
	for _, m := range messages {
		if m.Role == "system" {
			continue // handled via top-level system field
		}
		apiMsgs = append(apiMsgs, map[string]string{"role": m.Role, "content": m.Content})
	}

	reqBody := map[string]any{
		"model":      model,
		"messages":   apiMsgs,
		"max_tokens": 4096,
	}
	if opts.MaxTokens > 0 {
		reqBody["max_tokens"] = opts.MaxTokens
	}
	if opts.Temperature > 0 {
		reqBody["temperature"] = opts.Temperature
	}
	if opts.SystemPrompt != "" {
		reqBody["system"] = opts.SystemPrompt
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}
	if len(result.Content) == 0 {
		return "", fmt.Errorf("no content in response")
	}

	// Concatenate all text blocks
	var text string
	for _, block := range result.Content {
		if block.Type == "text" {
			text += block.Text
		}
	}
	return text, nil
}

func (c *Client) Capabilities() []bridge.Capability {
	return []bridge.Capability{bridge.CapChat, bridge.CapCode}
}

func (c *Client) Provider() string { return "claude-api" }
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/maniginam/projects/waggle && go test ./internal/bridge/claude/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd /Users/maniginam/projects/waggle
git add internal/bridge/claude/
git -c commit.gpgsign=false commit -m "Add Claude API bridge provider"
```

---

### Task 6: Bedrock Provider with Sigv4 Signing

**Files:**
- Create: `internal/bridge/bedrock/sigv4.go`
- Create: `internal/bridge/bedrock/sigv4_test.go`
- Create: `internal/bridge/bedrock/bedrock.go`
- Create: `internal/bridge/bedrock/bedrock_test.go`

- [ ] **Step 1: Write the Sigv4 tests**

Create `internal/bridge/bedrock/sigv4_test.go`:

```go
package bedrock

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSignRequest(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "https://bedrock-runtime.us-east-1.amazonaws.com/model/anthropic.claude-v2/invoke", strings.NewReader(`{"prompt":"hi"}`))
	req.Header.Set("Content-Type", "application/json")

	signTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	signRequest(req, "AKID", "SECRET", "us-east-1", "bedrock", signTime)

	auth := req.Header.Get("Authorization")
	if auth == "" {
		t.Fatal("expected Authorization header")
	}
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256") {
		t.Errorf("expected AWS4-HMAC-SHA256 prefix, got %s", auth[:30])
	}
	if !strings.Contains(auth, "AKID") {
		t.Error("expected access key ID in Authorization header")
	}

	amzDate := req.Header.Get("X-Amz-Date")
	if amzDate != "20260101T000000Z" {
		t.Errorf("expected X-Amz-Date 20260101T000000Z, got %s", amzDate)
	}
}

func TestSignRequestDeterministic(t *testing.T) {
	makeReq := func() *http.Request {
		r, _ := http.NewRequest(http.MethodPost, "https://bedrock-runtime.us-east-1.amazonaws.com/invoke", strings.NewReader("body"))
		r.Header.Set("Content-Type", "application/json")
		return r
	}

	signTime := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	req1 := makeReq()
	req2 := makeReq()
	signRequest(req1, "AK", "SK", "us-east-1", "bedrock", signTime)
	signRequest(req2, "AK", "SK", "us-east-1", "bedrock", signTime)

	if req1.Header.Get("Authorization") != req2.Header.Get("Authorization") {
		t.Error("same inputs should produce same signature")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/maniginam/projects/waggle && go test ./internal/bridge/bedrock/ -v -run TestSign`
Expected: FAIL — `signRequest` not defined

- [ ] **Step 3: Write the Sigv4 implementation**

Create `internal/bridge/bedrock/sigv4.go`:

```go
package bedrock

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

func signRequest(req *http.Request, accessKey, secretKey, region, service string, t time.Time) {
	datestamp := t.Format("20060102")
	amzdate := t.Format("20060102T150405Z")

	req.Header.Set("X-Amz-Date", amzdate)
	req.Header.Set("Host", req.URL.Host)

	// Read and hash payload
	var payloadHash string
	if req.Body != nil {
		bodyBytes, _ := io.ReadAll(req.Body)
		payloadHash = hashSHA256(bodyBytes)
		req.Body = io.NopCloser(strings.NewReader(string(bodyBytes)))
	} else {
		payloadHash = hashSHA256(nil)
	}

	// Canonical headers
	signedHeaderKeys := []string{"content-type", "host", "x-amz-date"}
	sort.Strings(signedHeaderKeys)

	var canonicalHeaders strings.Builder
	for _, key := range signedHeaderKeys {
		val := req.Header.Get(key)
		if key == "host" {
			val = req.URL.Host
		}
		canonicalHeaders.WriteString(key + ":" + strings.TrimSpace(val) + "\n")
	}
	signedHeaders := strings.Join(signedHeaderKeys, ";")

	// Canonical request
	canonicalRequest := strings.Join([]string{
		req.Method,
		req.URL.Path,
		req.URL.RawQuery,
		canonicalHeaders.String(),
		signedHeaders,
		payloadHash,
	}, "\n")

	// String to sign
	credentialScope := fmt.Sprintf("%s/%s/%s/aws4_request", datestamp, region, service)
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzdate,
		credentialScope,
		hashSHA256([]byte(canonicalRequest)),
	}, "\n")

	// Signing key
	signingKey := deriveKey(secretKey, datestamp, region, service)
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		accessKey, credentialScope, signedHeaders, signature,
	))
}

func deriveKey(secret, datestamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), []byte(datestamp))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	return hmacSHA256(kService, []byte("aws4_request"))
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func hashSHA256(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
```

- [ ] **Step 4: Run Sigv4 tests to verify they pass**

Run: `cd /Users/maniginam/projects/waggle && go test ./internal/bridge/bedrock/ -v -run TestSign`
Expected: PASS

- [ ] **Step 5: Write the Bedrock client tests**

Add to `internal/bridge/bedrock/bedrock_test.go`:

```go
package bedrock

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/maniginam/waggle/internal/bridge"
)

func TestChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}

		auth := r.Header.Get("Authorization")
		if auth == "" {
			t.Error("expected Authorization header with Sigv4 signature")
		}

		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "Hello from Bedrock"},
			},
		})
	}))
	defer srv.Close()

	b := New("us-east-1", "AKID", "SECRET", "claude-sonnet-4-6")
	b.endpointURL = srv.URL

	resp, err := b.Chat(context.Background(), []bridge.Message{
		{Role: "user", Content: "hi"},
	}, bridge.ChatOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if resp != "Hello from Bedrock" {
		t.Errorf("got %q, want %q", resp, "Hello from Bedrock")
	}
}

func TestChatAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"message":"access denied"}`))
	}))
	defer srv.Close()

	b := New("us-east-1", "AKID", "SECRET", "claude-sonnet-4-6")
	b.endpointURL = srv.URL

	_, err := b.Chat(context.Background(), []bridge.Message{
		{Role: "user", Content: "hi"},
	}, bridge.ChatOpts{})
	if err == nil {
		t.Fatal("expected error for 403 response")
	}
}

func TestChatEmptyContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"content": []any{}})
	}))
	defer srv.Close()

	b := New("us-east-1", "AKID", "SECRET", "claude-sonnet-4-6")
	b.endpointURL = srv.URL

	_, err := b.Chat(context.Background(), []bridge.Message{
		{Role: "user", Content: "hi"},
	}, bridge.ChatOpts{})
	if err == nil {
		t.Fatal("expected error for empty content")
	}
}

func TestProviderAndCapabilities(t *testing.T) {
	b := New("us-east-1", "AKID", "SECRET", "claude-sonnet-4-6")
	if b.Provider() != "bedrock" {
		t.Errorf("got provider %q, want %q", b.Provider(), "bedrock")
	}
	caps := b.Capabilities()
	if len(caps) != 2 {
		t.Errorf("got %d capabilities, want 2", len(caps))
	}
}
```

- [ ] **Step 6: Write the Bedrock client implementation**

Create `internal/bridge/bedrock/bedrock.go`:

```go
package bedrock

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/maniginam/waggle/internal/bridge"
)

// modelMap maps friendly names to Bedrock model IDs.
var modelMap = map[string]string{
	"claude-sonnet-4-6":  "anthropic.claude-sonnet-4-6-v1",
	"claude-opus-4-6":    "anthropic.claude-opus-4-6-v1",
	"claude-haiku-4-5":   "anthropic.claude-haiku-4-5-v1",
}

type Client struct {
	region       string
	accessKey    string
	secretKey    string
	defaultModel string
	endpointURL  string // override for testing
	http         *http.Client
}

func New(region, accessKey, secretKey, defaultModel string) *Client {
	return &Client{
		region:       region,
		accessKey:    accessKey,
		secretKey:    secretKey,
		defaultModel: defaultModel,
		http:         &http.Client{},
	}
}

func (c *Client) Chat(ctx context.Context, messages []bridge.Message, opts bridge.ChatOpts) (string, error) {
	model := c.defaultModel
	if opts.Model != "" {
		model = opts.Model
	}

	// Map friendly name to Bedrock model ID
	if bedrockID, ok := modelMap[model]; ok {
		model = bedrockID
	}

	var apiMsgs []map[string]string
	for _, m := range messages {
		if m.Role == "system" {
			continue
		}
		apiMsgs = append(apiMsgs, map[string]string{"role": m.Role, "content": m.Content})
	}

	reqBody := map[string]any{
		"anthropic_version": "bedrock-2023-10-16",
		"messages":          apiMsgs,
		"max_tokens":        4096,
	}
	if opts.MaxTokens > 0 {
		reqBody["max_tokens"] = opts.MaxTokens
	}
	if opts.Temperature > 0 {
		reqBody["temperature"] = opts.Temperature
	}
	if opts.SystemPrompt != "" {
		reqBody["system"] = opts.SystemPrompt
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	endpoint := c.endpointURL
	if endpoint == "" {
		endpoint = fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com", c.region)
	}
	url := fmt.Sprintf("%s/model/%s/invoke", endpoint, model)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	signRequest(req, c.accessKey, c.secretKey, c.region, "bedrock", time.Now().UTC())

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}
	if len(result.Content) == 0 {
		return "", fmt.Errorf("no content in response")
	}

	var text string
	for _, block := range result.Content {
		if block.Type == "text" {
			text += block.Text
		}
	}
	return text, nil
}

func (c *Client) Capabilities() []bridge.Capability {
	return []bridge.Capability{bridge.CapChat, bridge.CapCode}
}

func (c *Client) Provider() string { return "bedrock" }
```

- [ ] **Step 7: Run all Bedrock tests to verify they pass**

Run: `cd /Users/maniginam/projects/waggle && go test ./internal/bridge/bedrock/ -v`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
cd /Users/maniginam/projects/waggle
git add internal/bridge/bedrock/
git -c commit.gpgsign=false commit -m "Add Bedrock bridge provider with Sigv4 request signing"
```

---

### Task 7: Runner Event Loop

**Files:**
- Create: `internal/bridge/runner.go`
- Create: `internal/bridge/runner_test.go`

- [ ] **Step 1: Write the runner tests**

Create `internal/bridge/runner_test.go`. The runner talks to Waggle's REST API, so tests use an httptest server that simulates Waggle endpoints.

```go
package bridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestRunnerRegistersOnStart(t *testing.T) {
	var registered bool
	var regBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/agents/register" && r.Method == http.MethodPost:
			registered = true
			json.NewDecoder(r.Body).Decode(&regBody)
			json.NewEncoder(w).Encode(map[string]any{"id": "ag-1", "name": regBody["name"]})
		case r.URL.Path == "/api/messages" && r.Method == http.MethodGet:
			json.NewEncoder(w).Encode([]any{})
		default:
			json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	r := NewRunner(&fakeBridge{provider: "test", caps: []Capability{CapChat}}, RunnerConfig{
		AgentName: "test-agent",
		BaseURL:   srv.URL,
		Mode:      ModeMessageOnly,
		ProjectID: "wg-123",
	})
	r.Run(ctx)

	if !registered {
		t.Error("expected runner to register agent on start")
	}
	if regBody["name"] != "test-agent" {
		t.Errorf("expected name 'test-agent', got %q", regBody["name"])
	}
}

func TestRunnerSendsHeartbeats(t *testing.T) {
	var mu sync.Mutex
	heartbeatCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/agents/register":
			json.NewEncoder(w).Encode(map[string]any{"id": "ag-1", "name": "hb-agent"})
		case r.URL.Path == "/api/agents/hb-agent/heartbeat":
			mu.Lock()
			heartbeatCount++
			mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		case r.URL.Path == "/api/messages":
			json.NewEncoder(w).Encode([]any{})
		default:
			json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer cancel()

	r := NewRunner(&fakeBridge{provider: "test", caps: []Capability{CapChat}}, RunnerConfig{
		AgentName:         "hb-agent",
		BaseURL:           srv.URL,
		Mode:              ModeMessageOnly,
		HeartbeatInterval: 100 * time.Millisecond,
	})
	r.Run(ctx)

	mu.Lock()
	defer mu.Unlock()
	if heartbeatCount < 2 {
		t.Errorf("expected at least 2 heartbeats, got %d", heartbeatCount)
	}
}

func TestRunnerRespondsToMessages(t *testing.T) {
	var mu sync.Mutex
	var sentReply string
	messageServed := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/agents/register":
			json.NewEncoder(w).Encode(map[string]any{"id": "ag-1", "name": "msg-agent"})
		case r.URL.Path == "/api/messages" && r.Method == http.MethodGet:
			mu.Lock()
			served := messageServed
			mu.Unlock()
			if !served {
				mu.Lock()
				messageServed = true
				mu.Unlock()
				json.NewEncoder(w).Encode([]map[string]any{
					{"id": "m-1", "from": "user", "to": "msg-agent", "body": "hello", "read": false},
				})
			} else {
				json.NewEncoder(w).Encode([]any{})
			}
		case r.URL.Path == "/api/messages" && r.Method == http.MethodPost:
			var msg map[string]string
			json.NewDecoder(r.Body).Decode(&msg)
			mu.Lock()
			sentReply = msg["body"]
			mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		case r.URL.Path == "/api/messages" && r.Method == http.MethodPatch:
			json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		default:
			json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	r := NewRunner(&fakeBridge{response: "hi back", provider: "test", caps: []Capability{CapChat}}, RunnerConfig{
		AgentName:       "msg-agent",
		BaseURL:         srv.URL,
		Mode:            ModeMessageOnly,
		PollInterval:    50 * time.Millisecond,
	})
	r.Run(ctx)

	mu.Lock()
	defer mu.Unlock()
	if sentReply != "hi back" {
		t.Errorf("expected reply 'hi back', got %q", sentReply)
	}
}

func TestRunnerDisconnectsOnShutdown(t *testing.T) {
	var disconnected bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/agents/register":
			json.NewEncoder(w).Encode(map[string]any{"id": "ag-1", "name": "dc-agent"})
		case r.URL.Path == "/api/agents/dc-agent/status" && r.Method == http.MethodPost:
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["status"] == "disconnected" {
				disconnected = true
			}
			json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		case r.URL.Path == "/api/messages":
			json.NewEncoder(w).Encode([]any{})
		default:
			json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	r := NewRunner(&fakeBridge{provider: "test", caps: []Capability{CapChat}}, RunnerConfig{
		AgentName: "dc-agent",
		BaseURL:   srv.URL,
		Mode:      ModeMessageOnly,
	})
	r.Run(ctx)

	if !disconnected {
		t.Error("expected runner to disconnect on shutdown")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/maniginam/projects/waggle && go test ./internal/bridge/ -v -run TestRunner`
Expected: FAIL — `NewRunner`, `RunnerConfig` not defined

- [ ] **Step 3: Write the runner implementation**

Create `internal/bridge/runner.go`:

```go
package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

type RunnerConfig struct {
	AgentName         string
	BaseURL           string
	Mode              Mode
	ProjectID         string
	SystemPrompt      string
	HeartbeatInterval time.Duration
	PollInterval      time.Duration
}

type Runner struct {
	bridge  Bridge
	config  RunnerConfig
	http    *http.Client
	history []Message
}

func NewRunner(b Bridge, cfg RunnerConfig) *Runner {
	if cfg.HeartbeatInterval == 0 {
		cfg.HeartbeatInterval = 30 * time.Second
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 1 * time.Second
	}
	return &Runner{
		bridge: b,
		config: cfg,
		http:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (r *Runner) Run(ctx context.Context) {
	if err := r.register(); err != nil {
		log.Printf("bridge/%s: register failed: %v", r.config.AgentName, err)
		return
	}
	log.Printf("bridge/%s: registered as %s (%s mode)", r.config.AgentName, r.bridge.Provider(), r.config.Mode)

	heartbeat := time.NewTicker(r.config.HeartbeatInterval)
	poll := time.NewTicker(r.config.PollInterval)
	defer heartbeat.Stop()
	defer poll.Stop()

	for {
		select {
		case <-ctx.Done():
			r.disconnect()
			return
		case <-heartbeat.C:
			r.sendHeartbeat()
		case <-poll.C:
			r.pollMessages(ctx)
		}
	}
}

func (r *Runner) register() error {
	body := map[string]string{
		"name":       r.config.AgentName,
		"type":       r.bridge.Provider(),
		"project_id": r.config.ProjectID,
	}
	_, err := r.post("/api/agents/register", body)
	return err
}

func (r *Runner) sendHeartbeat() {
	r.post("/api/agents/"+r.config.AgentName+"/heartbeat", nil)
}

func (r *Runner) disconnect() {
	r.post("/api/agents/"+r.config.AgentName+"/status", map[string]string{"status": "disconnected"})
	log.Printf("bridge/%s: disconnected", r.config.AgentName)
}

type apiMessage struct {
	ID   string `json:"id"`
	From string `json:"from"`
	To   string `json:"to"`
	Body string `json:"body"`
	Read bool   `json:"read"`
}

func (r *Runner) pollMessages(ctx context.Context) {
	resp, err := r.get(fmt.Sprintf("/api/messages?to=%s&limit=10", r.config.AgentName))
	if err != nil {
		return
	}
	defer resp.Body.Close()

	var msgs []apiMessage
	if err := json.NewDecoder(resp.Body).Decode(&msgs); err != nil {
		return
	}

	for _, msg := range msgs {
		if msg.Read {
			continue
		}
		r.handleMessage(ctx, msg)
	}
}

func (r *Runner) handleMessage(ctx context.Context, msg apiMessage) {
	r.history = append(r.history, Message{Role: "user", Content: msg.Body})

	opts := ChatOpts{SystemPrompt: r.config.SystemPrompt}
	response, err := r.bridge.Chat(ctx, r.history, opts)
	if err != nil {
		log.Printf("bridge/%s: chat error: %v", r.config.AgentName, err)
		response = fmt.Sprintf("[bridge error: %v]", err)
	}

	r.history = append(r.history, Message{Role: "assistant", Content: response})

	// Reply
	r.post("/api/messages", map[string]string{
		"from": r.config.AgentName,
		"to":   msg.From,
		"body": response,
	})

	// Mark as read
	r.patch("/api/messages", map[string]any{
		"ids": []string{msg.ID},
	})
}

func (r *Runner) post(path string, body any) (*http.Response, error) {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	resp, err := r.http.Post(r.config.BaseURL+path, "application/json", &buf)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("POST %s: status %d: %s", path, resp.StatusCode, string(respBody))
	}
	return resp, nil
}

func (r *Runner) get(path string) (*http.Response, error) {
	return r.http.Get(r.config.BaseURL + path)
}

func (r *Runner) patch(path string, body any) {
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(body)
	req, err := http.NewRequest(http.MethodPatch, r.config.BaseURL+path, &buf)
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	r.http.Do(req)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/maniginam/projects/waggle && go test ./internal/bridge/ -v -run TestRunner`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd /Users/maniginam/projects/waggle
git add internal/bridge/runner.go internal/bridge/runner_test.go
git -c commit.gpgsign=false commit -m "Add bridge runner with event loop, message polling, and lifecycle management"
```

---

### Task 8: Default Registry with All Providers

**Files:**
- Modify: `internal/bridge/registry.go`
- Modify: `internal/bridge/registry_test.go`

- [ ] **Step 1: Write test for default registry with all providers**

Add to `internal/bridge/registry_test.go`:

```go
func TestDefaultRegistryHasAllProviders(t *testing.T) {
	r := DefaultRegistry()
	expected := []string{"bedrock", "claude-api", "gemini", "grok", "ollama", "openai"}
	providers := r.Providers()

	if len(providers) != len(expected) {
		t.Fatalf("got %d providers %v, want %d %v", len(providers), providers, len(expected), expected)
	}
	for i, name := range expected {
		if providers[i] != name {
			t.Errorf("provider[%d] = %q, want %q", i, providers[i], name)
		}
	}
}

func TestDefaultRegistryOpenAIRequiresKey(t *testing.T) {
	r := DefaultRegistry()
	constructor, ok := r.Get("openai")
	if !ok {
		t.Fatal("openai not registered")
	}

	// With no OPENAI_API_KEY set, constructor should fail
	origKey := os.Getenv("OPENAI_API_KEY")
	os.Unsetenv("OPENAI_API_KEY")
	defer os.Setenv("OPENAI_API_KEY", origKey)

	_, err := constructor()
	if err == nil {
		t.Error("expected error when OPENAI_API_KEY is not set")
	}
}

func TestDefaultRegistryOllamaNoKeyRequired(t *testing.T) {
	r := DefaultRegistry()
	constructor, ok := r.Get("ollama")
	if !ok {
		t.Fatal("ollama not registered")
	}

	b, err := constructor()
	if err != nil {
		t.Fatalf("ollama should not require an API key: %v", err)
	}
	if b.Provider() != "openai" {
		t.Errorf("got provider %q, want %q", b.Provider(), "openai")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/maniginam/projects/waggle && go test ./internal/bridge/ -v -run TestDefaultRegistry`
Expected: FAIL — `DefaultRegistry` not defined

- [ ] **Step 3: Add DefaultRegistry and imports**

Add to `internal/bridge/registry.go` (add `"os"` and `"fmt"` to imports):

```go
import (
	"fmt"
	"os"
	"sort"

	"github.com/maniginam/waggle/internal/bridge/bedrock"
	"github.com/maniginam/waggle/internal/bridge/claude"
	"github.com/maniginam/waggle/internal/bridge/gemini"
	oapkg "github.com/maniginam/waggle/internal/bridge/openai"
)

func DefaultRegistry() *Registry {
	r := NewRegistry()

	r.Register("openai", func() (Bridge, error) {
		key := os.Getenv("OPENAI_API_KEY")
		if key == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY environment variable is required")
		}
		return oapkg.New("https://api.openai.com/v1", key, "gpt-4o",
			[]Capability{CapChat, CapCode, CapVision}), nil
	})

	r.Register("grok", func() (Bridge, error) {
		key := os.Getenv("XAI_API_KEY")
		if key == "" {
			return nil, fmt.Errorf("XAI_API_KEY environment variable is required")
		}
		return oapkg.New("https://api.x.ai/v1", key, "grok-3",
			[]Capability{CapChat, CapImageGen}), nil
	})

	r.Register("ollama", func() (Bridge, error) {
		return oapkg.New("http://localhost:11434/v1", "", "llama3",
			[]Capability{CapChat, CapCode}), nil
	})

	r.Register("gemini", func() (Bridge, error) {
		key := os.Getenv("GOOGLE_API_KEY")
		if key == "" {
			return nil, fmt.Errorf("GOOGLE_API_KEY environment variable is required")
		}
		return gemini.New(key, "gemini-2.5-pro"), nil
	})

	r.Register("claude-api", func() (Bridge, error) {
		key := os.Getenv("ANTHROPIC_API_KEY")
		if key == "" {
			return nil, fmt.Errorf("ANTHROPIC_API_KEY environment variable is required")
		}
		return claude.New(key, "claude-sonnet-4-6"), nil
	})

	r.Register("bedrock", func() (Bridge, error) {
		region := os.Getenv("AWS_REGION")
		accessKey := os.Getenv("AWS_ACCESS_KEY_ID")
		secretKey := os.Getenv("AWS_SECRET_ACCESS_KEY")
		if region == "" || accessKey == "" || secretKey == "" {
			return nil, fmt.Errorf("AWS_REGION, AWS_ACCESS_KEY_ID, and AWS_SECRET_ACCESS_KEY are required")
		}
		return bedrock.New(region, accessKey, secretKey, "claude-sonnet-4-6"), nil
	})

	return r
}
```

Also add `"os"` to the import in `registry_test.go`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/maniginam/projects/waggle && go test ./internal/bridge/ -v -run TestDefaultRegistry`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd /Users/maniginam/projects/waggle
git add internal/bridge/registry.go internal/bridge/registry_test.go
git -c commit.gpgsign=false commit -m "Add default registry wiring all six providers"
```

---

### Task 9: Spawn Integration — Route Bridge Agents in handleSpawn

**Files:**
- Modify: `internal/api/api.go` (handleSpawn, API struct)
- Modify: `internal/api/api_test.go`

- [ ] **Step 1: Write tests for bridge spawn path**

Add to `internal/api/api_test.go`:

```go
func TestSpawnBridgeAgent(t *testing.T) {
	_, ts := setup(t)

	resp := mustPost(t, ts.URL+"/api/spawn", "application/json",
		strings.NewReader(`{
			"name": "gpt-helper",
			"type": "ollama",
			"mode": "message_only",
			"prompt": "You help with code review"
		}`))

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	if result["status"] != "spawned" {
		t.Errorf("expected status 'spawned', got %q", result["status"])
	}
	if result["type"] != "ollama" {
		t.Errorf("expected type 'ollama', got %q", result["type"])
	}

	// Verify agent was registered
	agentResp := mustGet(t, ts.URL+"/api/agents")
	var agents []map[string]any
	json.NewDecoder(agentResp.Body).Decode(&agents)

	found := false
	for _, a := range agents {
		if a["name"] == "gpt-helper" {
			found = true
			if a["type"] != "ollama" {
				t.Errorf("expected type 'ollama', got %v", a["type"])
			}
		}
	}
	if !found {
		t.Error("expected bridge agent to be registered")
	}
}

func TestSpawnBridgeAgentNoWorkDirRequired(t *testing.T) {
	_, ts := setup(t)

	// Bridge agents should NOT require work_dir (only Claude Code needs it)
	resp := mustPost(t, ts.URL+"/api/spawn", "application/json",
		strings.NewReader(`{"name": "gem-bot", "type": "gemini"}`))

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200 for bridge spawn without work_dir, got %d: %s", resp.StatusCode, string(body))
	}
}

func TestSpawnBridgeAgentMissingAPIKey(t *testing.T) {
	_, ts := setup(t)

	// OpenAI requires OPENAI_API_KEY — unset it to trigger the error
	origKey := os.Getenv("OPENAI_API_KEY")
	os.Unsetenv("OPENAI_API_KEY")
	defer os.Setenv("OPENAI_API_KEY", origKey)

	resp := mustPost(t, ts.URL+"/api/spawn", "application/json",
		strings.NewReader(`{"name": "gpt-bot", "type": "openai"}`))

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for missing API key, got %d", resp.StatusCode)
	}
}

func TestSpawnBridgeAgentUnknownType(t *testing.T) {
	_, ts := setup(t)

	resp := mustPost(t, ts.URL+"/api/spawn", "application/json",
		strings.NewReader(`{"name": "bot", "type": "unknown-model"}`))

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for unknown type, got %d", resp.StatusCode)
	}
}
```

Add `"os"` to the import list in `api_test.go` if not already present.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/maniginam/projects/waggle && go test ./internal/api/ -v -run TestSpawnBridge`
Expected: FAIL — bridge spawn path not implemented

- [ ] **Step 3: Modify handleSpawn to route bridge agents**

In `internal/api/api.go`, add the bridge registry to the API struct. Add to imports:

```go
"github.com/maniginam/waggle/internal/bridge"
```

Add field to API struct (after `agentDirs`):

```go
bridgeRegistry *bridge.Registry
bridgeRunners  map[string]context.CancelFunc // cancel funcs for bridge goroutines
```

Update `New()` to initialize:

```go
func New(s *store.Store, eh *event.Hub) *API {
	a := &API{
		store:          s,
		eventHub:       eh,
		ghAvail:        gh.Available(),
		rateLimiter:    newRateLimiter(120, time.Minute),
		procs:          make(map[string]*exec.Cmd),
		procsStdin:     make(map[string]io.WriteCloser),
		agentDirs:      make(map[string]string),
		bridgeRegistry: bridge.DefaultRegistry(),
		bridgeRunners:  make(map[string]context.CancelFunc),
	}
	// ... rest unchanged
```

Add `"context"` to imports if not already present.

Modify `handleSpawn` — after the name validation and before the `work_dir` check, add a bridge routing block. The full modified function replaces lines 1530-1721. The key change is after the spawn request struct is parsed, add `Type` and `Mode` fields, then check if the type is a bridge type:

Update the request struct at the top of `handleSpawn` (line 1530):

```go
var req struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	ProjectID    string `json:"project_id"`
	WorkDir      string `json:"work_dir"`
	Prompt       string `json:"prompt"`
	Model        string `json:"model"`
	PersonaID    string `json:"persona_id"`
	Mode         string `json:"mode"`
	SystemPrompt string `json:"system_prompt"`
}
```

After the name validation (after the `safeNameRe` check, around line 1548), add the bridge routing:

```go
// Check if this is a bridge agent type
if req.Type != "" && req.Type != "claude-code" {
	constructor, ok := a.bridgeRegistry.Get(req.Type)
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown_type",
			fmt.Sprintf("unknown agent type %q; supported: %v", req.Type, a.bridgeRegistry.Providers()))
		return
	}

	b, err := constructor()
	if err != nil {
		writeError(w, http.StatusBadRequest, "provider_error", err.Error())
		return
	}

	mode := bridge.ModeMessageOnly
	if req.Mode == "full_participant" {
		mode = bridge.ModeFullParticipant
	}

	cfg := bridge.RunnerConfig{
		AgentName:    req.Name,
		BaseURL:      fmt.Sprintf("http://localhost%s", a.listenAddr()),
		Mode:         mode,
		ProjectID:    req.ProjectID,
		SystemPrompt: req.SystemPrompt,
	}
	if req.Prompt != "" && cfg.SystemPrompt == "" {
		cfg.SystemPrompt = req.Prompt
	}

	runner := bridge.NewRunner(b, cfg)

	// Cancel existing bridge if running
	a.procsMu.Lock()
	if cancel, ok := a.bridgeRunners[req.Name]; ok {
		cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.bridgeRunners[req.Name] = cancel
	a.procsMu.Unlock()

	go func() {
		runner.Run(ctx)
		a.procsMu.Lock()
		delete(a.bridgeRunners, req.Name)
		a.procsMu.Unlock()
	}()

	a.eventHub.Publish(&model.Event{
		Type:    model.EventAgentJoined,
		AgentID: req.Name,
		Payload: map[string]string{"type": req.Type, "mode": string(mode)},
	})

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "spawned",
		"name":   req.Name,
		"type":   req.Type,
	})
	return
}
```

Add a helper method to get the listen address (add after `agentLogPath`):

```go
func (a *API) listenAddr() string {
	// Default to :4740 — this matches the default server port
	return ":4740"
}
```

Also, update the `work_dir` required check to only apply to Claude Code agents (it comes after the bridge routing block, so it already only runs for claude-code).

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/maniginam/projects/waggle && go test ./internal/api/ -v -run TestSpawnBridge -timeout 30s`
Expected: PASS

- [ ] **Step 5: Run full test suite to verify no regressions**

Run: `cd /Users/maniginam/projects/waggle && go test ./... -timeout 60s`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
cd /Users/maniginam/projects/waggle
git add internal/api/api.go internal/api/api_test.go
git -c commit.gpgsign=false commit -m "Integrate bridge agents into spawn system"
```

---

### Task 10: Dashboard Spawn UI — Agent Type Dropdown

**Files:**
- Modify: `internal/dashboard/static/index.html`

- [ ] **Step 1: Add agent type dropdown to spawn form**

In `internal/dashboard/static/index.html`, find the spawn form (around line 3465). After the "Agent Name" field, add a new "Agent Type" field. Modify the block starting at line 3466 to insert the type selector:

After the Agent Name spawn-field div (after line 3469), add:

```html
      <div class="spawn-field">
        <label class="spawn-label">Agent Type</label>
        <select class="spawn-select" id="spawn-type" onchange="onSpawnTypeChange()">
          <option value="claude-code">Claude Code</option>
          <option value="openai">OpenAI (GPT)</option>
          <option value="gemini">Google Gemini</option>
          <option value="grok">xAI Grok</option>
          <option value="claude-api">Claude API</option>
          <option value="ollama">Ollama (local)</option>
          <option value="bedrock">AWS Bedrock</option>
        </select>
      </div>
```

After the model dropdown field (around line 3490), add the mode and system prompt fields:

```html
      <div class="spawn-field" id="spawn-mode-field" style="display:none">
        <label class="spawn-label">Participation Mode</label>
        <select class="spawn-select" id="spawn-mode">
          <option value="message_only">Message Only (responds when asked)</option>
          <option value="full_participant">Full Participant (claims tasks)</option>
        </select>
      </div>
      <div class="spawn-field" id="spawn-sysprompt-field" style="display:none">
        <label class="spawn-label">System Prompt</label>
        <textarea class="spawn-textarea" id="spawn-sysprompt" placeholder="Custom instructions for this agent"></textarea>
        <div class="spawn-hint">Defines the agent's personality and behavior</div>
      </div>
```

- [ ] **Step 2: Add the onSpawnTypeChange function**

Add this function near the `onSpawnProjectChange` function (around line 3527):

```javascript
function onSpawnTypeChange() {
  const type = document.getElementById('spawn-type').value;
  const isBridge = type !== 'claude-code';
  const workdirField = document.getElementById('spawn-workdir').closest('.spawn-field');
  const modelField = document.getElementById('spawn-model').closest('.spawn-field');
  const modeField = document.getElementById('spawn-mode-field');
  const syspromptField = document.getElementById('spawn-sysprompt-field');

  // Bridge agents don't need work_dir; show mode and system prompt instead
  workdirField.style.display = isBridge ? 'none' : '';
  modeField.style.display = isBridge ? '' : 'none';
  syspromptField.style.display = isBridge ? '' : 'none';

  // Update model dropdown based on type
  const modelSelect = document.getElementById('spawn-model');
  modelSelect.innerHTML = '';
  if (type === 'claude-code' || type === 'claude-api') {
    modelSelect.innerHTML = `
      <option value="">Default (Sonnet)</option>
      <option value="claude-opus-4-6">Opus 4.6</option>
      <option value="claude-sonnet-4-6">Sonnet 4.6</option>
      <option value="claude-haiku-4-5-20251001">Haiku 4.5</option>`;
  } else if (type === 'openai') {
    modelSelect.innerHTML = `
      <option value="">Default (GPT-4o)</option>
      <option value="gpt-4o">GPT-4o</option>
      <option value="o3">o3</option>
      <option value="o1">o1</option>`;
  } else if (type === 'gemini') {
    modelSelect.innerHTML = `
      <option value="">Default (Gemini 2.5 Pro)</option>
      <option value="gemini-2.5-pro">Gemini 2.5 Pro</option>
      <option value="gemini-2.5-flash">Gemini 2.5 Flash</option>`;
  } else if (type === 'grok') {
    modelSelect.innerHTML = `
      <option value="">Default (Grok 3)</option>
      <option value="grok-3">Grok 3</option>`;
  } else if (type === 'ollama') {
    modelSelect.innerHTML = `
      <option value="">Default (llama3)</option>
      <option value="llama3">Llama 3</option>
      <option value="codellama">Code Llama</option>
      <option value="mistral">Mistral</option>`;
  } else if (type === 'bedrock') {
    modelSelect.innerHTML = `
      <option value="">Default (Claude Sonnet)</option>
      <option value="claude-sonnet-4-6">Claude Sonnet 4.6</option>
      <option value="claude-opus-4-6">Claude Opus 4.6</option>`;
  }
}
```

- [ ] **Step 3: Update doSpawn to include bridge fields**

Modify the `doSpawn` function (around line 3536) to include the new fields:

```javascript
async function doSpawn() {
  const name = document.getElementById('spawn-name').value.trim();
  const type = document.getElementById('spawn-type').value;
  const projectId = document.getElementById('spawn-project').value;
  const workDir = document.getElementById('spawn-workdir').value.trim();
  const model = document.getElementById('spawn-model').value;
  const prompt = document.getElementById('spawn-prompt').value.trim();
  const mode = document.getElementById('spawn-mode')?.value;
  const systemPrompt = document.getElementById('spawn-sysprompt')?.value?.trim();

  if (!name) { showToast('Agent name required', 'var(--red)'); return; }

  const isBridge = type !== 'claude-code';
  if (!isBridge && !workDir) { showToast('Working directory required', 'var(--red)'); return; }

  const body = { name, project_id: projectId, model: model || undefined, prompt: prompt || undefined };
  if (isBridge) {
    body.type = type;
    if (mode) body.mode = mode;
    if (systemPrompt) body.system_prompt = systemPrompt;
  } else {
    body.work_dir = workDir;
  }

  const resp = await fetch('/api/spawn', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify(body),
  });

  const result = await resp.json();
  if (!resp.ok) {
    showToast(result.error?.message || 'Spawn failed', 'var(--red)');
    return;
  }

  if (projectId && workDir) {
    projectPaths[projectId] = workDir;
    localStorage.setItem('waggle_project_paths', JSON.stringify(projectPaths));
  }
  showToast(`Spawned ${name} (${type})`, 'var(--cyan)');
  closePanel();
  await fetchAll();
}
```

- [ ] **Step 4: Verify manually**

Run: `cd /Users/maniginam/projects/waggle && go build ./cmd/waggle && ./waggle start &`
Open: `http://localhost:4740`, click spawn, verify the type dropdown appears and shows/hides fields correctly. Stop the server.

- [ ] **Step 5: Commit**

```bash
cd /Users/maniginam/projects/waggle
git add internal/dashboard/static/index.html
git -c commit.gpgsign=false commit -m "Add agent type selector and bridge fields to dashboard spawn UI"
```

---

### Task 11: Full Integration Test

**Files:**
- Modify: `internal/bridge/runner_test.go`

- [ ] **Step 1: Write an end-to-end test**

Add to `internal/bridge/runner_test.go`:

```go
func TestRunnerFullParticipantClaimsTask(t *testing.T) {
	var mu sync.Mutex
	taskClaimed := false
	taskCompleted := false
	var commentBody string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch {
		case r.URL.Path == "/api/agents/register":
			json.NewEncoder(w).Encode(map[string]any{"id": "ag-1", "name": "worker"})

		case r.URL.Path == "/api/messages" && r.Method == http.MethodGet:
			json.NewEncoder(w).Encode([]any{})

		case r.URL.Path == "/api/tasks" && r.Method == http.MethodGet:
			if !taskClaimed {
				json.NewEncoder(w).Encode([]map[string]any{
					{"id": "t-1", "title": "Write docs", "description": "Write API docs", "status": "ready",
						"criteria": []string{"clear", "concise"}},
				})
			} else {
				json.NewEncoder(w).Encode([]any{})
			}

		case r.URL.Path == "/api/tasks/t-1/claim":
			taskClaimed = true
			json.NewEncoder(w).Encode(map[string]any{"status": "ok"})

		case r.URL.Path == "/api/tasks/t-1/comments" && r.Method == http.MethodPost:
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			commentBody = body["body"]
			json.NewEncoder(w).Encode(map[string]any{"status": "ok"})

		case r.URL.Path == "/api/tasks/t-1/complete":
			taskCompleted = true
			json.NewEncoder(w).Encode(map[string]any{"status": "ok"})

		default:
			json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	r := NewRunner(
		&fakeBridge{response: "Here are the docs", provider: "test", caps: []Capability{CapChat, CapCode}},
		RunnerConfig{
			AgentName:    "worker",
			BaseURL:      srv.URL,
			Mode:         ModeFullParticipant,
			PollInterval: 50 * time.Millisecond,
			TaskInterval: 100 * time.Millisecond,
		},
	)
	r.Run(ctx)

	mu.Lock()
	defer mu.Unlock()
	if !taskClaimed {
		t.Error("expected task to be claimed")
	}
	if commentBody != "Here are the docs" {
		t.Errorf("expected comment 'Here are the docs', got %q", commentBody)
	}
	if !taskCompleted {
		t.Error("expected task to be completed")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/maniginam/projects/waggle && go test ./internal/bridge/ -v -run TestRunnerFullParticipant`
Expected: FAIL — `TaskInterval` field not in `RunnerConfig`, task polling not implemented

- [ ] **Step 3: Add task claiming to the runner**

Add `TaskInterval` to `RunnerConfig` in `internal/bridge/runner.go`:

```go
type RunnerConfig struct {
	AgentName         string
	BaseURL           string
	Mode              Mode
	ProjectID         string
	SystemPrompt      string
	HeartbeatInterval time.Duration
	PollInterval      time.Duration
	TaskInterval      time.Duration
}
```

Update `NewRunner` defaults:

```go
if cfg.TaskInterval == 0 {
	cfg.TaskInterval = 5 * time.Second
}
```

Add a task polling ticker in `Run()`, only when mode is full_participant:

```go
func (r *Runner) Run(ctx context.Context) {
	if err := r.register(); err != nil {
		log.Printf("bridge/%s: register failed: %v", r.config.AgentName, err)
		return
	}
	log.Printf("bridge/%s: registered as %s (%s mode)", r.config.AgentName, r.bridge.Provider(), r.config.Mode)

	heartbeat := time.NewTicker(r.config.HeartbeatInterval)
	poll := time.NewTicker(r.config.PollInterval)
	defer heartbeat.Stop()
	defer poll.Stop()

	var taskTicker *time.Ticker
	if r.config.Mode == ModeFullParticipant {
		taskTicker = time.NewTicker(r.config.TaskInterval)
		defer taskTicker.Stop()
	}

	for {
		select {
		case <-ctx.Done():
			r.disconnect()
			return
		case <-heartbeat.C:
			r.sendHeartbeat()
		case <-poll.C:
			r.pollMessages(ctx)
		case <-tickerChan(taskTicker):
			r.pollTasks(ctx)
		}
	}
}

func tickerChan(t *time.Ticker) <-chan time.Time {
	if t == nil {
		return nil // nil channel blocks forever
	}
	return t.C
}
```

Add the task polling method:

```go
type apiTask struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Status      string   `json:"status"`
	Criteria    []string `json:"criteria"`
}

func (r *Runner) pollTasks(ctx context.Context) {
	if r.working {
		return
	}

	query := "/api/tasks?status=ready"
	if r.config.ProjectID != "" {
		query += "&project_id=" + r.config.ProjectID
	}
	resp, err := r.get(query)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	var tasks []apiTask
	if err := json.NewDecoder(resp.Body).Decode(&tasks); err != nil || len(tasks) == 0 {
		return
	}

	task := tasks[0]
	r.claimAndWork(ctx, task)
}

func (r *Runner) claimAndWork(ctx context.Context, task apiTask) {
	// Claim
	_, err := r.post("/api/tasks/"+task.ID+"/claim", map[string]string{"agent": r.config.AgentName})
	if err != nil {
		return
	}
	r.working = true
	defer func() { r.working = false }()

	// Build prompt from task
	prompt := fmt.Sprintf("Task: %s\n", task.Title)
	if task.Description != "" {
		prompt += fmt.Sprintf("Description: %s\n", task.Description)
	}
	if len(task.Criteria) > 0 {
		prompt += "Acceptance Criteria:\n"
		for _, c := range task.Criteria {
			prompt += fmt.Sprintf("- %s\n", c)
		}
	}

	msgs := []Message{{Role: "user", Content: prompt}}
	opts := ChatOpts{SystemPrompt: r.config.SystemPrompt}

	response, err := r.bridge.Chat(ctx, msgs, opts)
	if err != nil {
		log.Printf("bridge/%s: task chat error: %v", r.config.AgentName, err)
		return
	}

	// Post result as comment
	r.post("/api/tasks/"+task.ID+"/comments", map[string]string{
		"author": r.config.AgentName,
		"body":   response,
	})

	// Complete task
	r.post("/api/tasks/"+task.ID+"/complete", map[string]string{"agent": r.config.AgentName})
}
```

Add `working bool` field to the `Runner` struct.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/maniginam/projects/waggle && go test ./internal/bridge/ -v -timeout 30s`
Expected: PASS

- [ ] **Step 5: Run full test suite**

Run: `cd /Users/maniginam/projects/waggle && go test ./... -timeout 60s`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
cd /Users/maniginam/projects/waggle
git add internal/bridge/runner.go internal/bridge/runner_test.go
git -c commit.gpgsign=false commit -m "Add full-participant task claiming to bridge runner"
```

---

### Task 12: Stop Bridge Agents

**Files:**
- Modify: `internal/api/api.go`
- Modify: `internal/api/api_test.go`

- [ ] **Step 1: Write test for stopping bridge agents**

Add to `internal/api/api_test.go`:

```go
func TestStopBridgeAgent(t *testing.T) {
	_, ts := setup(t)

	// Spawn a bridge agent first
	resp := mustPost(t, ts.URL+"/api/spawn", "application/json",
		strings.NewReader(`{"name": "stop-me", "type": "ollama"}`))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("spawn failed: %d", resp.StatusCode)
	}

	// Give it a moment to register
	time.Sleep(100 * time.Millisecond)

	// Stop via session action
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/sessions/stop-me", nil)
	stopResp := mustDo(t, req)
	if stopResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(stopResp.Body)
		t.Fatalf("expected 200, got %d: %s", stopResp.StatusCode, string(body))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/maniginam/projects/waggle && go test ./internal/api/ -v -run TestStopBridge -timeout 30s`
Expected: FAIL — session delete doesn't know about bridge runners

- [ ] **Step 3: Update handleSessionAction to handle bridge agents**

In `internal/api/api.go`, in the `handleSessionAction` function, in the DELETE case (where it kills tmux/process), add a check for bridge runners before the existing process check:

```go
// Check for bridge runner first
a.procsMu.Lock()
if cancel, ok := a.bridgeRunners[name]; ok {
	cancel()
	delete(a.bridgeRunners, name)
	a.procsMu.Unlock()
	a.store.DisconnectAgent(name)
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped", "name": name})
	return
}
a.procsMu.Unlock()
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/maniginam/projects/waggle && go test ./internal/api/ -v -run TestStopBridge -timeout 30s`
Expected: PASS

- [ ] **Step 5: Run full test suite**

Run: `cd /Users/maniginam/projects/waggle && go test ./... -timeout 60s`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
cd /Users/maniginam/projects/waggle
git add internal/api/api.go internal/api/api_test.go
git -c commit.gpgsign=false commit -m "Support stopping bridge agents via session delete"
```
