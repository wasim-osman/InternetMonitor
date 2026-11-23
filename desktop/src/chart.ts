// Hand-rolled canvas latency chart: min–max band, avg line, loss shading, shared hover crosshair
// + tooltip, drag-to-zoom, double-click reset. One instance per (node, target) row.
import { dot, esc } from "./dom";
import { C, MONO, endpointKey, endpointLabel, kindLabel, latencyColor, ms, pct, seriesColor, stateColor, uptime } from "./fmt";
import { model } from "./state";
import type { LiveTarget, Series, SeriesPoint, SummaryWindow, Target } from "./types";

export interface ChartData {
  target: Target;
  live?: LiveTarget;
  series?: Series;
  summary?: SummaryWindow[];
  showAxis: boolean;
}

const registry = new Set<TargetChart>();
let drag: { chart: TargetChart; a: number; b: number } | null = null;

window.addEventListener("mouseup", () => {
  if (!drag) return;
  const lo = Math.min(drag.a, drag.b), hi = Math.max(drag.a, drag.b);
  drag = null;
  if (hi - lo >= 10) model.setZoom([lo, hi]); else redrawAll();
});

export function redrawAll() {
  for (const c of registry) {
    if (!c.el.isConnected) continue;
    c.draw();
  }
}

/** Removes charts whose rows are gone from the DOM. */
export function pruneCharts() {
  for (const c of registry) if (!c.el.isConnected && !c.keep) registry.delete(c);
}

function niceStep(max: number, count: number): number {
  const raw = max / Math.max(count, 1);
  const p = Math.pow(10, Math.floor(Math.log10(raw)));
  const m = raw / p;
  return (m <= 1 ? 1 : m <= 2 ? 2 : m <= 5 ? 5 : 10) * p;
}

function timeLabel(t: number, span: number): string {
  const d = new Date(t * 1000);
  const p2 = (n: number) => String(n).padStart(2, "0");
  if (span > 2 * 86400) return d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
  if (span > 6 * 3600) return `${p2(d.getHours())}:${p2(d.getMinutes())}`;
  return `${p2(d.getHours())}:${p2(d.getMinutes())}:${p2(d.getSeconds())}`;
}

function niceTimeStep(span: number, count: number): number {
  const steps = [5, 10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600, 7200, 10800, 21600, 43200, 86400, 172800, 604800, 1209600];
  const raw = span / count;
  return steps.find((s) => s >= raw) ?? 2592000;
}

export class TargetChart {
  el: HTMLElement;
  private info: HTMLElement;
  private canvas: HTMLCanvasElement;
  private data?: ChartData;
  private ro: ResizeObserver;
  keep = false;

  constructor() {
    this.el = document.createElement("div");
    this.el.className = "trow";
    this.el.innerHTML = `<div class="tinfo"></div><div class="tsep"></div><div class="tchart"><canvas></canvas></div>`;
    this.info = this.el.querySelector(".tinfo")!;
    this.canvas = this.el.querySelector("canvas")!;
    this.ro = new ResizeObserver(() => this.draw());
    this.ro.observe(this.el.querySelector(".tchart")!);
    registry.add(this);
    this.bind();
  }

  get color() { return this.data ? seriesColor(endpointKey(this.data.target)) : C.amber; }
  get points(): SeriesPoint[] { return this.data?.series?.points ?? []; }
  get step(): number { return Math.max(1, this.data?.series?.stepSec ?? 2); }

  update(d: ChartData) {
    this.data = d;
    this.el.classList.toggle("axis", d.showAxis);
    const l = d.live, t = d.target, col = this.color;
    const down = l?.state === "down";
    const mini = (lab: string, v: string, warn = false) => `<div class="mini"><span>${lab}</span><b${warn ? ' class="red"' : ""}>${v}</b></div>`;
    const u24 = d.summary?.find((w) => w.label === "24h")?.uptimePct;
    const foot = l?.pathHint
      ? `<div class="hint" style="color:${l.pathHint === "relay" ? C.amber : C.cyan}">TAILSCALE · ${esc(l.pathHint.toUpperCase())}</div>`
      : u24 != null ? `<div class="hint">24H UPTIME ${uptime(u24)}</div>` : "";
    this.info.innerHTML = `
      <div class="row gap6">${dot(stateColor(l?.state))}<span class="tname" title="${esc(t.name)}">${esc(t.name)}</span></div>
      <div class="row gap4"><span class="ktag" style="color:${col};border-color:${col}b3">${kindLabel(t.kind)}</span><span class="ep" title="${esc(endpointLabel(t))}">${esc(endpointLabel(t))}</span></div>
      <div class="big" style="color:${down ? C.red : latencyColor(l?.lastRttMs)}">${down ? "DOWN" : ms(l?.lastRttMs)}${down ? "" : '<small>ms</small>'}</div>
      <div class="row gap10">${mini("AVG", ms(l?.avgMs))}${mini("P95", ms(l?.p95Ms))}${mini("JIT", ms(l?.jitterMs))}${mini("LOSS", pct(l?.lossPct), (l?.lossPct ?? 0) > 0)}</div>
      ${foot}`;
    this.draw();
  }

