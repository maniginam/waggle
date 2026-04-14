# Waggle Desktop App Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wrap the Waggle web dashboard in a Tauri v2 shell to produce a native macOS desktop app with system tray, native notifications, and `.dmg` distribution.

**Architecture:** A Tauri v2 app spawns (or connects to) the existing Go server as a child process, loads `localhost:4740` in its webview, manages server lifecycle, and bridges WebSocket events to native macOS notifications. The Go backend and frontend remain completely untouched.

**Tech Stack:** Tauri v2 (Rust), existing Go backend, existing vanilla JS frontend, macOS only.

---

## File Map

| Action | Path | Purpose |
|--------|------|---------|
| Create | `desktop/src-tauri/Cargo.toml` | Rust dependencies |
| Create | `desktop/src-tauri/tauri.conf.json` | Tauri window/app config |
| Create | `desktop/src-tauri/build.rs` | Tauri build script (required) |
| Create | `desktop/src-tauri/src/main.rs` | App entry: server lifecycle, tray, notifications |
| Create | `desktop/src-tauri/resources/.gitkeep` | Placeholder for Go binary at build time |
| Create | `desktop/src-tauri/icons/icon.png` | 512x512 app icon (simple honeycomb) |
| Create | `desktop/src-tauri/icons/tray.png` | 22x22 tray icon |
| Create | `desktop/src-tauri/icons/tray-unread.png` | 22x22 tray icon with dot |
| Create | `desktop/build.sh` | Two-step build: Go binary + Tauri app |
| Modify | `Makefile` | Add `app` and `dmg` targets |

---

### Task 1: Install Prerequisites

**Files:** None (system setup)

- [ ] **Step 1: Install Tauri CLI**

```bash
cargo install tauri-cli --version "^2"
```

Run this and wait for it to complete. Verify with:

```bash
cargo tauri --version
```

Expected: `tauri-cli 2.x.x`

- [ ] **Step 2: Commit — n/a (no code changes)**

---

### Task 2: Scaffold Tauri Project Structure

**Files:**
- Create: `desktop/src-tauri/Cargo.toml`
- Create: `desktop/src-tauri/build.rs`
- Create: `desktop/src-tauri/tauri.conf.json`
- Create: `desktop/src-tauri/src/main.rs` (minimal shell)
- Create: `desktop/src-tauri/resources/.gitkeep`

- [ ] **Step 1: Create directory structure**

```bash
mkdir -p desktop/src-tauri/src desktop/src-tauri/resources desktop/src-tauri/icons
touch desktop/src-tauri/resources/.gitkeep
```

- [ ] **Step 2: Create `desktop/src-tauri/Cargo.toml`**

```toml
[package]
name = "waggle-desktop"
version = "0.1.0"
edition = "2021"

[build-dependencies]
tauri-build = { version = "2", features = [] }

[dependencies]
tauri = { version = "2", features = ["tray-icon"] }
tauri-plugin-notification = "2"
tauri-plugin-shell = "2"
serde = { version = "1", features = ["derive"] }
serde_json = "1"
tungstenite = "0.24"
url = "2"
```

- [ ] **Step 3: Create `desktop/src-tauri/build.rs`**

```rust
fn main() {
    tauri_build::build()
}
```

- [ ] **Step 4: Create `desktop/src-tauri/tauri.conf.json`**

```json
{
  "$schema": "https://raw.githubusercontent.com/nicerdicer/tauri-v2-schema/main/tauri.conf.json",
  "productName": "Waggle",
  "version": "0.1.0",
  "identifier": "com.waggle.desktop",
  "build": {
    "frontendDist": "../frontend-placeholder"
  },
  "app": {
    "withGlobalTauri": false,
    "windows": [
      {
        "title": "Waggle",
        "width": 1280,
        "height": 800,
        "minWidth": 800,
        "minHeight": 600,
        "url": "http://localhost:4740",
        "resizable": true,
        "fullscreen": false
      }
    ],
    "trayIcon": {
      "iconPath": "icons/tray.png",
      "iconAsTemplate": true
    }
  },
  "bundle": {
    "active": true,
    "targets": ["dmg", "app"],
    "icon": [
      "icons/icon.png"
    ],
    "resources": [
      "resources/waggle"
    ],
    "macOS": {
      "minimumSystemVersion": "10.15"
    }
  },
  "plugins": {
    "notification": {
      "all": true
    },
    "shell": {
      "scope": {
        "waggle-server": {
          "name": "waggle",
          "cmd": "",
          "args": true
        }
      }
    }
  }
}
```

