// App model: saved nodes, polling, notifications, tray status. Mirrors AppModel.swift.
import { Agent, AgentError, api, errText } from "./api";
import { ms, worstOf } from "./fmt";
import { effectiveState } from "./services";
import type {
  AgentConfig, AppInfo, Identity, Incident, LinkState, LiveTarget, SavedNode, Series, Settings, SpeedResult, SummaryWindow,
  SystemInfo, Target, UpdateInfo,
} from "./types";

export const RANGES = [
  { id: "5m", label: "5M", sec: 300, refresh: 4 },
  { id: "1h", label: "1H", sec: 3600, refresh: 8 },
  { id: "6h", label: "6H", sec: 21600, refresh: 30 },
  { id: "24h", label: "24H", sec: 86400, refresh: 60 },
  { id: "7d", label: "7D", sec: 604800, refresh: 180 },
  { id: "30d", label: "30D", sec: 2592000, refresh: 300 },
  { id: "60d", label: "2 MONTHS", sec: 5184000, refresh: 300 },
] as const;
export type RangeId = (typeof RANGES)[number]["id"];

export const HISTORY_DAYS = 60;

export type Route = { kind: "overview" } | { kind: "node"; id: string } | { kind: "incidents" } | { kind: "services" } | { kind: "settings" };

export class NodeState {
  identity?: Identity;
  reachable?: boolean;
  lastError?: string;
  unauthorized = false;
  config?: AgentConfig;
  live: Record<string, LiveTarget> = {};
  series: Record<string, Series> = {};
  seriesLoaded = false;
  incidents: Incident[] = [];
  system?: SystemInfo;
  summaries: Record<string, SummaryWindow[]> = {};
  speed: SpeedResult[] = [];
  rssi: [number, number][] = [];
  failures = 0;
  constructor(public saved: SavedNode) {}
  get id() { return this.saved.nodeId; }
  get agent() { return new Agent({ host: this.saved.host, port: this.saved.port, token: this.saved.token }); }
  get name(): string {
    if (this.config?.nodeName) return this.config.nodeName;
    if (this.identity) return this.identity.nodeName || this.identity.hostname || this.identity.nodeId;
    return this.saved.name;
  }
  get targets(): Target[] { return (this.config?.targets ?? []).filter((t) => t.enabled); }
  get worst(): LinkState {
    if (this.reachable === false) return "down";
    return worstOf(Object.values(this.live).map((l) => l.state));
  }
}

function lsGet<T>(k: string, d: T): T {
  try { const v = localStorage.getItem(k); return v == null ? d : (JSON.parse(v) as T); } catch { return d; }
}
function lsSet(k: string, v: unknown) {
  try { localStorage.setItem(k, JSON.stringify(v)); } catch { /* storage unavailable */ }
}

type Listener = () => void;

class Model {
  nodes: NodeState[] = [];
  route: Route = { kind: "overview" };
  range: RangeId = lsGet<RangeId>("range", "1h");
  zoom: [number, number] | null = null;
  hoverT: number | null = null;
  now = Date.now() / 1000;
  expanded = new Set<string>(lsGet<string[] | null>("overviewExpanded", null) ?? []);
  icons: Record<string, string> = lsGet("nodeIcons", {});
  removing = new Set<string>();
  settings: Settings = { notifications: true, tray: true, autoUpdate: true };
  info: AppInfo = { version: "1.0.0", platform: "", dataDir: "", selftest: false };
  update: { info?: UpdateInfo; checking: boolean; error?: string; installing: boolean; status?: string; dismissed?: string; lastCheck?: number } = { checking: false, installing: false };
  wizard: { open: boolean; editing: boolean } = { open: false, editing: false };
  private listeners = new Set<Listener>();
  private previous = new Map<string, LinkState>();
  private lastSeriesFetch = new Map<string, number>();
  private seriesBusy = new Set<string>();
  private lastTray = "";
  private started = false;

  subscribe(l: Listener) { this.listeners.add(l); return () => this.listeners.delete(l); }
  private pending = false;
  changed() {
    if (this.pending) return;
    this.pending = true;
    requestAnimationFrame(() => { this.pending = false; this.listeners.forEach((l) => l()); });
  }

  async init() {
    [this.info, this.settings] = await Promise.all([api.appInfo(), api.settingsGet()]);
    const saved = await api.nodesLoad();
    this.nodes = saved.map((s) => new NodeState(s));
    if (lsGet<string[] | null>("overviewExpanded", null) === null) this.expanded = new Set(this.nodes.map((n) => n.id));
    this.wizard = { open: this.nodes.length === 0, editing: false };
  }

  get rangeDef() { return RANGES.find((r) => r.id === this.range) ?? RANGES[1]; }
  get domain(): [number, number] {
    if (this.zoom) return this.zoom;
    return [this.now - this.rangeDef.sec, this.now];
  }
  get worst(): LinkState { return worstOf(this.nodes.map((n) => n.worst)); }
  node(id: string) { return this.nodes.find((n) => n.id === id); }