  private yMax(): number {
    const pts = this.points;
    const vals = pts.map((p) => p.p95 ?? p.avg).filter((v): v is number => v != null);
    const m = vals.length ? Math.max(...vals) : 20;
    const maxes = pts.map((p) => p.max).filter((v): v is number => v != null);
    const top = maxes.length ? Math.max(...maxes) : m;
    return Math.max(10, Math.min(top, m * 2.5)) * 1.15;
  }

  private geom(w: number, h: number) {
    const left = 4, right = 40, top = 8, bottom = this.data?.showAxis ? 20 : 6;
    return { x0: left, x1: w - right, y0: top, y1: h - bottom };
  }

  private xToT(x: number): number {
    const r = this.canvas.getBoundingClientRect();
    const g = this.geom(r.width, r.height);
    const [d0, d1] = model.domain;
    const f = Math.min(1, Math.max(0, (x - g.x0) / (g.x1 - g.x0)));
    return d0 + f * (d1 - d0);
  }

  private bind() {
    const c = this.canvas;
    c.addEventListener("mousemove", (e) => {
      const t = this.xToT(e.offsetX);
      if (drag && drag.chart === this) { drag.b = t; }
      model.hoverT = t;
      redrawAll();
    });
    c.addEventListener("mouseleave", () => { model.hoverT = null; redrawAll(); });
    c.addEventListener("mousedown", (e) => {
      if (e.button !== 0) return;
      const t = this.xToT(e.offsetX);
      drag = { chart: this, a: t, b: t };
    });
    c.addEventListener("dblclick", () => { drag = null; model.setZoom(null); });
  }

