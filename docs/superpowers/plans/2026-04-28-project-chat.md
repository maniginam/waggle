# Project-Scoped Chat Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add project-scoped chat to the Waggle dashboard — a sidebar chat when viewing a project, and a top-level Chats page showing all conversations threaded by participant pairs.

**Architecture:** Add `project_id` column to the messages table. New `ProjectMessages` store method + API filter. Dashboard replaces the right sidebar with a chat panel when a project is selected, hides it on overview. New Chats view groups messages by project then by from/to thread pairs, rendered client-side.

**Tech Stack:** Go (store, API), SQLite (migration), vanilla JS/HTML/CSS (dashboard SPA)

**Spec:** `docs/superpowers/specs/2026-04-28-project-chat-design.md`

---

## File Map

| Action | File | Responsibility |
|--------|------|----------------|
| Modify | `internal/model/model.go:155-162` | Add `ProjectID` field to `Message` struct |
| Modify | `internal/store/store.go:106-113` | Add `project_id` column to messages table schema |
| Modify | `internal/store/store.go:215-234` | Add migration for existing DBs |
| Modify | `internal/store/store.go:207` | Add index on `messages(project_id)` |
| Modify | `internal/store/store.go:759-766` | Update `SendMessage` to include `project_id` |
| Modify | `internal/store/store.go:769-794` | Update `ReadMessages` to scan `project_id` |
| Modify | `internal/store/store.go:797-820` | Update `ListAllMessages` to scan `project_id` |
| Modify | `internal/store/store.go:822-847` | Update `AgentMessages` to scan `project_id` |
| Modify | `internal/store/store.go:866-876` | Update `SearchMessages` to scan `project_id` |
| Add | `internal/store/store.go` (after `AgentMessages`) | New `ProjectMessages` method |
| Modify | `internal/api/api.go:752-780` | Add `project_id` query param to GET, include in POST |
| Modify | `internal/mcp/mcp.go:270-276` | Add `project_id` param to `waggle_send_message` tool |
| Modify | `internal/mcp/mcp.go:696-709` | Auto-include agent's `project_id` when sending |
| Modify | `internal/dashboard/static/index.html` | CSS, sidebar chat, Chats view, nav updates |

---

### Task 1: Add `ProjectID` to Message Model

**Files:**
- Modify: `internal/model/model.go:155-162`

- [ ] **Step 1: Add field to Message struct**

In `internal/model/model.go`, replace the Message struct:

```go
type Message struct {
	ID        string    `json:"id"`
	From      string    `json:"from"`
	To        string    `json:"to,omitempty"`
	Body      string    `json:"body"`
	Read      bool      `json:"read"`
	ProjectID string    `json:"project_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}
