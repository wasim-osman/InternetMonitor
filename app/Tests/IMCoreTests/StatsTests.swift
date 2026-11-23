import Foundation
import Testing
@testable import IMCore

@Test func percentiles() {
    #expect(Stats.percentile([1, 2, 3, 4, 5], 50) == 3)
    #expect(Stats.percentile([10], 95) == 10)
    #expect(Stats.percentile([], 95) == nil)
    #expect(abs(Stats.percentile([0, 100], 95)! - 95) < 1e-9)
}
@Test func jitter() {
    #expect(Stats.jitter([10, 20, 10]) == 10)
    #expect(Stats.jitter([5]) == nil)
}
@Test func sweepRange() {
    let a = Discovery.sweepAddresses(ip: "192.168.1.10", prefix: 24)
    #expect(a.count == 253); #expect(!a.contains("192.168.1.10")); #expect(a.first == "192.168.1.1"); #expect(a.last == "192.168.1.254")
    #expect(Discovery.sweepAddresses(ip: "10.1.2.3", prefix: 8).count == 253)   // clamped to /24
    #expect(Discovery.sweepAddresses(ip: "10.1.2.3", prefix: 22).count == 1021)
    #expect(Discovery.sweepAddresses(ip: "10.1.2.3", prefix: 32).isEmpty)
}
@Test func tailscaleRange() {
    #expect(Discovery.isTailscaleIP("100.64.0.1")); #expect(Discovery.isTailscaleIP("100.100.0.1"))
    #expect(!Discovery.isTailscaleIP("100.128.0.1")); #expect(!Discovery.isTailscaleIP("192.168.1.1"))
}
@Test func maskPrefix() { #expect(Discovery.maskToPrefix("255.255.255.0") == 24); #expect(Discovery.maskToPrefix("255.255.252.0") == 22) }
@Test func targetDecodeIgnoresUnknown() throws {
    let j = #"{"id":"a","name":"n","kind":"icmp","host":"1.1.1.1","enabled":true,"group":"baseline","future":1}"#
    let t = try JSONDecoder().decode(Target.self, from: Data(j.utf8))
    #expect(t.kind == .icmp && t.endpointKey == "icmp|1.1.1.1|0")
}
@Test func csvEscape() { #expect(CSV.escape("a,b") == "\"a,b\""); #expect(CSV.escape("x") == "x") }
@Test func storeRoundTrip() throws {
    let dir = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    let s = NodeStore(directory: dir)
    s.save([SavedNode(nodeId: "n1", name: "Pi", host: "1.2.3.4", token: "tok")])
    #expect(s.load().first?.token == "tok")
    let attrs = try FileManager.default.attributesOfItem(atPath: s.url.path)
    #expect((attrs[.posixPermissions] as? Int) == 0o600)
}

@Test func semver() {
    #expect(SemVer("v1.2.10")! > SemVer("1.2.9")!)
    #expect(SemVer("1.0")! == SemVer("1.0.0")!)
    #expect(SemVer("2.0.0-beta1")! > SemVer("1.9.9")!)
    #expect(SemVer("abc") == nil)
}
@Test func updateDecision() throws {
    let j = #"{"tag_name":"v1.1.0","body":"notes","draft":false,"prerelease":false,"assets":[{"name":"InternetMonitor-macOS-universal.zip","browser_download_url":"https://x/y.zip","size":5}]}"#
    let r = try JSONDecoder().decode(Release.self, from: Data(j.utf8))
    #expect(UpdateCheck.isNewer(r, than: "1.0.0")); #expect(!UpdateCheck.isNewer(r, than: "1.1.0")); #expect(!UpdateCheck.isNewer(r, than: "1.2.0"))
    var pre = r; pre.prerelease = true; #expect(!UpdateCheck.isNewer(pre, than: "1.0.0"))
}
@Test func sumsParsing() {
    let h = String(repeating: "a", count: 64)
    let m = UpdateCheck.parseSums("\(h)  InternetMonitor-macOS-universal.zip\n\(h) *other.bin\nbad line\n")
    #expect(m["InternetMonitor-macOS-universal.zip"] == h); #expect(m["other.bin"] == h); #expect(m.count == 2)
}
@Test func sha256File() throws {
    let u = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    try Data("abc".utf8).write(to: u)
    #expect(try UpdateCheck.sha256(of: u) == "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad")
}
