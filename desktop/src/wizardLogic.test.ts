import { describe, expect, it } from "vitest";
import { buildRows, cellKey, customTarget, newConn, parseHostPort, setColumn, setRow, targetsFor, toggleCell, type Conn, type WCandidate } from "./wizardLogic";
import type { AgentConfig, Target } from "./types";

const defaults: Target[] = [
  { id: "t-gateway", name: "Gateway", kind: "icmp", host: "@gateway", enabled: true, group: "baseline" },
  { id: "t-cloudflare", name: "Cloudflare", kind: "icmp", host: "1.1.1.1", enabled: true, group: "baseline" },
  { id: "t-google", name: "Google", kind: "icmp", host: "8.8.8.8", enabled: true, group: "baseline" },
];
const cfg = (targets: Target[]): AgentConfig => ({
  nodeName: "", intervalMs: 2000, timeoutMs: 2000, retentionRawHours: 6, retentionRollupDays: 60, metricsPublic: false, targets,
  rules: { downAfterFailures: 3, degradedLatencyMs: 150, degradedForSec: 60, webhookUrl: "" },
  speedtest: { enabled: false, intervalMin: 60, url: "" },
});
const cand = (ip: string): WCandidate => ({ ip, name: ip, network: "LAN", isTailscale: false, extraIps: [] });
const conn = (nodeId: string, targets: Target[]): Conn => ({ ...newConn(), phase: "ready", identity: { service: "internetmonitor-agent", nodeId, hostname: "h-" + nodeId }, config: cfg(targets) });

describe("wizard targets grid", () => {
  it("fresh agents: everyone pings everyone else + baselines", () => {
    const cs = [cand("10.0.0.2"), cand("10.0.0.3")];
    const { rows, grid } = buildRows(cs, new Set(["10.0.0.2", "10.0.0.3"]), { "10.0.0.2": conn("a", defaults), "10.0.0.3": conn("b", defaults) }, false);
    expect(rows.map((r) => r.id)).toEqual(["node-a", "node-b", "base-gw", "base-cf", "base-g"]); // defaults merge into baselines by endpoint
    expect(grid.has(cellKey("node-a", "10.0.0.3"))).toBe(true);
    expect(grid.has(cellKey("node-a", "10.0.0.2"))).toBe(false); // never ping yourself
    expect(grid.has(cellKey("base-cf", "10.0.0.2"))).toBe(true);
    expect(grid.size).toBe(2 + 3 * 2);
  });

  it("configured agents keep their existing selection", () => {
    const custom: Target = { id: "custom-1", name: "NAS", kind: "tcp", host: "10.0.0.9", port: 445, enabled: true, group: "custom" };
    const old: Target = { id: "t-x", name: "Old", kind: "icmp", host: "9.9.9.9", enabled: true, group: "baseline" };
    const cs = [cand("10.0.0.2")];
    const { rows, grid } = buildRows(cs, new Set(["10.0.0.2"]), { "10.0.0.2": conn("a", [...defaults.slice(1), custom, old]) }, true);
    expect(rows.find((r) => r.id === "custom-1")?.section).toBe("Custom");
    expect(rows.find((r) => r.id === "t-x")?.section).toBe("Existing");
    expect(grid.has(cellKey("custom-1", "10.0.0.2"))).toBe(true);
    expect(grid.has(cellKey("base-cf", "10.0.0.2"))).toBe(true);
    expect(grid.has(cellKey("base-gw", "10.0.0.2"))).toBe(false); // gateway was not configured
  });

  it("toggle / row / column helpers respect self cells", () => {
    const cs = [cand("10.0.0.2"), cand("10.0.0.3")];
    const { rows, grid } = buildRows(cs, new Set(["10.0.0.2", "10.0.0.3"]), { "10.0.0.2": conn("a", defaults), "10.0.0.3": conn("b", defaults) }, false);
    const nodeA = rows[0];
    toggleCell(grid, nodeA, "10.0.0.2");
    expect(grid.has(cellKey("node-a", "10.0.0.2"))).toBe(false);
    setColumn(grid, rows, "10.0.0.3", false);
    expect([...grid].some((k) => k.endsWith("@@10.0.0.3"))).toBe(false);
    setRow(grid, nodeA, ["10.0.0.2", "10.0.0.3"], true);
    expect(grid.has(cellKey("node-a", "10.0.0.3"))).toBe(true);
    expect(grid.has(cellKey("node-a", "10.0.0.2"))).toBe(false);
  });

  it("targetsFor keeps interval overrides", () => {
    const cs = [cand("10.0.0.2")];
    const { rows, grid } = buildRows(cs, new Set(["10.0.0.2"]), { "10.0.0.2": conn("a", defaults) }, false);
    const out = targetsFor(rows, grid, "10.0.0.2", [{ ...defaults[0], id: "base-cf", intervalMs: 5000 }]);
    expect(out.map((t) => t.id)).toEqual(["base-gw", "base-cf", "base-g"]);
    expect(out.find((t) => t.id === "base-cf")?.intervalMs).toBe(5000);
  });

  it("custom targets", () => {
    expect(customTarget("http", "", "example.com", null)).toMatchObject({ url: "https://example.com", host: "example.com", name: "example.com", group: "custom" });
    expect(customTarget("tcp", "SSH", "pi.local", null)).toMatchObject({ host: "pi.local", port: 443, name: "SSH" });
    expect(customTarget("dns", "", "  ", null)).toBeNull();
  });

  it("host:port parsing", () => {
    expect(parseHostPort("10.0.0.2")).toEqual({ host: "10.0.0.2", port: 7878 });
    expect(parseHostPort("10.0.0.2:7899")).toEqual({ host: "10.0.0.2", port: 7899 });
    expect(parseHostPort("[fe80::1]:9")).toEqual({ host: "fe80::1", port: 9 });
    expect(parseHostPort("pi.local")).toEqual({ host: "pi.local", port: 7878 });
  });
});

describe("wizard keeps Services-page targets", () => {
  const svc: Target = { id: "svc-github", name: "GitHub", kind: "http", url: "https://github.com", host: "github.com", intervalMs: 30000, enabled: true, group: "service" };
  it("svc-* targets are not grid rows, do not count as 'configured', and survive apply", () => {
    const cs = [cand("10.0.0.2")];
    const conns = { "10.0.0.2": conn("a", [...defaults, svc]) };
    const { rows, grid } = buildRows(cs, new Set(["10.0.0.2"]), conns, false);
    expect(rows.some((r) => r.id === "svc-github")).toBe(false);
    expect(grid.has(cellKey("base-cf", "10.0.0.2"))).toBe(true); // still treated as a fresh agent
    const out = targetsFor(rows, grid, "10.0.0.2", [...defaults, svc]);
    expect(out.filter((t) => t.id === "svc-github")).toEqual([svc]);
    // also when editing with every grid cell cleared
    const edit = buildRows(cs, new Set(["10.0.0.2"]), conns, true);
    expect(targetsFor(edit.rows, new Set(), "10.0.0.2", [...defaults, svc]).map((t) => t.id)).toEqual(["svc-github"]);
  });
});
