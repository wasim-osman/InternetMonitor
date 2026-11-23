import Foundation

public struct AgentError: LocalizedError, Sendable {
    public var status: Int
    public var message: String
    public init(status: Int, message: String) { self.status = status; self.message = message }
    public var errorDescription: String? { status == 0 ? message : "\(message) (HTTP \(status))" }
    public var isUnauthorized: Bool { status == 401 }
}

/// Thin typed client for one agent. Stateless and Sendable.
public struct AgentClient: Sendable {
    public var host: String
    public var port: Int
    public var token: String?
    public init(host: String, port: Int = 7878, token: String? = nil) { self.host = host; self.port = port; self.token = token }

    var base: String { host.contains(":") && !host.hasPrefix("[") ? "http://[\(host)]:\(port)" : "http://\(host):\(port)" }

    static let session: URLSession = {
        let c = URLSessionConfiguration.ephemeral
        c.waitsForConnectivity = false
        c.httpMaximumConnectionsPerHost = 6
        c.requestCachePolicy = .reloadIgnoringLocalCacheData
        return URLSession(configuration: c)
    }()

    public func request<T: Decodable>(_ method: String, _ path: String, body: Encodable? = nil,
                                      timeout: TimeInterval = 6, auth: Bool = true, as: T.Type = T.self) async throws -> T {
        guard let url = URL(string: base + path) else { throw AgentError(status: 0, message: "Bad URL") }
        var req = URLRequest(url: url, timeoutInterval: timeout)
        req.httpMethod = method
        if auth, let token { req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization") }
        if let body {
            req.httpBody = try JSONEncoder().encode(AnyEncodable(body))
            req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        let data: Data, resp: URLResponse
        do { (data, resp) = try await Self.session.data(for: req) }
        catch { throw AgentError(status: 0, message: (error as? URLError)?.localizedDescription ?? error.localizedDescription) }
        let status = (resp as? HTTPURLResponse)?.statusCode ?? 0
        guard (200..<300).contains(status) else {
            let msg = (try? JSONDecoder().decode([String: String].self, from: data))?["error"] ?? String(data: data.prefix(200), encoding: .utf8) ?? "error"
            throw AgentError(status: status, message: msg)
        }
        if T.self == Empty.self { return Empty() as! T }
        do { return try JSONDecoder().decode(T.self, from: data) }
        catch { throw AgentError(status: status, message: "Unexpected response from agent: \(error.localizedDescription)") }
    }

    public struct Empty: Decodable { public init() {}; public init(from decoder: Decoder) throws {} }
    struct AnyEncodable: Encodable { let v: Encodable; init(_ v: Encodable) { self.v = v }
        func encode(to e: Encoder) throws { try v.encode(to: e) } }

    // MARK: Endpoints
    public func identity(timeout: TimeInterval = 2) async throws -> Identity { try await request("GET", "/v1/identity", timeout: timeout, auth: false) }
    public func pair(code: String, clientName: String) async throws -> PairResponse {
        try await request("POST", "/v1/pair", body: ["code": code, "clientName": clientName], auth: false)
    }
    public func config() async throws -> AgentConfig { try await request("GET", "/v1/config") }
    public func putConfig(_ c: AgentConfig) async throws -> AgentConfig { try await request("PUT", "/v1/config", body: c) }
    public func live() async throws -> LiveResponse { try await request("GET", "/v1/live", timeout: 4) }
    public func series(targets: [String], from: Int, to: Int, points: Int) async throws -> SeriesResponse {
        let ids = targets.joined(separator: ",").addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? ""
        return try await request("GET", "/v1/series?target=\(ids)&from=\(from)&to=\(to)&points=\(points)", timeout: 15)
    }
    public func summary() async throws -> SummaryResponse { try await request("GET", "/v1/summary", timeout: 15) }
    public func incidents(since: Int? = nil, limit: Int = 200) async throws -> [Incident] {
        try await request("GET", "/v1/incidents?limit=\(limit)" + (since.map { "&since=\($0)" } ?? ""))
    }
    public func system() async throws -> SystemInfo { try await request("GET", "/v1/system", timeout: 8) }
    public func traceroute(host: String, maxHops: Int = 20) async throws -> TraceResult {
        try await request("POST", "/v1/traceroute", body: TraceBody(host: host, maxHops: maxHops), timeout: 45)
    }
    public func speedtest() async throws -> SpeedResult { try await request("POST", "/v1/speedtest", timeout: 90) }
    public func speedHistory() async throws -> [SpeedResult] { try await request("GET", "/v1/speedtest/history") }
    public func newPairCode() async throws -> PairCode { try await request("POST", "/v1/pair-code") }
    public func clients() async throws -> [ClientInfo] { try await request("GET", "/v1/clients") }
    public func deleteClient(_ id: String) async throws { let _: Empty = try await request("DELETE", "/v1/clients/\(id)") }
    public func restart() async throws { let _: Empty = try await request("POST", "/v1/restart") }
    public func resetHistory() async throws { let _: Empty = try await request("POST", "/v1/reset-history") }
}

struct TraceBody: Encodable { var host: String; var maxHops: Int }
