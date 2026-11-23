// Formatting, colours and small pure helpers (mirrors IMCore/Stats.swift + Theme.swift).
import type { LinkState, Series, Target } from "./types";

export const C = {
  bg: "#0D0F12",
  panel: "#14181D",
  panel2: "#1A1F26",
  line: "#2A313B",
  text: "#E8EAED",
  dim: "#8A94A0",
  faint: "#56606B",
  amber: "#FFB000",
  green: "#39D98A",
  red: "#FF4D4F",
  cyan: "#2BD9FE",
  lime: "#A3E635",
};

export const SERIES = ["#FFB000", "#2BD9FE", "#A3E635", "#F472B6", "#A78BFA", "#FB923C", "#2DD4BF", "#F87171"];

export const MONO = `ui-monospace, "SF Mono", "Cascadia Mono", Consolas, "DejaVu Sans Mono", "Liberation Mono", monospace`;

const isNum = (v: unknown): v is number => typeof v === "number" && Number.isFinite(v);

export function ms(v: number | null | undefined): string {
  if (!isNum(v)) return "—";
  if (v >= 1000) return (v / 1000).toFixed(1) + "s";
  if (v >= 100) return v.toFixed(0);
  if (v >= 10) return v.toFixed(1);
  return v.toFixed(2);
}

export function pct(v: number | null | undefined): string {
  if (!isNum(v)) return "—";
  return v === 0 || v >= 10 ? v.toFixed(0) + "%" : v.toFixed(1) + "%";
}

export function uptime(v: number | null | undefined): string {
  if (!isNum(v)) return "—";
  return v >= 99.995 ? "100%" : v.toFixed(2) + "%";
}

export function duration(sec: number): string {
  const t = Math.max(0, Math.floor(sec));
  if (t < 60) return `${t}s`;
  if (t < 3600) return `${Math.floor(t / 60)}m ${t % 60}s`;
  if (t < 86400) return `${Math.floor(t / 3600)}h ${Math.floor((t % 3600) / 60)}m`;
  return `${Math.floor(t / 86400)}d ${Math.floor((t % 86400) / 3600)}h`;
}

export function relTime(unix: number, now = Date.now() / 1000): string {
  const d = now - unix;
  if (d < 5) return "just now";
  if (d < 60) return `${Math.floor(d)}s ago`;
  if (d < 3600) return `${Math.floor(d / 60)} min ago`;
  if (d < 86400) return `${Math.floor(d / 3600)} h ago`;
  return `${Math.floor(d / 86400)} d ago`;
}

export function dateTime(unix: number): string {
  return new Date(unix * 1000).toLocaleString(undefined, { year: "numeric", month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

export const severity: Record<LinkState, number> = { up: 0, unknown: 1, degraded: 2, down: 3 };

export function worstOf(states: LinkState[]): LinkState {
  let w: LinkState | null = null;
  for (const s of states) if (w === null || severity[s] > severity[w]) w = s;
  return w ?? "unknown";
}

export function stateColor(s: LinkState | undefined): string {
  switch (s) {
    case "up": return C.green;
    case "degraded": return C.amber;
    case "down": return C.red;
    default: return C.faint;
  }
}

/** Latency heat colour: green → lime → amber → red. */
export function latencyColor(v: number | null | undefined): string {
  if (!isNum(v)) return C.faint;
  if (v < 40) return C.green;
  if (v < 100) return C.lime;
  if (v < 200) return C.amber;
  return C.red;
}

/** FNV-1a 64-bit slot, identical to Palette.slot in the Swift app so colours match across apps. */
export function paletteSlot(key: string, count: number): number {
  let h = 1469598103934665603n; // same (non-standard) basis as Palette.slot in Stats.swift
  const prime = 0x100000001b3n;
  for (const b of new TextEncoder().encode(key)) {
    h ^= BigInt(b);
    h = (h * prime) & 0xffffffffffffffffn;
  }
  return Number(h % BigInt(Math.max(count, 1)));
}

export function seriesColor(key: string): string {
  return SERIES[paletteSlot(key, SERIES.length)];
}

export function kindLabel(k: string): string {
  return ({ icmp: "PING", tcp: "TCP", http: "HTTP", dns: "DNS" } as Record<string, string>)[k] ?? k.toUpperCase();
}

/** "What is being measured", independent of which node measures it. */
export function endpointKey(t: Target): string {
  return `${t.kind}|${(t.host ?? t.url ?? "").toLowerCase()}|${t.port ?? 0}`;
}

export function endpointLabel(t: Target): string {
  switch (t.kind) {
    case "icmp": return t.host ?? "?";
    case "tcp": return `${t.host ?? "?"}:${t.port ?? 0}`;
    case "http": return t.url ?? t.host ?? "?";
    case "dns": return `dns ${t.host ?? "?"}`;
    default: return t.host ?? "?";
  }
}

export function isTailscaleIP(ip: string): boolean {
  const p = ip.split(".").map(Number);
  if (p.length !== 4 || p.some((x) => !Number.isInteger(x) || x < 0 || x > 255)) return false;
  return p[0] === 100 && p[1] >= 64 && p[1] <= 127;
}

export function ipKey(ip: string): number {
  const p = ip.split(".").map(Number);
  if (p.length !== 4 || p.some((x) => !Number.isInteger(x))) return 0;
  return ((p[0] << 24) >>> 0) + (p[1] << 16) + (p[2] << 8) + p[3];
}

export function csvEscape(s: string): string {
  return /[",\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
}

export function seriesCSV(rows: { node: string; target: string; series: Series }[]): string {
  let out = "node,target,time_iso,time_unix,samples,loss_pct,min_ms,avg_ms,max_ms,p95_ms\n";
  const f = (v: number | null | undefined) => (isNum(v) ? v.toFixed(3) : "");
  for (const r of rows) {
    for (const p of r.series.points) {
      out += [csvEscape(r.node), csvEscape(r.target), new Date(p.t * 1000).toISOString().replace(".000Z", "Z"), String(p.t), p.n != null ? String(p.n) : "",
        f(p.loss), f(p.min), f(p.avg), f(p.max), f(p.p95)].join(",") + "\n";
    }
  }
  return out;
}

export function safeFileName(s: string): string {
  return s.replace(/[\\/:*?"<>|]+/g, "_").trim() || "export";
}

export function mean(v: number[]): number | null {
  return v.length ? v.reduce((a, b) => a + b, 0) / v.length : null;
}
