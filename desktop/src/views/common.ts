import { ChartStack, type ChartData } from "../chart";
import { dot, esc } from "../dom";
import { C, dateTime, duration, ms } from "../fmt";
import { RANGES, model, type NodeState } from "../state";
import type { Incident } from "../types";

const stacks = new Map<string, ChartStack>();

/** One persistent chart stack per node (only one route is visible at a time, so it is shared). */
export function stackFor(n: NodeState): ChartStack {
  let s = stacks.get(n.id);
  if (!s) { s = new ChartStack(); stacks.set(n.id, s); }
  s.update(chartRows(n));
  return s;
}

export function dropStacks(keep: Set<string>) {
  for (const [id, s] of stacks) if (!keep.has(id)) { s.destroy(); stacks.delete(id); }
}

export function chartRows(n: NodeState): ChartData[] {
  const ts = n.targets;
  return ts.map((t, i) => ({
    target: t,
    live: n.live[t.id],
    series: n.seriesLoaded ? n.series[t.id] ?? { target: t.id, stepSec: 60, points: [] } : undefined,
    summary: n.summaries[t.id],
    showAxis: i === ts.length - 1,
  }));
}

export function rangeBar(): string {
  const z = model.zoom;
  const btns = RANGES.map((r) => `<button class="rbtn ${model.range === r.id && !z ? "on" : ""}" data-act="range" data-v="${r.id}">${r.label}</button>`).join("");
  const tail = z
    ? `<span class="zoomlbl">ZOOM ${esc(new Date(z[0] * 1000).toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" }))} → ${esc(new Date(z[1] * 1000).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" }))}</span><button class="btn" data-act="zoom-reset">RESET</button>`
    : `<span class="hintline">DRAG ON A GRAPH TO ZOOM · DOUBLE-CLICK TO RESET</span>`;
  return `<div class="row gap10 rangebar">${btns}${tail}</div>`;
}

export function rangeHandlers() {
  return {
    range: (el: HTMLElement) => model.setRange(el.dataset.v as any),
    "zoom-reset": () => model.setZoom(null),
  };
}

export function panel(title: string | null, body: string, trailing = "", cls = ""): string {
  return `<section class="panel ${cls}">${title != null ? `<header><span>${esc(title.toUpperCase())}</span><div class="row gap8">${trailing}</div></header>` : ""}<div class="pbody">${body}</div></section>`;
}

export function banner(text: string, color = C.red): string {
  return `<div class="banner" style="color:${color};background:${color}1a">${esc(text)}</div>`;
}

export function incidentsList(items: [string, Incident][], showNode: boolean): string {
  if (!items.length) return `<div class="empty green">No incidents in the last 30 days</div>`;
  const now = Date.now() / 1000;
  const rows = [...items].sort((a, b) => b[1].startedAt - a[1].startedAt).map(([node, i]) => {
    const open = i.endedAt == null;
    const bar = open ? C.red : i.kind === "down" ? C.red + "80" : C.amber + "b3";
    const dur = (i.endedAt ?? now) - i.startedAt;
    return `<div class="inc">
      <div class="incbar" style="background:${bar}"></div>
      <div class="grow">
        <div class="row gap8"><b class="mono10" style="color:${i.kind === "down" ? C.red : C.amber}">${esc(i.kind.toUpperCase())}</b>
          <span class="incname">${esc((showNode ? `${node} → ` : "") + (i.targetName ?? i.targetId ?? ""))}</span>
          ${open ? `<span class="ongoing">ONGOING</span>` : ""}</div>
        <div class="mono10 dim">${esc(i.detail ?? "")}</div>
      </div>
      <div class="right">
        <div class="mono11">${esc(dateTime(i.startedAt))}</div>
        <div class="mono10 dim">duration ${duration(dur)}${i.peakMs != null ? ` · peak ${ms(i.peakMs)} ms` : ""}</div>
      </div></div>`;
  });
  return `<div class="inclist">${rows.join("")}</div>`;
}

export function statusDot(n: NodeState, size = 8) {
  return dot(stateColorOf(n.worst), size, n.worst !== "unknown");
}

function stateColorOf(s: string) {
  return s === "up" ? C.green : s === "degraded" ? C.amber : s === "down" ? C.red : C.faint;
}

export function avgLastRtt(n: NodeState): number | null {
  const v = Object.values(n.live).map((l) => l.lastRttMs).filter((x): x is number => typeof x === "number");
  return v.length ? v.reduce((a, b) => a + b, 0) / v.length : null;
}