- [ ] **Step 5: Create minimal `desktop/src-tauri/src/main.rs`**

This is a minimal shell that just opens the window. We'll add server lifecycle, tray, and notifications in subsequent tasks.

```rust
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

fn main() {
    tauri::Builder::default()
        .plugin(tauri_plugin_notification::init())
        .plugin(tauri_plugin_shell::init())
        .run(tauri::generate_context!())
        .expect("error while running Waggle desktop");
}
```

- [ ] **Step 6: Create frontend placeholder**

Tauri v2 requires a `frontendDist` path even if we're loading a URL. Create a minimal placeholder:

```bash
mkdir -p desktop/frontend-placeholder
```

Create `desktop/frontend-placeholder/index.html`:

```html
<!DOCTYPE html>
<html><body>Loading Waggle...</body></html>
```

- [ ] **Step 7: Verify it compiles**

```bash
cd desktop/src-tauri && cargo build 2>&1
```

Expected: Successful compilation (warnings are OK).

- [ ] **Step 8: Commit**

```bash
git add desktop/
git -c commit.gpgsign=false commit -m "Scaffold Tauri desktop app structure"
```

---

### Task 3: Server Lifecycle Management

**Files:**
- Modify: `desktop/src-tauri/src/main.rs`

- [ ] **Step 1: Replace `main.rs` with server lifecycle logic**

```rust
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use std::net::TcpStream;
use std::process::{Child, Command};
use std::sync::Mutex;
use std::time::{Duration, Instant};
use tauri::{AppHandle, Manager};

struct ServerState {
    child: Option<Child>,
    managed: bool,
}

fn port_in_use(port: u16) -> bool {
    TcpStream::connect(("127.0.0.1", port))
        .map(|_| true)
        .unwrap_or(false)
}

fn waggle_binary_path(app: &AppHandle) -> String {
    if let Ok(resource_dir) = app.path().resource_dir() {
        let bundled = resource_dir.join("resources").join("waggle");
        if bundled.exists() {
            return bundled.to_string_lossy().to_string();
        }
    }
    "waggle".to_string()
}

fn start_server(app: &AppHandle) -> ServerState {
    if port_in_use(4740) {
        eprintln!("[waggle-desktop] Server already running on :4740, connecting");
        return ServerState { child: None, managed: false };
    }

    let binary = waggle_binary_path(app);
    eprintln!("[waggle-desktop] Starting server: {} start", binary);
    match Command::new(&binary).arg("start").spawn() {
        Ok(child) => {
            let start = Instant::now();
            let timeout = Duration::from_secs(10);
            while start.elapsed() < timeout {
                if port_in_use(4740) {
                    eprintln!("[waggle-desktop] Server ready");
                    return ServerState { child: Some(child), managed: true };
                }
                std::thread::sleep(Duration::from_millis(200));
            }
            eprintln!("[waggle-desktop] Server started but port not responding within timeout");
            ServerState { child: Some(child), managed: true }
        }
        Err(e) => {
            eprintln!("[waggle-desktop] Failed to start server: {}", e);
            ServerState { child: None, managed: false }
        }
    }
}

fn stop_server(state: &mut ServerState) {
    if let Some(ref mut child) = state.child {
        if state.managed {
            eprintln!("[waggle-desktop] Stopping managed server");
            let _ = child.kill();
            let _ = child.wait();
        }
    }
}

fn main() {
    tauri::Builder::default()
        .plugin(tauri_plugin_notification::init())
        .plugin(tauri_plugin_shell::init())
        .setup(|app| {
            let server_state = start_server(&app.handle());
            app.manage(Mutex::new(server_state));
            Ok(())
        })
        .on_window_event(|window, event| {
            if let tauri::WindowEvent::CloseRequested { api, .. } = event {
                api.prevent_close();
                let _ = window.hide();
            }
        })
        .run(tauri::generate_context!())
        .expect("error while running Waggle desktop");
}
```

- [ ] **Step 2: Verify it compiles**

```bash
cd desktop/src-tauri && cargo build 2>&1
```

Expected: Successful compilation.

- [ ] **Step 3: Manual test — start the app while server is already running**

```bash
cd desktop/src-tauri && cargo run 2>&1
```

Expected: App opens, connects to existing server, loads dashboard in webview. The log should show "Server already running on :4740, connecting". Close the window — it should hide, not quit. Use Cmd+Q to fully quit.

