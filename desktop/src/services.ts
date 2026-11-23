// Online-service monitoring: catalog, node-target reconciliation, notification rules and the desktop
// reachability state machine. Pure (unit tested in services.test.ts); runtime lives in servicesModel.ts.
import catalogJson from "../../services/catalog.json";
import type { LinkState, Target } from "./types";

export type Adapter = "statuspage" | "gcp" | "aws" | "rss" | "slack" | "stripe" | "none";
export const ADAPTERS: readonly Adapter[] = ["statuspage", "gcp", "aws", "rss", "slack", "stripe", "none"];

export interface ServiceDef {
  id: string;
  name: string;
  category: string;
  adapter: Adapter;
  statusUrl: string | null;
  pageUrl: string;
  probeUrl: string;
  custom?: boolean;
}

export type OfficialState = "operational" | "degraded" | "outage" | "maintenance" | "unknown";

export interface Official {
  state: OfficialState;
  summary: string;
  incidents: { id: string; title: string; impact: string }[];
  components: { name: string; status: string }[];
  error?: string | null;
}

export interface ServiceSettings {
  selected: string[];
  nodes: string[];
  desktop: boolean;
  custom: ServiceDef[];
  /** selectionKey() at the last fully successful APPLY */
  applied: string;
}

export const CATALOG: ServiceDef[] = (catalogJson as { version: number; services: ServiceDef[] }).services;
export const CUSTOM_CATEGORY = "Custom";
export const SVC_PREFIX = "svc-";
export const SERVICE_GROUP = "service";
export const DESKTOP_WARNING = "⚠ Only collects data while this app is running";
export const NO_FEED = "No public status feed — reachability only";

/** Official status poll interval per adapter (gcp/aws feeds are large/slow-moving). */
export const officialIntervalSec = (a: Adapter) => (a === "gcp" || a === "aws" ? 300 : 90);
export const PROBE_INTERVAL_SEC = 30;
export const PROBE_KEEP = 2880;
export const DOWN_AFTER = 3;

export function defaultServiceSettings(): ServiceSettings {
  return { selected: [], nodes: [], desktop: false, custom: [], applied: "" };
}

export function normalizeSettings(v: unknown): ServiceSettings {
  const d = defaultServiceSettings();
  if (!v || typeof v !== "object") return d;
  const o = v as Partial<ServiceSettings>;
  const strs = (x: unknown) => (Array.isArray(x) ? x.filter((s): s is string => typeof s === "string") : []);
  return {
    selected: strs(o.selected),
    nodes: strs(o.nodes),
    desktop: o.desktop === true,
    custom: Array.isArray(o.custom) ? o.custom.filter((c) => c && typeof c.id === "string" && typeof c.probeUrl === "string").map((c) => ({ ...c, custom: true, category: CUSTOM_CATEGORY })) : [],
    applied: typeof o.applied === "string" ? o.applied : "",
  };
}

export function allServices(custom: ServiceDef[]): ServiceDef[] {
  return [...CATALOG, ...custom];
}

/** Category order of the catalog, custom services last. */
export function groupByCategory(list: ServiceDef[]): [string, ServiceDef[]][] {
  const m = new Map<string, ServiceDef[]>();
  for (const s of list) {
    if (!m.has(s.category)) m.set(s.category, []);
    m.get(s.category)!.push(s);
  }
  const keys = [...m.keys()].sort((a, b) => Number(a === CUSTOM_CATEGORY) - Number(b === CUSTOM_CATEGORY));
  return keys.map((k) => [k, m.get(k)!]);
}

export function filterServices(list: ServiceDef[], q: string): ServiceDef[] {
  const t = q.trim().toLowerCase();
  if (!t) return list;
  return list.filter((s) => s.name.toLowerCase().includes(t) || s.category.toLowerCase().includes(t) || s.id.includes(t));
}

