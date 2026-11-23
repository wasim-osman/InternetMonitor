//! Persistent files in the OS app-data dir: nodes.json (agent tokens, 0600 on unix) and settings.json.

use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::io::Write;
use std::path::{Path, PathBuf};

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct SavedNode {
    pub node_id: String,
    pub name: String,
    pub host: String,
    #[serde(default = "default_port")]
    pub port: u16,
    pub token: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub ssh_user: Option<String>,
    /// ISO-8601 (same format as the macOS app).
    #[serde(default)]
    pub added_at: String,
}

fn default_port() -> u16 {
    crate::agent::AGENT_PORT
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase", default)]
pub struct Settings {
    pub notifications: bool,
    pub tray: bool,
    pub auto_update: bool,
    /// Services page selection (shape owned by the frontend: src/services.ts `ServiceSettings`).
    #[serde(skip_serializing_if = "Value::is_null")]
    pub services: Value,
}

impl Default for Settings {
    fn default() -> Self {
        Settings { notifications: true, tray: true, auto_update: true, services: Value::Null }
    }
}

/// Atomic write (tmp + rename) with owner-only permissions on unix.
pub fn write_private(path: &Path, data: &[u8]) -> std::io::Result<()> {
    if let Some(d) = path.parent() {
        std::fs::create_dir_all(d)?;
    }
    let tmp = path.with_extension("tmp");
    {
        let mut opts = std::fs::OpenOptions::new();
        opts.write(true).create(true).truncate(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            opts.mode(0o600);
        }
        let mut f = opts.open(&tmp)?;
        f.write_all(data)?;
        f.sync_all()?;
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        std::fs::set_permissions(&tmp, std::fs::Permissions::from_mode(0o600))?;
    }
    std::fs::rename(&tmp, path)
}

pub fn load_nodes(dir: &Path) -> Vec<SavedNode> {
    std::fs::read(dir.join("nodes.json"))
        .ok()
        .and_then(|d| serde_json::from_slice(&d).ok())
        .unwrap_or_default()
}

pub fn save_nodes(dir: &Path, nodes: &[SavedNode]) -> std::io::Result<()> {
    let d = serde_json::to_vec_pretty(nodes).map_err(std::io::Error::other)?;
    write_private(&dir.join("nodes.json"), &d)
}

pub fn load_settings(dir: &Path) -> Settings {
    std::fs::read(dir.join("settings.json")).ok().and_then(|d| serde_json::from_slice(&d).ok()).unwrap_or_default()
}

pub fn save_settings(dir: &Path, s: &Settings) -> std::io::Result<()> {
    let d = serde_json::to_vec_pretty(s).map_err(std::io::Error::other)?;
    write_private(&dir.join("settings.json"), &d)
}

/// `IM_DATA_DIR` overrides the OS app-data dir (used for tests / self-test runs).
pub fn data_dir_override() -> Option<PathBuf> {
    std::env::var_os("IM_DATA_DIR").map(PathBuf::from)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn roundtrip_and_permissions() {
        let d = std::env::temp_dir().join(format!("im-store-test-{}", std::process::id()));
        let n = SavedNode {
            node_id: "abc".into(),
            name: "Pi".into(),
            host: "10.0.0.2".into(),
            port: 7878,
            token: "t".repeat(64),
            ssh_user: Some("pi".into()),
            added_at: "2026-11-23T00:00:00Z".into(),
        };
        save_nodes(&d, std::slice::from_ref(&n)).unwrap();
        assert_eq!(load_nodes(&d), vec![n]);
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            let m = std::fs::metadata(d.join("nodes.json")).unwrap().permissions().mode();
            assert_eq!(m & 0o777, 0o600);
        }
        // macOS-app format (no port / sshUser) still decodes
        std::fs::write(d.join("nodes.json"), r#"[{"nodeId":"x","name":"n","host":"h","token":"k","addedAt":"2026-01-01T00:00:00Z"}]"#).unwrap();
        assert_eq!(load_nodes(&d)[0].port, 7878);
        assert_eq!(load_settings(&d), Settings::default());
        // services selection round-trips opaquely; old settings files without it still load
        let st = Settings { services: serde_json::json!({"selected": ["github"], "desktop": true}), ..Settings::default() };
        save_settings(&d, &st).unwrap();
        assert_eq!(load_settings(&d), st);
        std::fs::write(d.join("settings.json"), r#"{"notifications":false,"tray":true,"autoUpdate":true}"#).unwrap();
        assert!(load_settings(&d).services.is_null());
        std::fs::remove_dir_all(&d).ok();
    }
}
