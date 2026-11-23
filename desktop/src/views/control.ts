import { errText } from "../api";
import { confirmDialog, esc, icons, patch, toast, type Handlers } from "../dom";
import { C, endpointLabel, kindLabel, relTime } from "../fmt";
import { model, type NodeState } from "../state";
import type { AgentConfig, ClientInfo, PairCode } from "../types";
import { panel } from "./common";
import type { SubView } from "./node";

function getPath(o: any, path: string): any { return path.split(".").reduce((a, k) => (a == null ? a : a[k]), o); }
function setPath(o: any, path: string, v: unknown) {
  const ks = path.split(".");
  let cur = o;
  for (const k of ks.slice(0, -1)) cur = cur[k] ??= {};
  cur[ks[ks.length - 1]] = v;
}

export function controlTab(n: NodeState): SubView {
  const el = document.createElement("div");
  el.className = "scroll fill";
  el.dataset.scroll = "ctl";
  let cfg: AgentConfig | null = n.config ? structuredClone(n.config) : null;
  let msg = "";
  let msgOk = false;
  let clients: ClientInfo[] = [];
  let code: PairCode | null = null;
  let uninstallMsg = "";
  let rendered = false;

  const loadClients = async () => { try { clients = (await n.agent.clients()) ?? []; } catch { clients = []; } render(); };

  const dirty = () => cfg != null && n.config != null && JSON.stringify(cfg) !== JSON.stringify(n.config);

  const label = (s: string) => `<label class="flabel">${esc(s.toUpperCase())}</label>`;
  const intField = (path: string, w = 100) =>
    `<input class="field" style="width:${w}px" inputmode="numeric" data-k="${path}" data-in="num" data-path="${path}" value="${esc(getPath(cfg, path) ?? "")}">`;
  const textField = (path: string, w = 240, ph = "") =>
    `<input class="field" style="width:${w}px" data-k="${path}" data-in="text" data-path="${path}" placeholder="${esc(ph)}" value="${esc(getPath(cfg, path) ?? "")}">`;
  const toggle = (path: string) => `<label class="switch"><input type="checkbox" data-in="bool" data-path="${path}" ${getPath(cfg, path) ? "checked" : ""}><span></span></label>`;
  const grid = (rows: [string, string][]) => `<div class="fgrid">${rows.map(([l, f]) => `${label(l)}<div>${f}</div>`).join("")}</div>`;

  const editor = () => {
    if (!cfg) return `<div class="dim">Loading configuration…</div>`;
    const targets = cfg.targets.map((t, i) => `<div class="row gap8 trowc">
        <label class="switch sm"><input type="checkbox" data-in="bool" data-path="targets.${i}.enabled" ${t.enabled ? "checked" : ""}><span></span></label>
        <span class="mono9 bold dim w40">${kindLabel(t.kind)}</span><span class="w200 ell">${esc(t.name)}</span>
        <span class="mono10 faint ell grow">${esc(endpointLabel(t))}</span>
        <span class="mono9 faint">every</span>
        <input class="field" style="width:80px" placeholder="default" data-k="ti-${i}" data-in="num-opt" data-path="targets.${i}.intervalMs" value="${t.intervalMs ?? ""}">
        <span class="mono9 faint">ms</span></div>`).join("");
    const save = `${msg ? `<span class="mono10" style="color:${msgOk ? C.green : C.red}">${esc(msg)}</span>` : ""}<button class="btn primary" data-act="save" ${dirty() ? "" : "disabled"}>SAVE &amp; APPLY</button>`;
    return [
      panel("Probing", grid([
        ["Node name", textField("nodeName")],
        ["Probe interval (ms)", intField("intervalMs")],
        ["Timeout (ms)", intField("timeoutMs")],
        ["Keep raw samples (h)", intField("retentionRawHours")],
        ["Keep rollups (days)", intField("retentionRollupDays")],
        ["Public /metrics", toggle("metricsPublic")],
      ]), save),
      panel("Alert rules", grid([
        ["Down after N failures", intField("rules.downAfterFailures")],
        ["Degraded above (ms)", intField("rules.degradedLatencyMs")],
        ["…for at least (s)", intField("rules.degradedForSec")],
        ["Webhook URL", textField("rules.webhookUrl", 420, "https://hooks.slack.com/…  (optional)")],
      ])),
      panel("Scheduled speed test", grid([
        ["Enabled", toggle("speedtest.enabled")],
        ["Every (min)", intField("speedtest.intervalMin")],
        ["Test URL", textField("speedtest.url", 420)],
      ])),
      panel("Targets", `<div class="col">${targets || `<span class="faint small">No targets</span>`}</div>`, `<button class="btn" data-act="edit-targets">EDIT TARGETS…</button>`),
    ].join("");
  };

  const clientsPanel = () => panel("Paired controllers", `<div class="col gap6">
      ${code ? `<div class="mono12 bold amber">Code ${esc(code.code)} — enter on another computer within 10 minutes</div>` : ""}
      ${clients.map((c) => `<div class="row gap8 small">${icons.laptop(14)}<span class="grow">${esc(c.name)}</span>
        <span class="mono10 faint">${c.lastSeen ? "seen " + relTime(c.lastSeen) : ""}</span>
        <button class="iconbtn red" title="Revoke" data-act="revoke" data-id="${esc(c.id)}" data-name="${esc(c.name)}">${icons.x(14)}</button></div>`).join("")}
    </div>`, `<button class="btn" data-act="pair-code">NEW PAIRING CODE</button>`);

  const servicePanel = () => {
    const removing = model.removing.has(n.id);
    return panel("Service", `<div class="row gap8 wrap">
      <button class="btn" data-act="restart">RESTART AGENT</button>
      <button class="btn danger" data-act="wipe">WIPE HISTORY</button>
      <button class="btn danger" data-act="stop">STOP MONITORING</button>
      <button class="btn danger" data-act="uninstall" ${removing ? "disabled" : ""}>${removing ? "REMOVING…" : "UNINSTALL AGENT &amp; REMOVE"}</button>
      <span class="grow"></span>
      <span class="small" style="color:${uninstallMsg ? C.red : C.faint}">${esc(uninstallMsg || "Stop monitoring keeps the agent running on the node; Uninstall deletes it and all its data.")}</span>
    </div>`);
  };

  const render = () => {
    rendered = true;
    patch(el, `<div class="pad col gap16">${editor()}${clientsPanel()}${servicePanel()}</div>`);
  };

  const refreshSave = () => {
    const b = el.querySelector<HTMLButtonElement>('[data-act="save"]');
    if (b) b.disabled = !dirty();
  };

  const update = () => {
    // Only re-render on first load; polling must never clobber an in-progress edit.
    if (!cfg && n.config) { cfg = structuredClone(n.config); render(); return; }
    if (!rendered) { render(); loadClients(); }
    const r = el.querySelector<HTMLButtonElement>('[data-act="uninstall"]');
    if (r) r.disabled = model.removing.has(n.id);
  };

  const handlers = (): Handlers => ({
    num: (e) => { const v = parseInt((e as HTMLInputElement).value, 10); if (Number.isFinite(v)) setPath(cfg, e.dataset.path!, v); refreshSave(); },
    "num-opt": (e) => { const s = (e as HTMLInputElement).value.trim(); const v = parseInt(s, 10); setPath(cfg, e.dataset.path!, s === "" || !Number.isFinite(v) ? null : v); refreshSave(); },
    text: (e) => { setPath(cfg, e.dataset.path!, (e as HTMLInputElement).value); refreshSave(); },
    bool: (e) => { setPath(cfg, e.dataset.path!, (e as HTMLInputElement).checked); refreshSave(); },
    save: async () => {
      if (!cfg) return;
      const c = cfg;
      const err = await model.updateConfig(n, () => c);
      msg = err ?? "Saved"; msgOk = !err;
      if (!err && n.config) cfg = structuredClone(n.config);
      render();
      setTimeout(() => { msg = ""; render(); }, 3000);
    },
    "edit-targets": () => { model.wizard = { open: true, editing: true }; model.changed(); },
    "pair-code": async () => { try { code = await n.agent.newPairCode(); } catch (e) { toast(errText(e)); } render(); },
    revoke: async (e) => {
      if (!(await confirmDialog({ title: `Revoke ${e.dataset.name}?`, message: "That controller will lose access to this agent. This cannot be undone.", confirm: "REVOKE", danger: true }))) return;
      try { await n.agent.deleteClient(e.dataset.id!); } catch (err) { toast(errText(err)); }
      loadClients();
    },
    restart: async () => { try { await n.agent.restart(); toast(`Restarting ${n.name}…`); } catch (e) { toast(errText(e)); } },
    wipe: async () => {
      if (!(await confirmDialog({ title: `Wipe all stored history on ${n.name}?`, message: "Deletes all samples, rollups and incidents on the node. Configuration and pairings are kept.", confirm: "WIPE HISTORY", danger: true }))) return;
      try { await n.agent.resetHistory(); n.series = {}; n.incidents = []; model.changed(); toast("History wiped"); } catch (e) { toast(errText(e)); }
    },
    stop: async () => {
      if (!(await confirmDialog({ title: `Stop monitoring ${n.name} from this computer?`, message: "The agent keeps running on the node with its history; you can re-add it later.", confirm: "REMOVE FROM THIS COMPUTER", danger: true }))) return;
      model.remove(n);
    },
    uninstall: async () => {
      if (!(await confirmDialog({ title: `Uninstall the agent from ${n.name}?`, message: `Stops the service and deletes the binary, config, tokens and all stored history on ${n.saved.host} over SSH. Cannot be undone.`, confirm: "UNINSTALL & DELETE ALL DATA", danger: true }))) return;
      const name = n.name;
      render();
      const r = await model.uninstallAndRemove(n);
      uninstallMsg = r ?? "";
      toast(r ?? `${name}: agent uninstalled, no files left`);
      if (el.isConnected) render();
    },
  });

  return { el, update, handlers };
}
