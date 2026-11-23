// Agent API types (docs/API.md) + app-side types. JSON keys are camelCase.

export type LinkState = "up" | "degraded" | "down" | "unknown";
export type ProbeKind = "icmp" | "tcp" | "http" | "dns";

export interface Identity {
  service: string;
  apiVersion?: number | null;
  version?: string | null;
  nodeId: string;
  hostname?: string | null;
  nodeName?: string | null;
  os?: string | null;
  arch?: string | null;
  uptimeSec?: number | null;
  startedAt?: number | null;
  paired?: boolean | null;
}

export interface Target {
  id: string;
  name: string;
  kind: ProbeKind;
  host?: string | null;
  port?: number | null;
  url?: string | null;
  resolver?: string | null;
  intervalMs?: number | null;
  enabled: boolean;
  group: string;
  nodeRef?: string | null;
}

export interface Rules {
  downAfterFailures: number;
  degradedLatencyMs: number;
  degradedForSec: number;
  webhookUrl: string;
}

export interface SpeedtestCfg {
  enabled: boolean;
  intervalMin: number;
  url: string;
}

export interface AgentConfig {
  nodeName: string;
  intervalMs: number;
  timeoutMs: number;
  retentionRawHours: number;
  retentionRollupDays: number;
  metricsPublic: boolean;
  targets: Target[];
  rules: Rules;
  speedtest: SpeedtestCfg;
  [extra: string]: unknown;
}

export interface LiveTarget {
  id: string;
  state: LinkState;
  lastRttMs?: number | null;
  lastAt?: number | null;
  lastError?: string | null;
  resolvedHost?: string | null;
  windowSec?: number | null;
  avgMs?: number | null;
  minMs?: number | null;
  maxMs?: number | null;
  p95Ms?: number | null;
  jitterMs?: number | null;
  lossPct?: number | null;
  samples?: number | null;
  pathHint?: string | null;
}

export interface LiveResponse {
  now: number;
  targets: LiveTarget[];
}

export interface SeriesPoint {
  t: number;
  n?: number | null;
  loss?: number | null;
  min?: number | null;
  avg?: number | null;
  max?: number | null;
  p95?: number | null;
}

export interface Series {
  target: string;
  stepSec: number;
  source?: string | null;
  points: SeriesPoint[];
}

export interface SeriesResponse {
  from: number;
  to: number;
  series: Series[];
}

export interface SummaryWindow {
  label: string;
  seconds?: number | null;
  uptimePct?: number | null;
  lossPct?: number | null;
  avgMs?: number | null;
  p95Ms?: number | null;
  minMs?: number | null;
  maxMs?: number | null;
  jitterMs?: number | null;
  samples?: number | null;
}

export interface SummaryResponse {
  summaries: { target: string; windows: SummaryWindow[] }[];
}

export interface Incident {
  id: string;
  targetId?: string | null;
  targetName?: string | null;
  kind: string;
  startedAt: number;
  endedAt?: number | null;
  detail?: string | null;
  peakMs?: number | null;
}

export interface IfaceInfo {
  name: string;
  kind?: string | null;
  addrs?: string[] | null;
  up?: boolean | null;
  rxBytes?: number | null;
  txBytes?: number | null;
}

export interface TSPeer {
  name: string;
  ip?: string | null;
  online?: boolean | null;
  path?: string | null;
  relay?: string | null;
  curAddr?: string | null;
}

export interface SystemInfo {
  hostname?: string | null;
  os?: string | null;
  kernel?: string | null;
  arch?: string | null;
  model?: string | null;
  uptimeSec?: number | null;
  loadAvg?: number[] | null;
  memTotalMB?: number | null;
  memUsedMB?: number | null;
  diskFreeMB?: number | null;
  tempC?: number | null;
  interfaces?: IfaceInfo[] | null;
  gateway?: string | null;
  defaultInterface?: string | null;
  wifi?: { iface?: string | null; ssid?: string | null; rssiDbm?: number | null; linkQuality?: number | null } | null;
  tailscale?: { selfIp?: string | null; peers?: TSPeer[] | null } | null;
}

export interface Hop {
  ttl: number;
  ip?: string | null;
  name?: string | null;
  rttMs: (number | null)[];
  reached?: boolean | null;
}

export interface TraceResult {
  host: string;
  ip?: string | null;
  hops: Hop[];
}

export interface SpeedResult {
  at: number;
  url?: string | null;
  bytes?: number | null;
  durationMs?: number | null;
  downMbps?: number | null;
  latencyMs?: number | null;
  error?: string | null;
}

export interface ClientInfo {
  id: string;
  name: string;
  createdAt?: number | null;
  lastSeen?: number | null;
}

export interface PairResponse {
  token: string;
  nodeId?: string | null;
  clientId?: string | null;
}

export interface PairCode {
  code: string;
  expiresAt?: number | null;
}

// ---- app side ----

export interface SavedNode {
  nodeId: string;
  name: string;
  host: string;
  port: number;
  token: string;
  sshUser?: string | null;
  addedAt: string;
}

export interface Candidate {
  ip: string;
  name: string;
  network: string;
  isTailscale: boolean;
  pingMs?: number | null;
  agent?: Identity | null;
  extraIps: string[];
  os?: string | null;
}

export interface Settings {
  notifications: boolean;
  tray: boolean;
  autoUpdate: boolean;
  /** Services page selection (normalised by services.ts normalizeSettings) */
  services?: unknown;
}

export interface AppInfo {
  version: string;
  platform: string;
  dataDir: string;
  selftest: boolean;
  selftestAgent?: string | null;
}

export interface UpdateInfo {
  current: string;
  latest: string;
  available: boolean;
  htmlUrl: string;
  notes: string;
  asset?: { name: string; url: string; size: number } | null;
  sumsUrl?: string | null;
  mode: "installer" | "appimage" | "page";
}

export interface RemoveResult {
  error?: string | null;
  leftovers: string[];
  user?: string | null;
}
