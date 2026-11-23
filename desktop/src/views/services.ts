// Services page: catalog (left), "monitor from" panel + one card per selected service (right).
import { api, errText } from "../api";
import { confirmDialog, dot, esc, icons, patch, toast, type Handlers } from "../dom";
import { C, ms, relTime } from "../fmt";
import { model, type NodeState } from "../state";
import {
  customService, DESKTOP_WARNING, filterServices, groupByCategory, NO_FEED, STATE_COLOR, type OfficialState, type Reach, type ServiceDef,
} from "../services";
import { services } from "../servicesModel";
import { panel } from "./common";
import type { View } from "./view";

const SPARK_POINTS = 240; // last 2 h at 30 s

function chip(s: OfficialState | undefined) {
  const st = s ?? "unknown";
  const c = STATE_COLOR[st];
  return `<span class="schip" data-state="${st}" style="color:${c};border-color:${c}80;background:${c}1a">${st.toUpperCase()}</span>`;
}

function sparkline(r: Reach): string {
  const pts = r.samples.slice(-SPARK_POINTS);
  if (pts.length < 2) return `<svg class="sspark"></svg>`;
  const W = 120, H = 22;
  const vals = pts.map((p) => p[1]).filter((v): v is number => v != null);
  const max = Math.max(50, ...vals) * 1.1;
  const x = (i: number) => (i / (pts.length - 1)) * W;
  let d = "";
  let pen = false;
  const fails: string[] = [];
  pts.forEach((p, i) => {
    if (p[1] == null) { pen = false; fails.push(`<line x1="${x(i).toFixed(1)}" x2="${x(i).toFixed(1)}" y1="0" y2="${H}" stroke="${C.red}" stroke-width="1"/>`); return; }
    d += `${pen ? "L" : "M"}${x(i).toFixed(1)} ${(H - 1 - (p[1] / max) * (H - 2)).toFixed(1)}`;
    pen = true;
  });
  return `<svg class="sspark" viewBox="0 0 ${W} ${H}" preserveAspectRatio="none">${fails.join("")}<path d="${d}" fill="none" stroke="${C.cyan}" stroke-width="1.2" vector-effect="non-scaling-stroke"/></svg>`;
}

function nodeRow(n: NodeState, d: ServiceDef): string {
  const on = services.nodeOn(n.id);
  const { applied, live } = services.nodeReach(n, d.id);
  if (!on && !applied) return "";
  let right: string;
  if (on && !applied) right = `<span class="mono9 amber">Not applied yet — press APPLY</span>`;
  else if (n.reachable === false) right = `<span class="mono9 red bold">NODE UNREACHABLE</span>`;
  else if (!live || live.state === "unknown") right = `<span class="mono10 faint">waiting…</span>`;
  else if (live.state === "down") right = `<b class="mono11 red">DOWN</b>`;
  else right = `<b class="mono11 green">${ms(live.lastRttMs)}<small> ms</small></b>`;
  const st = !applied ? C.faint : live?.state === "down" ? C.red : live && live.state !== "unknown" ? C.green : C.faint;
  const note = !on && applied ? ` <span class="mono9 faint">(deselected — APPLY removes)</span>` : "";
  return `<div class="row gap6 srow" data-src="node">${dot(st, 6)}${icons.server(12)}<span class="grow ell small">${esc(n.name)}${note}</span>${right}</div>`;
}

function desktopRow(d: ServiceDef): string {
  if (!services.st.desktop) return "";
  const r = services.reach.get(d.id);
  const col = !r || r.state === "unknown" ? C.faint : r.state === "down" ? C.red : C.green;
  const val = !r ? `<span class="mono10 faint">probing…</span>`
    : r.state === "down" ? `<b class="mono11 red">DOWN</b>`
    : r.lastMs == null ? `<span class="mono10 amber" title="${esc(r.lastError ?? "")}">FAIL ${r.failures}/3</span>`
    : `<b class="mono11 green">${ms(r.lastMs)}<small> ms</small></b>`;
  return `<div class="row gap6 srow" data-src="desktop">${dot(col, 6)}${icons.desktop(12)}<span class="grow ell small">This computer</span>${r ? sparkline(r) : ""}${val}</div>`;
}

