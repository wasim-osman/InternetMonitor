import Foundation

// MARK: - Agent API models (see docs/API.md). Everything uncertain is optional; unknown keys are ignored.

public struct Identity: Codable, Equatable, Sendable {
    public var service: String
    public var apiVersion: Int?
    public var version: String?
    public var nodeId: String
    public var hostname: String?
    public var nodeName: String?
    public var os: String?
    public var arch: String?
    public var uptimeSec: Int?
    public var startedAt: Int?
    public var paired: Bool?
    public var isAgent: Bool { service == "internetmonitor-agent" }
    public var displayName: String {
        if let n = nodeName, !n.isEmpty { return n }
        return hostname ?? nodeId
    }
}

public enum ProbeKind: String, Codable, CaseIterable, Sendable {
    case icmp, tcp, http, dns
    public var label: String {
        switch self { case .icmp: "PING"; case .tcp: "TCP"; case .http: "HTTP"; case .dns: "DNS" }
    }
}

public struct Target: Codable, Identifiable, Hashable, Sendable {
    public var id: String
    public var name: String
    public var kind: ProbeKind
    public var host: String?
    public var port: Int?
    public var url: String?
    public var resolver: String?
    public var intervalMs: Int?
    public var enabled: Bool
    public var group: String
    public var nodeRef: String?

    public init(id: String, name: String, kind: ProbeKind, host: String? = nil, port: Int? = nil, url: String? = nil,
                resolver: String? = nil, intervalMs: Int? = nil, enabled: Bool = true, group: String = "custom", nodeRef: String? = nil) {
        self.id = id; self.name = name; self.kind = kind; self.host = host; self.port = port; self.url = url
        self.resolver = resolver; self.intervalMs = intervalMs; self.enabled = enabled; self.group = group; self.nodeRef = nodeRef
    }

    /// Identity of "what is being measured", independent of which node measures it. Used for matrix columns.
    public var endpointKey: String {
        "\(kind.rawValue)|\((host ?? url ?? "").lowercased())|\(port ?? 0)"
    }
    public var endpointLabel: String {
        switch kind {
        case .icmp: return host ?? "?"
        case .tcp: return "\(host ?? "?"):\(port ?? 0)"
        case .http: return url ?? host ?? "?"
        case .dns: return "dns \(host ?? "?")"
        }
    }
}

public struct Rules: Codable, Equatable, Sendable {
    public var downAfterFailures: Int
    public var degradedLatencyMs: Int
    public var degradedForSec: Int
    public var webhookUrl: String
    public init(downAfterFailures: Int = 3, degradedLatencyMs: Int = 150, degradedForSec: Int = 60, webhookUrl: String = "") {
        self.downAfterFailures = downAfterFailures; self.degradedLatencyMs = degradedLatencyMs
        self.degradedForSec = degradedForSec; self.webhookUrl = webhookUrl
    }
}

public struct SpeedtestCfg: Codable, Equatable, Sendable {
    public var enabled: Bool
    public var intervalMin: Int
    public var url: String
    public init(enabled: Bool = false, intervalMin: Int = 60, url: String = "https://speed.cloudflare.com/__down?bytes=25000000") {
        self.enabled = enabled; self.intervalMin = intervalMin; self.url = url
    }
}

public struct AgentConfig: Codable, Equatable, Sendable {
    public var nodeName: String
    public var intervalMs: Int
    public var timeoutMs: Int
    public var retentionRawHours: Int
    public var retentionRollupDays: Int
    public var metricsPublic: Bool
    public var targets: [Target]
    public var rules: Rules
    public var speedtest: SpeedtestCfg
    public init(nodeName: String = "", intervalMs: Int = 2000, timeoutMs: Int = 2000, retentionRawHours: Int = 6,
                retentionRollupDays: Int = 14, metricsPublic: Bool = false, targets: [Target] = [],
                rules: Rules = Rules(), speedtest: SpeedtestCfg = SpeedtestCfg()) {
        self.nodeName = nodeName; self.intervalMs = intervalMs; self.timeoutMs = timeoutMs
        self.retentionRawHours = retentionRawHours; self.retentionRollupDays = retentionRollupDays
        self.metricsPublic = metricsPublic; self.targets = targets; self.rules = rules; self.speedtest = speedtest
    }
}

public enum LinkState: String, Codable, Sendable, Comparable {
    case up, degraded, down, unknown
    public var severity: Int { switch self { case .up: 0; case .unknown: 1; case .degraded: 2; case .down: 3 } }
    public static func < (a: LinkState, b: LinkState) -> Bool { a.severity < b.severity }
    public init(from decoder: Decoder) throws {
        let s = try decoder.singleValueContainer().decode(String.self)
        self = LinkState(rawValue: s) ?? .unknown
    }
}

public struct LiveTarget: Codable, Identifiable, Equatable, Sendable {
    public var id: String
    public var state: LinkState
    public var lastRttMs: Double?
    public var lastAt: Int?
    public var lastError: String?
    public var resolvedHost: String?
    public var windowSec: Int?
    public var avgMs: Double?
    public var minMs: Double?
    public var maxMs: Double?
    public var p95Ms: Double?
    public var jitterMs: Double?
    public var lossPct: Double?
    public var samples: Int?
    public var pathHint: String?
}

