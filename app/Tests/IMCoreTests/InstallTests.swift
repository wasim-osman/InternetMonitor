import Foundation
import Testing
@testable import IMCore

@Test func realInstall() async {
    guard let host = ProcessInfo.processInfo.environment["IM_INSTALL"] else { return }
    let bin = URL(fileURLWithPath: ProcessInfo.processInfo.environment["IM_BIN"]!)
    let users = SSHInstaller.guessUsers(saved: nil); print("USERS", users)
    let u = await SSHInstaller.probeLogin(host: host, users: users); print("LOGIN", u ?? "nil")
    guard let u else { return }
    let r = await SSHInstaller.install(user: u, host: host, binaryFor: { _ in bin }, log: { print("LOG", $0) })
    print("INSTALL", r)
    print("CODE", await SSHInstaller.fetchPairCode(user: u, host: host))
}