function card(d: ServiceDef): string {
  const e = services.official.get(d.id);
  const o = e?.data;
  const incs = (o?.incidents ?? []).slice(0, 3).map((i) => `<div class="row gap6 top"><span class="sbul" style="background:${STATE_COLOR[o!.state === "operational" ? "degraded" : o!.state]}"></span><span class="small">${esc(i.title)}</span></div>`).join("");
  const more = (o?.incidents.length ?? 0) > 3 ? `<div class="mono9 faint">+${o!.incidents.length - 3} more incidents</div>` : "";
  const comps = (o?.components ?? []).slice(0, 5).map((c) => `<div class="row gap6"><span class="grow ell mono10">${esc(c.name)}</span><span class="mono9 amber">${esc(c.status.replace(/_/g, " ").toUpperCase())}</span></div>`).join("");
  const rows = [...model.nodes.map((n) => nodeRow(n, d)), desktopRow(d)].filter(Boolean).join("");
  const summary = o ? o.summary || (o.state === "operational" ? "All systems operational" : "") : d.adapter === "none" ? NO_FEED : "Checking official status…";
  return `<div class="svccard" data-id="${esc(d.id)}">${panel(null, `<div class="col gap8">
    <div class="row gap8"><b class="grow ell">${esc(d.name)}</b>${chip(o?.state)}</div>
    <div class="mono9 faint track">${esc(d.category.toUpperCase())}</div>
    <div class="small dim ssum">${esc(summary)}</div>
    ${incs || more ? `<div class="col gap4">${incs}${more}</div>` : ""}
    ${comps ? `<div class="col gap2"><span class="hint">AFFECTED COMPONENTS</span>${comps}</div>` : ""}
    <div class="col gap4"><span class="hint">REACHABILITY</span>${rows || `<span class="mono10 faint">No source — enable a node or this computer above</span>`}</div>
    <div class="row gap8"><span class="mono9 faint grow">${e ? `checked ${esc(relTime(e.at))}` : ""}</span>
      <a class="link mono10 row gap4" data-act="svc-open" data-url="${esc(d.pageUrl)}">STATUS PAGE ${icons.open(11)}</a></div>
  </div>`)}</div>`;
}

