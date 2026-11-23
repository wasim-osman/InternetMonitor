import { api, errText } from "../api";
import { esc, fillSlots, icons, nodeIconChoices, patch, toast, type Handlers } from "../dom";
import { duration, safeFileName, seriesCSV } from "../fmt";
import { model, type NodeState } from "../state";
import { banner, incidentsList, rangeBar, rangeHandlers, stackFor, statusDot } from "./common";
import { controlTab } from "./control";
import { diagnosticsTab } from "./diagnostics";
import type { View } from "./view";

type Tab = "latency" | "incidents" | "diagnostics" | "control";
const TABS: Tab[] = ["latency", "incidents", "diagnostics", "control"];

export interface SubView { el: HTMLElement; update(): void; handlers(): Handlers }

export function nodeView(n: NodeState): View {
  const el = document.createElement("div");
  el.className = "page col";
  el.innerHTML = `<div class="nhead"></div><div class="nbanner"></div><div class="ntab grow"></div>`;
  const head = el.querySelector<HTMLElement>(".nhead")!;
  const ban = el.querySelector<HTMLElement>(".nbanner")!;
  const body = el.querySelector<HTMLElement>(".ntab")!;
  let tab: Tab = "latency";
  let iconMenu = false;
  let sub: SubView = latencyTab(n);
  body.appendChild(sub.el);

  const setTab = (t: Tab) => {
    if (t === tab) return;
    tab = t;
    sub.el.remove();
    sub = t === "latency" ? latencyTab(n) : t === "incidents" ? incidentsTab(n) : t === "diagnostics" ? diagnosticsTab(n) : controlTab(n);
    body.appendChild(sub.el);
    update();
  };

  const update = () => {
    const up = n.identity?.uptimeSec ?? n.system?.uptimeSec ?? 0;
    const temp = n.system?.tempC != null ? ` · ${n.system.tempC.toFixed(0)}°C` : "";
    const ic = (model.icons[n.id] ?? "cpu") as keyof typeof icons;
    patch(head, `<div class="row gap14">
      <div class="iconpick">
        <button class="nodeicon" data-act="icon-menu" title="Change icon">${(icons[ic] ?? icons.cpu)(20)}</button>
        ${iconMenu ? `<div class="menu">${nodeIconChoices.map(([k, l]) => `<button data-act="set-icon" data-v="${k}">${icons[k](14)} ${esc(l)}</button>`).join("")}</div>` : ""}
      </div>
      <div class="col gap2">
        <div class="row gap8">${statusDot(n)}<span class="h18">${esc(n.name)}</span></div>
        <div class="mono11 dim">${esc(n.saved.host)}${n.saved.port !== 7878 ? ":" + n.saved.port : ""} · agent v${esc(n.identity?.version ?? "?")} · up ${duration(up)}${temp}</div>
      </div>
      <span class="grow"></span>
      <div class="seg">${TABS.map((t) => `<button class="${t === tab ? "on" : ""}" data-act="tab" data-v="${t}">${t.toUpperCase()}</button>`).join("")}</div>
    </div>`);
    ban.innerHTML = n.unauthorized
      ? banner("This computer's pairing was revoked or the agent was reset. Re-pair from Add nodes (the wizard) to restore access.")
      : n.reachable === false ? banner(`Agent unreachable at ${n.saved.host}:${n.saved.port} — showing last known data. ${n.lastError ?? ""}`) : "";
    sub.update();
  };

  const handlers = (): Handlers => ({
    tab: (e) => setTab(e.dataset.v as Tab),
    "icon-menu": () => { iconMenu = !iconMenu; update(); },
    "set-icon": (e) => { iconMenu = false; model.setIcon(n.id, e.dataset.v!); },
    ...sub.handlers(),
  });

  return { el, update, handlers };
}

function latencyTab(n: NodeState): SubView {
  const el = document.createElement("div");
  el.className = "col fill";
  el.innerHTML = `<div class="toolbar"></div><div class="scroll grow lat" data-scroll="lat"></div>`;
  const bar = el.querySelector<HTMLElement>(".toolbar")!;
  const sc = el.querySelector<HTMLElement>(".lat")!;
  let barSig = "";
  const update = () => {
    const sig = model.range + JSON.stringify(model.zoom);
    if (sig !== barSig) {
      barSig = sig;
      bar.innerHTML = `<div class="row gap10">${rangeBar()}<span class="grow"></span>
        <button class="btn" data-act="csv">${icons.table(13)} CSV</button><button class="btn" data-act="png">${icons.image(13)} PNG</button></div>`;
    }
    if (!n.targets.length) {
      patch(sc, `<div class="center col gap10 fill"><span class="dim">${n.config ? "No targets configured on this node" : "Loading configuration…"}</span>
        ${n.config ? `<button class="btn primary" data-act="edit-targets">CHOOSE TARGETS</button>` : ""}</div>`);
      return;
    }
    if (!sc.querySelector(".stack")) { sc.innerHTML = `<div class="pad"><div data-slot="s"></div></div>`; fillSlots(sc, () => stackFor(n).el); }
    else stackFor(n);
  };
  const handlers = (): Handlers => ({
    ...rangeHandlers(),
    "edit-targets": () => { model.wizard = { open: true, editing: true }; model.changed(); },
    csv: async () => {
      const rows = n.targets.filter((t) => n.series[t.id]).map((t) => ({ node: n.name, target: t.name, series: n.series[t.id] }));
      try { const p = await api.saveText(`${safeFileName(n.name)}-latency.csv`, seriesCSV(rows)); if (p) toast(`Saved ${p}`); }
      catch (e) { toast(errText(e)); }
    },
    png: async () => {
      try { const p = await api.saveBase64(`${safeFileName(n.name)}-latency.png`, stackFor(n).toPNG(n.name)); if (p) toast(`Saved ${p}`); }
      catch (e) { toast(errText(e)); }
    },
  });
  return { el, update, handlers };
}

function incidentsTab(n: NodeState): SubView {
  const el = document.createElement("div");
  el.className = "scroll fill";
  el.dataset.scroll = "inc";
  return {
    el,
    update: () => patch(el, `<div class="pad">${incidentsList(n.incidents.map((i) => [n.name, i]), false)}</div>`),
    handlers: () => ({}),
  };
}

