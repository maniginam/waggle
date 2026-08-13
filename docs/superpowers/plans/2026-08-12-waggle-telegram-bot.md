# Waggle Telegram Bot Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a two-way Telegram interface to Waggle — outbound event/digest notifications and inbound slash-command + inline-button + natural-language control — as a new opt-in `internal/telegram` package.

**Architecture:** In-process adapter (sibling to `internal/mcp`), gated by `WAGGLE_TELEGRAM_ENABLED`, transport = Telegram long-polling (`getUpdates`). All Waggle reads/writes go through the local REST API over `http://localhost:<port>` (reusing every handler's validation + SSE emit). Outbound subscribes to the existing `event.Hub` plus a digest scheduler. NL fallback uses the official Anthropic Go SDK (Claude Haiku 4.5) behind a swappable `NLParser` interface.

**Tech Stack:** Go, `net/http` + `encoding/json` for the Telegram Bot API client, `github.com/anthropics/anthropic-sdk-go` for the NL parser, the existing `internal/event` hub, `internal/store`, `internal/api`.

## Global Constraints

- All features opt-in and OFF by default: nothing runs unless `WAGGLE_TELEGRAM_ENABLED=true` AND `WAGGLE_TELEGRAM_TOKEN` is set. Mirror the overseer opt-in in `internal/server/server.go:115` (`os.Getenv("OVERSEER_ENABLED") == "true"`).
- **Security:** Bot token read ONLY from `WAGGLE_TELEGRAM_TOKEN` (env). NEVER hardcode it, NEVER log it (not even masked). Enforce the `WAGGLE_TELEGRAM_ALLOWED_CHATS` allowlist (comma-separated integer chat IDs) BEFORE any handler runs; ignore non-allowlisted chats. `ANTHROPIC_API_KEY` (env) is read by the NL parser only; if absent, NL degrades gracefully.
- TDD mandatory: failing test first, minimal code to pass, refactor while green. Commit after each green step.
- Commits MUST use `git commit --no-gpg-sign`. No Co-Authored-By or attribution footers.
- Test helpers to follow: an in-process `api` httptest server is built as in `internal/mcp/mcp_test.go` (`store.New(tmp)` → `event.NewHub()` → `api.New(s, eh)` → `httptest.NewServer(a.Handler())`). Telegram API is faked with `httptest.NewServer`; the base URL is injectable on the client.
- Model IDs: use `anthropic.ModelClaudeHaiku4_5_20251001` exactly. Do NOT set `thinking` or `effort` on Haiku (both error). Parse tool inputs with `json.Unmarshal`, never raw string-match.
- Timestamps RFC3339 UTC where relevant. Follow existing package patterns (small files, one responsibility each).
- Telegram `callback_data` must be ≤64 bytes.

## File structure (all new, one responsibility each)

- `internal/telegram/config.go` — `Config` + `ConfigFromEnv()` + allowlist parsing/check.
- `internal/telegram/client.go` — Telegram Bot API client (getUpdates, sendMessage, editMessageText, answerCallbackQuery); base URL injectable.
- `internal/telegram/waggle.go` — thin Waggle REST client (get/post/patch over localhost) + typed helpers used by handlers.
- `internal/telegram/commands.go` — slash-command + callback handlers, reply formatting, inline keyboards.
- `internal/telegram/nl.go` — `NLParser` interface, `ClaudeNLParser` (Go SDK Haiku), `Intent` type.
- `internal/telegram/inbound.go` — long-poll loop, allowlist gate, update router (slash / callback / NL).
- `internal/telegram/outbound.go` — event-hub subscriber + digest scheduler + loop-suppression.
- `internal/telegram/bot.go` — `New(...)` + `Run(ctx)` orchestrator.
- `internal/server/server.go` — one opt-in wiring block (modify).
- `go.mod` / `go.sum` — add the Anthropic Go SDK (Task 5).

---

### Task 1: Config and allowlist

**Files:**
- Create: `internal/telegram/config.go`
- Test: `internal/telegram/config_test.go`

**Interfaces:**
- Produces:
  - `type Config struct { Token string; AllowedChats []int64; Port string; APIBaseURL string; TelegramBaseURL string }`
  - `func ConfigFromEnv() Config` — Token from `WAGGLE_TELEGRAM_TOKEN`; AllowedChats parsed from `WAGGLE_TELEGRAM_ALLOWED_CHATS` (comma-separated int64, bad entries skipped); Port from `WAGGLE_PORT` (default `"4740"`); `APIBaseURL` = `"http://localhost:"+Port`; `TelegramBaseURL` = `"https://api.telegram.org/bot"+Token`.
  - `func (c Config) ChatAllowed(id int64) bool` — true iff `id` in `AllowedChats` (empty list = deny all).

- [ ] **Step 1: Write the failing test**

```go
package telegram

import (
	"os"
	"testing"
)

func TestConfigFromEnvParsesAllowlist(t *testing.T) {
	os.Setenv("WAGGLE_TELEGRAM_TOKEN", "tok123")
	os.Setenv("WAGGLE_TELEGRAM_ALLOWED_CHATS", "111, 222 ,bad,333")
	os.Setenv("WAGGLE_PORT", "4999")
	defer func() {
		os.Unsetenv("WAGGLE_TELEGRAM_TOKEN")
		os.Unsetenv("WAGGLE_TELEGRAM_ALLOWED_CHATS")
		os.Unsetenv("WAGGLE_PORT")
	}()
	c := ConfigFromEnv()
	if c.Token != "tok123" {
		t.Errorf("token = %q", c.Token)
	}
	if len(c.AllowedChats) != 3 {
		t.Fatalf("expected 3 chats, got %d (%v)", len(c.AllowedChats), c.AllowedChats)
	}
	if c.APIBaseURL != "http://localhost:4999" {
		t.Errorf("APIBaseURL = %q", c.APIBaseURL)
	}
	if !c.ChatAllowed(222) || c.ChatAllowed(999) {
		t.Errorf("allowlist check wrong")
	}
}

func TestChatAllowedEmptyDeniesAll(t *testing.T) {
	c := Config{}
	if c.ChatAllowed(1) {
		t.Error("empty allowlist must deny")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/telegram/ -run 'TestConfigFromEnvParsesAllowlist|TestChatAllowedEmptyDeniesAll' -v`
Expected: FAIL (package/undefined).

- [ ] **Step 3: Write minimal implementation**

```go
package telegram

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Token           string
	AllowedChats    []int64
	Port            string
	APIBaseURL      string
	TelegramBaseURL string
}

func ConfigFromEnv() Config {
	port := os.Getenv("WAGGLE_PORT")
	if port == "" {
		port = "4740"
	}
	token := os.Getenv("WAGGLE_TELEGRAM_TOKEN")
	var chats []int64
	for _, part := range strings.Split(os.Getenv("WAGGLE_TELEGRAM_ALLOWED_CHATS"), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if id, err := strconv.ParseInt(part, 10, 64); err == nil {
			chats = append(chats, id)
		}
	}
	return Config{
		Token:           token,
		AllowedChats:    chats,
		Port:            port,
		APIBaseURL:      "http://localhost:" + port,
		TelegramBaseURL: "https://api.telegram.org/bot" + token,
	}
}

func (c Config) ChatAllowed(id int64) bool {
	for _, a := range c.AllowedChats {
		if a == id {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/telegram/ -run 'TestConfigFromEnvParsesAllowlist|TestChatAllowedEmptyDeniesAll' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/telegram/config.go internal/telegram/config_test.go
git commit --no-gpg-sign -m "feat(telegram): config and chat allowlist"
```

---

### Task 2: Telegram Bot API client

**Files:**
- Create: `internal/telegram/client.go`
- Test: `internal/telegram/client_test.go`

**Interfaces:**
- Consumes: `Config` (Task 1) — only `TelegramBaseURL`.
- Produces:
  - `type InlineButton struct { Text string; CallbackData string }`
  - `type Update struct { UpdateID int64; Message *Message; CallbackQuery *CallbackQuery }` with `type Message struct { MessageID int64; Chat Chat; Text string }`, `type Chat struct { ID int64 }`, `type CallbackQuery struct { ID string; Data string; Message *Message; From User }`, `type User struct { ID int64 }`.
  - `type Client struct { baseURL string; http *http.Client }`
  - `func NewClient(baseURL string) *Client`
  - `func (c *Client) GetUpdates(ctx context.Context, offset int64, timeoutSec int) ([]Update, error)`
  - `func (c *Client) SendMessage(ctx context.Context, chatID int64, text string, buttons [][]InlineButton) (int64, error)` — returns sent message ID; `buttons` nil = plain message.
  - `func (c *Client) EditMessageText(ctx context.Context, chatID, messageID int64, text string, buttons [][]InlineButton) error`
  - `func (c *Client) AnswerCallbackQuery(ctx context.Context, callbackID, text string) error`
- Never logs the token; the token lives only in `baseURL`.

- [ ] **Step 1: Write the failing test**

```go
package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendMessageWithButtons(t *testing.T) {
	var gotPath, gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true,"result":{"message_id":42}}`)
	}))
	defer ts.Close()

	c := NewClient(ts.URL)
	id, err := c.SendMessage(context.Background(), 111, "hello",
		[][]InlineButton{{{Text: "Done", CallbackData: "mv:t1:done"}}})
	if err != nil {
		t.Fatal(err)
	}
	if id != 42 {
		t.Errorf("message id = %d", id)
	}
	if !strings.HasSuffix(gotPath, "/sendMessage") {
		t.Errorf("path = %q", gotPath)
	}
	var payload map[string]any
	json.Unmarshal([]byte(gotBody), &payload)
	if payload["chat_id"].(float64) != 111 || payload["text"] != "hello" {
		t.Errorf("bad payload: %s", gotBody)
	}
	if _, ok := payload["reply_markup"]; !ok {
		t.Errorf("expected reply_markup, got %s", gotBody)
	}
}