public struct LiveResponse: Codable, Sendable { public var now: Int; public var targets: [LiveTarget] }

public struct SeriesPoint: Codable, Sendable, Equatable {
    public var t: Int
    public var n: Int?
    public var loss: Double?
    public var min: Double?
    public var avg: Double?
    public var max: Double?
    public var p95: Double?
    public init(t: Int, n: Int? = nil, loss: Double? = nil, min: Double? = nil, avg: Double? = nil, max: Double? = nil, p95: Double? = nil) {
        self.t = t; self.n = n; self.loss = loss; self.min = min; self.avg = avg; self.max = max; self.p95 = p95
    }
    public var date: Date { Date(timeIntervalSince1970: TimeInterval(t)) }
}

public struct Series: Codable, Sendable, Equatable {
    public var target: String
    public var stepSec: Int
    public var source: String?
    public var points: [SeriesPoint]
}
public struct SeriesResponse: Codable, Sendable { public var from: Int; public var to: Int; public var series: [Series] }

public struct SummaryWindow: Codable, Sendable, Equatable, Identifiable {
    public var label: String
    public var seconds: Int?
    public var uptimePct: Double?
    public var lossPct: Double?
    public var avgMs: Double?
    public var p95Ms: Double?
    public var minMs: Double?
    public var maxMs: Double?
    public var jitterMs: Double?
    public var samples: Int?
    public var id: String { label }
}
public struct TargetSummary: Codable, Sendable, Equatable { public var target: String; public var windows: [SummaryWindow] }
public struct SummaryResponse: Codable, Sendable { public var summaries: [TargetSummary] }

public struct Incident: Codable, Identifiable, Sendable, Equatable, Hashable {
    public var id: String
    public var targetId: String?
    public var targetName: String?
    public var kind: String
    public var startedAt: Int
    public var endedAt: Int?
    public var detail: String?
    public var peakMs: Double?
    public var isOpen: Bool { endedAt == nil }
    public var duration: TimeInterval { TimeInterval((endedAt ?? Int(Date().timeIntervalSince1970)) - startedAt) }
}

public struct IfaceInfo: Codable, Sendable, Equatable, Identifiable {
    public var name: String
    public var kind: String?
    public var addrs: [String]?
    public var up: Bool?
    public var rxBytes: Int?
    public var txBytes: Int?
    public var id: String { name }
}
public struct WifiInfo: Codable, Sendable, Equatable { public var iface: String?; public var ssid: String?; public var rssiDbm: Int?; public var linkQuality: Int? }
public struct TSPeer: Codable, Sendable, Equatable, Identifiable {
    public var name: String; public var ip: String?; public var online: Bool?; public var path: String?; public var relay: String?; public var curAddr: String?
    public var id: String { name + (ip ?? "") }
}
public struct TSInfo: Codable, Sendable, Equatable { public var selfIp: String?; public var peers: [TSPeer]? }
public struct SystemInfo: Codable, Sendable, Equatable {
    public var hostname: String?; public var os: String?; public var kernel: String?; public var arch: String?; public var model: String?
    public var uptimeSec: Int?; public var loadAvg: [Double]?
    public var memTotalMB: Int?; public var memUsedMB: Int?; public var diskFreeMB: Int?; public var tempC: Double?
    public var interfaces: [IfaceInfo]?; public var gateway: String?; public var defaultInterface: String?
    public var wifi: WifiInfo?; public var tailscale: TSInfo?
}

public struct Hop: Codable, Sendable, Equatable, Identifiable {
    public var ttl: Int; public var ip: String?; public var name: String?; public var rttMs: [Double?]; public var reached: Bool?
    public var id: Int { ttl }
    public var avg: Double? { let v = rttMs.compactMap { $0 }; return v.isEmpty ? nil : v.reduce(0, +) / Double(v.count) }
}
public struct TraceResult: Codable, Sendable, Equatable { public var host: String; public var ip: String?; public var hops: [Hop] }

public struct SpeedResult: Codable, Sendable, Equatable, Identifiable {
    public var at: Int; public var url: String?; public var bytes: Int?; public var durationMs: Int?
    public var downMbps: Double?; public var latencyMs: Double?; public var error: String?
    public var id: Int { at }
}

public struct ClientInfo: Codable, Sendable, Equatable, Identifiable {
    public var id: String; public var name: String; public var createdAt: Int?; public var lastSeen: Int?
}
public struct PairResponse: Codable, Sendable { public var token: String; public var nodeId: String?; public var clientId: String? }
public struct PairCode: Codable, Sendable { public var code: String; public var expiresAt: Int? }

public enum AppInfo {
    public static let author = "Wasim Osman"
    /// Agent version bundled with this app build; nodes running an older one are offered an upgrade.
    public static let bundledAgentVersion = "1.0.0"
    public static let githubURL = "https://github.com/wasim-osman/InternetMonitor"
}
