# Project-Scoped Chat

## Overview

When a project is selected in the Waggle dashboard, the right sidebar (currently "Live Feed") becomes a project chat showing a merged chronological feed of all messages associated with that project. A new top-level "Chats" view shows all active conversations grouped by thread within project sections.

## Data Model

Add `project_id` to `model.Message`:

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

SQLite migration adds the column and an index on `project_id`.

## API Changes

### GET /api/messages

New query parameter: `?project_id=<id>` returns all messages for a project, ordered by `created_at` ascending.

### POST /api/messages

Accepts optional `project_id` field in the JSON body. The dashboard sets this automatically when sending from the project chat sidebar. Agents include their `project_id` when sending via MCP.

### Store

New method: `ProjectMessages(projectID string, limit int) ([]*Message, error)` — returns messages where `project_id` matches, ordered chronologically.

## Project Chat Sidebar (Board View)

- **Replaces** the Live Feed sidebar when a project is selected.
- **Merged chronological timeline** — all agents' messages for the project interleaved in one feed.
- Each message shows: sender name, timestamp, message body.
- **Input bar** at the bottom with a text field and send button.
- **Send behavior:**
  - Default (no prefix): broadcast to all project agents. Message created with `from: "user"`, `to: ""`, `project_id: <selected>`.
  - `@agent-name` prefix: targeted message. Message created with `from: "user"`, `to: "agent-name"`, `project_id: <selected>`.
- **Auto-polls** for new messages on the existing polling interval.
- When **no project is selected** (overview), the sidebar is hidden and the board gets full width.

## Chats Page (Top-Level View)

- New view accessible from the header nav, alongside Overview, Inbox, Sessions, Swimlane.
- **Grouped by project** — each project with messages gets a section header.
- **Within each project, grouped by thread** — threads defined by unique `from`/`to` pairs (normalized so A-to-B and B-to-A are the same thread).
- Each thread row shows:
  - The two participants (e.g., "agent-1 <> user")
  - Last message preview (truncated)
  - Time since last message
- **Click a thread** to expand the full conversation inline.
- Can **send messages** from within expanded threads.
- Sections sorted by most recent activity across their threads.
- Threads within a section sorted by most recent message.

## Message Routing

- Broadcast messages (`to: ""` with `project_id`): visible to all agents polling that project's messages.
- Targeted messages (`to: "agent-name"` with `project_id`): visible to the specific agent and in the project feed.
- The MCP `waggle_send_message` tool passes through the agent's `project_id` when set.

## What Does NOT Change

- Existing agent-scoped chat (the Comms tab in the agent overlay panel) remains unchanged.
- The Inbox view remains unchanged.
- Message polling frequency stays the same.
- When no project is selected (overview), the right sidebar is hidden entirely — the board area gets full width.

## Migration

- SQLite: `ALTER TABLE messages ADD COLUMN project_id TEXT DEFAULT '';`
- Index: `CREATE INDEX idx_messages_project_id ON messages(project_id);`
- Existing messages get empty `project_id` (backward compatible).