func TestGetUpdatesParses(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"ok":true,"result":[{"update_id":5,"message":{"message_id":1,"chat":{"id":111},"text":"/next"}}]}`)
	}))
	defer ts.Close()
	c := NewClient(ts.URL)
	ups, err := c.GetUpdates(context.Background(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ups) != 1 || ups[0].Message.Text != "/next" || ups[0].Message.Chat.ID != 111 {
		t.Fatalf("bad updates: %+v", ups)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/telegram/ -run 'TestSendMessageWithButtons|TestGetUpdatesParses' -v`
Expected: FAIL (undefined).

- [ ] **Step 3: Write minimal implementation**

```go
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

type Chat struct {
	ID int64 `json:"id"`
}

type User struct {
	ID int64 `json:"id"`
}

type Message struct {
	MessageID int64  `json:"message_id"`
	Chat      Chat   `json:"chat"`
	Text      string `json:"text"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	Data    string   `json:"data"`
	Message *Message `json:"message"`
	From    User     `json:"from"`
}

type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

type InlineButton struct {
	Text         string
	CallbackData string
}

type Client struct {
	baseURL string
	http    *http.Client
}

func NewClient(baseURL string) *Client {
	return &Client{baseURL: baseURL, http: &http.Client{Timeout: 65 * time.Second}}
}

func (c *Client) call(ctx context.Context, method string, body any, out any) error {
	data, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/"+method, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var envelope struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return err
	}
	if !envelope.OK {
		return fmt.Errorf("telegram %s failed: %s", method, envelope.Description)
	}
	if out != nil && len(envelope.Result) > 0 {
		return json.Unmarshal(envelope.Result, out)
	}
	return nil
}

func replyMarkup(buttons [][]InlineButton) map[string]any {
	if len(buttons) == 0 {
		return nil
	}
	rows := make([][]map[string]string, 0, len(buttons))
	for _, row := range buttons {
		cells := make([]map[string]string, 0, len(row))
		for _, b := range row {
			cells = append(cells, map[string]string{"text": b.Text, "callback_data": b.CallbackData})
		}
		rows = append(rows, cells)
	}
	return map[string]any{"inline_keyboard": rows}
}

