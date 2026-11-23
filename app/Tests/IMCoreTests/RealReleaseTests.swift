import Foundation
import Testing
@testable import IMCore

@Test func realLatestRelease() async throws {
    guard ProcessInfo.processInfo.environment["IM_NET"] != nil else { return }
    let r = try await UpdateCheck.latest()
    print("LATEST", r.tag_name, r.assets.count)
    #expect(r.assets.contains { $0.name == UpdateCheck.macAssetName } && r.assets.contains { $0.name == UpdateCheck.sumsAssetName })
    #expect(!UpdateCheck.isNewer(r, than: "1.0.0")); #expect(UpdateCheck.isNewer(r, than: "0.9.0"))
    let sums = r.assets.first { $0.name == "SHA256SUMS" }!
    let (d, _) = try await URLSession.shared.data(from: URL(string: sums.browser_download_url)!)
    let m = UpdateCheck.parseSums(String(decoding: d, as: UTF8.self)); print("SUMS", m.count)
    #expect(m[UpdateCheck.macAssetName] != nil)
}