- [ ] **Step 4: Manual test — start the app with no server running**

Stop the waggle server first (`waggle stop`), then:

```bash
cd desktop/src-tauri && cargo run 2>&1
```

Expected: App spawns waggle server, waits for it, then opens the dashboard. Log shows "Starting server" then "Server ready". Cmd+Q should kill the server process too.

- [ ] **Step 5: Commit**

```bash
git add desktop/src-tauri/src/main.rs
git -c commit.gpgsign=false commit -m "Add server lifecycle management to desktop app"
```

---

### Task 4: System Tray

**Files:**
- Modify: `desktop/src-tauri/src/main.rs`
- Create: `desktop/src-tauri/icons/tray.png` (22x22 monochrome icon)

- [ ] **Step 1: Generate tray icon**

Create a simple 22x22 PNG for the tray. Use ImageMagick or a similar tool:

```bash
convert -size 22x22 xc:transparent \
  -fill white -draw "circle 11,11 11,2" \
  desktop/src-tauri/icons/tray.png
```

If `convert` isn't available, create any simple 22x22 PNG manually and place it at `desktop/src-tauri/icons/tray.png`. A white circle on transparent background works fine as a placeholder.

Also create the app icon (512x512):

```bash
convert -size 512x512 xc:transparent \
  -fill '#f0a500' -draw "circle 256,256 256,20" \
  desktop/src-tauri/icons/icon.png
```

- [ ] **Step 2: Add tray setup to `main.rs`**

Add these imports at the top of `main.rs`:

```rust
use tauri::menu::{Menu, MenuItem};
use tauri::tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent};
use tauri::image::Image;
```

Add this function before `main()`:

```rust
fn setup_tray(app: &AppHandle) -> Result<(), Box<dyn std::error::Error>> {
    let show = MenuItem::with_id(app, "show", "Show", true, None::<&str>)?;
    let hide = MenuItem::with_id(app, "hide", "Hide", true, None::<&str>)?;
    let quit = MenuItem::with_id(app, "quit", "Quit", true, None::<&str>)?;
    let menu = Menu::with_items(app, &[&show, &hide, &quit])?;

    let icon = Image::from_path("icons/tray.png")
        .or_else(|_| {
            let resource_dir = app.path().resource_dir().unwrap_or_default();
            Image::from_path(resource_dir.join("icons/tray.png"))
        })
        .unwrap_or_else(|_| Image::from_bytes(include_bytes!("../icons/tray.png")).expect("embedded tray icon"));

    let handle = app.clone();
    let handle2 = app.clone();

    TrayIconBuilder::new()
        .icon(icon)
        .icon_as_template(true)
        .menu(&menu)
        .on_menu_event(move |_app, event| {
            match event.id().as_ref() {
                "show" => {
                    if let Some(w) = handle.get_webview_window("main") {
                        let _ = w.show();
                        let _ = w.set_focus();
                    }
                }
                "hide" => {
                    if let Some(w) = handle.get_webview_window("main") {
                        let _ = w.hide();
                    }
                }
                "quit" => {
                    let state = handle.state::<Mutex<ServerState>>();
                    if let Ok(mut s) = state.lock() {
                        stop_server(&mut s);
                    }
                    handle.exit(0);
                }
                _ => {}
            }
        })
        .on_tray_icon_event(move |_tray, event| {
            if let TrayIconEvent::Click { button: MouseButton::Left, button_state: MouseButtonState::Up, .. } = event {
                if let Some(w) = handle2.get_webview_window("main") {
                    if w.is_visible().unwrap_or(false) {
                        let _ = w.hide();
                    } else {
                        let _ = w.show();
                        let _ = w.set_focus();
                    }
                }
            }
        })
        .build(app)?;

    Ok(())
}
```

- [ ] **Step 3: Wire tray into `setup` closure**

In the `.setup(|app| { ... })` block in `main()`, add after the `app.manage(...)` line:

```rust
            if let Err(e) = setup_tray(&app.handle()) {
                eprintln!("[waggle-desktop] Failed to setup tray: {}", e);
            }
```

- [ ] **Step 4: Verify it compiles**

```bash
cd desktop/src-tauri && cargo build 2>&1
```

Expected: Successful compilation.

- [ ] **Step 5: Manual test**

```bash
cd desktop/src-tauri && cargo run 2>&1
```

Expected: Tray icon appears in macOS menubar. Left-click toggles window. Right-click shows Show/Hide/Quit menu. Quit stops the managed server.