  /** Draws into the on-screen canvas (or into `ctx` for export). */
  draw(target?: { ctx: CanvasRenderingContext2D; w: number; h: number }) {
    let ctx: CanvasRenderingContext2D, w: number, h: number;
    if (target) ({ ctx, w, h } = target);
    else {
      const r = this.canvas.parentElement!.getBoundingClientRect();
      w = Math.max(50, Math.floor(r.width)); h = Math.max(40, Math.floor(r.height));
      const dpr = window.devicePixelRatio || 1;
      if (this.canvas.width !== Math.round(w * dpr) || this.canvas.height !== Math.round(h * dpr)) {
        this.canvas.width = Math.round(w * dpr); this.canvas.height = Math.round(h * dpr);
        this.canvas.style.width = w + "px"; this.canvas.style.height = h + "px";
      }
      ctx = this.canvas.getContext("2d")!;
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    }
    ctx.clearRect(0, 0, w, h);
    if (!this.data) return;
    const g = this.geom(w, h);
    const [d0, d1] = model.domain;
    const span = Math.max(1, d1 - d0);
    const yMax = this.yMax();
    const X = (t: number) => g.x0 + ((t - d0) / span) * (g.x1 - g.x0);
    const Y = (v: number) => g.y1 - (Math.min(v, yMax * 1.5) / yMax) * (g.y1 - g.y0);
    const col = this.color;
    ctx.font = `9px ${MONO}`;

    // grid + y labels
    const ys = niceStep(yMax, 3);
    ctx.strokeStyle = C.line; ctx.lineWidth = 0.5; ctx.fillStyle = C.faint; ctx.textBaseline = "middle"; ctx.textAlign = "left";
    for (let v = 0; v <= yMax; v += ys) {
      const y = Math.round(Y(v)) + 0.5;
      ctx.beginPath(); ctx.moveTo(g.x0, y); ctx.lineTo(g.x1, y); ctx.stroke();
      ctx.fillText(ms(v), g.x1 + 5, y);
    }
    // x grid + labels
    const ts = niceTimeStep(span, 6);
    const tz = new Date().getTimezoneOffset() * 60;
    ctx.strokeStyle = C.line + "99"; ctx.textAlign = "center"; ctx.textBaseline = "top";
    for (let t = Math.ceil((d0 - tz) / ts) * ts + tz; t <= d1; t += ts) {
      const x = Math.round(X(t)) + 0.5;
      ctx.beginPath(); ctx.moveTo(x, g.y0); ctx.lineTo(x, g.y1); ctx.stroke();
      if (this.data.showAxis) ctx.fillText(timeLabel(t, span), Math.min(Math.max(x, g.x0 + 24), g.x1 - 24), g.y1 + 5);
    }

    ctx.save();
    ctx.beginPath(); ctx.rect(g.x0, 0, g.x1 - g.x0, h); ctx.clip();
    const pts = this.points.filter((p) => p.t + this.step >= d0 && p.t <= d1);
    const step = this.step;
    const gap = Math.max(step * 2.5, 5);

    // split into contiguous runs (gaps break band + line)
    const runs: SeriesPoint[][] = [];
    let cur: SeriesPoint[] = [];
    let prevT = -Infinity;
    for (const p of pts) {
      if (p.avg == null || p.t - prevT > gap) { if (cur.length) runs.push(cur); cur = []; }
      if (p.avg != null) cur.push(p);
      prevT = p.avg != null ? p.t : prevT;
    }
    if (cur.length) runs.push(cur);

    // band
    ctx.fillStyle = col + "24";
    for (const r of runs) {
      const rr = r.filter((p) => p.min != null && p.max != null);
      if (!rr.length) continue;
      ctx.beginPath();
      rr.forEach((p, i) => (i ? ctx.lineTo(X(p.t), Y(p.max!)) : ctx.moveTo(X(p.t), Y(p.max!))));
      if (rr.length === 1) ctx.lineTo(X(rr[0].t + step), Y(rr[0].max!));
      for (let i = rr.length - 1; i >= 0; i--) ctx.lineTo(X(rr[i].t), Y(rr[i].min!));
      if (rr.length === 1) ctx.lineTo(X(rr[0].t + step), Y(rr[0].min!));
      ctx.closePath(); ctx.fill();
    }
    // loss shading
    for (const p of pts) {
      const l = p.loss ?? 0;
      if (l <= 0) continue;
      const x = X(p.t), wpx = Math.max(1, X(p.t + step) - x);
      if (l >= 99.9) { ctx.fillStyle = "rgba(255,77,79,0.28)"; ctx.fillRect(x, g.y0, wpx, g.y1 - g.y0); }
      else {
        ctx.fillStyle = `rgba(255,77,79,${Math.min(0.9, 0.3 + l / 100)})`;
        const bh = (g.y1 - g.y0) * 0.14;
        ctx.fillRect(x, g.y1 - bh, wpx, bh);
      }
    }
    // avg line
    ctx.strokeStyle = col; ctx.lineWidth = 1.6; ctx.lineJoin = "round";
    for (const r of runs) {
      ctx.beginPath();
      r.forEach((p, i) => (i ? ctx.lineTo(X(p.t), Y(p.avg!)) : ctx.moveTo(X(p.t), Y(p.avg!))));
      if (r.length === 1) ctx.lineTo(X(r[0].t) + 2, Y(r[0].avg!));
      ctx.stroke();
    }
    // drag selection
    if (!target && drag && drag.chart === this) {
      const a = X(Math.min(drag.a, drag.b)), b = X(Math.max(drag.a, drag.b));
      ctx.fillStyle = "rgba(255,176,0,0.18)"; ctx.fillRect(a, g.y0, b - a, g.y1 - g.y0);
      ctx.strokeStyle = C.amber; ctx.lineWidth = 1; ctx.strokeRect(a + 0.5, g.y0 + 0.5, b - a - 1, g.y1 - g.y0 - 1);
    }
    ctx.restore();

    if (!pts.length) {
      ctx.fillStyle = C.faint; ctx.font = `600 10px ${MONO}`; ctx.textAlign = "center"; ctx.textBaseline = "middle";
      ctx.fillText(this.data.series ? "NO DATA IN RANGE" : "LOADING…", (g.x0 + g.x1) / 2, (g.y0 + g.y1) / 2);
    }

    // shared crosshair + tooltip
    const hv = model.hoverT;
    if (!target && hv != null && hv >= d0 && hv <= d1) {
      const x = Math.round(X(hv)) + 0.5;
      ctx.strokeStyle = "rgba(232,234,237,0.5)"; ctx.lineWidth = 1;
      ctx.beginPath(); ctx.moveTo(x, g.y0); ctx.lineTo(x, g.y1); ctx.stroke();
      const p = this.nearest(hv);
      const lines: [string, string][] = [];
      lines.push([new Date((p?.t ?? hv) * 1000).toLocaleTimeString(), C.dim]);
      if (p) {
        if (p.avg != null) lines.push([`avg ${ms(p.avg)}  max ${ms(p.max)} ms`, C.text]); else lines.push(["no response", C.red]);
        if ((p.loss ?? 0) > 0) lines.push([`loss ${pct(p.loss)}`, C.red]);
        if (p.avg != null) { ctx.fillStyle = col; ctx.beginPath(); ctx.arc(X(p.t), Y(p.avg), 2.5, 0, Math.PI * 2); ctx.fill(); }
      }
      ctx.font = `9px ${MONO}`;
      const tw = Math.max(...lines.map(([s]) => ctx.measureText(s).width)) + 10;
      const th = lines.length * 11 + 6;
      let bx = x + 8;
      if (bx + tw > g.x1) bx = x - 8 - tw;
      const by = g.y0;
      ctx.fillStyle = "rgba(13,15,18,0.95)"; ctx.fillRect(bx, by, tw, th);
      ctx.strokeStyle = C.line; ctx.strokeRect(bx + 0.5, by + 0.5, tw - 1, th - 1);
      ctx.textAlign = "left"; ctx.textBaseline = "top";
      lines.forEach(([s, c], i) => { ctx.fillStyle = c; ctx.fillText(s, bx + 5, by + 4 + i * 11); });
    }
  }

