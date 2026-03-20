#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use std::{
  path::PathBuf,
  sync::Mutex,
};

use tauri::{AppHandle, Manager};
use tauri_plugin_shell::{process::CommandChild, ShellExt};

const API_ADDR: &str = "127.0.0.1:38673";

struct BackendState {
  child: Mutex<Option<CommandChild>>,
}

#[tauri::command]
fn backend_url() -> String {
  format!("http://{API_ADDR}")
}

fn repo_root() -> PathBuf {
  PathBuf::from(env!("CARGO_MANIFEST_DIR"))
    .join("../..")
    .canonicalize()
    .unwrap_or_else(|_| PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../.."))
}

fn backend_workdir(app: &AppHandle) -> Result<PathBuf, String> {
  if cfg!(debug_assertions) {
    return Ok(repo_root());
  }

  let dir = app
    .path()
    .app_data_dir()
    .map_err(|err| format!("failed to resolve app data dir: {err}"))?;
  std::fs::create_dir_all(&dir)
    .map_err(|err| format!("failed to create app data dir {}: {err}", dir.display()))?;
  Ok(dir)
}

fn spawn_backend(app: &AppHandle) -> Result<CommandChild, String> {
  let workdir = backend_workdir(app)?;
  let command = app
    .shell()
    .sidecar("syne-ui-api")
    .map_err(|err| format!("failed to configure sidecar: {err}"))?
    .args([
      "--addr",
      API_ADDR,
      "--workdir",
      workdir
        .to_str()
        .ok_or_else(|| "workdir contains invalid UTF-8".to_string())?,
    ]);
  let (_rx, child) = command
    .spawn()
    .map_err(|err| format!("failed to start sidecar: {err}"))?;
  Ok(child)
}

fn main() {
  tauri::Builder::default()
    .plugin(tauri_plugin_shell::init())
    .manage(BackendState {
      child: Mutex::new(None),
    })
    .invoke_handler(tauri::generate_handler![backend_url])
    .setup(|app| {
      let state = app.state::<BackendState>();
      let child = spawn_backend(app.handle())?;
      *state.child.lock().expect("backend child lock") = Some(child);
      Ok(())
    })
    .on_window_event(|window, event| {
      if let tauri::WindowEvent::Destroyed = event {
        let state = window.app_handle().state::<BackendState>();
        let child = {
          let mut guard = state.child.lock().expect("backend child lock");
          guard.take()
        };
        if let Some(child) = child {
          let _ = child.kill();
        }
      }
    })
    .run(tauri::generate_context!())
    .expect("error while running tauri application");
}
