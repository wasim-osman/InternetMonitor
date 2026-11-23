import { describe, expect, it } from "vitest";
import { csvEscape, duration, endpointKey, endpointLabel, ipKey, isTailscaleIP, latencyColor, ms, paletteSlot, pct, seriesCSV, uptime, worstOf, C } from "./fmt";
import type { Target } from "./types";

describe("fmt", () => {
  it("formats ms like the Swift app", () => {
    expect(ms(null)).toBe("—");
    expect(ms(1234)).toBe("1.2s");
    expect(ms(150.4)).toBe("150");
    expect(ms(12.34)).toBe("12.3");
    expect(ms(1.234)).toBe("1.23");
  });
  it("formats pct / uptime / duration", () => {
    expect(pct(0)).toBe("0%");
    expect(pct(2.5)).toBe("2.5%");
    expect(pct(12.5)).toBe("13%");
    expect(uptime(99.999)).toBe("100%");
    expect(uptime(99.5)).toBe("99.50%");
    expect(duration(59)).toBe("59s");
    expect(duration(125)).toBe("2m 5s");
    expect(duration(3 * 3600 + 120)).toBe("3h 2m");
    expect(duration(2 * 86400 + 3600)).toBe("2d 1h");
  });
  it("orders states by severity", () => {
    expect(worstOf([])).toBe("unknown");
    expect(worstOf(["up", "unknown"])).toBe("unknown");
    expect(worstOf(["up", "degraded", "unknown"])).toBe("degraded");
    expect(worstOf(["up", "down", "degraded"])).toBe("down");
  });
  it("latency heat colours", () => {
    expect(latencyColor(10)).toBe(C.green);
    expect(latencyColor(150)).toBe(C.amber);
    expect(latencyColor(500)).toBe(C.red);
  });
  it("palette slot matches the Swift FNV-1a implementation", () => {
    expect(paletteSlot("icmp|1.1.1.1|0", 8)).toBe(4);
    expect(paletteSlot("icmp|@gateway|0", 8)).toBe(4);
    expect(paletteSlot("tcp|example.com|443", 8)).toBe(2);
  });
  it("endpoint keys and labels", () => {
    const t: Target = { id: "a", name: "A", kind: "tcp", host: "Example.com", port: 443, enabled: true, group: "custom" };
    expect(endpointKey(t)).toBe("tcp|example.com|443");
    expect(endpointLabel(t)).toBe("Example.com:443");
    expect(endpointLabel({ ...t, kind: "dns" })).toBe("dns Example.com");
  });
  it("ip helpers", () => {
    expect(isTailscaleIP("100.64.0.1")).toBe(true);
    expect(isTailscaleIP("100.128.0.1")).toBe(false);
    expect(ipKey("10.0.0.2") < ipKey("10.0.0.10")).toBe(true);
    expect(ipKey("192.168.1.1") > ipKey("100.1.1.1")).toBe(true);
  });
  it("CSV export", () => {
    expect(csvEscape('a,"b"')).toBe('"a,""b"""');
    const csv = seriesCSV([{ node: "Pi, Toronto", target: "CF", series: { target: "x", stepSec: 60, points: [{ t: 1700000000, n: 30, loss: 0, min: 1, avg: 2.5, max: 4, p95: 3 }, { t: 1700000060, n: 30, loss: 100 }] } }]);
    const lines = csv.trim().split("\n");
    expect(lines[0]).toBe("node,target,time_iso,time_unix,samples,loss_pct,min_ms,avg_ms,max_ms,p95_ms");
    expect(lines[1]).toBe('"Pi, Toronto",CF,2023-11-14T22:13:20Z,1700000000,30,0.000,1.000,2.500,4.000,3.000');
    expect(lines[2]).toBe('"Pi, Toronto",CF,2023-11-14T22:14:20Z,1700000060,30,100.000,,,,');
  });
});
