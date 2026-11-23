import Foundation
import CryptoKit

public struct SemVer: Comparable, Equatable, Sendable, CustomStringConvertible {
    public var parts: [Int]
    public init?(_ s: String) {
        var t = s.trimmingCharacters(in: .whitespaces)
        if t.hasPrefix("v") || t.hasPrefix("V") { t.removeFirst() }
        t = String(t.split(separator: "-", maxSplits: 1).first ?? "")      // ignore pre-release suffix
        let p = t.split(separator: ".").map { Int($0) }
        guard !p.isEmpty, p.allSatisfy({ $0 != nil }) else { return nil }
        parts = p.map { $0! }
    }
    public static func == (a: SemVer, b: SemVer) -> Bool { !(a < b) && !(b < a) }
    public static func < (a: SemVer, b: SemVer) -> Bool {
        for i in 0..<max(a.parts.count, b.parts.count) {
            let x = i < a.parts.count ? a.parts[i] : 0, y = i < b.parts.count ? b.parts[i] : 0
            if x != y { return x < y }
        }
        return false
    }
    public var description: String { parts.map(String.init).joined(separator: ".") }
}

public struct ReleaseAsset: Codable, Equatable, Sendable { public var name: String; public var browser_download_url: String; public var size: Int? }
public struct Release: Codable, Equatable, Sendable {
    public var tag_name: String
    public var name: String?
    public var body: String?
    public var html_url: String?
    public var prerelease: Bool?
    public var draft: Bool?
    public var assets: [ReleaseAsset]
    public var version: SemVer? { SemVer(tag_name) }
}

public enum UpdateCheck {
    public static let repo = "wasim-osman/InternetMonitor"
    public static let macAssetName = "InternetMonitor-macOS-universal.zip"
    public static let sumsAssetName = "SHA256SUMS"

    public static func latest(session: URLSession = .shared) async throws -> Release {
        var req = URLRequest(url: URL(string: "https://api.github.com/repos/\(repo)/releases/latest")!, timeoutInterval: 15)
        req.setValue("application/vnd.github+json", forHTTPHeaderField: "Accept")
        req.setValue("InternetMonitor-updater", forHTTPHeaderField: "User-Agent")      // generic; no user details
        let (d, r) = try await session.data(for: req)
        let code = (r as? HTTPURLResponse)?.statusCode ?? 0
        guard code == 200 else { throw AgentError(status: code, message: code == 404 ? "No releases published yet" : "GitHub returned HTTP \(code)") }
        return try JSONDecoder().decode(Release.self, from: d)
    }

    public static func isNewer(_ release: Release, than current: String) -> Bool {
        guard release.draft != true, release.prerelease != true, let v = release.version, let c = SemVer(current) else { return false }
        return v > c
    }

    /// "<hash>  <file>" lines (sha256sum format, optionally "*file").
    public static func parseSums(_ text: String) -> [String: String] {
        var out: [String: String] = [:]
        for line in text.split(whereSeparator: \.isNewline) {
            let f = line.split(maxSplits: 1, whereSeparator: { $0 == " " || $0 == "\t" })
            guard f.count == 2, f[0].count == 64 else { continue }
            out[f[1].trimmingCharacters(in: CharacterSet(charactersIn: " *\t"))] = f[0].lowercased()
        }
        return out
    }

    public static func sha256(of url: URL) throws -> String {
        let h = try FileHandle(forReadingFrom: url); defer { try? h.close() }
        var hasher = SHA256()
        while let chunk = try h.read(upToCount: 1 << 20), !chunk.isEmpty { hasher.update(data: chunk) }
        return hasher.finalize().map { String(format: "%02x", $0) }.joined()
    }
}
