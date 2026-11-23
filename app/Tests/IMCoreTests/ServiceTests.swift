import Foundation
import Testing
@testable import IMCore

@Test func catalogLoads() {
    let c = ServiceCatalog.builtIn
    #expect(c.count >= 40)
    #expect(Set(c.map(\.id)).count == c.count)
    for id in ["azure", "gcp", "aws", "oci", "atlassian", "anthropic", "openai", "m365", "gworkspace", "td", "bmo", "rbc", "scotiabank"] { #expect(c.contains { $0.id == id }, "missing \(id)") }
    #expect(c.allSatisfy { URL(string: $0.probeUrl)?.host != nil })
    #expect(c.filter { $0.adapter != "none" }.allSatisfy { $0.statusUrl != nil })
    #expect(Set(c.map(\.adapter)).isSubset(of: ["statuspage", "gcp", "aws", "rss", "slack", "stripe", "none"]))
}
@Test func statuspageParsing() throws {
    let j = #"{"status":{"indicator":"minor","description":"Partially Degraded Service"},"incidents":[{"id":"i1","name":"Slow API","impact":"minor"}],"components":[{"name":"API","status":"degraded_performance"},{"name":"Web","status":"operational"},{"name":"Group","status":"major_outage","group":true}]}"#
    let s = try #require(StatusAdapter.statuspage(Data(j.utf8)))
    #expect(s.state == .degraded); #expect(s.incidents.map(\.title) == ["Slow API"]); #expect(s.components.map(\.name) == ["API"])
    let ok = try #require(StatusAdapter.statuspage(Data(#"{"status":{"indicator":"none","description":"All Systems Operational"}}"#.utf8)))
    #expect(ok.state == .operational)
    let major = try #require(StatusAdapter.statuspage(Data(#"{"status":{"indicator":"critical","description":"x"}}"#.utf8)))
    #expect(major.state == .outage)
    #expect(StatusAdapter.statuspage(Data("<html>".utf8)) == nil)
}
@Test func googleParsing() throws {
    let j = #"[{"id":"a","begin":"2026-01-01","end":"2026-01-02","status_impact":"SERVICE_OUTAGE","service_name":"Old"},{"id":"b","begin":"2026-01-03","status_impact":"SERVICE_DISRUPTION","service_name":"Cloud Run","external_desc":" Elevated errors "},{"id":"c","status_impact":"SERVICE_INFORMATION","service_name":"Info"},{"id":"d","status_impact":"SERVICE_OUTAGE","service_name":"GCE","external_desc":"Down"}]"#
    let s = try #require(StatusAdapter.googleIncidents(Data(j.utf8)))
    #expect(s.state == .outage); #expect(s.incidents.count == 2); #expect(s.incidents.first?.title == "Cloud Run: Elevated errors")
    #expect(try #require(StatusAdapter.googleIncidents(Data("[]".utf8))).state == .operational)
}
@Test func awsParsingUTF16() throws {
    let j = #"[{"arn":"arn:1","region_name":"UAE","summary":"Region Availability","status":"3"},{"arn":"arn:2","region_name":"X","summary":"info","status":"1"}]"#
    var d = Data([0xFE, 0xFF]); d.append(try #require(j.data(using: .utf16BigEndian)))
    let s = try #require(StatusAdapter.aws(d))
    #expect(s.state == .degraded); #expect(s.incidents.count == 1); #expect(s.incidents[0].title == "UAE: Region Availability")
    #expect(try #require(StatusAdapter.aws(Data("[]".utf8))).state == .operational)
}
@Test func rssParsing() throws {
    let empty = #"<?xml version="1.0"?><rss version="2.0"><channel><title>Azure Status</title></channel></rss>"#
    #expect(try #require(StatusAdapter.rss(Data(empty.utf8))).state == .operational)
    let one = #"<?xml version="1.0"?><rss version="2.0"><channel><title>Azure Status</title><item><title>Issue in East US</title></item></channel></rss>"#
    let s = try #require(StatusAdapter.rss(Data(one.utf8)))
    #expect(s.state == .degraded); #expect(s.incidents.map(\.title) == ["Issue in East US"])
    #expect(StatusAdapter.rss(Data("not xml".utf8)) == nil)
}
@Test func slackStripeParsing() throws {
    #expect(try #require(StatusAdapter.slack(Data(#"{"status":"ok","active_incidents":[]}"#.utf8))).state == .operational)
    let s = try #require(StatusAdapter.slack(Data(#"{"status":"active","active_incidents":[{"id":1,"title":"Msgs","type":"outage"}]}"#.utf8)))
    #expect(s.state == .outage)
    #expect(try #require(StatusAdapter.stripe(Data(#"{"largestatus":"up","message":"All services are online."}"#.utf8))).state == .operational)
    #expect(try #require(StatusAdapter.stripe(Data(#"{"largestatus":"down","message":"x"}"#.utf8))).state == .outage)
}
@Test func realFeedsParse() async throws {
    guard ProcessInfo.processInfo.environment["IM_NET"] != nil else { return }
    for svc in ServiceCatalog.builtIn where svc.adapter != "none" {
        do { let s = try await ServiceStatusFetcher.fetch(svc); print("FEED", svc.id, s.state.rawValue, s.summary) }
        catch { Issue.record("\(svc.id): \(error.localizedDescription)") }
    }
}

@Test func mergeKeepsOtherTargets() {
    let gh = ServiceCatalog.builtIn.first { $0.id == "github" }!, td = ServiceCatalog.builtIn.first { $0.id == "td" }!
    let base = AgentConfig(targets: [Target(id: "t-gw", name: "Gateway", kind: .icmp, host: "@gateway", group: "baseline"),
                                     Target(id: "svc-old", name: "Old", kind: .http, url: "https://old", group: "service")])
    let a = ServiceTargets.merged(base, wanted: [gh, td])
    #expect(a.targets.map(\.id) == ["t-gw", "svc-github", "svc-td"])
    let t = a.targets.first { $0.id == "svc-github" }!
    #expect(t.kind == .http && t.url == "https://github.com" && t.group == "service" && t.intervalMs == 30000 && t.host == "github.com")
    #expect(ServiceTargets.merged(a, wanted: [gh, td]) == a)                         // idempotent
    #expect(ServiceTargets.merged(a, wanted: []).targets.map(\.id) == ["t-gw"])      // deselect removes only svc-*
}
@Test func headerProbe() async {
    guard ProcessInfo.processInfo.environment["IM_NET"] != nil else { return }
    let ok = await HeaderProbe.run(URL(string: "https://github.com")!); print("PROBE github", ok ?? -1)
    #expect(ok != nil && ok! > 0 && ok! < 8000)
    let forbidden = await HeaderProbe.run(URL(string: "https://www.anthropic.com/nonexistent-page-xyz")!); print("PROBE 404", forbidden ?? -1)
    #expect(forbidden != nil)         // any HTTP response counts as reachable
    let bad = await HeaderProbe.run(URL(string: "https://this-host-does-not-exist-12345.invalid")!, timeout: 4)
    #expect(bad == nil)
}