- [ ] **Step 6: Commit**

```bash
git add desktop/src-tauri/src/main.rs desktop/src-tauri/icons/
git -c commit.gpgsign=false commit -m "Add system tray with show/hide/quit menu"
```

---

### Task 5: Native Notifications via WebSocket

**Files:**
- Modify: `desktop/src-tauri/src/main.rs`

- [ ] **Step 1: Add WebSocket listener function**

Add this function before `main()`:

```rust
fn spawn_notification_listener(app: AppHandle) {
    std::thread::spawn(move || {
        // Wait for server to be ready
        std::thread::sleep(Duration::from_secs(2));

        let ws_url = "ws://127.0.0.1:4740/ws";
        let connect_result = tungstenite::connect(url::Url::parse(ws_url).unwrap());

        let (mut socket, _response) = match connect_result {
            Ok(conn) => conn,
            Err(e) => {
                eprintln!("[waggle-desktop] WebSocket connect failed: {}", e);
                return;
            }
        };

        eprintln!("[waggle-desktop] WebSocket connected for notifications");

        loop {
            match socket.read() {
                Ok(tungstenite::Message::Text(text)) => {
                    if let Ok(event) = serde_json::from_str::<serde_json::Value>(&text) {
                        if let Some(notification) = event_to_notification(&event) {
                            let _ = app.notification()
                                .builder()
                                .title(&notification.0)
                                .body(&notification.1)
                                .show();
                        }
                    }
                }
                Ok(tungstenite::Message::Close(_)) => {
                    eprintln!("[waggle-desktop] WebSocket closed");
                    break;
                }
                Err(e) => {
                    eprintln!("[waggle-desktop] WebSocket error: {}", e);
                    break;
                }
                _ => {}
            }
        }
    });
}

fn event_to_notification(event: &serde_json::Value) -> Option<(String, String)> {
    let event_type = event.get("type")?.as_str()?;

    match event_type {
        "task_completed" => {
            let agent = event.get("agent_id").and_then(|v| v.as_str()).unwrap_or("Unknown");
            let task = event.get("task_id").and_then(|v| v.as_str()).unwrap_or("");
            Some(("Task Completed".into(), format!("{} completed task {}", agent, task)))
        }
        "message" => {
            let payload = event.get("payload")?;
            let from = payload.get("from").and_then(|v| v.as_str()).unwrap_or("Unknown");
            let to = payload.get("to").and_then(|v| v.as_str()).unwrap_or("");
            if to == "user" || to.is_empty() {
                let body = payload.get("body").and_then(|v| v.as_str()).unwrap_or("");
                let preview = if body.len() > 100 { &body[..100] } else { body };
                Some((format!("Message from {}", from), preview.to_string()))
            } else {
                None
            }
        }
        "agent_joined" => {
            let agent = event.get("agent_id").and_then(|v| v.as_str()).unwrap_or("Unknown");
            Some(("Agent Joined".into(), format!("{} connected", agent)))
        }
        "agent_left" => {
            let agent = event.get("agent_id").and_then(|v| v.as_str()).unwrap_or("Unknown");
            Some(("Agent Disconnected".into(), format!("{} disconnected", agent)))
        }
        "agent_stale" => {
            let agent = event.get("agent_id").and_then(|v| v.as_str()).unwrap_or("Unknown");
            Some(("Agent Stale".into(), format!("{} is not responding", agent)))
        }
        _ => None,
    }
}
```

- [ ] **Step 2: Add notification plugin import**

Add at the top of `main.rs`:

```rust
use tauri_plugin_notification::NotificationExt;
```

- [ ] **Step 3: Wire into setup**

In the `.setup(|app| { ... })` block, add after the tray setup:

```rust
            spawn_notification_listener(app.handle().clone());
```

- [ ] **Step 4: Verify it compiles**

```bash
cd desktop/src-tauri && cargo build 2>&1
```

Expected: Successful compilation.

- [ ] **Step 5: Manual test**

Run the app, then trigger events from the CLI:

```bash
# In one terminal
cd desktop/src-tauri && cargo run

# In another terminal
waggle msg send user "Test notification from CLI"
```

Expected: macOS notification appears with "Message from ..." title.

- [ ] **Step 6: Commit**

```bash
git add desktop/src-tauri/src/main.rs
git -c commit.gpgsign=false commit -m "Add native notifications from WebSocket events"
```

---

### Task 6: Graceful Shutdown on Cmd+Q

**Files:**
- Modify: `desktop/src-tauri/src/main.rs`