func (c *Client) GetUpdates(ctx context.Context, offset int64, timeoutSec int) ([]Update, error) {
	var updates []Update
	err := c.call(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": timeoutSec}, &updates)
	return updates, err
}

func (c *Client) SendMessage(ctx context.Context, chatID int64, text string, buttons [][]InlineButton) (int64, error) {
	body := map[string]any{"chat_id": chatID, "text": text}
	if rm := replyMarkup(buttons); rm != nil {
		body["reply_markup"] = rm
	}
	var out Message
	if err := c.call(ctx, "sendMessage", body, &out); err != nil {
		return 0, err
	}
	return out.MessageID, nil
}

func (c *Client) EditMessageText(ctx context.Context, chatID, messageID int64, text string, buttons [][]InlineButton) error {
	body := map[string]any{"chat_id": chatID, "message_id": messageID, "text": text}
	if rm := replyMarkup(buttons); rm != nil {
		body["reply_markup"] = rm
	}
	return c.call(ctx, "editMessageText", body, nil)
}

func (c *Client) AnswerCallbackQuery(ctx context.Context, callbackID, text string) error {
	return c.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": callbackID, "text": text}, nil)
}

var _ = url.QueryEscape // keep net/url import if unused after edits; remove if not needed
```

Note: delete the trailing `var _ = url.QueryEscape` line and the `net/url` import if you don't use them — they are only a scaffolding reminder.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/telegram/ -run 'TestSendMessageWithButtons|TestGetUpdatesParses' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/telegram/client.go internal/telegram/client_test.go
git commit --no-gpg-sign -m "feat(telegram): bot API client (getUpdates, sendMessage, edit, answer)"
```

---

### Task 3: Waggle REST client

**Files:**
- Create: `internal/telegram/waggle.go`
- Test: `internal/telegram/waggle_test.go`

**Interfaces:**
- Consumes: the local REST API (Tasks in the agile engine + context manager already shipped): `GET /api/tasks?...`, `POST /api/tasks/{id}/move`, `GET /api/sprints?project_id=`, `GET /api/sprints/{id}/burndown`, `GET /api/whats-next`, `GET /api/projects`, `POST /api/tasks`.
- Produces:
  - `type WaggleClient struct { baseURL string; http *http.Client }`
  - `func NewWaggleClient(baseURL string) *WaggleClient`
  - `func (w *WaggleClient) get(path string) ([]byte, error)` and `func (w *WaggleClient) post(path string, body any) ([]byte, error)` — return raw JSON bytes; non-2xx → error.
  - `func (w *WaggleClient) ListTasks(projectID string) ([]map[string]any, error)` — `GET /api/tasks` (+`?project_id=`), returns decoded task objects.
  - `func (w *WaggleClient) MoveTask(taskID, status string) error` — `POST /api/tasks/{id}/move` body `{status}` (board_order omitted, appends).
  - `func (w *WaggleClient) WhatsNext() ([]byte, error)` — raw `GET /api/whats-next`.
  - `func (w *WaggleClient) CreateTask(title, projectID string) (map[string]any, error)` — `POST /api/tasks`.

- [ ] **Step 1: Write the failing test**

```go
package telegram

import (
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/maniginam/waggle/internal/api"
	"github.com/maniginam/waggle/internal/event"
	"github.com/maniginam/waggle/internal/store"
)

func newAPIServer(t *testing.T) (*store.Store, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	eh := event.NewHub()
	a := api.New(s, eh)
	ts := httptest.NewServer(a.Handler())
	t.Cleanup(ts.Close)
	return s, ts
}

func TestWaggleClientCreateListMove(t *testing.T) {
	_, ts := newAPIServer(t)
	w := NewWaggleClient(ts.URL)

	created, err := w.CreateTask("bot task", "p1")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("no task id returned")
	}
	tasks, err := w.ListTasks("p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if err := w.MoveTask(id, "in_progress"); err != nil {
		t.Fatal(err)
	}
	tasks, _ = w.ListTasks("p1")
	if tasks[0]["status"] != "in_progress" {
		t.Errorf("status = %v", tasks[0]["status"])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/telegram/ -run TestWaggleClientCreateListMove -v`
Expected: FAIL (undefined NewWaggleClient).

- [ ] **Step 3: Write minimal implementation**

```go
package telegram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type WaggleClient struct {
	baseURL string
	http    *http.Client
}

func NewWaggleClient(baseURL string) *WaggleClient {
	return &WaggleClient{baseURL: baseURL, http: &http.Client{Timeout: 10 * time.Second}}
}

func (w *WaggleClient) get(path string) ([]byte, error) {
	resp, err := w.http.Get(w.baseURL + path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("GET %s: %s", path, string(body))
	}
	return body, nil
}

func (w *WaggleClient) post(path string, payload any) ([]byte, error) {
	var reader io.Reader
	if payload != nil {
		data, _ := json.Marshal(payload)
		reader = bytes.NewReader(data)
	}
	resp, err := w.http.Post(w.baseURL+path, "application/json", reader)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("POST %s: %s", path, string(body))
	}
	return body, nil
}

func (w *WaggleClient) ListTasks(projectID string) ([]map[string]any, error) {
	path := "/api/tasks"
	if projectID != "" {
		path += "?project_id=" + url.QueryEscape(projectID)
	}
	body, err := w.get(path)
	if err != nil {
		return nil, err
	}
	var tasks []map[string]any
	if err := json.Unmarshal(body, &tasks); err != nil {
		return nil, err
	}
	return tasks, nil
}

func (w *WaggleClient) MoveTask(taskID, status string) error {
	_, err := w.post("/api/tasks/"+url.PathEscape(taskID)+"/move", map[string]any{"status": status})
	return err
}

func (w *WaggleClient) WhatsNext() ([]byte, error) {
	return w.get("/api/whats-next")
}

func (w *WaggleClient) CreateTask(title, projectID string) (map[string]any, error) {
	body, err := w.post("/api/tasks", map[string]any{"title": title, "project_id": projectID})
	if err != nil {
		return nil, err
	}
	var task map[string]any
	if err := json.Unmarshal(body, &task); err != nil {
		return nil, err
	}
	return task, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/telegram/ -run TestWaggleClientCreateListMove -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/telegram/waggle.go internal/telegram/waggle_test.go
git commit --no-gpg-sign -m "feat(telegram): local Waggle REST client"
```

---

### Task 4: Slash commands and inline-button callbacks

**Files:**
- Create: `internal/telegram/commands.go`
- Test: `internal/telegram/commands_test.go`

**Interfaces:**
- Consumes: `Client` (Task 2), `WaggleClient` (Task 3).
- Produces:
  - `type Handler struct { tg *Client; wg *WaggleClient }`
  - `func NewHandler(tg *Client, wg *WaggleClient) *Handler`
  - `func (h *Handler) HandleCommand(ctx context.Context, chatID int64, text string)` — routes `/help`, `/next`, `/tasks`, and (for `/create <title>`) creates a task. Unknown commands → `/help` text. Each renders a reply via `tg.SendMessage`. `/tasks` renders each task with an inline button row `[In Progress][Review][Done]` whose `callback_data` is `mv:<shortID>:<status>`.
  - `func (h *Handler) HandleCallback(ctx context.Context, cb *CallbackQuery)` — parses `mv:<taskID>:<status>`, calls `wg.MoveTask`, edits the source message to confirm, and `AnswerCallbackQuery`.
  - `func moveButtons(taskID string) [][]InlineButton` — the three status buttons (statuses `in_progress`, `review`, `done`).
- Interface note for later tasks: the router (Task 6) calls `HandleCommand` for `/`-prefixed text and `HandleCallback` for callback queries.

- [ ] **Step 1: Write the failing test**

```go
package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeTelegram records outbound sendMessage/editMessageText calls.
func fakeTelegram(t *testing.T, sink *[]string, mu *sync.Mutex) *Client {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		*sink = append(*sink, r.URL.Path+" "+string(b))
		mu.Unlock()
		io.WriteString(w, `{"ok":true,"result":{"message_id":1}}`)
	}))
	t.Cleanup(ts.Close)
	return NewClient(ts.URL)
}

func TestHandleCommandCreateAndTasks(t *testing.T) {
	_, api := newAPIServer(t)
	wg := NewWaggleClient(api.URL)
	var sink []string
	var mu sync.Mutex
	tg := fakeTelegram(t, &sink, &mu)
	h := NewHandler(tg, wg)

	h.HandleCommand(context.Background(), 111, "/create ship the bot")
	tasks, _ := wg.ListTasks("")
	if len(tasks) != 1 || tasks[0]["title"] != "ship the bot" {
		t.Fatalf("task not created: %+v", tasks)
	}
	h.HandleCommand(context.Background(), 111, "/tasks")
	mu.Lock()
	joined := strings.Join(sink, "\n")
	mu.Unlock()
	if !strings.Contains(joined, "sendMessage") || !strings.Contains(joined, "reply_markup") {
		t.Errorf("expected /tasks to send a message with buttons, got:\n%s", joined)
	}
}

func TestHandleCallbackMovesTask(t *testing.T) {
	_, api := newAPIServer(t)
	wg := NewWaggleClient(api.URL)
	created, _ := wg.CreateTask("t", "")
	id := created["id"].(string)

	var sink []string
	var mu sync.Mutex
	tg := fakeTelegram(t, &sink, &mu)
	h := NewHandler(tg, wg)

	h.HandleCallback(context.Background(), &CallbackQuery{
		ID:      "cb1",
		Data:    "mv:" + id + ":done",
		Message: &Message{MessageID: 7, Chat: Chat{ID: 111}},
	})
	tasks, _ := wg.ListTasks("")
	if tasks[0]["status"] != "done" {
		t.Errorf("status = %v", tasks[0]["status"])
	}
	mu.Lock()
	joined := strings.Join(sink, "\n")
	mu.Unlock()
	if !strings.Contains(joined, "answerCallbackQuery") {
		t.Errorf("expected answerCallbackQuery, got:\n%s", joined)
	}
	_ = json.Marshal // keep import if unused
}
```

Note: drop the trailing `_ = json.Marshal` and the `encoding/json` import if unused after you finish.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/telegram/ -run 'TestHandleCommandCreateAndTasks|TestHandleCallbackMovesTask' -v`
Expected: FAIL (undefined NewHandler).

- [ ] **Step 3: Write minimal implementation**

```go
package telegram

import (
	"context"
	"fmt"
	"strings"
)

type Handler struct {
	tg *Client
	wg *WaggleClient
}

func NewHandler(tg *Client, wg *WaggleClient) *Handler {
	return &Handler{tg: tg, wg: wg}
}

const helpText = "Waggle bot commands:\n" +
	"/next - what needs attention across projects\n" +
	"/tasks [project] - list tasks with action buttons\n" +
	"/create <title> - create a task\n" +
	"/help - this message"

func moveButtons(taskID string) [][]InlineButton {
	return [][]InlineButton{{
		{Text: "In Progress", CallbackData: "mv:" + taskID + ":in_progress"},
		{Text: "Review", CallbackData: "mv:" + taskID + ":review"},
		{Text: "Done", CallbackData: "mv:" + taskID + ":done"},
	}}
}

func (h *Handler) HandleCommand(ctx context.Context, chatID int64, text string) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return
	}
	cmd := fields[0]
	arg := strings.TrimSpace(strings.TrimPrefix(text, cmd))
	switch cmd {
	case "/next":
		body, err := h.wg.WhatsNext()
		if err != nil {
			h.tg.SendMessage(ctx, chatID, "error: "+err.Error(), nil)
			return
		}
		h.tg.SendMessage(ctx, chatID, "What's next:\n"+string(body), nil)
	case "/tasks":
		tasks, err := h.wg.ListTasks(arg)
		if err != nil {
			h.tg.SendMessage(ctx, chatID, "error: "+err.Error(), nil)
			return
		}
		if len(tasks) == 0 {
			h.tg.SendMessage(ctx, chatID, "No tasks.", nil)
			return
		}
		for _, task := range tasks {
			id, _ := task["id"].(string)
			title, _ := task["title"].(string)
			status, _ := task["status"].(string)
			h.tg.SendMessage(ctx, chatID, fmt.Sprintf("%s [%s]", title, status), moveButtons(id))
		}
	case "/create":
		if arg == "" {
			h.tg.SendMessage(ctx, chatID, "usage: /create <title>", nil)
			return
		}
		task, err := h.wg.CreateTask(arg, "")
		if err != nil {
			h.tg.SendMessage(ctx, chatID, "error: "+err.Error(), nil)
			return
		}
		h.tg.SendMessage(ctx, chatID, "Created: "+task["title"].(string), nil)
	default:
		h.tg.SendMessage(ctx, chatID, helpText, nil)
	}
}