function normUrl(u: string): string | null {
  const v = u.trim();
  if (!v) return null;
  if (/^[a-z][a-z0-9+.-]*:\/\//i.test(v) && !/^https?:\/\//i.test(v)) return null; // ftp:// etc.
  const withScheme = /^https?:\/\//i.test(v) ? v : `https://${v}`;
  try {
    const p = new URL(withScheme);
    if (!p.hostname || !/^https?:$/.test(p.protocol)) return null;
    return withScheme;
  } catch { return null; }
}

/** Custom service from the "Add custom service" form. A status URL given as a Statuspage base
 * (e.g. https://status.example.com) becomes `<base>/api/v2/summary.json`. */
export function customService(name: string, probe: string, status: string, rand = Math.random()): ServiceDef | string {
  const probeUrl = normUrl(probe);
  if (!probeUrl) return "Enter a valid probe URL";
  let statusUrl: string | null = null;
  let pageUrl = probeUrl;
  if (status.trim()) {
    const s = normUrl(status);
    if (!s) return "Enter a valid status page URL";
    const base = s.replace(/\/+$/, "");
    if (/\.json(\?.*)?$/i.test(base)) {
      statusUrl = base;
      pageUrl = base.replace(/\/api\/v2\/[a-z_]+\.json(\?.*)?$/i, "") || base;
    } else {
      statusUrl = `${base}/api/v2/summary.json`;
      pageUrl = base;
    }
  }
  const host = new URL(probeUrl).hostname;
  const id = "c-" + host.replace(/[^a-z0-9]+/gi, "-").toLowerCase().slice(0, 24) + "-" + Math.floor(rand * 36 ** 4).toString(36).padStart(4, "0");
  return { id, name: name.trim() || host, category: CUSTOM_CATEGORY, adapter: statusUrl ? "statuspage" : "none", statusUrl, pageUrl, probeUrl, custom: true };
}

export const targetId = (svcId: string) => SVC_PREFIX + svcId;
export const isServiceTarget = (t: Pick<Target, "id" | "group">) => t.id.startsWith(SVC_PREFIX) || t.group === SERVICE_GROUP;

/** State used for node-target notifications. Internet services (group "service") routinely exceed the
 * 150 ms latency rule, so for them "degraded" counts as up: only down / recovered notify. */
export function effectiveState(id: string, group: string | undefined, s: LinkState): LinkState {
  return s === "degraded" && (group === SERVICE_GROUP || id.startsWith(SVC_PREFIX)) ? "up" : s;
}

export function hostOf(url: string): string {
  try { return new URL(url).hostname; } catch { return url; }
}

export function serviceTarget(s: ServiceDef): Target {
  return { id: targetId(s.id), name: s.name, kind: "http", url: s.probeUrl, host: hostOf(s.probeUrl), intervalMs: 30000, enabled: true, group: SERVICE_GROUP };
}

/** Removes undesired `svc-*` targets and adds missing desired ones; other targets untouched. */
export function reconcileTargets(targets: Target[], desired: ServiceDef[]): { targets: Target[]; changed: boolean; added: number; removed: number } {
  const want = new Map(desired.map((s) => [targetId(s.id), s]));
  const kept = targets.filter((t) => !t.id.startsWith(SVC_PREFIX) || want.has(t.id));
  const removed = targets.length - kept.length;
  const have = new Set(kept.map((t) => t.id));
  const add = desired.filter((s) => !have.has(targetId(s.id))).map(serviceTarget);
  return { targets: [...kept, ...add], changed: removed > 0 || add.length > 0, added: add.length, removed };
}

/** Identity of what APPLY would write (services, their probe URLs and the node toggles). */
export function selectionKey(st: ServiceSettings, defs: ServiceDef[]): string {
  const byId = new Map(defs.map((d) => [d.id, d]));
  const svcs = [...new Set(st.selected)].filter((id) => byId.has(id)).sort().map((id) => `${id}=${byId.get(id)!.probeUrl}`);
  return JSON.stringify([svcs, [...new Set(st.nodes)].sort()]);
}

export interface Note { title: string; body: string }

/** Notification for an official-status change. `prev` = last known (non-unknown) observation. */
export function officialNotes(name: string, prev: Official | undefined, next: Official): Note[] {
  if (next.state === "unknown") return [];
  const top = next.incidents[0]?.title ?? "";
  if (!prev) return next.state === "operational" ? [] : [{ title: `${name}: ${next.state} — ${next.summary}`, body: top }];
  if (prev.state !== next.state) {
    if (next.state === "operational") return [{ title: `${name} is back to operational`, body: next.summary }];
    return [{ title: `${name}: ${next.state} — ${next.summary}`, body: top }];
  }
  const old = new Set(prev.incidents.map((i) => i.id));
  return next.incidents.filter((i) => !old.has(i.id)).map((i) => ({ title: `${name}: ${next.state} — ${i.title}`, body: "New incident" }));
}

export type ReachState = "up" | "down" | "unknown";

export interface Reach {
  state: ReachState;
  failures: number;
  samples: [number, number | null][]; // [unix sec, ms | null = failure]
  lastMs: number | null;
  lastAt: number | null;
  lastError: string | null;
}

export const newReach = (): Reach => ({ state: "unknown", failures: 0, samples: [], lastMs: null, lastAt: null, lastError: null });

/** Applies one probe result; returns a notification on down / recovery. */
export function reachStep(r: Reach, name: string, ok: boolean, msVal: number | null, err: string | null, now: number): Note | null {
  r.samples.push([now, ok ? msVal : null]);
  if (r.samples.length > PROBE_KEEP) r.samples.splice(0, r.samples.length - PROBE_KEEP);
  r.lastAt = now;
  if (ok) {
    r.failures = 0;
    r.lastMs = msVal;
    r.lastError = null;
    const was = r.state;
    r.state = "up";
    return was === "down" ? { title: `${name} reachable again`, body: msVal != null ? `${Math.round(msVal)} ms from this computer` : "" } : null;
  }
  r.failures += 1;
  r.lastError = err;
  if (r.failures >= DOWN_AFTER && r.state !== "down") {
    r.state = "down";
    return { title: `${name} unreachable from this computer`, body: err ?? "No response" };
  }
  return null;
}

export const STATE_COLOR: Record<OfficialState, string> = {
  operational: "#39D98A",
  degraded: "#FFB000",
  outage: "#FF4D4F",
  maintenance: "#4D9DFF",
  unknown: "#56606B",
};

export const isBad = (s: OfficialState | undefined) => s === "degraded" || s === "outage";
