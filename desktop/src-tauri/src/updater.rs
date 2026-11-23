//! Unsigned self-updater driven by GitHub Releases.
//!
//! GET /repos/<owner>/<repo>/releases/latest → compare semver with the running version → pick the
//! platform asset (predictable names, see desktop/README.md) → download → verify SHA-256 against the
//! release's `SHA256SUMS` asset → hand over:
//! * Windows: launch the NSIS installer and exit (installer replaces the app);
//! * Linux AppImage (`$APPIMAGE` set): atomically replace the AppImage in place, then "restart to update";
//! * anything else (deb, macOS dev builds): open the release page.

use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::cmp::Ordering;
use std::collections::HashMap;
use std::path::{Path, PathBuf};
use std::time::Duration;

pub const REPO: &str = "wasim-osman/InternetMonitor";
pub const RELEASES_PAGE: &str = "https://github.com/wasim-osman/InternetMonitor/releases";

#[derive(Debug, Clone, Deserialize)]
pub struct GhAsset {
    pub name: String,
    pub browser_download_url: String,
    #[serde(default)]
    pub size: u64,
}

#[derive(Debug, Clone, Deserialize)]
pub struct GhRelease {
    pub tag_name: String,
    #[serde(default)]
    pub html_url: String,
    #[serde(default)]
    pub body: Option<String>,
    #[serde(default)]
    pub draft: bool,
    #[serde(default)]
    pub prerelease: bool,
    #[serde(default)]
    pub assets: Vec<GhAsset>,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct UpdateAsset {
    pub name: String,
    pub url: String,
    pub size: u64,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct UpdateInfo {
    pub current: String,
    pub latest: String,
    pub available: bool,
    pub html_url: String,
    pub notes: String,
    pub asset: Option<UpdateAsset>,
    pub sums_url: Option<String>,
    /// "installer" (Windows), "appimage" (Linux AppImage), "page" (open release page)
    pub mode: String,
}

#[derive(Debug, Clone, Copy, PartialEq)]
pub enum Platform {
    Windows,
    LinuxAppImage,
    LinuxOther,
    Other,
}

pub fn current_platform() -> Platform {
    if cfg!(windows) {
        Platform::Windows
    } else if cfg!(target_os = "linux") {
        if std::env::var_os("APPIMAGE").is_some() { Platform::LinuxAppImage } else { Platform::LinuxOther }
    } else {
        Platform::Other
    }
}

/// Parses "v1.2.3", "1.2", "1.2.3-beta.1" → ([1,2,3], pre-release?).
pub fn parse_version(s: &str) -> Option<(Vec<u64>, Option<String>)> {
    let s = s.trim().trim_start_matches(['v', 'V']);
    let (core, pre) = match s.split_once('-') {
        Some((c, p)) => (c, Some(p.to_string())),
        None => (s, None),
    };
    let core = core.split('+').next()?;
    let mut nums: Vec<u64> = core.split('.').map(|p| p.parse().ok()).collect::<Option<_>>()?;
    if nums.is_empty() {
        return None;
    }
    nums.resize(3, 0);
    Some((nums, pre))
}

pub fn compare_versions(a: &str, b: &str) -> Option<Ordering> {
    let (an, ap) = parse_version(a)?;
    let (bn, bp) = parse_version(b)?;
    Some(match an.cmp(&bn) {
        Ordering::Equal => match (ap, bp) {
            (None, None) => Ordering::Equal,
            (None, Some(_)) => Ordering::Greater, // release > pre-release
            (Some(_), None) => Ordering::Less,
            (Some(x), Some(y)) => x.cmp(&y),
        },
        o => o,
    })
}

pub fn is_newer(latest: &str, current: &str) -> bool {
    compare_versions(latest, current) == Some(Ordering::Greater)
}

/// Expected asset names for a version (what CI uploads).
pub fn expected_asset_name(platform: Platform, version: &str) -> Option<String> {
    match platform {
        Platform::Windows => Some(format!("InternetMonitor_{version}_x64-setup.exe")),
        Platform::LinuxAppImage => Some(format!("InternetMonitor_{version}_amd64.AppImage")),
        _ => None,
    }
}

/// Picks the asset for this platform: exact predictable name first, then a lenient suffix match.
pub fn pick_asset<'a>(assets: &'a [GhAsset], platform: Platform, version: &str) -> Option<&'a GhAsset> {
    let exact = expected_asset_name(platform, version)?;
    if let Some(a) = assets.iter().find(|a| a.name == exact) {
        return Some(a);
    }
    let suffixes: &[&str] = match platform {
        Platform::Windows => &["_x64-setup.exe", "-setup.exe", "_x64_en-US.msi", ".msi"],
        Platform::LinuxAppImage => &["_amd64.AppImage", "_x86_64.AppImage", ".AppImage"],
        _ => &[],
    };
    suffixes.iter().find_map(|s| assets.iter().find(|a| a.name.ends_with(s) && a.name.starts_with("InternetMonitor")))
}

