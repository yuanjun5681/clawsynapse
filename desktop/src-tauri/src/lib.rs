use std::io::{BufRead, BufReader, Read, Write};
use std::net::{TcpStream, ToSocketAddrs};
use std::path::PathBuf;
use std::process::{Child, Command, Stdio};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use nix::sys::signal::{self, Signal};
use nix::unistd::Pid;
use serde::Serialize;
#[cfg(target_os = "macos")]
use tauri::include_image;
use tauri::menu::{MenuBuilder, MenuItemBuilder};
use tauri::tray::TrayIconBuilder;
use tauri::{AppHandle, Emitter, Manager, RunEvent, WindowEvent};

#[cfg(target_os = "macos")]
const TRAY_TEMPLATE_ICON: tauri::image::Image<'_> = include_image!("./icons/trayTemplate.png");

struct BackendState {
    child: Option<Child>,
    ready: bool,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct BackendConfig {
    base_url: String,
    auth_token: Option<String>,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct DirConfig {
    bundle_dir: String,
    user_data_dir: String,
}

#[derive(Serialize, Clone)]
#[serde(rename_all = "camelCase")]
struct SetupStatus {
    node_installed: bool,
    node_version: String,
    docker_running: bool,
    container_image_built: bool,
    container_resources_ready: bool,
    api_key_configured: bool,
    user_data_dir: String,
}

/// macOS GUI apps don't inherit the user's shell PATH.
/// Resolve the full PATH from an interactive login shell and set it for this process,
/// so all Command::new() calls can find node, docker, etc.
/// Uses -i (interactive) so that .zshrc/.bashrc are sourced (needed for nvm, etc.).
fn fix_path_env() {
    let shell = std::env::var("SHELL").unwrap_or_else(|_| "/bin/zsh".to_string());
    // -i -l: interactive login — sources .zprofile/.zshrc so nvm, pyenv, etc. are loaded
    if let Ok(output) = Command::new(&shell)
        .args(["-i", "-l", "-c", "echo $PATH"])
        .stdin(Stdio::null())
        .stderr(Stdio::null())
        .output()
    {
        if output.status.success() {
            let raw = String::from_utf8_lossy(&output.stdout);
            // Interactive shells may print extra lines (motd, etc.) — take the last non-empty line
            let shell_path = raw
                .lines()
                .rev()
                .find(|l| !l.trim().is_empty() && l.contains('/'))
                .unwrap_or("")
                .trim()
                .to_string();
            if !shell_path.is_empty() {
                std::env::set_var("PATH", &shell_path);
                return;
            }
        }
    }

    // Fallback: append common macOS binary locations
    let current = std::env::var("PATH").unwrap_or_default();
    let extra = [
        "/opt/homebrew/bin",
        "/opt/homebrew/sbin",
        "/usr/local/bin",
        "/usr/local/sbin",
    ];
    let combined = format!("{}:{}", current, extra.join(":"));
    std::env::set_var("PATH", &combined);
}

fn is_release_build() -> bool {
    // CARGO_MANIFEST_DIR is baked at compile time.
    // In dev it points to a real path; in packaged .app it doesn't exist.
    !PathBuf::from(env!("CARGO_MANIFEST_DIR")).exists()
}

fn bundle_dir(app: &AppHandle) -> PathBuf {
    if let Ok(dir) = std::env::var("NANOCLAW_BUNDLE_DIR") {
        return PathBuf::from(dir);
    }
    if is_release_build() {
        app.path()
            .resource_dir()
            .expect("Failed to resolve resource dir")
    } else {
        let manifest = PathBuf::from(env!("CARGO_MANIFEST_DIR"));
        manifest.parent().unwrap().parent().unwrap().to_path_buf()
    }
}

fn user_data_dir(app: &AppHandle) -> PathBuf {
    if let Ok(dir) = std::env::var("NANOCLAW_DATA_DIR") {
        return PathBuf::from(dir);
    }
    if is_release_build() {
        // ~/Library/Application Support/com.nanoclaw.desktop/
        app.path()
            .app_data_dir()
            .expect("Failed to resolve app data dir")
    } else {
        let manifest = PathBuf::from(env!("CARGO_MANIFEST_DIR"));
        manifest.parent().unwrap().parent().unwrap().to_path_buf()
    }
}

/// Read .env file from user data dir and return key=value pairs
fn load_user_env(data_dir: &PathBuf) -> Vec<(String, String)> {
    let env_path = data_dir.join(".env");
    let mut pairs = Vec::new();
    if let Ok(content) = std::fs::read_to_string(&env_path) {
        for line in content.lines() {
            let trimmed = line.trim();
            if trimmed.is_empty() || trimmed.starts_with('#') {
                continue;
            }
            if let Some(eq_pos) = trimmed.find('=') {
                let key = trimmed[..eq_pos].trim().to_string();
                let val = trimmed[eq_pos + 1..].trim().to_string();
                if !key.is_empty() {
                    pairs.push((key, val));
                }
            }
        }
    }
    pairs
}

fn backend_host() -> String {
    std::env::var("HTTP_HOST").unwrap_or_else(|_| "127.0.0.1".to_string())
}

fn backend_port() -> u16 {
    std::env::var("PORT")
        .ok()
        .and_then(|v| v.parse::<u16>().ok())
        .unwrap_or(3100)
}

fn backend_base_url() -> String {
    format!("http://{}:{}", backend_host(), backend_port())
}

fn backend_auth_token() -> Option<String> {
    std::env::var("NANOCLAW_API_TOKEN")
        .ok()
        .filter(|v| !v.is_empty())
}

fn is_backend_healthy(host: &str, port: u16) -> bool {
    let addr = format!("{}:{}", host, port);
    let sockets: Vec<_> = match addr.to_socket_addrs() {
        Ok(iter) => iter.collect(),
        Err(_) => return false,
    };

    if sockets.is_empty() {
        return false;
    }

    for socket in sockets {
        let mut stream = match TcpStream::connect_timeout(&socket, Duration::from_millis(1500)) {
            Ok(s) => s,
            Err(_) => continue,
        };

        let _ = stream.set_read_timeout(Some(Duration::from_millis(1500)));
        let _ = stream.set_write_timeout(Some(Duration::from_millis(1500)));

        let request = format!(
            "GET /api/health HTTP/1.1\r\nHost: {}\r\nConnection: close\r\n\r\n",
            host
        );

        if stream.write_all(request.as_bytes()).is_err() {
            continue;
        }

        let mut response = String::new();
        if stream.read_to_string(&mut response).is_err() {
            continue;
        }

        if response.starts_with("HTTP/1.1 200") || response.starts_with("HTTP/1.0 200") {
            return true;
        }
    }

    false
}

fn is_nanoclaw_backend_listening_on_port(bundle: &PathBuf) -> bool {
    let port = backend_port();
    let lsof_output = Command::new("lsof")
        .args([
            "-nP",
            &format!("-iTCP:{}", port),
            "-sTCP:LISTEN",
            "-t",
        ])
        .output();

    let output = match lsof_output {
        Ok(v) => v,
        Err(_) => return false,
    };

    let backend_entry = bundle.join("dist/index.js");
    let backend_entry_text = backend_entry.to_string_lossy();

    let pids = String::from_utf8_lossy(&output.stdout);
    for line in pids.lines().filter(|v| !v.trim().is_empty()) {
        let pid = match line.trim().parse::<i32>() {
            Ok(v) if v > 0 => v,
            _ => continue,
        };

        let cmd_output = Command::new("ps")
            .args(["-p", &pid.to_string(), "-o", "command="])
            .output();

        let cmd = match cmd_output {
            Ok(v) => String::from_utf8_lossy(&v.stdout).trim().to_string(),
            Err(_) => String::new(),
        };

        let is_nanoclaw_backend =
            cmd.contains("node") && cmd.contains(backend_entry_text.as_ref());

        if is_nanoclaw_backend {
            return true;
        }
    }

    false
}

fn wait_for_backend_ready(app: AppHandle, state: Arc<Mutex<BackendState>>) {
    let host = backend_host();
    let port = backend_port();

    std::thread::spawn(move || {
        for _ in 0..80 {
            let still_running = {
                let s = state.lock().unwrap();
                s.child.is_some()
            };

            if !still_running {
                return;
            }

            if is_backend_healthy(&host, port) {
                let mut should_emit = false;
                {
                    let mut s = state.lock().unwrap();
                    if s.child.is_some() && !s.ready {
                        s.ready = true;
                        should_emit = true;
                    }
                }

                if should_emit {
                    let _ = app.emit("backend-ready", ());
                    if let Some(window) = app.get_webview_window("main") {
                        let _ = window.show();
                        let _ = window.set_focus();
                    }
                }
                return;
            }

            std::thread::sleep(Duration::from_millis(250));
        }
    });
}

fn mark_backend_ready(app: &AppHandle, state: &Arc<Mutex<BackendState>>) {
    {
        let mut s = state.lock().unwrap();
        s.ready = true;
    }
    let _ = app.emit("backend-ready", ());
    if let Some(window) = app.get_webview_window("main") {
        let _ = window.show();
        let _ = window.set_focus();
    }
}

fn kill_orphan_backend_on_port(bundle: &PathBuf) {
    let port = backend_port();
    let lsof_output = Command::new("lsof")
        .args([
            "-nP",
            &format!("-iTCP:{}", port),
            "-sTCP:LISTEN",
            "-t",
        ])
        .output();

    let output = match lsof_output {
        Ok(v) => v,
        Err(_) => return,
    };

    let backend_entry = bundle.join("dist/index.js");
    let backend_entry_text = backend_entry.to_string_lossy();

    let pids = String::from_utf8_lossy(&output.stdout);
    for line in pids.lines().filter(|v| !v.trim().is_empty()) {
        let pid = match line.trim().parse::<i32>() {
            Ok(v) if v > 0 => v,
            _ => continue,
        };

        let cmd_output = Command::new("ps")
            .args(["-p", &pid.to_string(), "-o", "command="])
            .output();

        let cmd = match cmd_output {
            Ok(v) => String::from_utf8_lossy(&v.stdout).trim().to_string(),
            Err(_) => String::new(),
        };

        let is_nanoclaw_backend =
            cmd.contains("node") && cmd.contains(backend_entry_text.as_ref());

        if is_nanoclaw_backend {
            let _ = signal::kill(Pid::from_raw(pid), Signal::SIGTERM);
        }
    }
}

fn spawn_backend(app: &AppHandle, state: &Arc<Mutex<BackendState>>) {
    let bundle = bundle_dir(app);
    let data = user_data_dir(app);
    let node_entry = bundle.join("dist/index.js");
    let host = backend_host();
    let port = backend_port();

    {
        let mut s = state.lock().unwrap();
        if let Some(child) = s.child.as_mut() {
            match child.try_wait() {
                Ok(Some(_)) | Err(_) => {
                    s.child = None;
                }
                Ok(None) => {
                    return;
                }
            }
        }
    }

    // Another NanoClaw backend is already running on configured host/port.
    // Reuse it instead of spawning a duplicate process that will fail with EADDRINUSE.
    if is_backend_healthy(&host, port) {
        eprintln!(
            "Backend already reachable at {}:{}; skipping local spawn",
            host, port
        );
        mark_backend_ready(app, state);
        return;
    }

    // Health checks can occasionally miss a backend during startup transitions.
    // Fallback to process-based detection so we avoid spawning a duplicate.
    if is_nanoclaw_backend_listening_on_port(&bundle) {
        eprintln!(
            "Backend already listening at {}:{}; skipping local spawn",
            host, port
        );
        mark_backend_ready(app, state);
        return;
    }

    if !node_entry.exists() {
        eprintln!(
            "Backend not built: {} not found. Run 'npm run build' in project root first.",
            node_entry.display()
        );
        return;
    }

    let mut cmd = Command::new("node");
    cmd.arg(&node_entry)
        .current_dir(&data) // process.cwd() = user data dir
        .env("NANOCLAW_BUNDLE_DIR", &bundle)
        .env("NANOCLAW_DATA_DIR", &data);

    // Load .env from user data dir and pass as env vars
    for (key, val) in load_user_env(&data) {
        cmd.env(&key, &val);
    }

    cmd.stdout(Stdio::piped()).stderr(Stdio::piped());

    let child = cmd.spawn();

    match child {
        Ok(mut child) => {
            let stdout = child.stdout.take().expect("Failed to capture stdout");
            let stderr = child.stderr.take().expect("Failed to capture stderr");

            {
                let mut s = state.lock().unwrap();
                s.child = Some(child);
                s.ready = false;
            }

            wait_for_backend_ready(app.clone(), Arc::clone(state));

            // Forward backend stdout and detect process exit
            let app_handle = app.clone();
            let state_clone = Arc::clone(state);
            std::thread::spawn(move || {
                let reader = BufReader::new(stdout);
                for line in reader.lines() {
                    match line {
                        Ok(line) => eprintln!("[backend] {}", line),
                        Err(_) => break,
                    }
                }
                // Backend process ended
                {
                    let mut s = state_clone.lock().unwrap();
                    s.ready = false;
                    s.child = None;
                }
                let _ = app_handle.emit("backend-stopped", ());
            });

            // Forward stderr
            std::thread::spawn(move || {
                let reader = BufReader::new(stderr);
                for line in reader.lines() {
                    match line {
                        Ok(line) => eprintln!("[backend:err] {}", line),
                        Err(_) => break,
                    }
                }
            });
        }
        Err(e) => {
            eprintln!("Failed to spawn backend: {}", e);
        }
    }
}

fn kill_backend(app: &AppHandle, state: &Arc<Mutex<BackendState>>) {
    let mut s = state.lock().unwrap();
    if let Some(ref child) = s.child {
        let pid = child.id() as i32;
        // Send SIGTERM for graceful shutdown
        let _ = signal::kill(Pid::from_raw(pid), Signal::SIGTERM);
    }
    s.ready = false;
    // Don't set child to None yet — the stdout thread will do that when the process exits

    drop(s);

    // Also stop any orphaned nanoclaw containers
    let bundle = bundle_dir(app);
    std::thread::spawn(move || {
        kill_orphan_backend_on_port(&bundle);

        let output = Command::new("docker")
            .args(["ps", "--filter", "name=nanoclaw-", "--format", "{{.Names}}"])
            .output();
        if let Ok(output) = output {
            let names = String::from_utf8_lossy(&output.stdout);
            for name in names.lines().filter(|l| !l.is_empty()) {
                let _ = Command::new("docker").args(["stop", name]).output();
            }
        }
    });
}

fn wait_for_backend_exit(state: &Arc<Mutex<BackendState>>, timeout: Duration) {
    let start = Instant::now();
    loop {
        let stopped = {
            let mut s = state.lock().unwrap();
            match s.child.as_mut() {
                Some(child) => match child.try_wait() {
                    Ok(Some(_)) => {
                        s.child = None;
                        true
                    }
                    Ok(None) => false,
                    Err(_) => {
                        s.child = None;
                        true
                    }
                },
                None => true,
            }
        };

        if stopped {
            return;
        }

        if start.elapsed() >= timeout {
            let maybe_pid = {
                let s = state.lock().unwrap();
                s.child.as_ref().map(|child| child.id() as i32)
            };

            if let Some(pid) = maybe_pid {
                let _ = signal::kill(Pid::from_raw(pid), Signal::SIGKILL);
            }
            return;
        }

        std::thread::sleep(Duration::from_millis(100));
    }
}

#[tauri::command]
fn get_backend_status(state: tauri::State<Arc<Mutex<BackendState>>>) -> bool {
    state.lock().unwrap().ready
}

#[tauri::command]
fn get_backend_config() -> BackendConfig {
    BackendConfig {
        base_url: backend_base_url(),
        auth_token: backend_auth_token(),
    }
}

#[tauri::command]
fn restart_backend(
    app: AppHandle,
    state: tauri::State<Arc<Mutex<BackendState>>>,
) -> Result<(), String> {
    let state = Arc::clone(&state);
    kill_backend(&app, &state);
    wait_for_backend_exit(&state, Duration::from_secs(5));
    spawn_backend(&app, &state);
    Ok(())
}

#[tauri::command]
fn get_dirs(app: AppHandle) -> DirConfig {
    DirConfig {
        bundle_dir: bundle_dir(&app).to_string_lossy().to_string(),
        user_data_dir: user_data_dir(&app).to_string_lossy().to_string(),
    }
}

#[tauri::command]
fn check_setup(app: AppHandle) -> SetupStatus {
    let data = user_data_dir(&app);
    let bundle = bundle_dir(&app);

    // Check Node.js
    let (node_installed, node_version) = match Command::new("node").arg("--version").output() {
        Ok(output) if output.status.success() => {
            let ver = String::from_utf8_lossy(&output.stdout).trim().to_string();
            (true, ver)
        }
        _ => (false, String::new()),
    };

    // Check Docker running
    let docker_running = Command::new("docker")
        .args(["info"])
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .status()
        .map(|s| s.success())
        .unwrap_or(false);

    // Check container image built
    let container_image_built = Command::new("docker")
        .args(["image", "inspect", "nanoclaw-agent-agno:latest"])
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .status()
        .map(|s| s.success())
        .unwrap_or(false);

    // Check required bundled resource for image build
    let container_resources_ready = bundle.join("container-agno").exists();

    // Check model credentials configured
    let api_key_configured = {
        let env_vars = load_user_env(&data);
        let has_value = |key: &str| {
            env_vars
                .iter()
                .any(|(k, v)| k == key && !v.trim().is_empty())
        };

        has_value("ANTHROPIC_API_KEY")
            || (has_value("AGNO_API_KEY")
                && has_value("AGNO_MODEL_ID")
                && has_value("AGNO_BASE_URL"))
    };

    SetupStatus {
        node_installed,
        node_version,
        docker_running,
        container_image_built,
        container_resources_ready,
        api_key_configured,
        user_data_dir: data.to_string_lossy().to_string(),
    }
}

#[tauri::command]
fn save_env_config(app: AppHandle, entries: Vec<(String, String)>) -> Result<(), String> {
    let data = user_data_dir(&app);
    let env_path = data.join(".env");

    // Read existing .env content, preserving entries not being overwritten
    let mut existing: Vec<(String, String)> = Vec::new();
    if let Ok(content) = std::fs::read_to_string(&env_path) {
        for line in content.lines() {
            let trimmed = line.trim();
            if trimmed.is_empty() || trimmed.starts_with('#') {
                continue;
            }
            if let Some(eq_pos) = trimmed.find('=') {
                let key = trimmed[..eq_pos].trim().to_string();
                let val = trimmed[eq_pos + 1..].trim().to_string();
                existing.push((key, val));
            }
        }
    }

    // Merge: new entries override existing
    let new_keys: Vec<&str> = entries.iter().map(|(k, _)| k.as_str()).collect();
    let mut merged: Vec<(String, String)> = existing
        .into_iter()
        .filter(|(k, _)| !new_keys.contains(&k.as_str()))
        .collect();
    for (k, v) in entries {
        if !v.is_empty() {
            merged.push((k, v));
        }
    }

    let content: String = merged
        .iter()
        .map(|(k, v)| format!("{}={}", k, v))
        .collect::<Vec<_>>()
        .join("\n")
        + "\n";

    std::fs::write(&env_path, content).map_err(|e| format!("Failed to write .env: {}", e))
}

#[tauri::command]
fn read_env_config(app: AppHandle) -> Vec<(String, String)> {
    let data = user_data_dir(&app);
    load_user_env(&data)
}

#[tauri::command]
async fn build_container_image(app: AppHandle) -> Result<String, String> {
    let bundle = bundle_dir(&app);
    let container_dir = bundle.join("container-agno");

    if !container_dir.exists() {
        return Err(format!(
            "Container directory not found: {}",
            container_dir.display()
        ));
    }

    let output = Command::new("docker")
        .args([
            "build",
            "-t",
            "nanoclaw-agent-agno:latest",
            ".",
        ])
        .current_dir(&container_dir)
        .output()
        .map_err(|e| format!("Failed to run docker build: {}", e))?;

    if output.status.success() {
        Ok("Container image built successfully".to_string())
    } else {
        let stderr = String::from_utf8_lossy(&output.stderr);
        Err(format!("Docker build failed: {}", stderr))
    }
}

pub fn run() {
    let backend_state = Arc::new(Mutex::new(BackendState {
        child: None,
        ready: false,
    }));

    let state_for_setup = Arc::clone(&backend_state);
    let state_for_exit = Arc::clone(&backend_state);

    tauri::Builder::default()
        .plugin(tauri_plugin_opener::init())
        .manage(backend_state)
        .invoke_handler(tauri::generate_handler![
            get_backend_status,
            get_backend_config,
            restart_backend,
            get_dirs,
            check_setup,
            save_env_config,
            read_env_config,
            build_container_image,
        ])
        .setup(move |app| {
            // Fix PATH for macOS GUI apps so node/docker are found
            fix_path_env();

            // Create user data directories on startup
            let data = user_data_dir(&app.handle());
            for subdir in ["store", "data", "groups"] {
                let dir = data.join(subdir);
                if !dir.exists() {
                    let _ = std::fs::create_dir_all(&dir);
                }
            }

            #[cfg(target_os = "macos")]
            {
                app.set_activation_policy(tauri::ActivationPolicy::Regular);
            }

            // Build tray menu
            let open_item =
                MenuItemBuilder::with_id("open", "Open Chat").build(app)?;
            let restart_item =
                MenuItemBuilder::with_id("restart", "Restart Backend").build(app)?;
            let quit_item =
                MenuItemBuilder::with_id("quit", "Quit").build(app)?;
            let menu = MenuBuilder::new(app)
                .item(&open_item)
                .item(&restart_item)
                .separator()
                .item(&quit_item)
                .build()?;

            let app_handle = app.handle().clone();
            let tray_state = Arc::clone(&state_for_setup);
            let tray_builder = {
                #[cfg(target_os = "macos")]
                {
                    TrayIconBuilder::new()
                        .icon(TRAY_TEMPLATE_ICON)
                        .icon_as_template(true)
                }

                #[cfg(not(target_os = "macos"))]
                {
                    if let Some(icon) = app.default_window_icon() {
                        TrayIconBuilder::new().icon(icon.clone())
                    } else {
                        TrayIconBuilder::new()
                    }
                }
            };

            tray_builder
                .menu(&menu)
                .show_menu_on_left_click(true)
                .on_menu_event(move |app, event| match event.id().as_ref() {
                    "open" => {
                        if let Some(window) = app.get_webview_window("main") {
                            let _ = window.show();
                            let _ = window.set_focus();
                        }
                    }
                    "restart" => {
                        let state = Arc::clone(&tray_state);
                        let app = app.clone();
                        std::thread::spawn(move || {
                            kill_backend(&app, &state);
                            wait_for_backend_exit(&state, Duration::from_secs(5));
                            spawn_backend(&app, &state);
                        });
                    }
                    "quit" => {
                        app.exit(0);
                    }
                    _ => {}
                })
                .build(app)?;

            // Spawn backend on startup
            spawn_backend(&app_handle, &state_for_setup);

            Ok(())
        })
        .on_window_event(|window, event| {
            // Close hides instead of destroying
            if let WindowEvent::CloseRequested { api, .. } = event {
                api.prevent_close();
                let _ = window.hide();
            }
        })
        .build(tauri::generate_context!())
        .expect("error while building tauri application")
        .run(move |app, event| match event {
            RunEvent::Reopen { .. } => {
                if let Some(w) = app.get_webview_window("main") {
                    let _ = w.show();
                    let _ = w.set_focus();
                }
            }
            RunEvent::ExitRequested { .. } => {
                kill_backend(app, &state_for_exit);
            }
            _ => {}
        });
}
