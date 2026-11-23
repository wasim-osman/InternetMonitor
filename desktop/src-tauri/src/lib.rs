//! Internet Monitor desktop (Windows / Linux; also builds on macOS for development).

pub mod agent;
pub mod discovery;
pub mod icmp;
pub mod ssh;
pub mod store;
pub mod tray;
pub mod updater;
pub mod util;

use serde::Serialize;
use serde_json::Value;
use std::path::PathBuf;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Mutex;
use std::time::Duration;
use store::{SavedNode, Settings};
use tauri::{AppHandle, Emitter, Manager, State};
use tauri_plugin_autostart::ManagerExt as _;
use tauri_plugin_dialog::DialogExt as _;
use tauri_plugin_notification::NotificationExt as _;
use tauri_plugin_opener::OpenerExt as _;

pub struct AppState {
    data_dir: PathBuf,
    settings: Mutex<Settings>,
    scanning: AtomicBool,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct AppInfo {
    version: String,
    platform: String,
    data_dir: String,
    selftest: bool,
    selftest_agent: Option<String>,
}

#[tauri::command]
fn app_info(state: State<'_, AppState>) -> AppInfo {
    AppInfo {
        version: env!("CARGO_PKG_VERSION").into(),
        platform: std::env::consts::OS.into(),
        data_dir: state.data_dir.display().to_string(),
        selftest: std::env::var_os("IM_SELFTEST").is_some(),
        selftest_agent: std::env::var("IM_SELFTEST_AGENT").ok(),
    }
}

#[tauri::command]
fn nodes_load(state: State<'_, AppState>) -> Vec<SavedNode> {
    store::load_nodes(&state.data_dir)
}

#[tauri::command]
fn nodes_save(state: State<'_, AppState>, nodes: Vec<SavedNode>) -> Result<(), String> {
    store::save_nodes(&state.data_dir, &nodes).map_err(|e| e.to_string())
}

#[tauri::command]
fn settings_get(state: State<'_, AppState>) -> Settings {
    state.settings.lock().map(|s| s.clone()).unwrap_or_default()
}

#[tauri::command]
fn settings_set(app: AppHandle, state: State<'_, AppState>, settings: Settings) -> Result<(), String> {
    tray::set_visible(&app, settings.tray);
    store::save_settings(&state.data_dir, &settings).map_err(|e| e.to_string())?;
    if let Ok(mut s) = state.settings.lock() {
        *s = settings;
    }
    Ok(())
}

#[tauri::command]
fn autostart_get(app: AppHandle) -> bool {
    app.autolaunch().is_enabled().unwrap_or(false)
}

#[tauri::command]
fn autostart_set(app: AppHandle, enabled: bool) -> Result<bool, String> {
    let al = app.autolaunch();
    if enabled { al.enable() } else { al.disable() }.map_err(|e| e.to_string())?;
    Ok(al.is_enabled().unwrap_or(false))
}

#[tauri::command]
#[allow(clippy::too_many_arguments)]
async fn agent_request(
    host: String,
    port: u16,
    token: Option<String>,
    method: String,
    path: String,
    body: Option<Value>,
    timeout_ms: Option<u64>,
) -> Result<Value, agent::AgentError> {
    let t = Duration::from_millis(timeout_ms.unwrap_or(6000).clamp(200, 120_000));
    agent::request(&host, port, token.as_deref(), &method, &path, body.as_ref(), t).await
}

#[tauri::command]
async fn probe_agent(host: String, port: Option<u16>, timeout_ms: Option<u64>) -> Option<agent::Identity> {
    agent::probe_identity(&host, port.unwrap_or(agent::AGENT_PORT), Duration::from_millis(timeout_ms.unwrap_or(2500))).await
}

struct ScanGuard<'a>(&'a AtomicBool);
impl Drop for ScanGuard<'_> {
    fn drop(&mut self) {
        self.0.store(false, Ordering::SeqCst);
    }
}

#[tauri::command]
async fn discover_start(app: AppHandle, state: State<'_, AppState>) -> Result<Vec<discovery::Candidate>, String> {
    if state.scanning.swap(true, Ordering::SeqCst) {
        return Err("A scan is already running".into());
    }
    let _g = ScanGuard(&state.scanning);
    let (a1, a2) = (app.clone(), app.clone());
    let res = discovery::scan(
        move |p| {
            let _ = a1.emit("discovery:progress", p);
        },
        move |list| {
            let _ = a2.emit("discovery:update", list);
        },
    )
    .await;
    Ok(res)
}

#[tauri::command]
async fn probe_host(host: String) -> Result<discovery::Candidate, String> {
    discovery::probe_host(&host).await
}

#[tauri::command]
fn local_ips() -> Vec<String> {
    discovery::local_interfaces().into_iter().map(|i| i.ip).collect()
}

#[tauri::command]
fn client_name() -> String {
    util::client_name()
}