func (h *Handler) HandleCallback(ctx context.Context, cb *CallbackQuery) {
	parts := strings.SplitN(cb.Data, ":", 3)
	if len(parts) != 3 || parts[0] != "mv" {
		h.tg.AnswerCallbackQuery(ctx, cb.ID, "unknown action")
		return
	}
	taskID, status := parts[1], parts[2]
	if err := h.wg.MoveTask(taskID, status); err != nil {
		h.tg.AnswerCallbackQuery(ctx, cb.ID, "error")
		return
	}
	if cb.Message != nil {
		h.tg.EditMessageText(ctx, cb.Message.Chat.ID, cb.Message.MessageID,
			fmt.Sprintf("Moved to %s", status), nil)
	}
	h.tg.AnswerCallbackQuery(ctx, cb.ID, "Moved to "+status)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/telegram/ -run 'TestHandleCommandCreateAndTasks|TestHandleCallbackMovesTask' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/telegram/commands.go internal/telegram/commands_test.go
git commit --no-gpg-sign -m "feat(telegram): slash commands and inline-button callbacks"
```

---

### Task 5: Natural-language parser (Claude Haiku behind an interface)

**Files:**
- Create: `internal/telegram/nl.go`
- Test: `internal/telegram/nl_test.go`
- Modify: `go.mod`, `go.sum` (add the Anthropic Go SDK)

**Interfaces:**
- Produces:
  - `type Intent struct { Action string; Args map[string]string }`
  - `type NLParser interface { Parse(ctx context.Context, text string) (Intent, error) }`
  - `type ClaudeNLParser struct { client anthropic.Client }`
  - `func NewClaudeNLParser() (*ClaudeNLParser, bool)` — returns `(nil, false)` if `ANTHROPIC_API_KEY` is unset (NL disabled); else a parser and `true`. Uses `anthropic.NewClient()` (reads `ANTHROPIC_API_KEY`).
  - `func (p *ClaudeNLParser) Parse(ctx, text) (Intent, error)` — one `client.Messages.New` call, model `anthropic.ModelClaudeHaiku4_5_20251001`, `MaxTokens: 1024`, a single tool `route` whose input schema is `{action: enum[list_tasks, create_task, whats_next, move_task, help], title?: string, task_id?: string, status?: string, project?: string}`, plus a system instruction to always call `route`. Parse the first `ToolUseBlock` input into `Intent` (Action = `action`; Args = the remaining string fields). If no tool block, return `Intent{Action:"help"}`.
- The `route` action set intentionally maps 1:1 onto the command handlers (Task 6 dispatches on it). Do NOT set `Thinking` or `effort` on Haiku.

- [ ] **Step 1: Add the SDK dependency**

Run:
```bash
go get github.com/anthropics/anthropic-sdk-go@latest
```
Expected: `go.mod`/`go.sum` updated.

- [ ] **Step 2: Write the failing test** (fake parser + a real-parser guard that skips without a key)

```go
package telegram

import (
	"context"
	"os"
	"testing"
)

type fakeNLParser struct{ intent Intent }

func (f fakeNLParser) Parse(_ context.Context, _ string) (Intent, error) { return f.intent, nil }

func TestNLParserDisabledWithoutKey(t *testing.T) {
	old := os.Getenv("ANTHROPIC_API_KEY")
	os.Unsetenv("ANTHROPIC_API_KEY")
	defer os.Setenv("ANTHROPIC_API_KEY", old)
	if _, ok := NewClaudeNLParser(); ok {
		t.Error("expected NL disabled when ANTHROPIC_API_KEY is unset")
	}
}

func TestFakeNLParserRoundtrips(t *testing.T) {
	f := fakeNLParser{intent: Intent{Action: "create_task", Args: map[string]string{"title": "x"}}}
	got, _ := f.Parse(context.Background(), "make a task called x")
	if got.Action != "create_task" || got.Args["title"] != "x" {
		t.Errorf("got %+v", got)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/telegram/ -run 'TestNLParserDisabledWithoutKey|TestFakeNLParserRoundtrips' -v`
Expected: FAIL (undefined `Intent`/`NewClaudeNLParser`).

- [ ] **Step 4: Write minimal implementation**

```go
package telegram

import (
	"context"
	"encoding/json"
	"os"

	"github.com/anthropics/anthropic-sdk-go"
)

type Intent struct {
	Action string
	Args   map[string]string
}

type NLParser interface {
	Parse(ctx context.Context, text string) (Intent, error)
}

type ClaudeNLParser struct {
	client anthropic.Client
}

func NewClaudeNLParser() (*ClaudeNLParser, bool) {
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		return nil, false
	}
	return &ClaudeNLParser{client: anthropic.NewClient()}, true
}

const nlSystem = "You translate a user's chat message into a single Waggle bot action by calling the `route` tool. " +
	"Choose the closest action. If unclear, use action \"help\"."

func routeTool() anthropic.ToolUnionParam {
	tool := anthropic.ToolParam{
		Name:        "route",
		Description: anthropic.String("Route the user's message to a Waggle action."),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"action": map[string]any{
					"type": "string",
					"enum": []string{"list_tasks", "create_task", "whats_next", "move_task", "help"},
				},
				"title":   map[string]any{"type": "string"},
				"task_id": map[string]any{"type": "string"},
				"status":  map[string]any{"type": "string"},
				"project": map[string]any{"type": "string"},
			},
			Required: []string{"action"},
		},
	}
	return anthropic.ToolUnionParam{OfTool: &tool}
}

