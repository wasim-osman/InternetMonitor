import Foundation
import Testing
@testable import IMCore

/// Runs only when IM_AGENT=host:port and IM_TOKEN are set (a real agent). Verifies every decoder against real JSON.
@Suite(.serialized) struct LiveAgent {
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

@Test func liveAgentServiceApply() async throws {
    guard let a = ProcessInfo.processInfo.environment["IM_AGENT"], let tok = ProcessInfo.processInfo.environment["IM_TOKEN"] else { return }
    let p = a.split(separator: ":"); let c = AgentClient(host: String(p[0]), port: Int(p[1])!, token: tok)
    let base = try await c.config()
    let want = ServiceCatalog.builtIn.filter { ["github", "bmo"].contains($0.id) }
    let saved = try await c.putConfig(ServiceTargets.merged(base, wanted: want))
    #expect(saved.targets.contains { $0.id == "svc-github" } && saved.targets.contains { $0.id == "svc-bmo" })
    var l: LiveTarget?
    for _ in 0..<15 { try await Task.sleep(for: .seconds(1)); l = try await c.live().targets.first { $0.id == "svc-github" }; if (l?.samples ?? 0) > 0 { break } }
    let all = try await c.live().targets; print("LIVE-IDS", all.map { "\($0.id):\($0.state.rawValue):\($0.samples ?? -1)" }); print("CFG-IDS", saved.targets.map(\.id))
    print("NODE-LIVE", l?.state.rawValue ?? "none", l?.lastRttMs ?? -1)
    #expect(l != nil && l?.state != .down)
    let back = try await c.putConfig(ServiceTargets.merged(saved, wanted: []))
    #expect(!back.targets.contains { $0.id.hasPrefix("svc-") }); #expect(back.targets.count == base.targets.count)
}
}
