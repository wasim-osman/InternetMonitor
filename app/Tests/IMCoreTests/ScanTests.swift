import Foundation
import Testing
@testable import IMCore

@Test func realScan() async {
    guard ProcessInfo.processInfo.environment["IM_SCAN"] != nil else { return }
    let r = await Discovery.scan(progress: { print("PROGRESS", $0) }, onUpdate: { _ in })
    for c in r { print("FOUND", c.ip, c.name, c.network, c.pingMs ?? -1, c.agent?.nodeId ?? "-") }
    let mine = Set(Discovery.localInterfaces().map(\.ip))
    #expect(r.allSatisfy { !mine.contains($0.ip) })
}
