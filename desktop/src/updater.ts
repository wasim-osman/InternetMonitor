// Update scheduling + UI state. The heavy lifting (GitHub API, download, SHA-256, install) is in
// src-tauri/src/updater.rs.
import { api, errText } from "./api";
import { model } from "./state";

export const REPO_URL = "https://github.com/wasim-osman/InternetMonitor";
const SIX_HOURS = 6 * 3600 * 1000;

let timer: number | undefined;

/** `auto` = scheduled/background check: silent on errors, installs when automatic updates are on. */
export async function checkForUpdates(auto: boolean) {
  const u = model.update;
  if (u.checking || u.installing) return;
  if (auto && !model.settings.autoUpdate) return;
  u.checking = true; u.error = undefined; if (!auto) u.status = undefined;
  model.changed();
  try {
    u.info = await api.updateCheck();
    u.lastCheck = Date.now() / 1000;
  } catch (e) {
    if (!auto) u.error = errText(e);
  } finally {
    u.checking = false;
    model.changed();
  }
  if (auto && u.info?.available && model.settings.autoUpdate && u.info.mode !== "page") await installUpdate();
}

export async function installUpdate() {
  const u = model.update;
  if (!u.info || u.installing) return;
  u.installing = true; u.error = undefined;
  u.status = u.info.mode === "page" ? "Opening release page…" : `Downloading ${u.info.asset?.name ?? "update"} and verifying SHA-256…`;
  model.changed();
  try {
    const r = await api.updateInstall(u.info);
    u.status = r.status === "restart" ? `${r.message}` : r.message;
    if (r.status === "restart") u.status = `Update ${u.info.latest} installed — restart to update`;
  } catch (e) {
    u.error = `Update failed: ${errText(e)}`;
    u.status = undefined;
  } finally {
    u.installing = false;
    model.changed();
  }
}

export function startUpdater() {
  window.clearInterval(timer);
  window.setTimeout(() => checkForUpdates(true), 8000);
  timer = window.setInterval(() => checkForUpdates(true), SIX_HOURS);
}

/** Banner HTML for the top of the main area (empty string = none). */
export function updateBanner(): string {
  const u = model.update;
  const i = u.info;
  if (u.status?.includes("restart")) {
    return `<div class="ubanner"><span>${u.status}</span><span class="grow"></span><button class="btn primary" data-act="upd-restart">RESTART TO UPDATE</button></div>`;
  }
  if (u.installing) return `<div class="ubanner"><span>${u.status ?? "Updating…"}</span></div>`;
  if (!i?.available || u.dismissed === i.latest) return "";
  const err = u.error ? `<span class="red"> · ${u.error}</span>` : "";
  const action = i.mode === "page"
    ? `<button class="btn primary" data-act="upd-install">OPEN RELEASE PAGE</button>`
    : `<button class="btn primary" data-act="upd-install">INSTALL UPDATE</button>`;
  return `<div class="ubanner"><span>Internet Monitor ${i.latest} is available (you have ${i.current}).${err}</span><span class="grow"></span>
    <button class="btn" data-act="upd-notes">RELEASE NOTES</button>${action}<button class="btn" data-act="upd-dismiss">LATER</button></div>`;
}

export const bannerHandlers = {
  "upd-install": () => installUpdate(),
  "upd-restart": () => api.restartApp(),
  "upd-notes": () => { if (model.update.info) api.openUrl(model.update.info.htmlUrl).catch(() => {}); },
  "upd-dismiss": () => { if (model.update.info) model.update.dismissed = model.update.info.latest; model.changed(); },
};
