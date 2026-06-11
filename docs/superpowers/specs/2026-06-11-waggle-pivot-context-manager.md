# Waggle Pivot: From Agent Orchestrator to Context Manager

**Date**: 2026-06-11
**Status**: PROPOSAL
**Author**: waggle session (user-directed)

## Problem Statement

Waggle was built as a multi-agent orchestration platform (task queues, heartbeats, message
polling, bridge agents, real-time coordination). In practice, it's unused because:

1. **Solo dev, not a team** — The ceremony (register, heartbeat every 60s, poll messages
   every 2-3 min, graceful disconnect) costs more than it delivers for one person.
2. **Colony already orchestrates** — The Clojure daemon handles autonomous task execution,
   worker spawning, scheduling, and retry. Waggle duplicates this.
3. **Context loss is the real pain** — 72 projects, 36 MEMORY.md files, 2 Claude accounts,
   constant context-switching. When returning to a project after weeks, there's no single
   source of truth for "where was I? what happened while I was gone?"
4. **Waggle protocol injection wastes tokens** — 30+ lines injected into every session via
   hook, burning context on heartbeat/polling instructions that never get followed.

## Design Principle

**Waggle complements the mind — it doesn't reflect or replace it.**

The human mind context-switches rapidly. Waggle should be the stable anchor that:
- Remembers what the mind forgets (project state across time gaps)
- Surfaces what the mind can't track (cross-project status, automated progress)
- Guides what the mind struggles to prioritize (what's next across everything)

## What Changes

### REMOVE (Agent Orchestration Layer)
- Mandatory heartbeat protocol
- Mandatory message polling
- Agent registration ceremony
- Bridge agents (6 LLM providers)
- Personas system
- Proposals system
- Code review system
- Token usage tracking
- Agent spawning from dashboard
- Sessions/CCTV view
- Stale agent detection/reaping
- The 27-tool MCP surface area
- waggle-protocol.md hook injection

### KEEP (Core Infrastructure)
- Go single binary + embedded SQLite (proven, zero-dep, fast)
- Project registry (the catalog of all 72+ projects)
- Task backlog per project (simple queue, no ceremony)
- Dashboard (reimagined — see below)
- REST API (simplified)
- MCP adapter (reduced tool set)
- SSE for dashboard real-time updates

### ADD (Context Management Layer)
- Project health/status tracking
- "Where I left off" snapshots
- Cross-project priority view
- Automated progress ingestion (Colony, crons, deploys)
- Session journal (what happened this session, auto-logged)
- Time-since-touched indicators

## New Data Model

### Projects (enhanced)
```sql
ALTER TABLE projects ADD COLUMN status TEXT DEFAULT 'active';
  -- active, dormant, paused, earning, broken, killed
ALTER TABLE projects ADD COLUMN account TEXT;
  -- 'pro' or 'team' (Claude account mapping)
ALTER TABLE projects ADD COLUMN category TEXT;
  -- 'revenue', 'cleancoders', 'personal', 'experimental', 'infra'
ALTER TABLE projects ADD COLUMN last_touched_at TIMESTAMP;
  -- Updated whenever a session opens this project
ALTER TABLE projects ADD COLUMN parking_note TEXT;
  -- "Where I left off" — set when leaving a project
ALTER TABLE projects ADD COLUMN health TEXT DEFAULT 'unknown';
  -- green (tests pass, deployed), yellow (needs attention), red (broken)
ALTER TABLE projects ADD COLUMN revenue_status TEXT;
  -- null, 'earning', 'setup', 'stalled'
ALTER TABLE projects ADD COLUMN tech_stack TEXT;
  -- 'clojure', 'go', 'typescript', 'python', 'csharp', etc.
```

### Sessions (new table)
```sql
CREATE TABLE sessions (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL,
  started_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  ended_at TIMESTAMP,
  summary TEXT,           -- Auto or manual: "what I did this session"
  account TEXT,           -- 'pro' or 'team'
  FOREIGN KEY (project_id) REFERENCES projects(id)
);
```

### Progress Log (new table — replaces events for context tracking)
```sql
CREATE TABLE progress (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL,
  source TEXT NOT NULL,    -- 'user', 'colony', 'cron', 'deploy', 'test'
  summary TEXT NOT NULL,   -- Human-readable: "Colony wrote 3 articles for aileapers"
  detail TEXT,             -- Optional JSON payload
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (project_id) REFERENCES projects(id)
);
```

### Tasks (simplified — remove agent assignment columns)
Keep: id, title, description, status, priority, project_id, created_at, updated_at
Remove: assignee, criteria (JSON), estimate, deadline, depends_on, task_type,
        parent_id, issue_number, issue_url, tags

## New MCP Tools (8, down from 27)

### Context Tools (primary)
1. **waggle_briefing** — "What's the state of this project?"
   - Last touched date + who/what touched it
   - Parking note (where you left off)
   - Open tasks (count + top 3 by priority)
   - Recent progress (last 5 entries)
   - Health status
   - Time since last session

2. **waggle_park** — "Parking this project, here's where I left off"
   - Sets parking_note on project
   - Ends current session with summary
   - Updates last_touched_at

3. **waggle_log** — "Record what happened"
   - Adds progress entry (source: user)
   - Used by agents OR by Colony/crons via REST

4. **waggle_whats_next** — "Across all projects, what needs attention?"
   - Returns prioritized list based on:
     - Projects with health=red (broken things first)
     - Revenue projects with stalled status
     - High-priority open tasks across all projects
     - Projects not touched in 14+ days with open tasks
   - Filters by account (pro/team) if specified

### Management Tools (secondary)
5. **waggle_list_projects** — Project catalog with status/health/last-touched
   - Filterable by: status, category, account, health

6. **waggle_update_project** — Update project metadata
   - Status, health, parking_note, category, revenue_status

7. **waggle_add_task** — Simple task creation (title, priority, project_id)

8. **waggle_list_tasks** — Tasks for a project (status filter)

## New Dashboard

### Single Screen: Mission Control

Replace the current multi-view dashboard (Overview, Kanban, Inbox, Sessions, Chats,
Live Feed) with ONE screen that shows everything at a glance:

```
+------------------------------------------------------------------+
|  WAGGLE — Mission Control                          [pro] [team]  |
+------------------------------------------------------------------+
|                                                                   |
|  NEEDS ATTENTION (health=red or revenue=stalled)                 |
|  +------------------+  +------------------+  +------------------+|
|  | legacyleaps      |  | musicbox         |  | kdp-uploads      ||
|  | RED - tests fail |  | YELLOW - stale   |  | STALLED          ||
|  | 14 days ago      |  | 21 days ago      |  | session expired  ||
|  | "fixing auth bug"|  | "css not render" |  | "weekly limit"   ||
|  +------------------+  +------------------+  +------------------+|
|                                                                   |
|  EARNING                                                         |
|  +------------------+  +------------------+  +------------------+|
|  | aileapers        |  | redbubble        |  | invoice-ocr      ||
|  | GREEN - 150 arts |  | GREEN - 100 live |  | GREEN - deployed ||
|  | Colony: +3 today |  | 15 pending       |  | 168 specs pass   ||
|  +------------------+  +------------------+  +------------------+|
|                                                                   |
|  ACTIVE PROJECTS                          sorted by last-touched |
|  +------------------+  +------------------+  +------------------+|
|  | c3suite          |  | colony           |  | waggle           ||
|  | team | clojure   |  | pro | clojure    |  | pro | go         ||
|  | 2 days ago       |  | 1 day ago        |  | today            ||
|  | 3 open tasks     |  | 482 specs pass   |  | "pivot to ctx"   ||
|  +------------------+  +------------------+  +------------------+|
|                                                                   |
|  DORMANT (>30 days)                                              |
|  pala | pencilmein | pinewood-derby | smart-contract-audit-lab   |
|                                                                   |
|  KILLED                                                          |
|  cleanflow (June 2026)                                           |
|                                                                   |
+------------------------------------------------------------------+
|  Recent Progress                                                  |
|  - Colony wrote 3 articles for aileapers.com          2h ago     |
|  - c3suite: Xero OAuth2 flow working                 2 days ago  |
|  - legacyleaps: 579 tests passing, deployed          14 days ago |
|  - redbubble: 15 uploads pending (Cloudflare limit)  3 days ago  |
+------------------------------------------------------------------+
```

### Click Into Project = Detail View
- Full parking note
- All open tasks (simple list, not kanban)
- Progress timeline
- Session history (when you worked on it, what you did)
- Quick actions: add task, log progress, update status/health

### No More
- Kanban swimlanes (overkill for solo)
- Agent cards / sessions / CCTV
- Chat/messaging views
- Command bar slash commands
- Spawn forms
- Code review panels

## New Protocol Hook (replaces waggle-protocol.md)

Strip the SessionStart hook to 5 lines:

```markdown
## Waggle Context
- Call `waggle_briefing` at session start to see project state
- Call `waggle_park` before ending session to save where you left off
- Call `waggle_log` to record significant progress
```

That's it. No heartbeats. No message polling. No registration ceremony.

## REST API for External Integrations

Colony, crons, and deploy scripts can POST progress:

```
POST /api/progress
{
  "project_id": "wg-d2b49a",
  "source": "colony",
  "summary": "Wrote 3 articles for aileapers.com",
  "detail": {"articles": ["ai-tools-15", "ai-tools-16", "ai-tools-17"]}
}

POST /api/projects/:id/health
{
  "health": "green",
  "reason": "All 482 specs passing"
}
```

This lets Colony report its own progress into Waggle without any MCP ceremony.

## Migration Path

### Phase 1: Data Migration (keep existing DB, add new columns/tables)
- Add new columns to projects table
- Create sessions and progress tables
- Populate project metadata from existing MEMORY.md files
  (status, category, account, tech_stack, parking_note)
- Import key progress entries from existing events table

### Phase 2: New MCP Tools
- Implement 8 new tools
- Keep old tools temporarily (deprecated, hidden from new protocol)
- Update waggle-inject.sh to use new 5-line protocol

### Phase 3: Dashboard Rebuild
- Replace 8000-line index.html with Mission Control view
- Single page, project cards, progress feed
- Click-into-project detail view
- Mobile-first (you manage from phone too)

### Phase 4: Cleanup
- Remove old MCP tools
- Remove bridge/, push/, ws/ packages (unused)
- Remove personas, proposals, reviews tables
- Remove old dashboard views
- Strip binary size

### Phase 5: Colony Integration
- Colony POSTs progress to Waggle REST API
- Colony POSTs health checks to Waggle
- Cron scripts POST deploy/test results
- Waggle becomes the read-side of all automated work

## Success Criteria

1. Opening any project → `waggle_briefing` tells you exactly where you were
2. Dashboard on phone → see all 72 projects at a glance, sorted by urgency
3. Colony progress visible in Waggle without any manual tracking
4. Protocol injection < 5 lines (vs current 30+)
5. MCP tools: 8 (vs current 27)
6. Dashboard: 1 view (vs current 6+)
7. Zero heartbeats, zero message polling, zero registration ceremony

## What This ISN'T

- Not a replacement for Colony (Colony does autonomous work)
- Not a replacement for MEMORY.md (per-project memory stays)
- Not a task management system (tasks are secondary to context)
- Not a communication platform (no inter-agent messaging needed)

Waggle becomes the **map of your world** — not the engine that runs it.
