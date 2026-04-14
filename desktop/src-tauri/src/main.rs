#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use std::net::TcpStream;
use std::process::{Child, Command};
use std::sync::Mutex;
use std::time::{Duration, Instant};
use tauri::menu::{Menu, MenuItem};
use tauri::tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent};
use tauri::image::Image;
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

fn main() {
    tauri::Builder::default()
        .plugin(tauri_plugin_notification::init())
        .plugin(tauri_plugin_shell::init())
        .setup(|app| {
            let server_state = start_server(&app.handle());
            app.manage(Mutex::new(server_state));
            if let Err(e) = setup_tray(&app.handle()) {
                eprintln!("[waggle-desktop] Failed to setup tray: {}", e);
            }
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
