import { describe, expect, it } from "vitest";
import {
  ADAPTERS, CATALOG, customService, defaultServiceSettings, effectiveState, filterServices, groupByCategory, newReach, normalizeSettings,
  officialNotes, PROBE_KEEP, reachStep, reconcileTargets, selectionKey, serviceTarget, type Official, type ServiceDef,
} from "./services";
import type { Target } from "./types";

const off = (state: Official["state"], ids: string[] = [], summary = "s"): Official => ({
  state, summary, incidents: ids.map((id) => ({ id, title: "T " + id, impact: "minor" })), components: [],
});
const svc = (id: string): ServiceDef => CATALOG.find((s) => s.id === id)!;

describe("catalog", () => {
  it("every adapter in services/catalog.json is implemented", () => {
    expect(CATALOG.length).toBeGreaterThanOrEqual(46);
    for (const s of CATALOG) {
      expect(ADAPTERS, s.id).toContain(s.adapter);
      if (s.adapter !== "none") expect(s.statusUrl, s.id).toMatch(/^https:\/\//);
      expect(s.probeUrl, s.id).toMatch(/^https:\/\//);
    }
    expect(new Set(CATALOG.map((s) => s.id)).size).toBe(CATALOG.length);
  });
  it("groups by category in catalog order, custom last", () => {
    const c = customService("Mine", "mine.example.com", "", 0.5) as ServiceDef;
    const g = groupByCategory([c, ...CATALOG]);
    expect(g[0][0]).toBe("Cloud");
    expect(g.at(-1)![0]).toBe("Custom");
    expect(g.map((x) => x[0])).toContain("Banks (Canada)");
    expect(filterServices(CATALOG, "bank").every((s) => s.category === "Banks (Canada)")).toBe(true);
    expect(filterServices(CATALOG, "git").map((s) => s.id)).toContain("github");
  });
});

describe("custom services", () => {
  it("derives the Statuspage summary URL from a base URL", () => {
    const c = customService(" Acme ", "app.acme.io", "https://status.acme.io/", 0) as ServiceDef;
    expect(c).toMatchObject({ name: "Acme", adapter: "statuspage", statusUrl: "https://status.acme.io/api/v2/summary.json", pageUrl: "https://status.acme.io", probeUrl: "https://app.acme.io", category: "Custom", custom: true });
    expect(c.id).toMatch(/^c-app-acme-io-/);
    const j = customService("", "https://x.io", "https://status.x.io/api/v2/summary.json") as ServiceDef;
    expect(j).toMatchObject({ name: "x.io", statusUrl: "https://status.x.io/api/v2/summary.json", pageUrl: "https://status.x.io" });
    const n = customService("N", "https://n.io/login", "") as ServiceDef;
    expect(n).toMatchObject({ adapter: "none", statusUrl: null, pageUrl: "https://n.io/login" });
    expect(customService("", "", "")).toBe("Enter a valid probe URL");
    expect(customService("", "x.io", "ftp://bad")).toBe("Enter a valid status page URL");
  });
  it("settings normalise defensively", () => {
    expect(normalizeSettings(undefined)).toEqual(defaultServiceSettings());
    const s = normalizeSettings({ selected: ["github", 3], desktop: true, custom: [{ id: "c-1", name: "A", probeUrl: "https://a", category: "x" }, { bad: 1 }] });
    expect(s.selected).toEqual(["github"]);
    expect(s.desktop).toBe(true);
    expect(s.custom).toHaveLength(1);
    expect(s.custom[0].category).toBe("Custom");
  });
});

describe("node targets", () => {
  const base: Target[] = [
    { id: "t-cf", name: "Cloudflare", kind: "icmp", host: "1.1.1.1", enabled: true, group: "baseline" },
    { id: "custom-1", name: "svc-like name", kind: "http", url: "https://e.com", enabled: true, group: "custom" },
  ];
  it("service target shape", () => {
    expect(serviceTarget(svc("atlassian"))).toEqual({ id: "svc-atlassian", name: "Atlassian", kind: "http", url: "https://www.atlassian.com", host: "www.atlassian.com", intervalMs: 30000, enabled: true, group: "service" });
  });
  it("adds missing, removes undesired svc-*, leaves the rest", () => {
    const r1 = reconcileTargets(base, [svc("github"), svc("td")]);
    expect(r1.changed).toBe(true);
    expect(r1.targets.map((t) => t.id)).toEqual(["t-cf", "custom-1", "svc-github", "svc-td"]);
    const r2 = reconcileTargets(r1.targets, [svc("github"), svc("td")]);
    expect(r2.changed).toBe(false);
    const r3 = reconcileTargets(r1.targets, [svc("td")]);
    expect(r3.targets.map((t) => t.id)).toEqual(["t-cf", "custom-1", "svc-td"]);
    expect([r3.added, r3.removed]).toEqual([0, 1]);
    expect(reconcileTargets(r1.targets, []).targets).toEqual(base);
  });
  it("selection key ignores order and unknown ids", () => {
    const a = { ...defaultServiceSettings(), selected: ["td", "github", "gone"], nodes: ["b", "a"] };
    const b = { ...defaultServiceSettings(), selected: ["github", "td"], nodes: ["a", "b"], desktop: true };
    expect(selectionKey(a, CATALOG)).toBe(selectionKey(b, CATALOG));
    expect(selectionKey(a, CATALOG)).not.toBe(selectionKey({ ...b, nodes: ["a"] }, CATALOG));
  });
  it("service targets never notify 'degraded'", () => {
    expect(effectiveState("svc-github", "service", "degraded")).toBe("up");
    expect(effectiveState("svc-github", "service", "down")).toBe("down");
    expect(effectiveState("t-cf", "baseline", "degraded")).toBe("degraded");
  });
});

describe("official notifications", () => {
  it("quiet on first healthy observation, loud on first unhealthy", () => {
    expect(officialNotes("Atlassian", undefined, off("operational"))).toEqual([]);
    expect(officialNotes("Atlassian", undefined, off("degraded", ["i1"], "Partial outage"))).toEqual([{ title: "Atlassian: degraded — Partial outage", body: "T i1" }]);
  });
  it("state changes, recovery, new incidents, unknown ignored", () => {
    expect(officialNotes("Atlassian", off("operational"), off("outage", [], "Major"))[0].title).toBe("Atlassian: outage — Major");
    expect(officialNotes("Atlassian", off("degraded", ["i1"]), off("operational"))[0].title).toBe("Atlassian is back to operational");
    expect(officialNotes("Atlassian", off("degraded", ["i1"]), off("degraded", ["i1"]))).toEqual([]);
    expect(officialNotes("Atlassian", off("degraded", ["i1"]), off("degraded", ["i1", "i2"]))).toEqual([{ title: "Atlassian: degraded — T i2", body: "New incident" }]);
    expect(officialNotes("Atlassian", off("operational"), off("unknown"))).toEqual([]);
  });
});

describe("desktop reachability", () => {
  it("down after 3 failures, recovered on next success, bounded history", () => {
    const r = newReach();
    expect(reachStep(r, "TD Bank", true, 42, null, 1)).toBeNull();
    expect(r.state).toBe("up");
    expect(reachStep(r, "TD Bank", false, null, "timed out", 2)).toBeNull();
    expect(reachStep(r, "TD Bank", false, null, "timed out", 3)).toBeNull();
    expect(reachStep(r, "TD Bank", false, null, "timed out", 4)).toEqual({ title: "TD Bank unreachable from this computer", body: "timed out" });
    expect(r.state).toBe("down");
    expect(reachStep(r, "TD Bank", false, null, "timed out", 5)).toBeNull(); // no repeat
    expect(reachStep(r, "TD Bank", true, 80.4, null, 6)).toEqual({ title: "TD Bank reachable again", body: "80 ms from this computer" });
    for (let i = 0; i < PROBE_KEEP + 10; i++) reachStep(r, "x", true, 1, null, 10 + i);
    expect(r.samples.length).toBe(PROBE_KEEP);
  });
});