func (p *ClaudeNLParser) Parse(ctx context.Context, text string) (Intent, error) {
	resp, err := p.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.ModelClaudeHaiku4_5_20251001,
		MaxTokens: 1024,
		System:    []anthropic.TextBlockParam{{Text: nlSystem}},
		Tools:     []anthropic.ToolUnionParam{routeTool()},
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(text))},
	})
	if err != nil {
		return Intent{}, err
	}
	for _, block := range resp.Content {
		if tu, ok := block.AsAny().(anthropic.ToolUseBlock); ok {
			var raw map[string]any
			if err := json.Unmarshal([]byte(tu.JSON.Input.Raw()), &raw); err != nil {
				return Intent{Action: "help"}, nil
			}
			intent := Intent{Args: map[string]string{}}
			for k, v := range raw {
				s, _ := v.(string)
				if k == "action" {
					intent.Action = s
				} else if s != "" {
					intent.Args[k] = s
				}
			}
			if intent.Action == "" {
				intent.Action = "help"
			}
			return intent, nil
		}
	}
	return Intent{Action: "help"}, nil
}
```

Note: if any symbol (`anthropic.ToolInputSchemaParam.Required`, `TextBlockParam`, `ToolParam.InputSchema`) does not match the installed SDK version, WebFetch `https://github.com/anthropics/anthropic-sdk-go` and correct against the actual types — do NOT guess. The manual-loop tool-use shape (`client.Messages.New`, `MessageNewParams{Model,MaxTokens,Tools,Messages}`, `resp.Content` → `block.AsAny().(anthropic.ToolUseBlock)` → `tu.JSON.Input.Raw()`) is from the SDK docs and is the load-bearing part.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/telegram/ -run 'TestNLParserDisabledWithoutKey|TestFakeNLParserRoundtrips' -v && go build ./...`
Expected: PASS + clean build.

- [ ] **Step 6: Commit**

```bash
git add internal/telegram/nl.go internal/telegram/nl_test.go go.mod go.sum
git commit --no-gpg-sign -m "feat(telegram): Claude Haiku NL intent parser behind NLParser interface"
```

---

### Task 6: Inbound router (allowlist gate + slash/callback/NL dispatch)

**Files:**
- Create: `internal/telegram/inbound.go`
- Test: `internal/telegram/inbound_test.go`

**Interfaces:**
- Consumes: `Config` (Task 1), `Handler` (Task 4), `NLParser` (Task 5, may be nil).
- Produces:
  - `type Router struct { cfg Config; h *Handler; nl NLParser }`
  - `func NewRouter(cfg Config, h *Handler, nl NLParser) *Router`
  - `func (r *Router) Dispatch(ctx context.Context, u Update)` — the single per-update entry point:
    - Determine chat ID (from `u.Message.Chat.ID` or `u.CallbackQuery.Message.Chat.ID`); if `!cfg.ChatAllowed(chatID)`, return immediately (ignore).
    - `u.CallbackQuery != nil` → `h.HandleCallback`.
    - `u.Message != nil` and text starts with `/` → `h.HandleCommand`.
    - `u.Message != nil` non-slash text → if `nl != nil`, `nl.Parse` then `r.dispatchIntent`; else send the help text.
  - `func (r *Router) dispatchIntent(ctx, chatID int64, in Intent)` — maps `Intent.Action` to the same handler calls: `list_tasks`→`HandleCommand("/tasks "+project)`, `create_task`→`HandleCommand("/create "+title)`, `whats_next`→`HandleCommand("/next")`, `move_task`→`h.wg.MoveTask` + a confirmation send, `help`/unknown→help text.

- [ ] **Step 1: Write the failing test**

```go
package telegram

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestDispatchIgnoresDisallowedChat(t *testing.T) {
	_, api := newAPIServer(t)
	wg := NewWaggleClient(api.URL)
	var sink []string
	var mu sync.Mutex
	tg := fakeTelegram(t, &sink, &mu)
	r := NewRouter(Config{AllowedChats: []int64{999}}, NewHandler(tg, wg), nil)

	r.Dispatch(context.Background(), Update{Message: &Message{Chat: Chat{ID: 111}, Text: "/next"}})
	mu.Lock()
	n := len(sink)
	mu.Unlock()
	if n != 0 {
		t.Errorf("expected no outbound calls for disallowed chat, got %d", n)
	}
}

func TestDispatchNLIntentCreatesTask(t *testing.T) {
	_, api := newAPIServer(t)
	wg := NewWaggleClient(api.URL)
	var sink []string
	var mu sync.Mutex
	tg := fakeTelegram(t, &sink, &mu)
	nl := fakeNLParser{intent: Intent{Action: "create_task", Args: map[string]string{"title": "from nl"}}}
	r := NewRouter(Config{AllowedChats: []int64{111}}, NewHandler(tg, wg), nl)

	r.Dispatch(context.Background(), Update{Message: &Message{Chat: Chat{ID: 111}, Text: "please make a task from nl"}})
	tasks, _ := wg.ListTasks("")
	if len(tasks) != 1 || tasks[0]["title"] != "from nl" {
		t.Fatalf("nl intent did not create task: %+v", tasks)
	}
	_ = strings.TrimSpace
	_ = http.StatusOK
	_ = httptest.NewServer
}
```

