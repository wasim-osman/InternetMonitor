import Foundation

/// A node the user chose to monitor. Token authorises this Mac against that agent.
public struct SavedNode: Codable, Identifiable, Equatable, Sendable {
    public var nodeId: String
    public var name: String
    public var host: String
    public var port: Int
    public var token: String
    public var sshUser: String?
    public var addedAt: Date
    public var id: String { nodeId }
    public init(nodeId: String, name: String, host: String, port: Int = 7878, token: String, sshUser: String? = nil, addedAt: Date = Date()) {
        self.nodeId = nodeId; self.name = name; self.host = host; self.port = port; self.token = token; self.sshUser = sshUser; self.addedAt = addedAt
    }
}

public final class NodeStore: @unchecked Sendable {
    public let url: URL
    public init(directory: URL? = nil) {
        let dir = directory ?? FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("InternetMonitor", isDirectory: true)
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        url = dir.appendingPathComponent("nodes.json")
    }
    public func load() -> [SavedNode] {
        guard let d = try? Data(contentsOf: url) else { return [] }
        let dec = JSONDecoder(); dec.dateDecodingStrategy = .iso8601
        return (try? dec.decode([SavedNode].self, from: d)) ?? []
    }
    public func save(_ nodes: [SavedNode]) {
        let enc = JSONEncoder(); enc.dateEncodingStrategy = .iso8601; enc.outputFormatting = [.prettyPrinted, .sortedKeys]
        guard let d = try? enc.encode(nodes) else { return }
        let tmp = url.appendingPathExtension("tmp")
        FileManager.default.createFile(atPath: tmp.path, contents: d, attributes: [.posixPermissions: 0o600])
        _ = try? FileManager.default.replaceItemAt(url, withItemAt: tmp)
    }
}
