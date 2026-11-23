import { errText } from "../api";
import { drawBars, drawSpark } from "../chart";
import { dot, esc, patch, spinner, type Handlers } from "../dom";
import { C, latencyColor, ms, uptime } from "../fmt";
import type { NodeState } from "../state";
import type { Hop, TraceResult } from "../types";
import { panel } from "./common";
import type { SubView } from "./node";

const WINDOWS = ["1h", "24h", "7d", "30d", "60d"];

export function slaTable(n: NodeState): string {
  const head = `<tr><th class="l">TARGET</th>${WINDOWS.map((w) => `<th>${w.toUpperCase()}</th>`).join("")}</tr>`;
  const rows = n.targets.map((t) => `<tr><td class="l ell">${esc(t.name)}</td>${WINDOWS.map((lab) => {
    const w = n.summaries[t.id]?.find((x) => x.label === lab);
    const col = (w?.uptimePct ?? 100) < 99 ? C.amber : C.green;
    return `<td><div style="color:${col}">${uptime(w?.uptimePct)}</div><div class="mono8 faint">p95 ${ms(w?.p95Ms)} · avg ${ms(w?.avgMs)}</div></td>`;
  }).join("")}</tr>`).join("");
  return panel("SLA / uptime per target", `<table class="sla">${head}${rows}</table>${n.targets.length ? "" : `<div class="faint small">No targets</div>`}`);
}

function hopLabel(h: Hop) {
  if (!h.ip) return "* * *";
  return h.name ? `${h.name} (${h.ip})` : h.ip;
}
const hopAvg = (h: Hop) => { const v = h.rttMs.filter((x): x is number => x != null); return v.length ? v.reduce((a, b) => a + b, 0) / v.length : null; };

