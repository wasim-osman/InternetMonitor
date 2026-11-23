import Foundation
import Testing
@testable import IMCore

/// Runs only when IM_AGENT=host:port and IM_TOKEN are set (a real agent). Verifies every decoder against real JSON.
@Test func liveAgentDecoding() async throws {
    guard let a = ProcessInfo.processInfo.environment["IM_AGENT"], let tok = ProcessInfo.processInfo.environment["IM_TOKEN"] else { return }
    let p = a.split(separator: ":"); let c = AgentClient(host: String(p[0]), port: Int(p[1])!, token: tok)
    let id = try await c.identity(); #expect(id.isAgent)
    let cfg = try await c.config(); #expect(!cfg.targets.isEmpty)
    let live = try await c.live(); #expect(live.targets.count > 0)
    let ids = cfg.targets.map(\.id); let now = Int(Date().timeIntervalSince1970)
    let s = try await c.series(targets: ids, from: now - 600, to: now, points: 100); #expect(s.series.count > 0)
    _ = try await c.summary(); _ = try await c.incidents(); _ = try await c.system(); _ = try await c.speedHistory()
    _ = try await c.clients()
    let t = try await c.traceroute(host: "127.0.0.1", maxHops: 3); #expect(!t.hops.isEmpty)
    let back = try await c.putConfig(cfg); #expect(back.targets.count == cfg.targets.count)
    _ = try await c.newPairCode()
}
