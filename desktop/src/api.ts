// Typed wrappers around the Rust commands (src-tauri/src/lib.rs).
import { invoke } from "@tauri-apps/api/core";
import { listen, type UnlistenFn } from "@tauri-apps/api/event";
import type {
  AgentConfig, AppInfo, Candidate, ClientInfo, Identity, Incident, LiveResponse, PairCode, PairResponse,
  RemoveResult, SavedNode, SeriesResponse, Settings, SpeedResult, SummaryResponse, SystemInfo, TraceResult, UpdateInfo,
} from "./types";

export class AgentError extends Error {
  constructor(public status: number, message: string) {
    super(message);
  }
  get unauthorized() { return this.status === 401; }
  override toString() { return this.status === 0 ? this.message : `${this.message} (HTTP ${this.status})`; }
}

export function errText(e: unknown): string {
  if (e instanceof AgentError) return e.toString();
  if (e instanceof Error) return e.message;
  if (typeof e === "string") return e;
  return JSON.stringify(e);
}

export interface Endpoint { host: string; port: number; token?: string | null }

/** Client for one agent; all HTTP goes through Rust (no CORS / webview networking). */
export class Agent {
  constructor(public ep: Endpoint) {}

  async req<T>(method: string, path: string, body?: unknown, timeoutMs = 6000, auth = true): Promise<T> {
    try {
      return await invoke<T>("agent_request", {
        host: this.ep.host, port: this.ep.port, token: auth ? this.ep.token ?? null : null,
        method, path, body: body ?? null, timeoutMs,
      });
    } catch (e: any) {
      if (e && typeof e === "object" && "status" in e) throw new AgentError(e.status, e.message);
      throw new AgentError(0, String(e));
    }
  }
  identity(timeoutMs = 2500) { return this.req<Identity>("GET", "/v1/identity", undefined, timeoutMs, false); }
  pair(code: string, clientName: string) { return this.req<PairResponse>("POST", "/v1/pair", { code, clientName }, 8000, false); }
  config() { return this.req<AgentConfig>("GET", "/v1/config"); }
  putConfig(c: AgentConfig) { return this.req<AgentConfig>("PUT", "/v1/config", c, 10000); }
  live() { return this.req<LiveResponse>("GET", "/v1/live", undefined, 4000); }
  series(targets: string[], from: number, to: number, points: number) {
    return this.req<SeriesResponse>("GET", `/v1/series?target=${encodeURIComponent(targets.join(","))}&from=${from}&to=${to}&points=${points}`, undefined, 15000);
  }
  summary() { return this.req<SummaryResponse>("GET", "/v1/summary", undefined, 15000); }
  incidents(since?: number, limit = 200) {
    return this.req<Incident[]>("GET", `/v1/incidents?limit=${limit}` + (since ? `&since=${since}` : ""));
  }
  system() { return this.req<SystemInfo>("GET", "/v1/system", undefined, 8000); }
  traceroute(host: string, maxHops = 20) { return this.req<TraceResult>("POST", "/v1/traceroute", { host, maxHops }, 45000); }
  speedtest() { return this.req<SpeedResult>("POST", "/v1/speedtest", undefined, 90000); }
  speedHistory() { return this.req<SpeedResult[]>("GET", "/v1/speedtest/history"); }
  newPairCode() { return this.req<PairCode>("POST", "/v1/pair-code"); }
  clients() { return this.req<ClientInfo[]>("GET", "/v1/clients"); }
  deleteClient(id: string) { return this.req<unknown>("DELETE", `/v1/clients/${encodeURIComponent(id)}`); }
  restart() { return this.req<unknown>("POST", "/v1/restart"); }
  resetHistory() { return this.req<unknown>("POST", "/v1/reset-history", undefined, 15000); }
}

export const api = {
  appInfo: () => invoke<AppInfo>("app_info"),
  nodesLoad: () => invoke<SavedNode[]>("nodes_load"),
  nodesSave: (nodes: SavedNode[]) => invoke<void>("nodes_save", { nodes }),
  settingsGet: () => invoke<Settings>("settings_get"),
  settingsSet: (settings: Settings) => invoke<void>("settings_set", { settings }),
  autostartGet: () => invoke<boolean>("autostart_get"),
  autostartSet: (enabled: boolean) => invoke<boolean>("autostart_set", { enabled }),
  probeAgent: (host: string, port: number | null = null, timeoutMs = 2500) => invoke<Identity | null>("probe_agent", { host, port, timeoutMs }),
  discoverStart: () => invoke<Candidate[]>("discover_start"),
  probeHost: (host: string) => invoke<Candidate>("probe_host", { host }),
  localIps: () => invoke<string[]>("local_ips"),
  clientName: () => invoke<string>("client_name"),
  sshGuessUsers: (saved: string | null) => invoke<string[]>("ssh_guess_users", { saved }),
  sshProbeLogin: (host: string, users: string[]) => invoke<string | null>("ssh_probe_login", { host, users }),
  sshFetchPairCode: (user: string, host: string) => invoke<string>("ssh_fetch_pair_code", { user, host }),
  sshInstallAgent: (user: string, host: string) => invoke<void>("ssh_install_agent", { user, host }),
  removeAgent: (host: string, sshUser: string | null) => invoke<RemoveResult>("remove_agent", { host, sshUser }),
  notify: (title: string, body: string) => invoke<void>("notify", { title, body }),
  trayUpdate: (level: string, tooltip: string, lines: string[]) => invoke<void>("tray_update", { level, tooltip, lines }),
  openUrl: (url: string) => invoke<void>("open_url", { url }),
  saveText: (name: string, contents: string) => invoke<string | null>("save_text_file", { name, contents }),
  saveBase64: (name: string, data: string) => invoke<string | null>("save_base64_file", { name, data }),
  updateCheck: () => invoke<UpdateInfo>("update_check"),
  updateInstall: (info: UpdateInfo) => invoke<{ status: string; message: string }>("update_install", { info }),
  restartApp: () => invoke<void>("restart_app"),
  selftestReport: (report: string) => invoke<void>("selftest_report", { report }),
  quit: () => invoke<void>("quit_app"),
};

export function onEvent<T>(name: string, cb: (payload: T) => void): Promise<UnlistenFn> {
  return listen<T>(name, (e) => cb(e.payload));
}