  setRange(r: RangeId) { this.range = r; lsSet("range", r); this.zoom = null; this.refreshSeries(true); this.changed(); }
  setZoom(z: [number, number] | null) { this.zoom = z; this.refreshSeries(true); this.changed(); }
  setIcon(id: string, icon: string) { this.icons[id] = icon; lsSet("nodeIcons", this.icons); this.changed(); }

  go(r: Route) {
    this.route = r; this.zoom = null;
    if (r.kind === "overview") this.refreshSeries(true);
    if (r.kind === "node") {
      const n = this.node(r.id);
      if (n) {
        this.fetchSeries(n);
        n.agent.speedHistory().then((s) => { n.speed = s ?? []; this.changed(); }).catch(() => {});
      }
    }
    this.changed();
  }

  toggleExpanded(n: NodeState) {
    if (this.expanded.has(n.id)) this.expanded.delete(n.id); else { this.expanded.add(n.id); this.fetchSeries(n); }
    lsSet("overviewExpanded", [...this.expanded]); this.changed();
  }
  setAllExpanded(on: boolean) {
    this.expanded = on ? new Set(this.nodes.map((n) => n.id)) : new Set();
    lsSet("overviewExpanded", [...this.expanded]);
    if (on) this.refreshSeries(true);
    this.changed();
  }

  async persist() { await api.nodesSave(this.nodes.map((n) => n.saved)); }

  add(s: SavedNode) {
    const ex = this.nodes.find((n) => n.id === s.nodeId);
    if (ex) { ex.saved = s; ex.unauthorized = false; } else { this.nodes.push(new NodeState(s)); this.expanded.add(s.nodeId); lsSet("overviewExpanded", [...this.expanded]); }
    this.persist().catch(console.error);
    const n = this.node(s.nodeId)!;
    this.slowOne(n, 0).then(() => this.liveOne(n)).then(() => this.fetchSeries(n, true));
    this.changed();
  }

  remove(n: NodeState) {
    this.nodes = this.nodes.filter((x) => x.id !== n.id);
    this.persist().catch(console.error);
    if (this.route.kind === "node" && this.route.id === n.id) this.route = { kind: "overview" };
    this.changed();
  }

  start() {
    if (this.started) return;
    this.started = true;
    const liveLoop = async () => { await this.pollLive(); setTimeout(liveLoop, 2000); };
    let tick = 0;
    const slowLoop = async () => { await this.pollSlow(tick++); setTimeout(slowLoop, 5000); };
    liveLoop(); slowLoop();
  }

  async pollLive() {
    this.now = Date.now() / 1000;
    await Promise.all(this.nodes.map((n) => this.liveOne(n)));
    this.updateTray();
    this.changed();
  }

  private async liveOne(n: NodeState) {
    try {
      const r = await n.agent.live();
      n.failures = 0;
      if (n.reachable !== true) {
        if (n.reachable === false) this.notify(`${n.name} reachable`, "Agent is back online.");
        n.reachable = true; n.lastError = undefined; n.unauthorized = false;
      }
      const m: Record<string, LiveTarget> = {};
      for (const t of r.targets ?? []) m[t.id] = t;
      n.live = m;
      this.evaluate(n, m);
    } catch (e) {
      n.failures += 1;
      n.lastError = errText(e);
      if (e instanceof AgentError && e.unauthorized) { n.unauthorized = true; n.reachable = true; }
      else if (n.failures >= 3) {
        if (n.reachable !== false) this.notify(`${n.name} unreachable`, `Cannot reach the agent: ${e instanceof AgentError ? e.message : n.lastError}`);
        n.reachable = false;
      }
    }
  }

  private evaluate(n: NodeState, m: Record<string, LiveTarget>) {
    for (const [id, t] of Object.entries(m)) {
      const key = `${n.id}|${id}`;
      const tgt = n.config?.targets.find((x) => x.id === id);
      const tname = tgt?.name ?? id;
      const state = effectiveState(id, tgt?.group, t.state);
      const old = this.previous.get(key);
      this.previous.set(key, state);
      if (old === undefined || old === state) continue;
      if (old === "unknown" && state !== "down") continue;
      if (state === "down") this.notify(`${n.name} → ${tname} DOWN`, t.lastError ?? "No response");
      else if (state === "degraded") this.notify(`${n.name} → ${tname} degraded`, `Latency ${ms(t.avgMs)} ms avg`);
      else if (state === "up") this.notify(`${n.name} → ${tname} recovered`, `Back to normal (${ms(t.lastRttMs)} ms)`);
    }
  }

  notify(title: string, body: string) {
    if (!this.settings.notifications) return;
    api.notify(title, body).catch(() => {});
  }