Note: remove the three trailing `_ =` throwaway lines (and unused imports) once real assertions compile.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/telegram/ -run 'TestDispatchIgnoresDisallowedChat|TestDispatchNLIntentCreatesTask' -v`
Expected: FAIL (undefined NewRouter).

- [ ] **Step 3: Write minimal implementation**

```go
package telegram

import "context"

type Router struct {
	cfg Config
	h   *Handler
	nl  NLParser
}

func NewRouter(cfg Config, h *Handler, nl NLParser) *Router {
	return &Router{cfg: cfg, h: h, nl: nl}
}

func (r *Router) Dispatch(ctx context.Context, u Update) {
	var chatID int64
	switch {
	case u.CallbackQuery != nil && u.CallbackQuery.Message != nil:
		chatID = u.CallbackQuery.Message.Chat.ID
	case u.Message != nil:
		chatID = u.Message.Chat.ID
	default:
		return
	}
	if !r.cfg.ChatAllowed(chatID) {
		return
	}
	if u.CallbackQuery != nil {
		r.h.HandleCallback(ctx, u.CallbackQuery)
		return
	}
	text := u.Message.Text
	if len(text) > 0 && text[0] == '/' {
		r.h.HandleCommand(ctx, chatID, text)
		return
	}
	if r.nl == nil {
		r.h.tg.SendMessage(ctx, chatID, helpText, nil)
		return
	}
	intent, err := r.nl.Parse(ctx, text)
	if err != nil {
		r.h.tg.SendMessage(ctx, chatID, helpText, nil)
		return
	}
	r.dispatchIntent(ctx, chatID, intent)
}