  nearest(t: number): SeriesPoint | undefined {
    let best: SeriesPoint | undefined, bd = Infinity;
    for (const p of this.points) { const d = Math.abs(p.t - t); if (d < bd) { bd = d; best = p; } }
    return best && bd <= Math.max(this.step * 2, 5) ? best : undefined;
  }

  /** Renders the row (info text + chart) into an export canvas context at (0, y). */
  exportInto(ctx: CanvasRenderingContext2D, y: number, w: number, h: number) {
    if (!this.data) return;
    const d = this.data, l = d.live;
    ctx.save();
    ctx.translate(0, y);
    ctx.fillStyle = C.panel; ctx.fillRect(0, 0, w, h);
    ctx.fillStyle = stateColor(l?.state); ctx.beginPath(); ctx.arc(16, 18, 4, 0, Math.PI * 2); ctx.fill();
    ctx.fillStyle = C.text; ctx.font = `600 12px system-ui, sans-serif`; ctx.textBaseline = "middle"; ctx.textAlign = "left";
    ctx.fillText(d.target.name.slice(0, 26), 28, 18);
    ctx.fillStyle = this.color; ctx.font = `bold 8px ${MONO}`; ctx.fillText(kindLabel(d.target.kind), 12, 36);
    ctx.fillStyle = C.faint; ctx.font = `9px ${MONO}`; ctx.fillText(endpointLabel(d.target).slice(0, 28), 44, 36);
    const down = l?.state === "down";
    ctx.fillStyle = down ? C.red : latencyColor(l?.lastRttMs); ctx.font = `bold 20px ${MONO}`;
    ctx.fillText(down ? "DOWN" : `${ms(l?.lastRttMs)} ms`, 12, 60);
    ctx.fillStyle = C.dim; ctx.font = `9px ${MONO}`;
    ctx.fillText(`AVG ${ms(l?.avgMs)}  P95 ${ms(l?.p95Ms)}  LOSS ${pct(l?.lossPct)}`, 12, 84);
    ctx.fillStyle = C.line; ctx.fillRect(200, 0, 1, h);
    ctx.translate(206, 0);
    this.draw({ ctx, w: w - 216, h });
    ctx.restore();
  }

  destroy() { this.ro.disconnect(); registry.delete(this); this.el.remove(); }
}

