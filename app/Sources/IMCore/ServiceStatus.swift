import Foundation

public enum ServiceState: String, Codable, Sendable, Comparable {
    case operational, maintenance, unknown, degraded, outage
    var rank: Int { switch self { case .operational: 0; case .maintenance: 1; case .unknown: 2; case .degraded: 3; case .outage: 4 } }
    public static func < (a: ServiceState, b: ServiceState) -> Bool { a.rank < b.rank }
    public var label: String { switch self { case .operational: "OPERATIONAL"; case .maintenance: "MAINTENANCE"; case .unknown: "NO FEED"; case .degraded: "DEGRADED"; case .outage: "OUTAGE" } }
    public var isProblem: Bool { self == .degraded || self == .outage }
}

public struct ServiceDef: Codable, Identifiable, Hashable, Sendable {
    public var id: String
    public var name: String
    public var category: String
    public var adapter: String
    public var statusUrl: String?
    public var pageUrl: String
    public var probeUrl: String
    public var custom: Bool?
    public init(id: String, name: String, category: String = "Custom", adapter: String = "none", statusUrl: String? = nil, pageUrl: String, probeUrl: String, custom: Bool? = nil) {
        self.id = id; self.name = name; self.category = category; self.adapter = adapter; self.statusUrl = statusUrl; self.pageUrl = pageUrl; self.probeUrl = probeUrl; self.custom = custom
    }
    /// Agent target id used when a node monitors this service.
    public var targetId: String { "svc-" + id }
    /// Poll interval for the official feed (the Google/AWS feeds are ~300 KB, so they are fetched less often).
    public var pollSeconds: TimeInterval { ["gcp", "aws"].contains(adapter) ? 300 : 90 }
    public var probeHost: String { URL(string: probeUrl)?.host ?? probeUrl }
}

public struct ServiceCatalog: Codable, Sendable {
    public var version: Int
    public var services: [ServiceDef]
    public static let builtIn: [ServiceDef] = {
        (try? JSONDecoder().decode(ServiceCatalog.self, from: Data(ServiceCatalogData.json.utf8)))?.services ?? []
    }()
}

public struct ServiceIncident: Equatable, Sendable, Identifiable { public var id: String; public var title: String; public var impact: ServiceState }
public struct ServiceComponent: Equatable, Sendable, Identifiable { public var name: String; public var state: ServiceState; public var id: String { name } }

public struct ServiceStatus: Equatable, Sendable {
    public var state: ServiceState
    public var summary: String
    public var incidents: [ServiceIncident]
    public var components: [ServiceComponent]
    public var checkedAt: Date
    public init(state: ServiceState, summary: String, incidents: [ServiceIncident] = [], components: [ServiceComponent] = [], checkedAt: Date = Date()) {
        self.state = state; self.summary = summary; self.incidents = incidents; self.components = components; self.checkedAt = checkedAt
    }
}

/// Parsers for each public status-feed format. All pure; network fetching lives in `ServiceStatusFetcher`.
public enum StatusAdapter {
    public static func parse(adapter: String, data: Data) -> ServiceStatus? {
        switch adapter {
        case "statuspage": return statuspage(data)
        case "gcp": return googleIncidents(data)
        case "aws": return aws(data)
        case "rss": return rss(data)
        case "slack": return slack(data)
        case "stripe": return stripe(data)
        default: return nil
        }
    }

    static func json(_ d: Data) -> Any? { try? JSONSerialization.jsonObject(with: d) }

    // Atlassian Statuspage: /api/v2/summary.json
    public static func statuspage(_ d: Data) -> ServiceStatus? {
        guard let o = json(d) as? [String: Any], let st = o["status"] as? [String: Any], let ind = st["indicator"] as? String else { return nil }
        let state: ServiceState = switch ind { case "none": .operational; case "minor": .degraded; case "major", "critical": .outage; case "maintenance": .maintenance; default: .unknown }
        let incidents = (o["incidents"] as? [[String: Any]] ?? []).map { i -> ServiceIncident in
            let impact = i["impact"] as? String ?? "minor"
            return ServiceIncident(id: i["id"] as? String ?? UUID().uuidString, title: i["name"] as? String ?? "Incident",
                                   impact: impact == "critical" || impact == "major" ? .outage : (impact == "maintenance" ? .maintenance : .degraded))
        }
        let comps = (o["components"] as? [[String: Any]] ?? []).compactMap { c -> ServiceComponent? in
            guard let s = c["status"] as? String, s != "operational", c["group"] as? Bool != true else { return nil }
            let cs: ServiceState = s == "major_outage" ? .outage : (s == "under_maintenance" ? .maintenance : .degraded)
            return ServiceComponent(name: c["name"] as? String ?? "?", state: cs)
        }
        return ServiceStatus(state: state, summary: st["description"] as? String ?? ind, incidents: incidents, components: comps)
    }