- [ ] **Step 1: Add `RunEvent` handler to `main()`**

Replace the `.run(tauri::generate_context!())` call with a `build` + `run` pattern to handle the exit event:

Replace:
```rust
        .run(tauri::generate_context!())
        .expect("error while running Waggle desktop");
```

With:
```rust
        .build(tauri::generate_context!())
        .expect("error while building Waggle desktop")
        .run(|app, event| {
            if let tauri::RunEvent::ExitRequested { .. } = event {
                let state = app.state::<Mutex<ServerState>>();
                if let Ok(mut s) = state.lock() {
                    stop_server(&mut s);
                }
            }
        });
```

- [ ] **Step 2: Verify it compiles**

```bash
cd desktop/src-tauri && cargo build 2>&1
```

Expected: Successful compilation.

- [ ] **Step 3: Manual test**

Start the app without the server running (so it spawns one). Press Cmd+Q. Check that the waggle server process is no longer running:

```bash
waggle status
```

Expected: Should report the server is not running.

- [ ] **Step 4: Commit**

```bash
git add desktop/src-tauri/src/main.rs
git -c commit.gpgsign=false commit -m "Gracefully stop managed server on app quit"
```

---

### Task 7: Build Script and Makefile Targets

**Files:**
- Create: `desktop/build.sh`
- Modify: `Makefile`

- [ ] **Step 1: Create `desktop/build.sh`**

```bash
#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

echo "==> Building Go binary..."
cd "$PROJECT_ROOT"
make build

echo "==> Copying binary to Tauri resources..."
cp "$PROJECT_ROOT/waggle" "$SCRIPT_DIR/src-tauri/resources/waggle"
chmod +x "$SCRIPT_DIR/src-tauri/resources/waggle"

echo "==> Building Tauri app..."
cd "$SCRIPT_DIR/src-tauri"
cargo tauri build

echo "==> Done!"
echo "    App: target/release/bundle/macos/Waggle.app"
echo "    DMG: target/release/bundle/dmg/"
```

Make it executable:

```bash
chmod +x desktop/build.sh
```

- [ ] **Step 2: Add Makefile targets**

Append to the existing `Makefile`:

```makefile

.PHONY: app dmg

app: build
	cp waggle desktop/src-tauri/resources/waggle
	chmod +x desktop/src-tauri/resources/waggle
	cd desktop/src-tauri && cargo tauri build

dmg: app
	@echo "DMG at desktop/src-tauri/target/release/bundle/dmg/"
	@ls desktop/src-tauri/target/release/bundle/dmg/*.dmg 2>/dev/null || echo "(no DMG found)"
```

- [ ] **Step 3: Test the build**

```bash
cd /Users/maniginam/projects/waggle && make app 2>&1
```

Expected: Go binary compiles, copies to resources, Tauri builds the `.app` bundle. Output shows the `.app` and `.dmg` paths.

- [ ] **Step 4: Test the DMG**

```bash
ls desktop/src-tauri/target/release/bundle/dmg/
```

Expected: A `.dmg` file is listed.

- [ ] **Step 5: Test opening the built app**

```bash
open desktop/src-tauri/target/release/bundle/macos/Waggle.app
```

Expected: Waggle desktop app opens, connects to or starts server, shows dashboard.

- [ ] **Step 6: Commit**

```bash
git add desktop/build.sh Makefile
git -c commit.gpgsign=false commit -m "Add build script and Makefile targets for desktop app"
```

---

### Task 8: Add `desktop/` to `.gitignore` (build artifacts)

**Files:**
- Create or modify: `desktop/.gitignore`

- [ ] **Step 1: Create `desktop/.gitignore`**

```gitignore
src-tauri/target/
src-tauri/resources/waggle
frontend-placeholder/
```

- [ ] **Step 2: Commit**

```bash
git add desktop/.gitignore
git -c commit.gpgsign=false commit -m "Add gitignore for desktop build artifacts"
```

---

## Summary

| Task | Description | Dependencies |
|------|-------------|--------------|
| 1 | Install Tauri CLI | None |
| 2 | Scaffold Tauri project | Task 1 |
| 3 | Server lifecycle management | Task 2 |
| 4 | System tray | Task 3 |
| 5 | Native notifications via WebSocket | Task 4 |
| 6 | Graceful shutdown on Cmd+Q | Task 5 |
| 7 | Build script and Makefile targets | Task 6 |
| 8 | Gitignore for build artifacts | Task 2 |