export function diagnosticsTab(n: NodeState): SubView {
  const el = document.createElement("div");
  el.className = "scroll fill";
  el.dataset.scroll = "diag";
  let traceHost = "1.1.1.1";
  let trace: TraceResult | null = null;
  let tracing = false, traceError = "";
  let speeding = false, speedError = "";

  const kv = (k: string, v: string) => `<div class="kv"><span>${esc(k.toUpperCase())}</span><span class="sel">${esc(v)}</span></div>`;

  const system = () => {
    const s = n.system;
    const w = s?.wifi;
    const wifi = w ? kv("Wi-Fi", `${w.ssid ?? w.iface ?? "wlan"} · ${w.rssiDbm != null ? w.rssiDbm + " dBm" : "—"} · quality ${w.linkQuality != null ? w.linkQuality + "%" : "—"}`)
      + (n.rssi.length > 2 ? `<canvas class="spark" data-spark="rssi"></canvas>` : "") : "";
    return panel("System", `<div class="col gap8">
      ${kv("Model", s?.model ?? s?.hostname ?? "—")}
      ${kv("OS", `${s?.os ?? "—"} ${s?.kernel ?? ""} ${s?.arch ?? ""}`)}
      ${kv("Load", s?.loadAvg?.map((x) => x.toFixed(2)).join(" ") ?? "—")}
      ${kv("Memory", s?.memTotalMB != null ? `${s.memUsedMB ?? 0} / ${s.memTotalMB} MB` : "—")}
      ${kv("Disk free", s?.diskFreeMB != null ? `${s.diskFreeMB} MB` : "—")}
      ${kv("Temperature", s?.tempC != null ? `${s.tempC.toFixed(1)} °C` : "—")}
      ${kv("Gateway", `${s?.gateway ?? "—"} via ${s?.defaultInterface ?? "—"}`)}
      ${wifi}</div>`, "", "grow");
  };

  const ifaces = () => {
    const s = n.system;
    const list = (s?.interfaces ?? []).map((i) => `<div class="row gap8">${dot(i.up ? C.green : C.faint, 6)}
      <b class="mono11 w70">${esc(i.name)}</b><span class="mono9 w70" style="color:${i.kind === "tailscale" ? C.cyan : C.dim}">${esc((i.kind ?? "").toUpperCase())}</span>
      <span class="mono10 dim ell grow">${esc((i.addrs ?? []).join(", "))}</span>${i.name === s?.defaultInterface ? `<span class="mono8 amber bold">DEFAULT</span>` : ""}</div>`).join("");
    const ts = s?.tailscale ? `<div class="hr"></div><div class="mono9 faint bold">TAILSCALE PEERS FROM THIS NODE${s.tailscale.selfIp ? ` · SELF ${esc(s.tailscale.selfIp)}` : ""}</div>` +
      (s.tailscale.peers ?? []).map((p) => {
        const path = p.path ?? (p.relay ? "relay" : null);
        const pathHtml = p.online && path ? `<span class="mono9 bold" style="color:${path === "relay" ? C.amber : C.green}">${esc(path.toUpperCase())}${path === "relay" && p.relay ? " · " + esc(p.relay) : ""}</span>` : (p.online ? "" : `<span class="mono9 faint">OFFLINE</span>`);
        return `<div class="row gap8">${dot(p.online ? C.green : C.faint, 6)}<span class="mono11 w150 ell">${esc(p.name)}</span><span class="mono10 dim w110">${esc(p.ip ?? "")}</span>${pathHtml}</div>`;
      }).join("") : "";
    return panel("Network paths", `<div class="col gap8">${list}${ts}${s ? "" : `<span class="faint">Waiting for system info…</span>`}</div>`, "", "grow");
  };

  const tracePanel = () => {
    const picks = n.targets.filter((t) => t.host && t.host !== "@gateway").map((t) => `<option value="${esc(t.host)}">${esc(t.name)}</option>`).join("");
    let hops = "";
    if (trace) {
      const max = Math.max(1, ...trace.hops.map(hopAvg).filter((v): v is number => v != null));
      hops = `<div class="col gap4 mt8">${trace.hops.map((h) => {
        const a = hopAvg(h);
        return `<div class="row gap10 hop"><span class="mono10 faint bold w22 r">${h.ttl}</span><span class="mono11 w300 ell">${esc(hopLabel(h))}</span>
          <div class="hopbar grow"><div style="width:${a != null ? Math.max(0.5, (a / max) * 100) : 0}%;background:${latencyColor(a)}"></div></div>
          <span class="mono11 w80 r">${a != null ? ms(a) + " ms" : "—"}</span><span class="mono9 faint w140 r">${h.rttMs.map((x) => (x == null ? "*" : ms(x))).join(" / ")}</span></div>`;
      }).join("")}</div>`;
    }
    return panel("Traceroute", `<div class="row gap8">
        <input class="field w260" data-k="trace" data-in="trace-host" data-enter="trace" value="${esc(traceHost)}" placeholder="host">
        <select class="field" data-in="trace-pick"><option value="">PICK TARGET</option>${picks}</select>
        <button class="btn primary" data-act="trace" ${tracing || !traceHost ? "disabled" : ""}>${tracing ? "TRACING…" : "TRACE"}</button>
        ${tracing ? spinner() : ""}${traceError ? `<span class="small red">${esc(traceError)}</span>` : ""}
      </div>${hops}`, `<span class="mono9 faint">FROM ${esc(n.name.toUpperCase())}</span>`);
  };

  const speedPanel = () => {
    const ok = n.speed.filter((s) => !s.error);
    const latest = ok[0];
    const body = n.speed.length === 0
      ? `<span class="small faint">No speed tests yet. Enable scheduled tests under Control, or run one now.</span>`
      : `<div class="row gap30">${latest ? `<div class="stat"><span>LATEST DOWN</span><b style="color:${C.cyan}">${(latest.downMbps ?? 0).toFixed(1)}<small>Mbps</small></b></div>
          <div class="stat"><span>LATENCY</span><b>${ms(latest.latencyMs)}<small>ms</small></b></div>` : ""}</div><canvas class="bars" data-bars="speed"></canvas>`;
    return panel("Bandwidth", `${speedError ? `<div class="small red">${esc(speedError)}</div>` : ""}${body}`,
      `<button class="btn" data-act="speed" ${speeding ? "disabled" : ""}>${speeding ? "RUNNING…" : "RUN SPEED TEST"}</button>`);
  };

  const update = () => {
    const a = document.activeElement;
    if (a && a.tagName === "SELECT" && el.contains(a)) return; // don't close an open dropdown
    patch(el, `<div class="pad col gap16">${slaTable(n)}<div class="row gap16 top">${system()}${ifaces()}</div>${tracePanel()}${speedPanel()}</div>`);
    const sp = el.querySelector<HTMLCanvasElement>('[data-spark="rssi"]');
    if (sp) drawSpark(sp, n.rssi, -100, -30, C.cyan);
    const bc = el.querySelector<HTMLCanvasElement>('[data-bars="speed"]');
    if (bc) drawBars(bc, n.speed.filter((s) => !s.error).map((s) => [s.at, s.downMbps ?? 0]), C.cyan);
  };

  const runTrace = async () => {
    if (!traceHost.trim() || tracing) return;
    tracing = true; traceError = ""; update();
    try { trace = await n.agent.traceroute(traceHost.trim()); } catch (e) { traceError = errText(e); }
    tracing = false; update();
  };

  const handlers = (): Handlers => ({
    "trace-host": (e) => { traceHost = (e as HTMLInputElement).value; },
    "trace-pick": (e) => { const v = (e as HTMLSelectElement).value; if (v) { traceHost = v; update(); } },
    trace: () => runTrace(),
    speed: async () => {
      speeding = true; speedError = ""; update();
      try { await n.agent.speedtest(); n.speed = (await n.agent.speedHistory()) ?? n.speed; } catch (e) { speedError = errText(e); }
      speeding = false; update();
    },
  });
  return { el, update, handlers };
}
