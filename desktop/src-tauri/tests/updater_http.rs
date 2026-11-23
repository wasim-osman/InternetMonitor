//! Updater end-to-end against a local fake "GitHub releases" HTTP server (loopback only).

use internet_monitor_lib::updater::{self, Platform};
use std::collections::HashMap;
use std::sync::Arc;
use tokio::io::{AsyncReadExt, AsyncWriteExt};

async fn serve(routes: HashMap<String, Vec<u8>>) -> String {
    let l = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let base = format!("http://{}", l.local_addr().unwrap());
    let routes = Arc::new(routes);
    tokio::spawn(async move {
        loop {
            let Ok((mut s, _)) = l.accept().await else { return };
            let routes = routes.clone();
            tokio::spawn(async move {
                let mut buf = vec![0u8; 8192];
                let n = s.read(&mut buf).await.unwrap_or(0);
                let req = String::from_utf8_lossy(&buf[..n]).to_string();
                let path = req.split_whitespace().nth(1).unwrap_or("/").to_string();
                let (status, body) = match routes.get(&path) {
                    Some(b) => ("200 OK", b.clone()),
                    None => ("404 Not Found", b"{}".to_vec()),
                };
                let head = format!("HTTP/1.1 {status}\r\nContent-Length: {}\r\nConnection: close\r\n\r\n", body.len());
                let _ = s.write_all(head.as_bytes()).await;
                let _ = s.write_all(&body).await;
            });
        }
    });
    base
}

#[tokio::test]
async fn check_download_verify_and_swap() {
    let asset = b"fake AppImage payload".to_vec();
    let exe = b"fake installer".to_vec();
    let good = updater::sha256_hex(&asset);
    let mut routes = HashMap::new();
    routes.insert("/dl/InternetMonitor_9.9.9_amd64.AppImage".to_string(), asset.clone());
    routes.insert("/dl/InternetMonitor_9.9.9_x64-setup.exe".to_string(), exe.clone());
    // the .exe checksum is deliberately wrong
    routes.insert(
        "/dl/SHA256SUMS".to_string(),
        format!("{good}  InternetMonitor_9.9.9_amd64.AppImage\n{}  InternetMonitor_9.9.9_x64-setup.exe\n", "0".repeat(64)).into_bytes(),
    );
    let base = serve(routes.clone()).await;
    let release = serde_json::json!({
        "tag_name": "v9.9.9", "html_url": format!("{base}/release"), "body": "notes", "draft": false, "prerelease": false,
        "assets": [
            {"name": "InternetMonitor_9.9.9_amd64.AppImage", "browser_download_url": format!("{base}/dl/InternetMonitor_9.9.9_amd64.AppImage"), "size": asset.len()},
            {"name": "InternetMonitor_9.9.9_x64-setup.exe", "browser_download_url": format!("{base}/dl/InternetMonitor_9.9.9_x64-setup.exe"), "size": exe.len()},
            {"name": "SHA256SUMS", "browser_download_url": format!("{base}/dl/SHA256SUMS"), "size": 10}
        ]
    });
    routes.insert("/latest".to_string(), serde_json::to_vec(&release).unwrap());
    let base2 = serve(routes).await;
    std::env::set_var("IM_UPDATE_URL", format!("{base2}/latest"));

    // check(): real HTTP + JSON decode; on this host the platform decides the mode
    let info = updater::check("1.0.0").await.expect("check");
    assert!(info.available);
    assert_eq!(info.latest, "9.9.9");
    assert!(!updater::check("9.9.9").await.unwrap().available);

    // AppImage path: download + verify + in-place replacement
    let rel: updater::GhRelease = serde_json::from_value(release).unwrap();
    let ai = updater::build_info(&rel, "1.0.0", Platform::LinuxAppImage);
    assert_eq!(ai.mode, "appimage");
    let data = updater::fetch_verified(&ai).await.expect("verified download");
    assert_eq!(data, asset);
    let dir = std::env::temp_dir().join(format!("im-upd-http-{}", std::process::id()));
    std::fs::create_dir_all(&dir).unwrap();
    let target = dir.join("InternetMonitor.AppImage");
    std::fs::write(&target, b"old").unwrap();
    updater::replace_appimage(&target, &data).unwrap();
    assert_eq!(std::fs::read(&target).unwrap(), asset);
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        assert_eq!(std::fs::metadata(&target).unwrap().permissions().mode() & 0o777, 0o755);
    }
    std::fs::remove_dir_all(&dir).ok();

    // Windows path: checksum mismatch must be refused
    let win = updater::build_info(&rel, "1.0.0", Platform::Windows);
    assert_eq!(win.mode, "installer");
    let err = updater::fetch_verified(&win).await.unwrap_err();
    assert!(err.contains("checksum mismatch"), "{err}");
}