/** A persistent stack of TargetCharts for one node (keyed by target id). */
export class ChartStack {
  el: HTMLElement;
  private charts = new Map<string, TargetChart>();
  constructor() {
    this.el = document.createElement("div");
    this.el.className = "stack";
  }
  update(rows: ChartData[]) {
    const want = new Set(rows.map((r) => r.target.id));
    for (const [id, c] of this.charts) if (!want.has(id)) { c.destroy(); this.charts.delete(id); }
    rows.forEach((r, i) => {
      let c = this.charts.get(r.target.id);
      if (!c) { c = new TargetChart(); c.keep = true; this.charts.set(r.target.id, c); }
      if (this.el.children[i] !== c.el) this.el.insertBefore(c.el, this.el.children[i] ?? null);
      c.update(r);
    });
  }
  list(): TargetChart[] { return [...this.charts.values()]; }
  destroy() { for (const c of this.charts.values()) c.destroy(); this.charts.clear(); this.el.remove(); }

  /** PNG data URL of the whole stack (2× scale). */
  toPNG(title: string): string {
    const rows = [...this.el.children].map((e) => this.list().find((c) => c.el === e)).filter(Boolean) as TargetChart[];
    const W = 1200, RH = 110, head = 44, scale = 2;
    const cv = document.createElement("canvas");
    cv.width = W * scale; cv.height = (head + rows.length * (RH + 1) + 30) * scale;
    const ctx = cv.getContext("2d")!;
    ctx.scale(scale, scale);
    ctx.fillStyle = C.bg; ctx.fillRect(0, 0, W, cv.height / scale);
    ctx.fillStyle = C.amber; ctx.font = `bold 11px ${MONO}`; ctx.textBaseline = "top";
    ctx.fillText("INTERNET MONITOR", 12, 10);
    ctx.fillStyle = C.text; ctx.font = `600 14px system-ui, sans-serif`;
    const [d0, d1] = model.domain;
    ctx.fillText(`${title} · ${new Date(d0 * 1000).toLocaleString()} → ${new Date(d1 * 1000).toLocaleString()}`, 12, 24);
    rows.forEach((r, i) => r.exportInto(ctx, head + i * (RH + 1), W, RH));
    return cv.toDataURL("image/png");
  }
}

/** Small sparkline / bar helpers used by diagnostics. */
export function drawSpark(canvas: HTMLCanvasElement, vals: [number, number][], lo: number, hi: number, color: string) {
  const r = canvas.getBoundingClientRect();
  const dpr = window.devicePixelRatio || 1;
  canvas.width = Math.max(1, r.width * dpr); canvas.height = Math.max(1, r.height * dpr);
  const ctx = canvas.getContext("2d")!;
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, r.width, r.height);
  if (vals.length < 2) return;
  const t0 = vals[0][0], t1 = vals[vals.length - 1][0];
  ctx.strokeStyle = color; ctx.lineWidth = 1.3; ctx.beginPath();
  vals.forEach(([t, v], i) => {
    const x = ((t - t0) / Math.max(1, t1 - t0)) * r.width;
    const y = r.height - ((Math.min(hi, Math.max(lo, v)) - lo) / (hi - lo)) * r.height;
    i ? ctx.lineTo(x, y) : ctx.moveTo(x, y);
  });
  ctx.stroke();
}

export function drawBars(canvas: HTMLCanvasElement, vals: [number, number][], color: string) {
  const r = canvas.getBoundingClientRect();
  const dpr = window.devicePixelRatio || 1;
  canvas.width = Math.max(1, r.width * dpr); canvas.height = Math.max(1, r.height * dpr);
  const ctx = canvas.getContext("2d")!;
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, r.width, r.height);
  if (!vals.length) return;
  const max = Math.max(...vals.map((v) => v[1]), 1) * 1.1;
  const t0 = Math.min(...vals.map((v) => v[0])), t1 = Math.max(...vals.map((v) => v[0]));
  ctx.font = `9px ${MONO}`; ctx.fillStyle = C.faint; ctx.textBaseline = "top";
  ctx.fillText(`${max.toFixed(0)} Mbps`, 2, 2);
  ctx.fillStyle = color;
  for (const [t, v] of vals) {
    const x = t1 === t0 ? r.width / 2 : 8 + ((t - t0) / (t1 - t0)) * (r.width - 22);
    const hh = (v / max) * (r.height - 14);
    ctx.fillRect(x, r.height - hh, 6, hh);
  }
  ctx.strokeStyle = C.line; ctx.beginPath(); ctx.moveTo(0, r.height - 0.5); ctx.lineTo(r.width, r.height - 0.5); ctx.stroke();
}