#[tauri::command]
fn ssh_guess_users(saved: Option<String>) -> Vec<String> {
    ssh::guess_users(saved.as_deref())
}

#[tauri::command]
async fn ssh_probe_login(host: String, users: Vec<String>) -> Option<String> {
    ssh::probe_login(&host, &users).await
}

#[tauri::command]
async fn ssh_fetch_pair_code(user: String, host: String) -> Result<String, String> {
    ssh::fetch_pair_code(&user, &host).await
}

fn agent_dirs(app: &AppHandle) -> Vec<PathBuf> {
    let mut v = vec![];
    if let Some(d) = std::env::var_os("IM_AGENTS_DIR") {
        v.push(PathBuf::from(d));
    }
    if let Ok(r) = app.path().resource_dir() {
        v.push(r.join("agents"));
        v.push(r.join("resources").join("agents"));
    }
    if cfg!(debug_assertions) {
        let m = PathBuf::from(env!("CARGO_MANIFEST_DIR"));
        v.push(m.join("resources").join("agents"));
        v.push(m.join("..").join("..").join("agent").join("dist"));
    }
    v
}

#[derive(Serialize, Clone)]
struct SshLog {
    host: String,
    line: String,
}

#[tauri::command]
async fn ssh_install_agent(app: AppHandle, user: String, host: String) -> Result<(), String> {
    let dirs = agent_dirs(&app);
    let (a, h) = (app.clone(), host.clone());
    let log = move |line: String| {
        let _ = a.emit("ssh:log", SshLog { host: h.clone(), line });
    };
    let r = ssh::install(&user, &host, &dirs, &log).await;
    if r.is_err() {
        log(format!("Manual alternative: {}", ssh::manual_install_command(&host, &user)));
    }
    r
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct RemoveResult {
    error: Option<String>,
    leftovers: Vec<String>,
    user: Option<String>,
}

/// Uninstalls the agent (and all its files) over SSH, then independently checks :7878 is closed.
#[tauri::command]
async fn remove_agent(host: String, ssh_user: Option<String>) -> RemoveResult {
    let users = ssh::guess_users(ssh_user.as_deref());
    let Some(user) = ssh::probe_login(&host, &users).await else {
        return RemoveResult {
            error: Some(format!(
                "SSH login to {host} failed (tried {}), so the agent could not be removed. Manual: ssh pi@{host} 'sudo imonitord uninstall --purge'",
                users.join(", ")
            )),
            leftovers: vec![],
            user: None,
        };
    };
    match ssh::uninstall(&user, &host).await {
        Err(e) => RemoveResult { error: Some(e), leftovers: vec![], user: Some(user) },
        Ok(left) => {
            if agent::probe_identity(&host, agent::AGENT_PORT, Duration::from_secs(2)).await.is_some() {
                return RemoveResult { error: Some(format!("Agent still answers on {host}:7878 after removal.")), leftovers: left, user: Some(user) };
            }
            RemoveResult { error: None, leftovers: left, user: Some(user) }
        }
    }
}

#[tauri::command]
fn notify(app: AppHandle, state: State<'_, AppState>, title: String, body: String) {
    let enabled = state.settings.lock().map(|s| s.notifications).unwrap_or(true);
    if enabled {
        let _ = app.notification().builder().title(title).body(body).show();
    }
}

#[tauri::command]
fn tray_update(app: AppHandle, level: String, tooltip: String, lines: Vec<String>) -> Result<(), String> {
    tray::update(&app, &level, &tooltip, &lines).map_err(|e| e.to_string())
}

#[tauri::command]
fn open_url(app: AppHandle, url: String) -> Result<(), String> {
    if !(url.starts_with("https://") || url.starts_with("http://")) {
        return Err("only http(s) URLs can be opened".into());
    }
    app.opener().open_url(url, None::<&str>).map_err(|e| e.to_string())
}

async fn save_dialog(app: &AppHandle, name: String, data: Vec<u8>) -> Result<Option<String>, String> {
    let app = app.clone();
    tauri::async_runtime::spawn_blocking(move || {
        let Some(fp) = app.dialog().file().set_file_name(&name).blocking_save_file() else { return Ok(None) };
        let path = fp.into_path().map_err(|e| e.to_string())?;
        std::fs::write(&path, &data).map_err(|e| e.to_string())?;
        Ok(Some(path.display().to_string()))
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
async fn save_text_file(app: AppHandle, name: String, contents: String) -> Result<Option<String>, String> {
    save_dialog(&app, name, contents.into_bytes()).await
}

#[tauri::command]
async fn save_base64_file(app: AppHandle, name: String, data: String) -> Result<Option<String>, String> {
    use base64::Engine as _;
    let b64 = data.split_once(',').map(|(_, d)| d).unwrap_or(&data);
    let bytes = base64::engine::general_purpose::STANDARD.decode(b64.trim()).map_err(|e| e.to_string())?;
    save_dialog(&app, name, bytes).await
}

#[tauri::command]
async fn update_check() -> Result<updater::UpdateInfo, String> {
    updater::check(env!("CARGO_PKG_VERSION")).await
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct InstallOutcome {
    /// "launched" (installer started, app exits), "restart" (AppImage replaced), "opened" (release page)
    status: String,
    message: String,
}

#[tauri::command]
async fn update_install(app: AppHandle, info: updater::UpdateInfo) -> Result<InstallOutcome, String> {
    let open_page = |msg: &str| -> Result<InstallOutcome, String> {
        app.opener().open_url(info.html_url.clone(), None::<&str>).map_err(|e| e.to_string())?;
        Ok(InstallOutcome { status: "opened".into(), message: msg.into() })
    };
    match info.mode.as_str() {
        "installer" => {
            let data = updater::fetch_verified(&info).await?;
            let name = info.asset.as_ref().map(|a| a.name.clone()).unwrap_or_else(|| "InternetMonitor-setup.exe".into());
            let dir = std::env::temp_dir().join("InternetMonitor-update");
            std::fs::create_dir_all(&dir).map_err(|e| e.to_string())?;
            let path = dir.join(name);
            std::fs::write(&path, &data).map_err(|e| e.to_string())?;
            let is_msi = path.extension().is_some_and(|e| e.eq_ignore_ascii_case("msi"));
            let spawn = if is_msi {
                std::process::Command::new("msiexec").arg("/i").arg(&path).spawn()
            } else {
                std::process::Command::new(&path).spawn()
            };
            spawn.map_err(|e| format!("could not start installer: {e}"))?;
            let a = app.clone();
            tauri::async_runtime::spawn(async move {
                tokio::time::sleep(Duration::from_millis(800)).await;
                a.exit(0);
            });
            Ok(InstallOutcome { status: "launched".into(), message: format!("Installer for {} started", info.latest) })
        }
        "appimage" => {
            let target = std::env::var_os("APPIMAGE").map(PathBuf::from).ok_or("APPIMAGE is not set")?;
            let data = updater::fetch_verified(&info).await?;
            updater::replace_appimage(&target, &data)?;
            Ok(InstallOutcome { status: "restart".into(), message: format!("Updated to {} — restart to finish", info.latest) })
        }
        _ => open_page("Opened the release page — download and install the new version from there."),
    }
}

#[tauri::command]
fn restart_app(app: AppHandle) {
    if let Some(p) = std::env::var_os("APPIMAGE") {
        if std::process::Command::new(p).spawn().is_ok() {
            app.exit(0);
            return;
        }
    }
    app.restart();
}

#[tauri::command]
fn selftest_report(app: AppHandle, report: String) {
    println!("SELFTEST {report}");
    if std::env::var_os("IM_SELFTEST_EXIT").is_some() {
        app.exit(0);
    }
}

#[tauri::command]
fn quit_app(app: AppHandle) {
    app.exit(0);
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    let minimized = std::env::args().any(|a| a == "--minimized");
    tauri::Builder::default()
        .plugin(tauri_plugin_single_instance::init(|app, _args, _cwd| tray::show_main(app)))
        .plugin(tauri_plugin_notification::init())
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_autostart::init(tauri_plugin_autostart::MacosLauncher::LaunchAgent, Some(vec!["--minimized"])))
        .setup(move |app| {
            let data_dir = match store::data_dir_override() {
                Some(d) => d,
                None => app.path().app_data_dir()?,
            };
            std::fs::create_dir_all(&data_dir)?;
            let settings = store::load_settings(&data_dir);
            let tray_on = settings.tray;
            app.manage(AppState { data_dir, settings: Mutex::new(settings), scanning: AtomicBool::new(false) });
            if let Err(e) = tray::create(app.handle(), tray_on) {
                eprintln!("tray unavailable: {e}");
            }
            if !(minimized && tray_on) {
                tray::show_main(app.handle());
            }
            Ok(())
        })
        .on_window_event(|window, event| {
            if let tauri::WindowEvent::CloseRequested { api, .. } = event {
                let keep = window.app_handle().state::<AppState>().settings.lock().map(|s| s.tray).unwrap_or(true);
                if keep {
                    api.prevent_close();
                    let _ = window.hide();
                } else {
                    window.app_handle().exit(0);
                }
            }
        })
        .invoke_handler(tauri::generate_handler![
            app_info,
            nodes_load,
            nodes_save,
            settings_get,
            settings_set,
            autostart_get,
            autostart_set,
            agent_request,
            probe_agent,
            discover_start,
            probe_host,
            local_ips,
            client_name,
            ssh_guess_users,
            ssh_probe_login,
            ssh_fetch_pair_code,
            ssh_install_agent,
            remove_agent,
            notify,
            tray_update,
            open_url,
            save_text_file,
            save_base64_file,
            update_check,
            update_install,
            restart_app,
            selftest_report,
            quit_app,
        ])
        .run(tauri::generate_context!())
        .expect("error while running Internet Monitor");
}