/// Parses `sha256sum` output: "<hex>  <name>" or "<hex> *<name>".
pub fn parse_sha256sums(text: &str) -> HashMap<String, String> {
    let mut m = HashMap::new();
    for line in text.lines() {
        let mut it = line.split_whitespace();
        let (Some(hash), Some(name)) = (it.next(), it.next()) else { continue };
        if hash.len() == 64 && hash.chars().all(|c| c.is_ascii_hexdigit()) {
            let name = name.trim_start_matches('*');
            let name = name.rsplit(['/', '\\']).next().unwrap_or(name);
            m.insert(name.to_string(), hash.to_ascii_lowercase());
        }
    }
    m
}

pub fn sha256_hex(data: &[u8]) -> String {
    let d = Sha256::digest(data);
    d.iter().map(|b| format!("{b:02x}")).collect()
}

pub fn build_info(rel: &GhRelease, current: &str, platform: Platform) -> UpdateInfo {
    let latest = rel.tag_name.trim_start_matches(['v', 'V']).to_string();
    let available = !rel.draft && !rel.prerelease && is_newer(&latest, current);
    let asset = pick_asset(&rel.assets, platform, &latest)
        .map(|a| UpdateAsset { name: a.name.clone(), url: a.browser_download_url.clone(), size: a.size });
    let sums_url = rel.assets.iter().find(|a| a.name == "SHA256SUMS" || a.name == "SHA256SUMS.txt").map(|a| a.browser_download_url.clone());
    let mode = match (platform, &asset, &sums_url) {
        (Platform::Windows, Some(_), Some(_)) => "installer",
        (Platform::LinuxAppImage, Some(_), Some(_)) => "appimage",
        _ => "page",
    };
    UpdateInfo {
        current: current.to_string(),
        latest,
        available,
        html_url: if rel.html_url.is_empty() { RELEASES_PAGE.to_string() } else { rel.html_url.clone() },
        notes: rel.body.clone().unwrap_or_default(),
        asset,
        sums_url,
        mode: mode.into(),
    }
}

fn http() -> &'static reqwest::Client {
    crate::agent::client()
}

pub async fn check(current: &str) -> Result<UpdateInfo, String> {
    let url = std::env::var("IM_UPDATE_URL").unwrap_or_else(|_| format!("https://api.github.com/repos/{REPO}/releases/latest"));
    let resp = http()
        .get(&url)
        .header("Accept", "application/vnd.github+json")
        .timeout(Duration::from_secs(20))
        .send()
        .await
        .map_err(|e| format!("update check failed: {e}"))?;
    if resp.status().as_u16() == 404 {
        return Err("No published releases yet".into());
    }
    if !resp.status().is_success() {
        return Err(format!("update check failed: HTTP {}", resp.status().as_u16()));
    }
    let rel: GhRelease = resp.json().await.map_err(|e| format!("bad release JSON: {e}"))?;
    Ok(build_info(&rel, current, current_platform()))
}

async fn download(url: &str, max: u64) -> Result<Vec<u8>, String> {
    let resp = http()
        .get(url)
        .timeout(Duration::from_secs(600))
        .send()
        .await
        .map_err(|e| format!("download failed: {e}"))?;
    if !resp.status().is_success() {
        return Err(format!("download failed: HTTP {}", resp.status().as_u16()));
    }
    let bytes = resp.bytes().await.map_err(|e| format!("download failed: {e}"))?;
    if bytes.len() as u64 > max {
        return Err("download too large".into());
    }
    Ok(bytes.to_vec())
}

/// Downloads the asset and verifies its checksum. Returns the verified bytes.
pub async fn fetch_verified(info: &UpdateInfo) -> Result<Vec<u8>, String> {
    let asset = info.asset.as_ref().ok_or("no installable asset for this platform")?;
    let sums_url = info.sums_url.as_ref().ok_or("release has no SHA256SUMS — refusing to install unverified update")?;
    let sums = String::from_utf8_lossy(&download(sums_url, 1 << 20).await?).into_owned();
    let expected = parse_sha256sums(&sums).get(&asset.name).cloned().ok_or_else(|| format!("{} is not listed in SHA256SUMS", asset.name))?;
    let data = download(&asset.url, 1 << 30).await?;
    let actual = sha256_hex(&data);
    if actual != expected {
        return Err(format!("checksum mismatch for {} (expected {expected}, got {actual})", asset.name));
    }
    Ok(data)
}