    // Google Cloud and Google Workspace: incidents.json; active incidents have no "end".
    public static func googleIncidents(_ d: Data) -> ServiceStatus? {
        guard let arr = json(d) as? [[String: Any]] else { return nil }
        var inc: [ServiceIncident] = []
        for x in arr where x["end"] == nil || x["end"] is NSNull {
            let impact = x["status_impact"] as? String ?? ""
            let st: ServiceState = impact == "SERVICE_OUTAGE" ? .outage : (impact == "SERVICE_DISRUPTION" ? .degraded : .operational)
            guard st != .operational else { continue }
            let title = [x["service_name"] as? String, (x["external_desc"] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines)].compactMap { $0 }.joined(separator: ": ")
            inc.append(ServiceIncident(id: x["id"] as? String ?? title, title: title, impact: st))
        }
        let worst = inc.map(\.impact).max() ?? .operational
        return ServiceStatus(state: worst, summary: inc.isEmpty ? "No active incidents" : "\(inc.count) active incident\(inc.count == 1 ? "" : "s")", incidents: inc)
    }

    // AWS Health: UTF-16 JSON array of current events. Long-running regional events are common, so they show as degraded, not outage.
    public static func aws(_ d: Data) -> ServiceStatus? {
        var data = d
        if d.count >= 2, (d[0] == 0xFE && d[1] == 0xFF) || (d[0] == 0xFF && d[1] == 0xFE) {
            guard let s = String(data: d, encoding: .utf16) else { return nil }
            data = Data(s.utf8)
        }
        guard let arr = json(data) as? [[String: Any]] else { return nil }
        var inc: [ServiceIncident] = []
        for e in arr {
            let status = Int("\(e["status"] ?? "0")") ?? 0
            guard status >= 2 else { continue }
            let title = [e["region_name"] as? String, e["summary"] as? String].compactMap { $0 }.joined(separator: ": ")
            inc.append(ServiceIncident(id: e["arn"] as? String ?? title, title: title, impact: .degraded))
        }
        return ServiceStatus(state: inc.isEmpty ? .operational : .degraded, summary: inc.isEmpty ? "No active events" : "\(inc.count) active event\(inc.count == 1 ? "" : "s")", incidents: inc)
    }

    // Azure Status RSS: every <item> is an active issue.
    public static func rss(_ d: Data) -> ServiceStatus? {
        final class P: NSObject, XMLParserDelegate {
            var titles: [String] = []; var inItem = false; var inTitle = false; var buf = ""
            func parser(_ p: XMLParser, didStartElement e: String, namespaceURI: String?, qualifiedName: String?, attributes: [String: String]) {
                if e == "item" { inItem = true }; if inItem && e == "title" { inTitle = true; buf = "" }
            }
            func parser(_ p: XMLParser, foundCharacters s: String) { if inTitle { buf += s } }
            func parser(_ p: XMLParser, didEndElement e: String, namespaceURI: String?, qualifiedName: String?) {
                if e == "title" && inTitle { titles.append(buf.trimmingCharacters(in: .whitespacesAndNewlines)); inTitle = false }
                if e == "item" { inItem = false }
            }
        }
        let p = P(); let x = XMLParser(data: d); x.delegate = p
        guard x.parse(), String(data: d.prefix(400), encoding: .utf8)?.contains("<rss") == true else { return nil }
        let inc = p.titles.map { ServiceIncident(id: $0, title: $0, impact: .degraded) }
        return ServiceStatus(state: inc.isEmpty ? .operational : .degraded, summary: inc.isEmpty ? "No active issues" : "\(inc.count) active issue\(inc.count == 1 ? "" : "s")", incidents: inc)
    }

    public static func slack(_ d: Data) -> ServiceStatus? {
        guard let o = json(d) as? [String: Any], let st = o["status"] as? String else { return nil }
        let incs = (o["active_incidents"] as? [[String: Any]] ?? []).map { i in
            ServiceIncident(id: "\(i["id"] ?? i["title"] ?? "")", title: i["title"] as? String ?? "Incident", impact: (i["type"] as? String) == "outage" ? .outage : .degraded)
        }
        if st == "ok" { return ServiceStatus(state: .operational, summary: "All systems operational") }
        return ServiceStatus(state: incs.map(\.impact).max() ?? .degraded, summary: "\(max(incs.count, 1)) active incident(s)", incidents: incs)
    }

    public static func stripe(_ d: Data) -> ServiceStatus? {
        guard let o = json(d) as? [String: Any], let l = o["largestatus"] as? String else { return nil }
        let msg = o["message"] as? String ?? l
        return ServiceStatus(state: l == "up" ? .operational : (l == "down" ? .outage : .degraded), summary: msg)
    }
}

public enum ServiceStatusFetcher {
    public static func fetch(_ svc: ServiceDef, session: URLSession = .shared) async throws -> ServiceStatus {
        guard svc.adapter != "none", let u = svc.statusUrl, let url = URL(string: u) else {
            return ServiceStatus(state: .unknown, summary: "No public status feed — reachability only")
        }
        var req = URLRequest(url: url, timeoutInterval: 15)
        req.setValue("InternetMonitor", forHTTPHeaderField: "User-Agent")
        let (data, resp) = try await session.data(for: req)
        let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
        guard (200..<300).contains(code) else { throw AgentError(status: code, message: "Status feed returned HTTP \(code)") }
        guard let st = StatusAdapter.parse(adapter: svc.adapter, data: data) else { throw AgentError(status: 0, message: "Unrecognised status feed format") }
        return st
    }
}
