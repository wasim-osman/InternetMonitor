import { api, errText } from "../api";
import { esc, icons, patch, toast, type Handlers } from "../dom";
import { C } from "../fmt";
import { model } from "../state";
import { checkForUpdates, installUpdate, REPO_URL } from "../updater";
import { incidentsList, panel } from "./common";
import type { View } from "./view";

export function incidentsView(): View {
  const el = document.createElement("div");
  el.className = "page col";
  el.innerHTML = `<div class="pagehead row"><span class="mono11 bold track">INCIDENT TIMELINE</span><span class="grow"></span><span class="mono10 faint">LAST 30 DAYS · ALL NODES</span></div><div class="scroll grow" data-scroll="inc"></div>`;
  const body = el.querySelector<HTMLElement>(".scroll")!;
  return {
    el,
    update: () => patch(body, `<div class="pad">${incidentsList(model.nodes.flatMap((n) => n.incidents.map((i) => [n.name, i] as [string, typeof i])), true)}</div>`),
    handlers: () => ({}),
  };
}

export function settingsView(): View {
  const el = document.createElement("div");
  el.className = "page scroll";
  el.dataset.scroll = "settings";
  let autostart: boolean | null = null;
  api.autostartGet().then((v) => { autostart = v; update(); }).catch(() => { autostart = false; update(); });

  const sw = (act: string, on: boolean, label: string, disabled = false) =>
    `<label class="row gap10 toggle-row"><span class="switch"><input type="checkbox" data-in="${act}" ${on ? "checked" : ""} ${disabled ? "disabled" : ""}><span></span></span><span>${esc(label)}</span></label>`;

  const updateStatus = () => {
    const u = model.update;
    if (u.checking) return `<span class="dim">Checking for updates…</span>`;
    if (u.installing) return `<span class="amber">${esc(u.status ?? "Downloading update…")}</span>`;
    if (u.error) return `<span class="red">${esc(u.error)}</span>`;
    if (u.status) return `<span class="green">${esc(u.status)}</span>`;
    if (u.info?.available) return `<span class="amber">Version ${esc(u.info.latest)} is available.</span>`;
    if (u.info) return `<span class="green">You're up to date (latest release ${esc(u.info.latest)}).</span>`;
    return `<span class="faint">Not checked yet.</span>`;
  };

  const update = () => {
    const s = model.settings;
    const u = model.update;
    patch(el, `<div class="pad col gap16 maxw">
      ${panel("Application", `<div class="col gap12">
        ${sw("notifications", s.notifications, "Notifications for outages, degradation and recovery")}
        ${sw("tray", s.tray, "Show status in the system tray (closing the window keeps monitoring in the tray)")}
        ${sw("autostart", autostart === true, autostart === null ? "Launch at login (checking…)" : "Launch at login (starts minimised to the tray)", autostart === null)}
        ${sw("autoUpdate", s.autoUpdate, "Automatic updates (checks GitHub every 6 hours, verifies SHA-256, installs)")}
      </div>`)}
      ${panel("Updates", `<div class="col gap10">
        <div class="row gap10"><span class="mono11">Version ${esc(model.info.version)}</span><span class="faint">·</span>${updateStatus()}</div>
        <div class="row gap8">
          <button class="btn" data-act="check" ${u.checking || u.installing ? "disabled" : ""}>CHECK FOR UPDATES</button>
          ${u.info?.available && !u.installing ? `<button class="btn primary" data-act="install">${u.info.mode === "page" ? "OPEN RELEASE PAGE" : `INSTALL ${esc(u.info.latest)}`}</button>` : ""}
          ${u.status?.includes("restart") ? `<button class="btn primary" data-act="restart">RESTART TO UPDATE</button>` : ""}
        </div>
        ${u.lastCheck ? `<span class="mono10 faint">Last checked ${esc(new Date(u.lastCheck * 1000).toLocaleString())}</span>` : ""}
      </div>`)}
      ${panel("About", `<div class="col gap8">
        <div class="row gap8"><span style="color:${C.amber}">${icons.pulse(18)}</span><b>Internet Monitor</b><span class="mono11 dim">v${esc(model.info.version)}</span></div>
        <div>Developed by Wasim Osman</div>
        <a class="link row gap6" data-act="github">${icons.github(14)} ${esc(REPO_URL)}</a>
        <div class="small faint">Released under the MIT License. Free to use, modify and distribute; provided "as is", without warranty of any kind.</div>
      </div>`)}
      ${panel("How it works", `<div class="small dim">Each node runs a tiny agent (imonitord) that does the probing and stores history locally. This app only displays and controls them, so graphs keep their history even when this computer is off or asleep. Agents are paired with a token stored in <span class="mono10">${esc(model.info.dataDir)}${model.info.platform === "windows" ? "\\" : "/"}nodes.json</span>${model.info.platform === "windows" ? " (in your user profile)" : " (owner-only permissions)"}.</div>`)}
    </div>`);
  };

  const handlers = (): Handlers => ({
    notifications: (e) => model.saveSettings({ notifications: (e as HTMLInputElement).checked }),
    tray: (e) => model.saveSettings({ tray: (e as HTMLInputElement).checked }),
    autoUpdate: (e) => { const on = (e as HTMLInputElement).checked; model.saveSettings({ autoUpdate: on }).then(() => { if (on) checkForUpdates(true); }); },
    autostart: async (e) => {
      try { autostart = await api.autostartSet((e as HTMLInputElement).checked); } catch (err) { toast(errText(err)); }
      update();
    },
    check: () => checkForUpdates(false),
    install: () => installUpdate(),
    restart: () => api.restartApp(),
    github: () => api.openUrl(REPO_URL).catch((e) => toast(errText(e))),
  });
  return { el, update, handlers };
}