```

- [ ] **Step 2: Verify it compiles**

Run: `cd /Users/maniginam/projects/waggle && go build ./...`
Expected: SUCCESS (no code references project_id yet in queries, but the struct change alone compiles)

- [ ] **Step 3: Commit**

```bash
git add internal/model/model.go
git commit -m "Add ProjectID field to Message model"
```

---

### Task 2: Add `project_id` Column to Messages Table + Migration

**Files:**
- Modify: `internal/store/store.go:106-113` (CREATE TABLE)
- Modify: `internal/store/store.go:215-234` (migration loop)
- Modify: `internal/store/store.go:207` (indexes)
- Test: `internal/store/store_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/store/store_test.go`:

```go
func TestProjectMessages(t *testing.T) {
	s := tempStore(t)
	s.SendMessage(&model.Message{From: "a1", To: "user", Body: "hello", ProjectID: "proj-1"})
	s.SendMessage(&model.Message{From: "a2", To: "user", Body: "world", ProjectID: "proj-1"})
	s.SendMessage(&model.Message{From: "a3", To: "user", Body: "other", ProjectID: "proj-2"})

	msgs, err := s.ProjectMessages("proj-1", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Errorf("expected 2 messages for proj-1, got %d", len(msgs))
	}
	for _, m := range msgs {
		if m.ProjectID != "proj-1" {
			t.Errorf("expected project_id 'proj-1', got '%s'", m.ProjectID)
		}
	}

	// Verify project_id is preserved on read
	all, _ := s.ListAllMessages(50)
	found := false
	for _, m := range all {
		if m.Body == "hello" && m.ProjectID == "proj-1" {
			found = true
		}
	}
	if !found {
		t.Error("expected ListAllMessages to include project_id")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/maniginam/projects/waggle && go test ./internal/store/ -run TestProjectMessages -v`
Expected: FAIL — `SendMessage` doesn't write `project_id`, `ProjectMessages` doesn't exist

- [ ] **Step 3: Update CREATE TABLE schema**

In `internal/store/store.go`, update the messages table definition (around line 106):

```sql
CREATE TABLE IF NOT EXISTS messages (
    id         TEXT PRIMARY KEY,
    "from"     TEXT NOT NULL,
    "to"       TEXT DEFAULT '',
    body       TEXT NOT NULL,
    read       INTEGER DEFAULT 0,
    project_id TEXT DEFAULT '',
    created_at TEXT NOT NULL
);
```

- [ ] **Step 4: Add migration for existing databases**

In `internal/store/store.go`, add to the migration loop (around line 227, inside the `[]struct{ table, name, def string }` slice):

```go
{"messages", "project_id", "TEXT DEFAULT ''"},
```

- [ ] **Step 5: Add index on project_id**

In `internal/store/store.go`, after the existing `idx_messages_to` index line (around line 207), add:

```sql
CREATE INDEX IF NOT EXISTS idx_messages_project ON messages(project_id);
```

- [ ] **Step 6: Update `SendMessage` to write `project_id`**

In `internal/store/store.go`, update `SendMessage` (around line 759):

```go
func (s *Store) SendMessage(msg *model.Message) error {
	if msg.ID == "" {
		msg.ID = id.New()
	}
	msg.CreatedAt = time.Now().UTC()
	_, err := s.db.Exec(`INSERT INTO messages (id, "from", "to", body, read, project_id, created_at) VALUES (?, ?, ?, ?, 0, ?, ?)`,
		msg.ID, msg.From, msg.To, msg.Body, msg.ProjectID, msg.CreatedAt.Format(time.RFC3339))
	return err
}
```

- [ ] **Step 7: Update all scan functions to include `project_id`**

Every function that scans message rows needs to include `project_id`. Update these functions in `internal/store/store.go`:

**`ReadMessages`** (around line 769):
```go
func (s *Store) ReadMessages(to string, limit int) ([]*model.Message, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT id, "from", "to", body, read, project_id, created_at FROM messages WHERE "to" = ? OR "to" = '' ORDER BY created_at DESC LIMIT ?`, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []*model.Message
	for rows.Next() {
		var m model.Message
		var readInt int
		var ts string
		if err := rows.Scan(&m.ID, &m.From, &m.To, &m.Body, &readInt, &m.ProjectID, &ts); err != nil {
			return nil, err
		}
		m.Read = readInt != 0
		m.CreatedAt, _ = time.Parse(time.RFC3339, ts)
		messages = append(messages, &m)
	}
	return messages, rows.Err()
}
```

**`ListAllMessages`** (around line 797):
```go
func (s *Store) ListAllMessages(limit int) ([]*model.Message, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT id, "from", "to", body, read, project_id, created_at FROM messages ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []*model.Message
	for rows.Next() {
		var m model.Message
		var readInt int
		var ts string
		if err := rows.Scan(&m.ID, &m.From, &m.To, &m.Body, &readInt, &m.ProjectID, &ts); err != nil {
			return nil, err
		}
		m.Read = readInt != 0
		m.CreatedAt, _ = time.Parse(time.RFC3339, ts)
		messages = append(messages, &m)
	}
	return messages, rows.Err()
}
```

**`AgentMessages`** (around line 822):
```go
func (s *Store) AgentMessages(agent string, limit int) ([]*model.Message, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(
		`SELECT id, "from", "to", body, read, project_id, created_at FROM messages WHERE "from" = ? OR "to" = ? ORDER BY created_at DESC LIMIT ?`,
		agent, agent, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []*model.Message
	for rows.Next() {
		var m model.Message
		var readInt int
		var ts string
		if err := rows.Scan(&m.ID, &m.From, &m.To, &m.Body, &readInt, &m.ProjectID, &ts); err != nil {
			return nil, err
		}
		m.Read = readInt != 0
		m.CreatedAt, _ = time.Parse(time.RFC3339, ts)
		messages = append(messages, &m)
	}
	return messages, rows.Err()
}
```

**`SearchMessages`** (around line 866):
```go
func (s *Store) SearchMessages(query string, limit int) ([]*model.Message, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(
		`SELECT id, "from", "to", body, read, project_id, created_at FROM messages WHERE body LIKE ? ORDER BY created_at DESC LIMIT ?`,
		"%"+query+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []*model.Message
	for rows.Next() {
		var m model.Message
		var readInt int
		var ts string
		if err := rows.Scan(&m.ID, &m.From, &m.To, &m.Body, &readInt, &m.ProjectID, &ts); err != nil {
			return nil, err
		}
		m.Read = readInt != 0
		m.CreatedAt, _ = time.Parse(time.RFC3339, ts)
		messages = append(messages, &m)
	}
	return messages, rows.Err()
}
```

- [ ] **Step 8: Add `ProjectMessages` method**

Add after `AgentMessages` in `internal/store/store.go`:

```go
func (s *Store) ProjectMessages(projectID string, limit int) ([]*model.Message, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(
		`SELECT id, "from", "to", body, read, project_id, created_at FROM messages WHERE project_id = ? ORDER BY created_at ASC LIMIT ?`,
		projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []*model.Message
	for rows.Next() {
		var m model.Message
		var readInt int
		var ts string
		if err := rows.Scan(&m.ID, &m.From, &m.To, &m.Body, &readInt, &m.ProjectID, &ts); err != nil {
			return nil, err
		}
		m.Read = readInt != 0
		m.CreatedAt, _ = time.Parse(time.RFC3339, ts)
		messages = append(messages, &m)
	}
	return messages, rows.Err()
}
```

Note: `ORDER BY created_at ASC` — chronological for chat display (oldest first).

- [ ] **Step 9: Run test to verify it passes**

Run: `cd /Users/maniginam/projects/waggle && go test ./internal/store/ -run TestProjectMessages -v`
Expected: PASS

- [ ] **Step 10: Run all store tests**

Run: `cd /Users/maniginam/projects/waggle && go test ./internal/store/ -v`
Expected: ALL PASS (existing message tests still work with the extra column)

- [ ] **Step 11: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit -m "Add project_id to messages table with migration and ProjectMessages query"
```

---

### Task 3: Add `project_id` Filter to Messages API

**Files:**
- Modify: `internal/api/api.go:752-780`
- Test: `internal/api/api_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/api/api_test.go`:

```go
func TestMessagesFilterByProject(t *testing.T) {
	_, ts := setup(t)

	// Send messages with project_id
	mustPost(t, ts.URL+"/api/messages", "application/json",
		bytes.NewBufferString(`{"from":"a1","to":"user","body":"proj1 msg","project_id":"proj-1"}`))
	mustPost(t, ts.URL+"/api/messages", "application/json",
		bytes.NewBufferString(`{"from":"a2","to":"user","body":"proj2 msg","project_id":"proj-2"}`))
	mustPost(t, ts.URL+"/api/messages", "application/json",
		bytes.NewBufferString(`{"from":"a3","to":"user","body":"no proj msg"}`))

	// Filter by project_id
	resp := mustGet(t, ts.URL+"/api/messages?project_id=proj-1")
	defer resp.Body.Close()
	var msgs []map[string]any
	json.NewDecoder(resp.Body).Decode(&msgs)
	if len(msgs) != 1 {
		t.Errorf("expected 1 message for proj-1, got %d", len(msgs))
	}
	if msgs[0]["body"] != "proj1 msg" {
		t.Errorf("expected 'proj1 msg', got %v", msgs[0]["body"])
	}
	if msgs[0]["project_id"] != "proj-1" {
		t.Errorf("expected project_id 'proj-1', got %v", msgs[0]["project_id"])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/maniginam/projects/waggle && go test ./internal/api/ -run TestMessagesFilterByProject -v`
Expected: FAIL — no `project_id` query param handling

- [ ] **Step 3: Update handleMessages GET to support project_id**

In `internal/api/api.go`, update the `handleMessages` GET handler (around line 754):

```go
case http.MethodGet:
    to := r.URL.Query().Get("to")
    agent := r.URL.Query().Get("agent")
    q := r.URL.Query().Get("q")
    projectID := r.URL.Query().Get("project_id")
    limit := 50
    if l := r.URL.Query().Get("limit"); l != "" {
        fmt.Sscanf(l, "%d", &limit)
    }
    var msgs []*model.Message
    var err error
    if q != "" {
        msgs, err = a.store.SearchMessages(q, limit)
    } else if projectID != "" {
        msgs, err = a.store.ProjectMessages(projectID, limit)
    } else if agent != "" {
        msgs, err = a.store.AgentMessages(agent, limit)
    } else if to == "" {
        msgs, err = a.store.ListAllMessages(limit)
    } else {
        msgs, err = a.store.ReadMessages(to, limit)
    }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/maniginam/projects/waggle && go test ./internal/api/ -run TestMessagesFilterByProject -v`
Expected: PASS

- [ ] **Step 5: Run all API tests**

Run: `cd /Users/maniginam/projects/waggle && go test ./internal/api/ -v`
Expected: ALL PASS

- [ ] **Step 6: Commit**

```bash
git add internal/api/api.go internal/api/api_test.go
git commit -m "Add project_id filter to messages API"
```

---

### Task 4: Auto-Include `project_id` in MCP Message Sending

**Files:**
- Modify: `internal/mcp/mcp.go:270-276` (tool definition)
- Modify: `internal/mcp/mcp.go:696-709` (handler)

- [ ] **Step 1: Add `project_id` to waggle_send_message tool definition**

In `internal/mcp/mcp.go`, update the `waggle_send_message` tool definition (around line 270):

```go
toolDef("waggle_send_message", "Send a message to another agent or broadcast.", map[string]any{
    "type": "object",
    "properties": map[string]any{
        "to":         prop("string", "Recipient agent name (empty for broadcast)"),
        "body":       prop("string", "Message body"),
        "project_id": prop("string", "Project ID (auto-filled from agent registration if not set)"),
    },
    "required": []string{"body"},
}),
```

- [ ] **Step 2: Update handler to auto-include project_id**

In `internal/mcp/mcp.go`, update the `waggle_send_message` case (around line 696):

```go
case "waggle_send_message":
    if a.agentName == "" {
        return nil, fmt.Errorf("must call waggle_register_agent first")
    }
    to, _ := args["to"].(string)
    body, _ := args["body"].(string)
    if body == "" {
        return nil, fmt.Errorf("body is required")
    }
    projectID, _ := args["project_id"].(string)
    payload := map[string]string{
        "from": a.agentName,
        "to":   to,
        "body": body,
    }
    if projectID != "" {
        payload["project_id"] = projectID
    }
    return a.postJSON("/api/messages", payload)
```

- [ ] **Step 3: Verify it compiles**

Run: `cd /Users/maniginam/projects/waggle && go build ./...`
Expected: SUCCESS

- [ ] **Step 4: Commit**

```bash
git add internal/mcp/mcp.go
git commit -m "Add project_id support to MCP waggle_send_message"
```

---

### Task 5: Dashboard — Project Chat Sidebar CSS

**Files:**
- Modify: `internal/dashboard/static/index.html` (CSS section)

- [ ] **Step 1: Add project chat sidebar styles**

In `internal/dashboard/static/index.html`, find the `/* -- SIDEBAR -- */` CSS section (around line 609) and add AFTER it:

```css
/* ── PROJECT CHAT SIDEBAR ── */
.project-chat {
  display: flex; flex-direction: column; height: 100%;
}
.project-chat-header {
  padding: 10px 16px; border-bottom: 1px solid var(--border);
  font-size: 0.7rem; text-transform: uppercase; letter-spacing: 1.5px;
  color: var(--text-dim); font-weight: 600;
  display: flex; align-items: center; justify-content: space-between;
}
.project-chat-messages {
  flex: 1; overflow-y: auto; padding: 12px; display: flex; flex-direction: column; gap: 8px;
}
.project-chat-messages::-webkit-scrollbar { width: 4px; }
.project-chat-messages::-webkit-scrollbar-thumb { background: var(--border); border-radius: 2px; }
.pchat-msg {
  padding: 6px 10px; border-radius: var(--radius); max-width: 90%;
  animation: chatMsgIn 0.2s ease-out;
}
.pchat-msg.from-me {
  background: var(--amber-glow-strong); align-self: flex-end; border: 1px solid var(--amber-dim);
}
.pchat-msg.from-them {
  background: var(--bg-elevated); align-self: flex-start; border: 1px solid var(--border);
}
.pchat-msg-sender {
  font-size: 0.6rem; color: var(--text-dim); margin-bottom: 2px; font-weight: 600;
}
.pchat-msg-body { font-size: 0.75rem; line-height: 1.4; word-break: break-word; }
.pchat-msg-time { font-size: 0.55rem; color: var(--text-dim); margin-top: 2px; }
.project-chat-input {
  padding: 10px 12px; border-top: 1px solid var(--border);
  display: flex; gap: 6px; align-items: center;
}
.project-chat-input input {
  flex: 1; background: var(--bg-card); border: 1px solid var(--border);
  border-radius: var(--radius); padding: 6px 10px; color: var(--text-primary);
  font-family: var(--font-mono); font-size: 0.75rem; outline: none;
}
.project-chat-input input:focus { border-color: var(--amber-dim); }
.project-chat-input input::placeholder { color: var(--text-dim); }
.project-chat-input button {
  background: var(--amber); color: #000; border: none; border-radius: var(--radius);
  padding: 6px 12px; font-weight: 700; font-size: 0.7rem; cursor: pointer;
  font-family: var(--font-mono);
}
.project-chat-input button:hover { background: #ffb820; }
```

- [ ] **Step 2: Update `.main` grid to hide sidebar on overview**

Find the `.main` CSS rule (around line 507):

```css
.main { display: grid; grid-template-columns: 1fr 280px; overflow: hidden; }
```

Replace with:

```css
.main { display: grid; grid-template-columns: 1fr 280px; overflow: hidden; }
.main.no-sidebar { grid-template-columns: 1fr; }
.main.no-sidebar .sidebar { display: none; }
```

- [ ] **Step 3: Commit**

```bash
git add internal/dashboard/static/index.html
git commit -m "Add project chat sidebar CSS styles"
```

---

### Task 6: Dashboard — Project Chat Sidebar Logic

**Files:**
- Modify: `internal/dashboard/static/index.html` (JS section)

- [ ] **Step 1: Add project chat state variable**

Find the state variables section (around line 2322, near `let selectedProject`). Add after it:

```javascript
let projectChatMessages = [];
let projectChatTimer = null;
```

- [ ] **Step 2: Add `renderProjectChat` function**

Add the following function in the JS section (after the existing `renderBoard` function, around line 2500):

```javascript
async function loadProjectChat() {
  if (!selectedProject || selectedProject === 'none') return;
  try {
    const msgs = await fetch(`/api/messages?project_id=${encodeURIComponent(selectedProject)}&limit=100`).then(r => r.json());
    projectChatMessages = msgs || [];
    renderProjectChatMessages();
  } catch(e) { console.error('Failed to load project chat:', e); }
}

function renderProjectChatMessages() {
  const container = document.getElementById('project-chat-messages');
  if (!container) return;
  const wasScrolledToBottom = container.scrollHeight - container.scrollTop - container.clientHeight < 40;
  if (projectChatMessages.length === 0) {
    container.innerHTML = '<div style="flex:1;display:flex;align-items:center;justify-content:center;color:var(--text-dim);font-size:0.75rem">No messages yet</div>';
    return;
  }
  container.innerHTML = projectChatMessages.map(m => {
    const isMe = m.from === 'user' || m.from === 'dashboard';
    const time = new Date(m.created_at).toLocaleTimeString([], {hour:'2-digit',minute:'2-digit'});
    return `<div class="pchat-msg ${isMe ? 'from-me' : 'from-them'}">
      <div class="pchat-msg-sender">${esc(m.from)}${m.to && m.to !== 'user' && m.to !== '' ? ' → ' + esc(m.to) : ''}</div>
      <div class="pchat-msg-body">${esc(m.body)}</div>
      <div class="pchat-msg-time">${time}</div>
    </div>`;
  }).join('');
  if (wasScrolledToBottom) container.scrollTop = container.scrollHeight;
}

async function sendProjectChat() {
  const input = document.getElementById('project-chat-input');
  if (!input || !input.value.trim() || !selectedProject || selectedProject === 'none') return;
  const text = input.value.trim();
  input.value = '';

  let to = '';
  let body = text;
  const atMatch = text.match(/^@(\S+)\s+([\s\S]+)/);
  if (atMatch) {
    to = atMatch[1];
    body = atMatch[2];
  }

  const msg = { from: 'user', to: to, body: body, project_id: selectedProject };
  try {
    await fetch('/api/messages', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify(msg)
    });
    await loadProjectChat();
  } catch(e) { console.error('Failed to send project chat:', e); }
}

function startProjectChatPolling() {
  stopProjectChatPolling();
  loadProjectChat();
  projectChatTimer = setInterval(loadProjectChat, 5000);
}

function stopProjectChatPolling() {
  if (projectChatTimer) { clearInterval(projectChatTimer); projectChatTimer = null; }
}
```

- [ ] **Step 3: Update the sidebar HTML to show chat when project is selected**

Find the `renderBoard` function. Currently the sidebar in `#board-view` (around line 2258) has static HTML:

```html
<div class="sidebar">
  <div class="sidebar-section" style="flex:1;display:flex;flex-direction:column;overflow:hidden;">
    <div class="sidebar-title">Live Feed</div>
    <div class="event-feed" id="event-feed"></div>
  </div>
</div>
```

Replace with:

```html
<div class="sidebar" id="board-sidebar">
  <div id="sidebar-livefeed" style="flex:1;display:flex;flex-direction:column;overflow:hidden;">
    <div class="sidebar-section" style="flex:1;display:flex;flex-direction:column;overflow:hidden;">
      <div class="sidebar-title">Live Feed</div>
      <div class="event-feed" id="event-feed"></div>
    </div>
  </div>
  <div id="sidebar-projectchat" style="display:none;flex:1;overflow:hidden;">
    <div class="project-chat">
      <div class="project-chat-header">
        <span>Project Chat</span>
      </div>
      <div class="project-chat-messages" id="project-chat-messages"></div>
      <div class="project-chat-input">
        <input id="project-chat-input" placeholder="Message... (@agent to target)" onkeydown="if(event.key==='Enter')sendProjectChat()">
        <button onclick="sendProjectChat()">Send</button>
      </div>
    </div>
  </div>
</div>
```

- [ ] **Step 4: Toggle sidebar content in `navigateTo`**

In the `navigateTo` function (around line 4619), in the `else` branch (project selected, around line 4650), add after `renderUsage();`:

```javascript
// Show project chat sidebar, hide live feed
const mainEl = document.querySelector('.main');
const livefeed = document.getElementById('sidebar-livefeed');
const pchat = document.getElementById('sidebar-projectchat');
if (mainEl) mainEl.classList.remove('no-sidebar');
if (livefeed) livefeed.style.display = 'none';
if (pchat) pchat.style.display = 'flex';
startProjectChatPolling();
```

In the `overview` branch (around line 4634), add after `renderOverview();`:

```javascript
stopProjectChatPolling();
const mainEl = document.querySelector('.main');
if (mainEl) mainEl.classList.add('no-sidebar');
```

In the `inbox`, `sessions`, and `swimlane` branches, add `stopProjectChatPolling();` after the existing render call in each.

- [ ] **Step 5: Verify manually**

Run: `cd /Users/maniginam/projects/waggle && go run ./cmd/waggle`
Open http://localhost:4740, select a project — sidebar should show "Project Chat" with an input bar. Go to Overview — sidebar should be hidden.

- [ ] **Step 6: Commit**

```bash
git add internal/dashboard/static/index.html
git commit -m "Add project chat sidebar to board view"
```

---

### Task 7: Dashboard — Chats Page (Top-Level View)

**Files:**
- Modify: `internal/dashboard/static/index.html` (HTML, CSS, JS)

- [ ] **Step 1: Add CSS for Chats view**

Add after the project chat CSS (from Task 5):

```css
/* ── CHATS VIEW ── */
.chats-view { flex: 1; overflow-y: auto; padding: 20px 24px; }
.chats-project-section { margin-bottom: 24px; }
.chats-project-header {
  font-family: var(--font-sans); font-size: 1rem; font-weight: 600;
  color: var(--amber); margin-bottom: 8px; padding-bottom: 6px;
  border-bottom: 1px solid var(--border);
}
.chats-thread {
  background: var(--bg-card); border: 1px solid var(--border); border-radius: var(--radius);
  margin-bottom: 4px; cursor: pointer; transition: all 0.15s;
}
.chats-thread:hover { background: var(--bg-card-hover); border-color: var(--amber-dim); }
.chats-thread-row {
  display: flex; align-items: center; gap: 12px; padding: 10px 14px;
}
.chats-thread-participants {
  font-size: 0.8rem; font-weight: 600; color: var(--text-primary); min-width: 180px;
}
.chats-thread-preview {
  flex: 1; font-size: 0.75rem; color: var(--text-secondary);
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
}
.chats-thread-time { font-size: 0.65rem; color: var(--text-dim); white-space: nowrap; }
.chats-thread-expanded {
  border-top: 1px solid var(--border); padding: 12px 14px;
  max-height: 400px; overflow-y: auto; display: flex; flex-direction: column; gap: 6px;
}
.chats-thread-expanded::-webkit-scrollbar { width: 4px; }
.chats-thread-expanded::-webkit-scrollbar-thumb { background: var(--border); border-radius: 2px; }
.chats-thread-input {
  display: flex; gap: 6px; padding: 8px 14px; border-top: 1px solid var(--border);
}
.chats-thread-input input {
  flex: 1; background: var(--bg-elevated); border: 1px solid var(--border);
  border-radius: var(--radius); padding: 5px 8px; color: var(--text-primary);
  font-family: var(--font-mono); font-size: 0.75rem; outline: none;
}
.chats-thread-input input:focus { border-color: var(--amber-dim); }
.chats-thread-input button {
  background: var(--amber); color: #000; border: none; border-radius: var(--radius);
  padding: 5px 10px; font-weight: 700; font-size: 0.7rem; cursor: pointer;
  font-family: var(--font-mono);
}
.chats-empty { color: var(--text-dim); text-align: center; padding: 60px 20px; font-size: 0.85rem; }
```

- [ ] **Step 2: Add Chats view HTML container**

In the `#main-view` section (around line 2243), add after the swimlane-view div:

```html
<div id="chats-view" class="chats-view" style="display:none"></div>
```

- [ ] **Step 3: Add Chats nav item**

In the `renderProjectNav` function (around line 4580, after the swimlane nav button):

```javascript
html += `<button class="project-nav-item${currentView==='chats'?' active':''}" data-view="chats">
    <div class="nav-icon" style="background:var(--blue-dim);border-color:var(--blue)"></div>
    <span class="project-nav-name">Chats</span>
    <span class="project-nav-count"></span>
  </button>`;
```

- [ ] **Step 4: Add chats-view to `navigateTo`**

In `navigateTo` function, update the `hideAll` lambda (around line 4632) to include chats:

```javascript
const chatsEl = document.getElementById('chats-view');
const hideAll = () => { overviewEl.style.display='none'; boardEl.style.display='none'; inboxEl.style.display='none'; sessEl.style.display='none'; swimEl.style.display='none'; chatsEl.style.display='none'; };
```

Add a new `else if` branch after the swimlane branch (around line 4648):

```javascript
} else if (view === 'chats') {
    selectedProject = '';
    hideAll(); chatsEl.style.display = '';
    stopProjectChatPolling();
    renderChats();
```

- [ ] **Step 5: Add `renderChats` function**

Add in the JS section:

```javascript
async function renderChats() {
  const el = document.getElementById('chats-view');
  if (!el) return;
  el.innerHTML = '<div class="chats-empty">Loading chats...</div>';

  let allMsgs;
  try {
    allMsgs = await fetch('/api/messages?limit=500').then(r => r.json());
  } catch(e) {
    el.innerHTML = '<div class="chats-empty">Failed to load messages</div>';
    return;
  }
  if (!allMsgs || allMsgs.length === 0) {
    el.innerHTML = '<div class="chats-empty">No conversations yet</div>';
    return;
  }

  // Group by project, then by thread (normalized from/to pair)
  const byProject = {};
  for (const m of allMsgs) {
    const pid = m.project_id || '_unassigned';
    if (!byProject[pid]) byProject[pid] = [];
    byProject[pid].push(m);
  }

  function threadKey(m) {
    const pair = [m.from || '', m.to || ''].sort();
    return pair.join(' <> ');
  }

  let html = '';
  // Sort projects: named first (by name), unassigned last
  const sortedPids = Object.keys(byProject).sort((a, b) => {
    if (a === '_unassigned') return 1;
    if (b === '_unassigned') return -1;
    const pa = projects.find(p => p.id === a);
    const pb = projects.find(p => p.id === b);
    return (pa ? pa.name : a).localeCompare(pb ? pb.name : b);
  });

  for (const pid of sortedPids) {
    const proj = projects.find(p => p.id === pid);
    const projName = proj ? proj.name : (pid === '_unassigned' ? 'Unassigned' : pid);
    const msgs = byProject[pid];

    // Group by thread
    const threads = {};
    for (const m of msgs) {
      const tk = threadKey(m);
      if (!threads[tk]) threads[tk] = [];
      threads[tk].push(m);
    }

    // Sort threads by most recent message
    const sortedThreads = Object.entries(threads).sort((a, b) => {
      const latestA = new Date(a[1][a[1].length - 1].created_at);
      const latestB = new Date(b[1][b[1].length - 1].created_at);
      return latestB - latestA;
    });

    html += `<div class="chats-project-section">`;
    html += `<div class="chats-project-header">${esc(projName)}</div>`;

    for (const [tk, tmsgs] of sortedThreads) {
      const latest = tmsgs[tmsgs.length - 1];
      const timeAgo = formatTimeAgo(new Date(latest.created_at));
      const tid = btoa(pid + ':' + tk).replace(/[^a-zA-Z0-9]/g, '');

      html += `<div class="chats-thread" id="thread-${tid}">
        <div class="chats-thread-row" onclick="toggleChatThread('${tid}', '${esc(pid)}', '${esc(tk)}')">
          <span class="chats-thread-participants">${esc(tk)}</span>
          <span class="chats-thread-preview">${esc(latest.body)}</span>
          <span class="chats-thread-time">${timeAgo}</span>
        </div>
      </div>`;
    }
    html += `</div>`;
  }

  el.innerHTML = html;
}

function formatTimeAgo(date) {
  const now = new Date();
  const diff = Math.floor((now - date) / 1000);
  if (diff < 60) return 'just now';
  if (diff < 3600) return Math.floor(diff / 60) + 'm ago';
  if (diff < 86400) return Math.floor(diff / 3600) + 'h ago';
  return Math.floor(diff / 86400) + 'd ago';
}

function toggleChatThread(tid, projectId, threadKey) {
  const threadEl = document.getElementById('thread-' + tid);
  if (!threadEl) return;
  const existing = threadEl.querySelector('.chats-thread-expanded');
  if (existing) {
    existing.remove();
    const inputEl = threadEl.querySelector('.chats-thread-input');
    if (inputEl) inputEl.remove();
    return;
  }

  // Find messages for this thread
  const participants = threadKey.split(' <> ');
  fetch(`/api/messages?${projectId && projectId !== '_unassigned' ? 'project_id=' + encodeURIComponent(projectId) + '&' : ''}limit=200`)
    .then(r => r.json())
    .then(allMsgs => {
      const tmsgs = (allMsgs || []).filter(m => {
        const pair = [m.from || '', m.to || ''].sort();
        return pair[0] === participants[0] && pair[1] === participants[1];
      }).sort((a, b) => new Date(a.created_at) - new Date(b.created_at));

      let expandHtml = '<div class="chats-thread-expanded">';
      for (const m of tmsgs) {
        const isMe = m.from === 'user' || m.from === 'dashboard';
        const time = new Date(m.created_at).toLocaleTimeString([], {hour:'2-digit',minute:'2-digit'});
        expandHtml += `<div class="pchat-msg ${isMe ? 'from-me' : 'from-them'}">
          <div class="pchat-msg-sender">${esc(m.from)}${m.to ? ' → ' + esc(m.to) : ''}</div>
          <div class="pchat-msg-body">${esc(m.body)}</div>
          <div class="pchat-msg-time">${time}</div>
        </div>`;
      }
      expandHtml += '</div>';

      // Add inline reply
      const otherParticipant = participants.find(p => p !== 'user' && p !== 'dashboard') || participants[0];
      expandHtml += `<div class="chats-thread-input">
        <input placeholder="Reply to ${esc(otherParticipant)}..." onkeydown="if(event.key==='Enter')sendThreadReply(this,'${esc(otherParticipant)}','${esc(projectId)}')">
        <button onclick="sendThreadReply(this.previousElementSibling,'${esc(otherParticipant)}','${esc(projectId)}')">Send</button>
      </div>`;

      threadEl.insertAdjacentHTML('beforeend', expandHtml);
      const expanded = threadEl.querySelector('.chats-thread-expanded');
      if (expanded) expanded.scrollTop = expanded.scrollHeight;
    });
}

async function sendThreadReply(inputEl, to, projectId) {
  const text = inputEl.value.trim();
  if (!text) return;
  inputEl.value = '';

  const msg = { from: 'user', to: to, body: text };
  if (projectId && projectId !== '_unassigned') msg.project_id = projectId;

  try {
    await fetch('/api/messages', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify(msg)
    });
    // Re-render chats to show new message
    renderChats();
  } catch(e) { console.error('Failed to send thread reply:', e); }
}
```

- [ ] **Step 6: Add keyboard shortcut for Chats**

Find the keyboard shortcuts section (around line 6475) and the existing shortcut handler. Add `c` for chats:

```javascript
if (e.key === 'c' && !e.ctrlKey && !e.metaKey) { navigateTo('chats'); return; }
```

And in the shortcuts overlay HTML (around line 2282), add:

```html
<div class="shortcut-row"><span class="shortcut-desc">Chats view</span><span class="shortcut-key">c</span></div>
```

- [ ] **Step 7: Verify manually**

Run: `cd /Users/maniginam/projects/waggle && go run ./cmd/waggle`
Open http://localhost:4740, click "Chats" in the left nav — should show conversations grouped by project and thread. Click a thread to expand. Press `c` to jump to Chats.

- [ ] **Step 8: Commit**

```bash
git add internal/dashboard/static/index.html
git commit -m "Add Chats top-level view with threaded conversations"
```

---

### Task 8: Wire Up Existing Polling to Refresh Chat

**Files:**
- Modify: `internal/dashboard/static/index.html` (JS)

- [ ] **Step 1: Refresh project chat on SSE message events**

Find the SSE/EventSource handler that processes incoming events (search for `EventMessage` or `message` event handling in the JS). When a message event arrives and the board view is showing a project, reload the project chat:

```javascript
// Inside the SSE event handler for message events:
if (selectedProject && selectedProject !== 'none') {
  loadProjectChat();
}
```

- [ ] **Step 2: Refresh chats view if open**

In the same SSE handler, if `currentView === 'chats'`, call `renderChats()`.

- [ ] **Step 3: Verify manually**

Open two browser tabs — one on a project board, one on Chats. Send a message via the API:
```bash
curl -X POST http://localhost:4740/api/messages -H 'Content-Type: application/json' -d '{"from":"test-agent","to":"user","body":"hello from test","project_id":"wg-d2b49a"}'
```
Both views should update.

- [ ] **Step 4: Commit**

```bash
git add internal/dashboard/static/index.html
git commit -m "Wire SSE events to refresh project chat and chats view"
```
