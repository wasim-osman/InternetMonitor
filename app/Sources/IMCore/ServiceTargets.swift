import Foundation

public enum ServiceTargets {
    public static let prefix = "svc-"
    public static let intervalMs = 30000

    public static func target(for s: ServiceDef) -> Target {
        Target(id: s.targetId, name: s.name, kind: .http, host: s.probeHost, url: s.probeUrl, intervalMs: intervalMs, enabled: true, group: "service")
    }

    /// Returns `cfg` with exactly `wanted` service targets: stale `svc-*` ones removed, missing ones added, everything else untouched.
    public static func merged(_ cfg: AgentConfig, wanted: [ServiceDef]) -> AgentConfig {
        var out = cfg
        let ids = Set(wanted.map(\.targetId))
        out.targets.removeAll { $0.id.hasPrefix(prefix) && !ids.contains($0.id) }
        for s in wanted where !out.targets.contains(where: { $0.id == s.targetId }) { out.targets.append(target(for: s)) }
        return out
    }
}