func (r *Router) dispatchIntent(ctx context.Context, chatID int64, in Intent) {
	switch in.Action {
	case "list_tasks":
		r.h.HandleCommand(ctx, chatID, "/tasks "+in.Args["project"])
	case "create_task":
		r.h.HandleCommand(ctx, chatID, "/create "+in.Args["title"])
	case "whats_next":
		r.h.HandleCommand(ctx, chatID, "/next")
	case "move_task":
		if err := r.h.wg.MoveTask(in.Args["task_id"], in.Args["status"]); err != nil {
			r.h.tg.SendMessage(ctx, chatID, "error: "+err.Error(), nil)
			return
		}
		r.h.tg.SendMessage(ctx, chatID, "Moved to "+in.Args["status"], nil)
	default:
		r.h.tg.SendMessage(ctx, chatID, helpText, nil)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/telegram/ -run 'TestDispatchIgnoresDisallowedChat|TestDispatchNLIntentCreatesTask' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/telegram/inbound.go internal/telegram/inbound_test.go
git commit --no-gpg-sign -m "feat(telegram): inbound router with allowlist gate and NL dispatch"
```

---

### Task 7: Outbound event notifier with loop suppression

**Files:**
- Create: `internal/telegram/outbound.go`
- Test: `internal/telegram/outbound_test.go`

**Interfaces:**
- Consumes: `*event.Hub`, `Config` (for `AllowedChats`), `Client`.
- Produces:
  - `type Notifier struct { hub *event.Hub; tg *Client; chats []int64; mu sync.Mutex; suppress map[string]int }`
  - `func NewNotifier(hub *event.Hub, tg *Client, chats []int64) *Notifier`
  - `func (n *Notifier) Suppress(taskID string)` — record one self-initiated action for `taskID` (increment a short-lived counter; the next matching event is skipped once).
  - `func (n *Notifier) format(e *model.Event) (string, bool)` — returns the message + `true` for a whitelisted event type (`task_created`, `task_completed`, `message`, and any overseer/change event type the hub carries), else `("", false)`.
  - `func (n *Notifier) Run(ctx context.Context)` — subscribe to the hub (`hub.Subscribe("", "")`), read `sub.Ch` until `ctx.Done()`, and for each whitelisted event that is not currently suppressed for its `TaskID`, send to every allowlisted chat. Unsubscribe on exit.
- The router (Task 4/6) calls `Suppress(taskID)` before a `/move` or button move so the resulting `task_updated` isn't echoed. (For v1, `task_updated` is NOT whitelisted, so echo suppression primarily guards `task_completed` from a `done` move; still record suppression for correctness.)

- [ ] **Step 1: Write the failing test**

```go
package telegram

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maniginam/waggle/internal/event"
	"github.com/maniginam/waggle/internal/model"
)

func TestNotifierSendsWhitelistedEvent(t *testing.T) {
	hub := event.NewHub()
	var sink []string
	var mu sync.Mutex
	tg := fakeTelegram(t, &sink, &mu)
	n := NewNotifier(hub, tg, []int64{111})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go n.Run(ctx)
	time.Sleep(20 * time.Millisecond) // let the subscription register

	hub.Publish(&model.Event{Type: model.EventTaskCreated, TaskID: "t1", Payload: map[string]any{"title": "hi"}})
	// a non-whitelisted event should NOT be sent
	hub.Publish(&model.Event{Type: model.EventTaskUpdated, TaskID: "t2"})

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		joined := strings.Join(sink, "\n")
		mu.Unlock()
		if strings.Contains(joined, "t1") || strings.Contains(joined, "hi") {
			if strings.Contains(joined, "t2") {
				t.Fatalf("non-whitelisted event was sent: %s", joined)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("expected a message for the whitelisted event")
}

func TestNotifierSuppressesSelfAction(t *testing.T) {
	hub := event.NewHub()
	var sink []string
	var mu sync.Mutex
	tg := fakeTelegram(t, &sink, &mu)
	n := NewNotifier(hub, tg, []int64{111})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go n.Run(ctx)
	time.Sleep(20 * time.Millisecond)

	n.Suppress("t9")
	hub.Publish(&model.Event{Type: model.EventTaskCompleted, TaskID: "t9"})

	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	joined := strings.Join(sink, "\n")
	mu.Unlock()
	if strings.Contains(joined, "t9") {
		t.Fatalf("suppressed event was still sent: %s", joined)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/telegram/ -run 'TestNotifierSendsWhitelistedEvent|TestNotifierSuppressesSelfAction' -v`
Expected: FAIL (undefined NewNotifier).

- [ ] **Step 3: Write minimal implementation**

```go
package telegram

import (
	"context"
	"fmt"
	"sync"

	"github.com/maniginam/waggle/internal/event"
	"github.com/maniginam/waggle/internal/model"
)

type Notifier struct {
	hub      *event.Hub
	tg       *Client
	chats    []int64
	mu       sync.Mutex
	suppress map[string]int
}

func NewNotifier(hub *event.Hub, tg *Client, chats []int64) *Notifier {
	return &Notifier{hub: hub, tg: tg, chats: chats, suppress: map[string]int{}}
}

func (n *Notifier) Suppress(taskID string) {
	if taskID == "" {
		return
	}
	n.mu.Lock()
	n.suppress[taskID]++
	n.mu.Unlock()
}

// consumeSuppression returns true if this event should be skipped (and consumes one token).
func (n *Notifier) consumeSuppression(taskID string) bool {
	if taskID == "" {
		return false
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.suppress[taskID] > 0 {
		n.suppress[taskID]--
		if n.suppress[taskID] == 0 {
			delete(n.suppress, taskID)
		}
		return true
	}
	return false
}

func (n *Notifier) format(e *model.Event) (string, bool) {
	switch e.Type {
	case model.EventTaskCreated:
		return fmt.Sprintf("New task %s", e.TaskID), true
	case model.EventTaskCompleted:
		return fmt.Sprintf("Task completed: %s", e.TaskID), true
	case model.EventMessage:
		return fmt.Sprintf("Message from %s", e.AgentID), true
	default:
		return "", false
	}
}

func (n *Notifier) Run(ctx context.Context) {
	sub := n.hub.Subscribe("", "")
	defer n.hub.Unsubscribe(sub)
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-sub.Ch:
			if !ok {
				return
			}
			msg, send := n.format(e)
			if !send {
				continue
			}
			if n.consumeSuppression(e.TaskID) {
				continue
			}
			for _, chat := range n.chats {
				n.tg.SendMessage(ctx, chat, msg, nil)
			}
		}
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/telegram/ -run 'TestNotifierSendsWhitelistedEvent|TestNotifierSuppressesSelfAction' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/telegram/outbound.go internal/telegram/outbound_test.go
git commit --no-gpg-sign -m "feat(telegram): outbound event notifier with loop suppression"
```

---

### Task 8: Digest scheduler

**Files:**
- Create: `internal/telegram/digest.go`
- Test: `internal/telegram/digest_test.go`

**Interfaces:**
- Consumes: `WaggleClient`, `Client`, `Config`.
- Produces:
  - `type Digester struct { wg *WaggleClient; tg *Client; chats []int64 }`
  - `func NewDigester(wg *WaggleClient, tg *Client, chats []int64) *Digester`
  - `func (d *Digester) SendDigest(ctx context.Context)` — build a text digest from `wg.WhatsNext()` and send it to every allowlisted chat. (This is the unit under test; wall-clock scheduling is a thin ticker in Task 9's `Run`, not tested here to avoid time flakiness.)
  - `func (d *Digester) buildDigest(ctx context.Context) (string, error)` — compose the digest string (header + whats-next body).

- [ ] **Step 1: Write the failing test**

```go
package telegram

import (
	"context"
	"strings"
	"sync"
	"testing"
)

func TestDigestSendsWhatsNext(t *testing.T) {
	s, api := newAPIServer(t)
	// seed one project so whats-next has content
	if _, err := s.Exec(`INSERT INTO projects (id, name, created_at, updated_at) VALUES ('p1','Demo','2026-08-12T00:00:00Z','2026-08-12T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	wg := NewWaggleClient(api.URL)
	var sink []string
	var mu sync.Mutex
	tg := fakeTelegram(t, &sink, &mu)
	d := NewDigester(wg, tg, []int64{111})

	d.SendDigest(context.Background())
	mu.Lock()
	joined := strings.Join(sink, "\n")
	mu.Unlock()
	if !strings.Contains(joined, "sendMessage") {
		t.Fatalf("expected a digest sendMessage, got:\n%s", joined)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/telegram/ -run TestDigestSendsWhatsNext -v`
Expected: FAIL (undefined NewDigester).

- [ ] **Step 3: Write minimal implementation**

```go
package telegram

import "context"

type Digester struct {
	wg    *WaggleClient
	tg    *Client
	chats []int64
}

func NewDigester(wg *WaggleClient, tg *Client, chats []int64) *Digester {
	return &Digester{wg: wg, tg: tg, chats: chats}
}

func (d *Digester) buildDigest(ctx context.Context) (string, error) {
	body, err := d.wg.WhatsNext()
	if err != nil {
		return "", err
	}
	return "Daily Waggle digest:\n" + string(body), nil
}

func (d *Digester) SendDigest(ctx context.Context) {
	msg, err := d.buildDigest(ctx)
	if err != nil {
		msg = "digest error: " + err.Error()
	}
	for _, chat := range d.chats {
		d.tg.SendMessage(ctx, chat, msg, nil)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/telegram/ -run TestDigestSendsWhatsNext -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/telegram/digest.go internal/telegram/digest_test.go
git commit --no-gpg-sign -m "feat(telegram): daily digest builder"
```

---

### Task 9: Bot orchestrator (Run loop)

**Files:**
- Create: `internal/telegram/bot.go`
- Test: `internal/telegram/bot_test.go`

**Interfaces:**
- Consumes: everything above + `*event.Hub`.
- Produces:
  - `type Bot struct { cfg Config; tg *Client; router *Router; notifier *Notifier; digester *Digester }`
  - `func New(hub *event.Hub, cfg Config) *Bot` — constructs the Telegram client (`NewClient(cfg.TelegramBaseURL)`), the Waggle client (`NewWaggleClient(cfg.APIBaseURL)`), the `Handler`, the `NLParser` (via `NewClaudeNLParser()`, nil if disabled), the `Router`, the `Notifier`, and the `Digester`.
  - `func (b *Bot) New` wires `router.h.tg` so that `Handler` shares the same `Client`.
  - `func (b *Bot) Run(ctx context.Context)` — starts the outbound notifier goroutine, a digest ticker goroutine (24h; fires `digester.SendDigest`), and the inbound long-poll loop: repeatedly `tg.GetUpdates(ctx, offset, 30)`, advance `offset = update.UpdateID + 1`, and call `router.Dispatch` for each. Each `Dispatch` runs inside a `func(){ defer recover() ... }()` so one bad update can't kill the loop (panic isolation, like the overseer poller). Returns when `ctx.Done()`.
- The long-poll loop is driven by the injectable `tg` base URL, so the test points it at a fake Telegram server that returns one update then empties.

- [ ] **Step 1: Write the failing test**

```go
package telegram

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maniginam/waggle/internal/event"
)

func TestBotRunProcessesOneUpdateThenStops(t *testing.T) {
	_, api := newAPIServer(t)

	var calls int32
	var mu sync.Mutex
	var sent []string
	tgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/getUpdates") {
			if atomic.AddInt32(&calls, 1) == 1 {
				io.WriteString(w, `{"ok":true,"result":[{"update_id":1,"message":{"message_id":1,"chat":{"id":111},"text":"/next"}}]}`)
				return
			}
			io.WriteString(w, `{"ok":true,"result":[]}`)
			return
		}
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		sent = append(sent, r.URL.Path+" "+string(b))
		mu.Unlock()
		io.WriteString(w, `{"ok":true,"result":{"message_id":1}}`)
	}))
	defer tgSrv.Close()

	hub := event.NewHub()
	cfg := Config{AllowedChats: []int64{111}, APIBaseURL: api.URL, TelegramBaseURL: tgSrv.URL}
	bot := New(hub, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	bot.Run(ctx)

	mu.Lock()
	joined := strings.Join(sent, "\n")
	mu.Unlock()
	if !strings.Contains(joined, "sendMessage") {
		t.Fatalf("expected /next to trigger a sendMessage, got:\n%s", joined)
	}
}
```

Note: add `"io"` to the import list.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/telegram/ -run TestBotRunProcessesOneUpdateThenStops -v`
Expected: FAIL (undefined `New`).

- [ ] **Step 3: Write minimal implementation**

```go
package telegram

import (
	"context"
	"log"
	"time"

	"github.com/maniginam/waggle/internal/event"
)

type Bot struct {
	cfg      Config
	tg       *Client
	router   *Router
	notifier *Notifier
	digester *Digester
}

func New(hub *event.Hub, cfg Config) *Bot {
	tg := NewClient(cfg.TelegramBaseURL)
	wg := NewWaggleClient(cfg.APIBaseURL)
	handler := NewHandler(tg, wg)
	var nl NLParser
	if p, ok := NewClaudeNLParser(); ok {
		nl = p
	}
	return &Bot{
		cfg:      cfg,
		tg:       tg,
		router:   NewRouter(cfg, handler, nl),
		notifier: NewNotifier(hub, tg, cfg.AllowedChats),
		digester: NewDigester(wg, tg, cfg.AllowedChats),
	}
}

func (b *Bot) Run(ctx context.Context) {
	go b.notifier.Run(ctx)

	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				b.digester.SendDigest(ctx)
			}
		}
	}()

	var offset int64
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		updates, err := b.tg.GetUpdates(ctx, offset, 30)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("telegram getUpdates: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}
		for _, u := range updates {
			offset = u.UpdateID + 1
			func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("telegram dispatch panic: %v", r)
					}
				}()
				b.router.Dispatch(ctx, u)
			}()
		}
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/telegram/ -run TestBotRunProcessesOneUpdateThenStops -v` then `go test ./internal/telegram/`
Expected: PASS, whole package green.

- [ ] **Step 5: Commit**

```bash
git add internal/telegram/bot.go internal/telegram/bot_test.go
git commit --no-gpg-sign -m "feat(telegram): bot orchestrator with long-poll loop and panic isolation"
```

---

### Task 10: Server wiring (opt-in, default off)

**Files:**
- Modify: `internal/server/server.go` (add a wiring block near the overseer block at line ~115)
- Test: `internal/server/telegram_wiring_test.go`

**Interfaces:**
- Consumes: `telegram.New`, `telegram.ConfigFromEnv`, the server's `s.eventHub`.
- Produces: a `startTelegram(ctx)`-style block gated by `WAGGLE_TELEGRAM_ENABLED == "true"` AND a non-empty token; starts `go bot.Run(ctx)`. A small exported-or-internal helper `telegramEnabled(cfg telegram.Config) bool` is added so it can be unit-tested without booting the server (returns `os.Getenv("WAGGLE_TELEGRAM_ENABLED") == "true" && cfg.Token != ""`).

- [ ] **Step 1: Write the failing test**

```go
package server

import (
	"os"
	"testing"

	"github.com/maniginam/waggle/internal/telegram"
)

func TestTelegramEnabledGate(t *testing.T) {
	os.Setenv("WAGGLE_TELEGRAM_ENABLED", "true")
	defer os.Unsetenv("WAGGLE_TELEGRAM_ENABLED")

	if telegramEnabled(telegram.Config{Token: ""}) {
		t.Error("must be disabled without a token even when flag is true")
	}
	if !telegramEnabled(telegram.Config{Token: "tok"}) {
		t.Error("must be enabled with flag true and a token present")
	}

	os.Unsetenv("WAGGLE_TELEGRAM_ENABLED")
	if telegramEnabled(telegram.Config{Token: "tok"}) {
		t.Error("must be disabled when flag is unset")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/server/ -run TestTelegramEnabledGate -v`
Expected: FAIL (undefined telegramEnabled).

- [ ] **Step 3: Write minimal implementation**

In `internal/server/server.go`, add the import `"github.com/maniginam/waggle/internal/telegram"`, add the helper:

```go
func telegramEnabled(cfg telegram.Config) bool {
	return os.Getenv("WAGGLE_TELEGRAM_ENABLED") == "true" && cfg.Token != ""
}
```

And near the overseer wiring block (after line ~128), add:

```go
	tgCfg := telegram.ConfigFromEnv()
	if telegramEnabled(tgCfg) {
		bot := telegram.New(s.eventHub, tgCfg)
		go bot.Run(ctx)
		log.Printf("telegram bot enabled (chats: %d)", len(tgCfg.AllowedChats))
	}
```

Use the same `ctx` the overseer wiring uses; if the surrounding function has no `ctx` in scope, use the server's existing context field or `context.Background()` consistent with how the overseer goroutine is started (match the neighboring code exactly — read lines 110-135 first).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/server/ -run TestTelegramEnabledGate -v` then `go build ./...`
Expected: PASS + clean build.

- [ ] **Step 5: Commit**

```bash
git add internal/server/server.go internal/server/telegram_wiring_test.go
git commit --no-gpg-sign -m "feat(server): opt-in Telegram bot wiring (WAGGLE_TELEGRAM_ENABLED, default off)"
```

---

## Final verification

- [ ] **Full suite:** `go test ./...` — all green.
- [ ] **Build:** `go build ./...` — clean.
- [ ] **Manual smoke (optional, needs a real bot):** set `WAGGLE_TELEGRAM_ENABLED=true`, `WAGGLE_TELEGRAM_TOKEN=<token>`, `WAGGLE_TELEGRAM_ALLOWED_CHATS=<your chat id>`, run the daemon, then in Telegram: `/help`, `/next`, `/create test`, `/tasks` (tap a button), and a free-form message (with `ANTHROPIC_API_KEY` set for NL). Confirm outbound: create a task via the dashboard and watch for a Telegram notification.
- [ ] **Review before merge:** dispatch parallel review agents (backend for the Go packages, security for the token/allowlist handling) per repo standards; fix critical/high findings before merging.

## Self-review notes (author)

- Spec coverage: config+allowlist (T1), Telegram client (T2), Waggle REST reuse (T3), slash+buttons (T4), NL parser Haiku behind interface (T5), inbound router+allowlist gate+NL dispatch (T6), outbound events+loop-suppression (T7), digest (T8), orchestrator long-poll+panic isolation (T9), opt-in server wiring (T10). All spec sections mapped. Security spine (env-only token, never-logged, allowlist-before-handler) enforced in T1/T2/T6/T10.
- Type consistency across tasks: `Config`/`ChatAllowed` (T1) used by T6/T10; `Client` methods (T2) used by T4/T7/T8/T9; `WaggleClient` (T3) used by T4/T6/T8/T9; `Handler` (T4) used by T6/T9; `NLParser`/`Intent` (T5) used by T6/T9; `Notifier.Suppress` (T7) exposed for the move handlers; `New`/`Run` (T9) used by T10. Names are stable across tasks.
- Out of scope (per spec): webhook transport, per-user permissions beyond the flat allowlist, rich media/charts, editing pages/generic-DB over Telegram.
- SDK caveat baked into T5: if the installed `anthropic-sdk-go` types differ, WebFetch the repo and correct — do not guess bindings.