/// Writes the verified AppImage next to `$APPIMAGE` and renames it over the running file.
pub fn replace_appimage(target: &Path, data: &[u8]) -> Result<(), String> {
    let tmp: PathBuf = target.with_extension("AppImage.new");
    std::fs::write(&tmp, data).map_err(|e| format!("cannot write {}: {e}", tmp.display()))?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        std::fs::set_permissions(&tmp, std::fs::Permissions::from_mode(0o755)).map_err(|e| e.to_string())?;
    }
    std::fs::rename(&tmp, target).map_err(|e| {
        let _ = std::fs::remove_file(&tmp);
        format!("cannot replace {}: {e}", target.display())
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn asset(n: &str) -> GhAsset {
        GhAsset { name: n.into(), browser_download_url: format!("https://x/{n}"), size: 1 }
    }

    #[test]
    fn versions() {
        assert!(is_newer("1.0.1", "1.0.0"));
        assert!(is_newer("v1.1", "1.0.9"));
        assert!(is_newer("2.0.0", "1.99.99"));
        assert!(!is_newer("1.0.0", "1.0.0"));
        assert!(!is_newer("0.9.0", "1.0.0"));
        assert!(is_newer("1.0.0", "1.0.0-beta.2"));
        assert!(!is_newer("1.0.0-rc.1", "1.0.0"));
        assert!(!is_newer("garbage", "1.0.0"));
        assert_eq!(compare_versions("1.10.0", "1.9.0"), Some(Ordering::Greater));
    }

    #[test]
    fn asset_selection() {
        let assets = vec![
            asset("InternetMonitor_1.2.0_amd64.deb"),
            asset("InternetMonitor_1.2.0_amd64.AppImage"),
            asset("InternetMonitor_1.2.0_x64-setup.exe"),
            asset("SHA256SUMS"),
        ];
        assert_eq!(pick_asset(&assets, Platform::Windows, "1.2.0").unwrap().name, "InternetMonitor_1.2.0_x64-setup.exe");
        assert_eq!(pick_asset(&assets, Platform::LinuxAppImage, "1.2.0").unwrap().name, "InternetMonitor_1.2.0_amd64.AppImage");
        assert!(pick_asset(&assets, Platform::LinuxOther, "1.2.0").is_none());
        // lenient fallback to an .msi
        let msi = vec![asset("InternetMonitor_1.2.0_x64_en-US.msi")];
        assert_eq!(pick_asset(&msi, Platform::Windows, "1.2.0").unwrap().name, "InternetMonitor_1.2.0_x64_en-US.msi");
    }

    #[test]
    fn sums() {
        let h = "a".repeat(64);
        let txt = format!("{h}  InternetMonitor_1.2.0_x64-setup.exe\n{}  *dist/InternetMonitor_1.2.0_amd64.AppImage\nnot a line\n", "B".repeat(64));
        let m = parse_sha256sums(&txt);
        assert_eq!(m.get("InternetMonitor_1.2.0_x64-setup.exe"), Some(&h));
        assert_eq!(m.get("InternetMonitor_1.2.0_amd64.AppImage"), Some(&"b".repeat(64)));
        assert_eq!(m.len(), 2);
        assert_eq!(sha256_hex(b"abc"), "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad");
    }

    #[test]
    fn info_modes() {
        let rel = GhRelease {
            tag_name: "v1.2.0".into(),
            html_url: "https://github.com/x/releases/tag/v1.2.0".into(),
            body: Some("notes".into()),
            draft: false,
            prerelease: false,
            assets: vec![asset("InternetMonitor_1.2.0_x64-setup.exe"), asset("InternetMonitor_1.2.0_amd64.AppImage"), asset("SHA256SUMS")],
        };
        let w = build_info(&rel, "1.0.0", Platform::Windows);
        assert!(w.available);
        assert_eq!(w.latest, "1.2.0");
        assert_eq!(w.mode, "installer");
        assert_eq!(build_info(&rel, "1.0.0", Platform::LinuxAppImage).mode, "appimage");
        assert_eq!(build_info(&rel, "1.0.0", Platform::LinuxOther).mode, "page");
        assert!(!build_info(&rel, "1.2.0", Platform::Windows).available);
        let mut no_sums = rel.clone();
        no_sums.assets.retain(|a| a.name != "SHA256SUMS");
        assert_eq!(build_info(&no_sums, "1.0.0", Platform::Windows).mode, "page", "never auto-install unverifiable assets");
    }

    #[test]
    fn appimage_replace() {
        let d = std::env::temp_dir().join(format!("im-upd-{}", std::process::id()));
        std::fs::create_dir_all(&d).unwrap();
        let t = d.join("InternetMonitor.AppImage");
        std::fs::write(&t, b"old").unwrap();
        replace_appimage(&t, b"new").unwrap();
        assert_eq!(std::fs::read(&t).unwrap(), b"new");
        assert!(!d.join("InternetMonitor.AppImage.new").exists());
        std::fs::remove_dir_all(&d).ok();
    }
}