  private updateTray() {
    const level = this.nodes.length ? this.worst : "unknown";
    const lines = this.nodes.map((n) => {
      const st = n.reachable === false ? "UNREACHABLE" : n.worst.toUpperCase();
      const rtts = Object.values(n.live).map((l) => l.lastRttMs).filter((v): v is number => typeof v === "number");
      const avg = rtts.length ? ` · ${ms(rtts.reduce((a, b) => a + b, 0) / rtts.length)} ms` : "";
      return `${n.worst === "down" ? "●" : n.worst === "degraded" ? "◐" : "○"} ${n.name} — ${st}${avg}`;
    });
    const tooltip = `Internet Monitor — ${this.nodes.length ? level.toUpperCase() : "no nodes"}`;
    const sig = JSON.stringify([level, tooltip, lines]);
    if (sig === this.lastTray) return;
    this.lastTray = sig;
    api.trayUpdate(level, tooltip, lines).catch(() => {});
  }

  async pollSlow(tick: number) {
    await Promise.all(this.nodes.map((n) => this.slowOne(n, tick)));
    this.refreshSeries(false);
    this.changed();
  }

  async slowOne(n: NodeState, tick: number) {
    const a = n.agent;
    if (!n.identity || tick % 6 === 0) { try { n.identity = await a.identity(); } catch { /* keep last */ } }
    if (!n.config || tick % 6 === 0) {
      try {
        let cfg = await a.config();
        // Keep two months of history on every node.
        if ((cfg.retentionRollupDays ?? 0) < HISTORY_DAYS) {
          cfg.retentionRollupDays = HISTORY_DAYS;
          try { cfg = await a.putConfig(cfg); } catch { /* agent may reject; keep reading */ }
        }
        n.config = cfg;
      } catch { /* unreachable / unauthorized handled by live loop */ }
    }
    if (tick % 3 === 0) {
      try { n.incidents = await a.incidents(Math.floor(Date.now() / 1000) - 30 * 86400); } catch { /* ignore */ }
      try {
        n.system = await a.system();
        const r = n.system?.wifi?.rssiDbm;
        if (typeof r === "number") { n.rssi.push([Date.now() / 1000, r]); if (n.rssi.length > 720) n.rssi.splice(0, 60); }
      } catch { /* ignore */ }
    }
    if (tick % 12 === 0) {
      try {
        const s = await a.summary();
        const m: Record<string, SummaryWindow[]> = {};
        for (const x of s.summaries ?? []) m[x.target] = x.windows;
        n.summaries = m;
      } catch { /* ignore */ }
    }
  }

  /** Nodes whose graphs are on screen. */
  get visibleNodes(): NodeState[] {
    if (this.route.kind === "node") { const id = this.route.id; return this.nodes.filter((n) => n.id === id); }
    if (this.route.kind === "overview") return this.nodes.filter((n) => this.expanded.has(n.id));
    return [];
  }

  refreshSeries(force: boolean) {
    if (this.zoom && !force) return; // zoomed window is static
    for (const n of this.visibleNodes) {
      const last = this.lastSeriesFetch.get(n.id);
      if (!force && last && Date.now() / 1000 - last < this.rangeDef.refresh) continue;
      this.fetchSeries(n);
    }
  }

  fetchSeries(n: NodeState, force = false) {
    const ids = n.targets.map((t) => t.id);
    if (!ids.length || (this.seriesBusy.has(n.id) && !force)) return;
    let from: number, to: number;
    if (this.zoom) { [from, to] = this.zoom.map(Math.floor) as [number, number]; }
    else { to = Math.floor(Date.now() / 1000); from = to - this.rangeDef.sec; }
    this.seriesBusy.add(n.id); this.lastSeriesFetch.set(n.id, Date.now() / 1000);
    n.agent.series(ids, from, to, 500)
      .then((r) => {
        const m: Record<string, Series> = {};
        for (const s of r.series ?? []) m[s.target] = s;
        n.series = m; n.seriesLoaded = true;
        this.changed();
      })
      .catch(() => {})
      .finally(() => this.seriesBusy.delete(n.id));
  }

  async updateConfig(n: NodeState, edit: (c: AgentConfig) => AgentConfig | void): Promise<string | null> {
    if (!n.config) return "Config not loaded yet";
    const c = structuredClone(n.config);
    const r = edit(c) ?? c;
    try { n.config = await n.agent.putConfig(r); this.changed(); return null; } catch (e) { return errText(e); }
  }

  /** Uninstalls the agent over SSH and forgets the node. Returns an error / warning string, or null. */
  async uninstallAndRemove(n: NodeState): Promise<string | null> {
    this.removing.add(n.id); this.changed();
    try {
      const r = await api.removeAgent(n.saved.host, n.saved.sshUser ?? null);
      if (r.error) return r.error;
      this.remove(n);
      return r.leftovers.length ? "Removed, but these files remain: " + r.leftovers.join(", ") : null;
    } finally { this.removing.delete(n.id); this.changed(); }
  }

  async saveSettings(s: Partial<Settings>) {
    this.settings = { ...this.settings, ...s };
    await api.settingsSet(this.settings);
    this.changed();
  }
}

export const model = new Model();
