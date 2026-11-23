// Pure state for the setup wizard's Targets grid (mirrors WizardModel.swift buildRows & co).
import { endpointKey } from "./fmt";
import { SVC_PREFIX } from "./services";
import type { AgentConfig, Candidate, Identity, ProbeKind, Target } from "./types";

export type Phase = "checking" | "noAgent" | "needsCode" | "working" | "ready" | "failed";

export interface Conn {
  phase: Phase;
  identity?: Identity | null;
  token?: string;
  sshUser: string;
  code: string;
  message: string;
  log: string[];
  recognised: boolean;
  config?: AgentConfig;
}

export const newConn = (): Conn => ({ phase: "checking", sshUser: "", code: "", message: "", log: [], recognised: false });

export type Section = "Nodes" | "Baseline" | "Existing" | "Custom";

export interface TargetRow {
  id: string;
  template: Target;
  section: Section;
  /** node rows cannot be selected on their own column */
  forNodeIP?: string;
}

export interface WCandidate extends Candidate {
  port?: number;
}

export const cellKey = (rowId: string, ip: string) => `${rowId}@@${ip}`;

/** Service targets (`svc-*`) belong to the Services page: hidden from the grid, always preserved. */
const gridTargets = (c: AgentConfig | undefined): Target[] => (c?.targets ?? []).filter((t) => !t.id.startsWith(SVC_PREFIX));

function displayName(id: Identity | null | undefined): string | undefined {
  if (!id) return undefined;
  return id.nodeName || id.hostname || id.nodeId;
}

export function buildRows(candidates: WCandidate[], selected: Set<string>, conns: Record<string, Conn>, editing: boolean): { rows: TargetRow[]; grid: Set<string> } {
  const out: TargetRow[] = [];
  const sel = candidates.filter((c) => selected.has(c.ip));
  for (const c of sel) {
    const ident = conns[c.ip]?.identity;
    const nid = ident?.nodeId ?? c.ip;
    out.push({
      id: `node-${nid}`,
      template: { id: `node-${nid}`, name: displayName(ident) ?? c.name, kind: "icmp", host: c.ip, enabled: true, group: "node", nodeRef: ident?.nodeId ?? null },
      section: "Nodes",
      forNodeIP: c.ip,
    });
  }
  const base = (id: string, name: string, host: string): TargetRow => ({ id, template: { id, name, kind: "icmp", host, enabled: true, group: "baseline" }, section: "Baseline" });
  out.push(base("base-gw", "Gateway", "@gateway"), base("base-cf", "Cloudflare DNS", "1.1.1.1"), base("base-g", "Google DNS", "8.8.8.8"));

  let g = new Set<string>();
  let anyExisting = false;
  for (const c of sel) {
    const cfg = conns[c.ip]?.config;
    if (!cfg) continue;
    for (const t of gridTargets(cfg)) {
      anyExisting = true;
      const r = out.find((x) => x.id === t.id || endpointKey(x.template) === endpointKey(t));
      if (r) g.add(cellKey(r.id, c.ip));
      else { out.push({ id: t.id, template: t, section: t.group === "custom" ? "Custom" : "Existing" }); g.add(cellKey(t.id, c.ip)); }
    }
  }
  if (!anyExisting || (!editing && sel.every((c) => gridTargets(conns[c.ip]?.config).length <= 3))) {
    // fresh agents (only built-in defaults) → everyone pings everyone else + baselines
    g = new Set();
    for (const r of out) if (r.section === "Nodes" || r.section === "Baseline") for (const c of sel) if (r.forNodeIP !== c.ip) g.add(cellKey(r.id, c.ip));
    for (const c of sel) for (const t of gridTargets(conns[c.ip]?.config)) {
      if (!out.some((x) => endpointKey(x.template) === endpointKey(t))) { out.push({ id: t.id, template: t, section: "Existing" }); g.add(cellKey(t.id, c.ip)); }
    }
  }
  return { rows: out, grid: g };
}

export function toggleCell(grid: Set<string>, row: TargetRow, ip: string) {
  if (row.forNodeIP === ip) return;
  const k = cellKey(row.id, ip);
  if (grid.has(k)) grid.delete(k); else grid.add(k);
}

export function setColumn(grid: Set<string>, rows: TargetRow[], ip: string, on: boolean) {
  for (const r of rows) if (r.forNodeIP !== ip) { const k = cellKey(r.id, ip); if (on) grid.add(k); else grid.delete(k); }
}

export function setRow(grid: Set<string>, row: TargetRow, ips: Iterable<string>, on: boolean) {
  for (const ip of ips) if (row.forNodeIP !== ip) { const k = cellKey(row.id, ip); if (on) grid.add(k); else grid.delete(k); }
}

export function customTarget(kind: ProbeKind, name: string, host: string, port: number | null): Target | null {
  const h = host.trim();
  if (!h) return null;
  const id = "custom-" + Math.random().toString(36).slice(2, 10);
  const t: Target = { id, name: name.trim() || h, kind, enabled: true, group: "custom" };
  if (kind === "http") {
    t.url = /^https?:\/\//i.test(h) ? h : `https://${h}`;
    try { t.host = new URL(t.url).hostname; } catch { t.host = null; }
  } else if (kind === "tcp") { t.host = h; t.port = port ?? 443; }
  else t.host = h;
  return t;
}

export function targetsFor(rows: TargetRow[], grid: Set<string>, ip: string, old: Target[]): Target[] {
  const picked = rows.filter((r) => grid.has(cellKey(r.id, ip))).map((r) => {
    const t = { ...r.template };
    const prev = old.find((o) => o.id === t.id);
    if (prev) t.intervalMs = prev.intervalMs; // keep existing per-target interval overrides
    return t;
  });
  return [...picked, ...old.filter((t) => t.id.startsWith(SVC_PREFIX) && !picked.some((p) => p.id === t.id))];
}

/** "host", "host:port", "[v6]:port" → {host, port}. */
export function parseHostPort(s: string, def = 7878): { host: string; port: number } {
  const v = s.trim();
  const m6 = /^\[([^\]]+)\](?::(\d+))?$/.exec(v);
  if (m6) return { host: m6[1], port: m6[2] ? Number(m6[2]) : def };
  const m = /^([^:]+):(\d{1,5})$/.exec(v);
  if (m) return { host: m[1], port: Number(m[2]) };
  return { host: v, port: def };
}
