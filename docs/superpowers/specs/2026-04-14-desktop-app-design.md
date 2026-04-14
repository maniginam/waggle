# Waggle Desktop App — Design Spec

## Overview

Wrap the existing Waggle web dashboard in a Tauri shell to produce a native macOS desktop app with system tray, native notifications, and `.dmg` distribution. The Go backend, frontend HTML/JS, and all APIs remain untouched.

## Architecture

The desktop app is a thin Tauri wrapper that:

1. Bundles the compiled Go `waggle` binary inside the app bundle
2. Spawns the server on launch (or connects to an already-running instance)
3. Points its webview at `localhost:4740` to load the existing dashboard
4. Manages the server lifecycle (start on open, stop on quit)

No frontend duplication — the webview loads from the running server.

## Project Structure

```
waggle/
├── desktop/
│   ├── src-tauri/
│   │   ├── Cargo.toml           # Rust dependencies (tauri, serde, etc.)
│   │   ├── tauri.conf.json      # Window config, app metadata, bundling
│   │   ├── src/
│   │   │   └── main.rs          # Server lifecycle, tray, notifications
│   │   ├── resources/           # Go binary copied here before build
│   │   └── icons/               # App icons (.icns for macOS)
│   └── build.sh                 # Compile Go + Tauri in sequence
├── Makefile                     # Add `app` and `dmg` targets
└── ...                          # Existing Go project unchanged
```

## Server Lifecycle Management

### On Launch
1. Check if port 4740 is already in use
2. If occupied: connect to existing server, set `managed = false`
3. If free: spawn `waggle start` as child process, set `managed = true`
4. Wait for server to be ready (poll health endpoint), then load webview

### On Quit
- `managed = true`: kill child process (graceful shutdown)
- `managed = false`: close window only, leave server running

### Server Crash
- If managed child process dies unexpectedly, show native notification offering restart

## System Tray

- Waggle icon in macOS menubar
- Left-click: toggle window show/hide
- Right-click context menu: Show, Hide, Quit
- Unread indicator: swap to dot-variant icon when unread messages > 0

## Native Notifications

Tauri opens a WebSocket to `ws://localhost:4740/ws` and forwards these events as macOS notifications:

- Task completed by an agent
- New message directed at user
- Agent joined or disconnected
- Alert triggered (stale agent, critical task)

No backend changes required — uses existing WebSocket event stream.

## Window Behavior

- Close button (red X): hides to tray, does not quit
- Cmd+Q: fully quits app (and stops managed server)
- Window size/position restored on reopen

## Build & Distribution

### Build Pipeline
1. `make app` compiles Go binary, copies to `desktop/src-tauri/resources/`, runs `cargo tauri build`
2. Tauri produces `.app` bundle with embedded Go binary

### Distribution
- `make dmg` produces a `.dmg` installer
- Output: `desktop/src-tauri/target/release/bundle/dmg/`
- macOS only for v1
- No code signing initially (Gatekeeper warning on first run)

### Makefile Targets
```makefile
app: build
	cp waggle desktop/src-tauri/resources/
	cd desktop/src-tauri && cargo tauri build

dmg: app
	@echo "DMG at desktop/src-tauri/target/release/bundle/dmg/"
```

### Prerequisites
- Rust toolchain (`rustup`)
- Tauri CLI (`cargo install tauri-cli`)

## Scope Boundaries

### In Scope
- Tauri wrapper with webview pointing at localhost:4740
- Server lifecycle management (spawn/connect/stop)
- System tray with show/hide/quit
- Native macOS notifications from WebSocket events
- `.dmg` packaging for macOS
- `make app` and `make dmg` build targets

### Out of Scope
- Mobile app
- Linux/Windows builds (future)
- Code signing / notarization (future)
- Frontend changes
- Backend changes
- Auto-update mechanism
