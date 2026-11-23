//! System tray: status-coloured icon (worst state across nodes) and a small status menu.

use tauri::image::Image;
use tauri::menu::{Menu, MenuItem, PredefinedMenuItem};
use tauri::tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent};
use tauri::{AppHandle, Manager, Runtime};

pub const TRAY_ID: &str = "main-tray";

pub fn status_rgb(level: &str) -> [u8; 3] {
    match level {
        "up" => [0x39, 0xD9, 0x8A],
        "degraded" => [0xFF, 0xB0, 0x00],
        "down" => [0xFF, 0x4D, 0x4F],
        _ => [0x8A, 0x94, 0xA0],
    }
}

fn seg_dist(px: f32, py: f32, a: (f32, f32), b: (f32, f32)) -> f32 {
    let (dx, dy) = (b.0 - a.0, b.1 - a.1);
    let len2 = dx * dx + dy * dy;
    let t = if len2 == 0.0 { 0.0 } else { (((px - a.0) * dx + (py - a.1) * dy) / len2).clamp(0.0, 1.0) };
    let (cx, cy) = (a.0 + t * dx, a.1 + t * dy);
    ((px - cx).powi(2) + (py - cy).powi(2)).sqrt()
}

/// 32×32 RGBA: filled status disc with a dark ECG trace (the app's motif).
pub fn status_icon_rgba(level: &str) -> Vec<u8> {
    const N: usize = 32;
    let [r, g, b] = status_rgb(level);
    let ecg = [(4.0, 17.0), (11.0, 17.0), (13.5, 9.0), (17.0, 24.0), (19.5, 15.0), (21.0, 17.0), (28.0, 17.0)];
    let mut out = vec![0u8; N * N * 4];
    for y in 0..N {
        for x in 0..N {
            let (px, py) = (x as f32 + 0.5, y as f32 + 0.5);
            let d = ((px - 16.0).powi(2) + (py - 16.0).powi(2)).sqrt();
            let disc = (15.0 - d).clamp(0.0, 1.0);
            if disc <= 0.0 {
                continue;
            }
            let line = ecg.windows(2).map(|w| seg_dist(px, py, w[0], w[1])).fold(f32::MAX, f32::min);
            let ink = (2.1 - line).clamp(0.0, 1.0); // ~2px stroke, anti-aliased
            let mix = |c: u8, dark: u8| (f32::from(c) * (1.0 - ink) + f32::from(dark) * ink) as u8;
            let i = (y * N + x) * 4;
            out[i] = mix(r, 0x0D);
            out[i + 1] = mix(g, 0x0F);
            out[i + 2] = mix(b, 0x12);
            out[i + 3] = (disc * 255.0) as u8;
        }
    }
    out
}

pub fn status_image(level: &str) -> Image<'static> {
    Image::new_owned(status_icon_rgba(level), 32, 32)
}

fn build_menu<R: Runtime>(app: &AppHandle<R>, lines: &[String]) -> tauri::Result<Menu<R>> {
    let menu = Menu::new(app)?;
    menu.append(&MenuItem::with_id(app, "title", "INTERNET MONITOR", false, None::<&str>)?)?;
    if lines.is_empty() {
        menu.append(&MenuItem::with_id(app, "none", "No nodes configured", false, None::<&str>)?)?;
    }
    for (i, l) in lines.iter().take(20).enumerate() {
        menu.append(&MenuItem::with_id(app, format!("line-{i}"), l, false, None::<&str>)?)?;
    }
    menu.append(&PredefinedMenuItem::separator(app)?)?;
    menu.append(&MenuItem::with_id(app, "open", "Open Dashboard", true, None::<&str>)?)?;
    menu.append(&MenuItem::with_id(app, "quit", "Quit Internet Monitor", true, None::<&str>)?)?;
    Ok(menu)
}

pub fn show_main<R: Runtime>(app: &AppHandle<R>) {
    if let Some(w) = app.get_webview_window("main") {
        let _ = w.unminimize();
        let _ = w.show();
        let _ = w.set_focus();
    }
}

pub fn create<R: Runtime>(app: &AppHandle<R>, visible: bool) -> tauri::Result<()> {
    let menu = build_menu(app, &[])?;
    let tray = TrayIconBuilder::with_id(TRAY_ID)
        .icon(status_image("unknown"))
        .tooltip("Internet Monitor")
        .menu(&menu)
        .show_menu_on_left_click(false)
        .on_menu_event(|app, ev| match ev.id().as_ref() {
            "open" => show_main(app),
            "quit" => app.exit(0),
            _ => {}
        })
        .on_tray_icon_event(|tray, ev| {
            if let TrayIconEvent::Click { button: MouseButton::Left, button_state: MouseButtonState::Up, .. } = ev {
                show_main(tray.app_handle());
            }
        })
        .build(app)?;
    tray.set_visible(visible)?;
    Ok(())
}

pub fn update<R: Runtime>(app: &AppHandle<R>, level: &str, tooltip: &str, lines: &[String]) -> tauri::Result<()> {
    if let Some(t) = app.tray_by_id(TRAY_ID) {
        t.set_icon(Some(status_image(level)))?;
        t.set_tooltip(Some(tooltip))?;
        t.set_menu(Some(build_menu(app, lines)?))?;
    }
    Ok(())
}

pub fn set_visible<R: Runtime>(app: &AppHandle<R>, on: bool) {
    if let Some(t) = app.tray_by_id(TRAY_ID) {
        let _ = t.set_visible(on);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn icon_pixels() {
        let px = status_icon_rgba("down");
        assert_eq!(px.len(), 32 * 32 * 4);
        // corner transparent, a disc pixel away from the trace is the status red
        assert_eq!(px[3], 0);
        let i = (6 * 32 + 16) * 4;
        assert_eq!(&px[i..i + 4], &[0xFF, 0x4D, 0x4F, 255]);
    }
}