export function servicesView(): View {
  const el = document.createElement("div");
  el.className = "page svcpage";
  let q = "";
  const form = { name: "", probe: "", status: "" };
  let formErr = "";

  const catalog = () => {
    const groups = groupByCategory(filterServices(services.defs, q));
    const body = groups.map(([cat, list]) => `<div class="scat">${esc(cat.toUpperCase())}</div>${list.map((d) => `
      <label class="srv ${services.isSelected(d.id) ? "on" : ""}">
        <input type="checkbox" data-in="svc-toggle" data-id="${esc(d.id)}" ${services.isSelected(d.id) ? "checked" : ""}>
        <span class="grow ell">${esc(d.name)}</span>
        ${d.adapter === "none" ? `<span class="ktag" title="${esc(NO_FEED)}">PROBE</span>` : ""}
        ${d.custom ? `<button class="iconbtn red" title="Delete custom service" data-act="svc-del" data-id="${esc(d.id)}">${icons.trash(13)}</button>` : ""}
      </label>`).join("")}`).join("") || `<div class="mono10 faint pad12">No match</div>`;
    const f = (k: keyof typeof form, ph: string) => `<input class="field" data-k="svc-f-${k}" data-in="svc-f" data-f="${k}" placeholder="${esc(ph)}" value="${esc(form[k])}" data-enter="svc-cadd">`;
    return `<aside class="svccat">
      <div class="pad12 col gap8"><div class="row"><span class="mono10 bold dim track grow">SERVICES</span><span class="mono9 faint">${services.st.selected.length} SELECTED</span></div>
        <input class="field" data-k="svc-q" data-in="svc-q" placeholder="Search services…" value="${esc(q)}"></div>
      <div class="scroll grow" data-scroll="svccat">${body}</div>
      <div class="pad12 col gap6 saddf"><span class="hint">ADD CUSTOM SERVICE</span>
        ${f("name", "Name")}${f("probe", "Probe URL (https://app.example.com)")}${f("status", "Status page URL (optional, Statuspage)")}
        ${formErr ? `<span class="mono9 red">${esc(formErr)}</span>` : ""}
        <button class="btn" data-act="svc-cadd">${icons.plus(12)} ADD</button></div>
    </aside>`;
  };

  const sources = () => {
    const nodes = model.nodes.map((n) => {
      const r = services.applyResults[n.id];
      return `<div class="col gap2"><label class="row gap10 toggle-row"><span class="switch"><input type="checkbox" data-in="svc-node" data-id="${esc(n.id)}" ${services.nodeOn(n.id) ? "checked" : ""}><span></span></span>
        ${icons.server(13)}<span>${esc(n.name)}</span><span class="mono10 faint">${esc(n.saved.host)}</span></label>
        ${r ? `<span class="mono10 sres" style="color:${r.ok ? C.green : C.red};padding-left:40px">${esc(r.msg)}</span>` : ""}</div>`;
    }).join("") || `<span class="mono10 faint">No nodes saved yet — add nodes to monitor services from them.</span>`;
    const desk = `<div class="col gap2"><label class="row gap10 toggle-row"><span class="switch"><input type="checkbox" data-in="svc-desktop" ${services.st.desktop ? "checked" : ""}><span></span></span>
        ${icons.desktop(13)}<span>This computer (desktop app)</span></label>
        ${services.st.desktop ? `<span class="svcwarn">${esc(DESKTOP_WARNING)}</span>` : ""}</div>`;
    const need = services.applyNeeded;
    const status = services.applying ? `<span class="mono10 amber">Applying…</span>`
      : need ? `<span class="mono10 amber">Changes not applied to nodes</span>` : `<span class="mono10 faint">Node configs up to date</span>`;
    return panel("Monitor selected services from", `<div class="col gap10">${nodes}${desk}
      <div class="row gap10 mt8"><button class="btn primary" data-act="svc-apply" ${need && !services.applying && model.nodes.length ? "" : "disabled"}>APPLY</button>${status}</div></div>`);
  };

  const update = () => {
    const sel = services.selected;
    const cards = sel.length ? `<div class="svccards">${sel.map(card).join("")}</div>`
      : panel(null, `<div class="empty">Select services in the catalog to watch their official status and reachability.</div>`);
    patch(el, `${catalog()}<div class="vline"></div><div class="grow scroll" data-scroll="svcmain"><div class="pad col gap16">${sources()}${cards}</div></div>`);
  };

  const handlers = (): Handlers => ({
    "svc-q": (e) => { q = (e as HTMLInputElement).value; update(); },
    "svc-toggle": (e) => services.toggleService(e.dataset.id!, (e as HTMLInputElement).checked),
    "svc-node": (e) => services.toggleNode(e.dataset.id!, (e as HTMLInputElement).checked),
    "svc-desktop": (e) => services.setDesktop((e as HTMLInputElement).checked),
    "svc-apply": async () => {
      const ok = await services.apply();
      toast(ok ? "Service targets applied to nodes" : "Some nodes could not be updated — see results");
    },
    "svc-f": (e) => { form[e.dataset.f as keyof typeof form] = (e as HTMLInputElement).value; },
    "svc-cadd": () => {
      const r = customService(form.name, form.probe, form.status);
      if (typeof r === "string") { formErr = r; update(); return; }
      formErr = ""; form.name = form.probe = form.status = "";
      services.addCustom(r);
    },
    "svc-del": async (e) => {
      const d = services.def(e.dataset.id!);
      if (!d) return;
      if (await confirmDialog({ title: `Delete ${d.name}?`, message: "Removes the custom service. Press APPLY afterwards to remove it from nodes.", confirm: "DELETE", danger: true })) services.removeCustom(d.id);
    },
    "svc-open": (e) => api.openUrl(e.dataset.url!).catch((err) => toast(errText(err))),
  });

  return { el, update, handlers };
}
