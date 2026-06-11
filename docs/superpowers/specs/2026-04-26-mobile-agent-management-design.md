# Mobile Agent Management

## Problem
Waggle dashboard is unusable for agent management on mobile. Spawn form has too many fields, kill buttons are hidden behind panels/context menus, and the tab bar doesn't surface agent actions. Result: user lost a full day unable to spawn or kill agents from phone.

## Changes

### 1. Quick-Spawn (One-Tap Re-Spawn)
- Store last 5 spawned agent configs in localStorage under `waggle_recent_spawns`
- Each entry: `{ name, project_id, work_dir, model, type, prompt }`
- Show "Quick Spawn" cards at top of spawn panel on mobile - tap to re-launch
- Also show "Respawn" button on disconnected agent cards using last known config from `agentDirs` + localStorage

### 2. Simplified Mobile Spawn Form
- On screens <= 480px, spawn form defaults to compact mode: name, project (dropdown), prompt
- Model defaults to sonnet, type defaults to claude-code, workdir auto-fills from project
- "More options" toggle reveals full form (model, type, workdir, mode, system prompt)
- FAB long-press shows menu: "New Task" | "Spawn Agent"

### 3. Kill Buttons on Mobile
- Agent cards in overview and sidebar get visible red kill button on mobile (not hidden)
- Kill calls `DELETE /api/sessions/:name` (existing endpoint)
- Confirmation dialog uses existing `showConfirm()` with touch-sized buttons
- Disconnected agents show dismiss button (already exists but too small)

### 4. Tab Bar: Replace Cmd with Spawn
- Bottom tab "Cmd" becomes "Spawn" with rocket icon
- Tapping opens simplified spawn panel directly
- Cmd bar remains accessible from header area

### 5. Agent Cards: Better Mobile Status
- Larger status dots on mobile (10px vs 7px)
- Stale agents: inline "Poke" button visible without opening panel
- Disconnected agents: inline "Respawn" button (uses last known config)
- Active agents: inline "Kill" button (red)

## Scope
All changes are in `internal/dashboard/static/index.html` (single file: CSS + HTML + JS). No backend changes needed - all APIs already exist.

## Files Modified
- `internal/dashboard/static/index.html` - CSS media queries, spawn form JS, FAB behavior, tab bar, agent card rendering
