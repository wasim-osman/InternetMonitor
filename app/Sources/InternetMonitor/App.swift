import SwiftUI
import AppKit
import IMCore

final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationShouldTerminateAfterLastWindowClosed(_ app: NSApplication) -> Bool { false }
}

@main
struct InternetMonitorApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) var delegate
    @StateObject var model = AppModel()
    @AppStorage("menuBarEnabled") var menuBar = true

    var body: some Scene {
        Window("Internet Monitor", id: "main") {
            RootView().environmentObject(model)
                .frame(minWidth: 1100, minHeight: 700)
                .onAppear { model.start(); NSApp.appearance = NSAppearance(named: .darkAqua) }
        }
        .defaultSize(width: 1360, height: 860)
        .windowStyle(.hiddenTitleBar)

        MenuBarExtra(isInserted: $menuBar) {
            MenuBarView().environmentObject(model)
        } label: {
            Image(nsImage: MenuIcon.image(for: model.worst))
        }
        .menuBarExtraStyle(.window)
    }
}

enum MenuIcon {
    static func image(for s: LinkState) -> NSImage {
        let color: NSColor = switch s { case .up: .systemGreen; case .degraded: .systemOrange; case .down: .systemRed; case .unknown: .systemGray }
        let img = NSImage(size: NSSize(width: 18, height: 14), flipped: false) { r in
            color.setFill(); NSBezierPath(ovalIn: NSRect(x: 9, y: 3, width: 8, height: 8)).fill()
            NSColor.labelColor.setStroke()
            let p = NSBezierPath(); p.lineWidth = 1.4
            p.move(to: NSPoint(x: 0, y: 7)); p.line(to: NSPoint(x: 3, y: 7)); p.line(to: NSPoint(x: 4.5, y: 12)); p.line(to: NSPoint(x: 6.5, y: 1)); p.line(to: NSPoint(x: 8, y: 7))
            p.stroke(); return true
        }
        img.isTemplate = false
        return img
    }
}

struct MenuBarView: View {
    @EnvironmentObject var model: AppModel
    @Environment(\.openWindow) var openWindow
    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("INTERNET MONITOR").font(Theme.mono(10, .bold)).tracking(1.5).foregroundStyle(Theme.amber)
            if model.nodes.isEmpty { Text("No nodes configured").font(.caption).foregroundStyle(Theme.dim) }
            ForEach(model.nodes) { n in
                VStack(alignment: .leading, spacing: 3) {
                    HStack { StatusDot(state: n.worst, size: 7); Text(n.name).font(.system(size: 12, weight: .semibold)); Spacer()
                        if n.reachable == false { Text("UNREACHABLE").font(Theme.mono(9, .bold)).foregroundStyle(Theme.red) } }
                    ForEach(n.targets.prefix(5)) { t in
                        HStack { Text(t.name).font(.system(size: 11)).foregroundStyle(Theme.dim).lineLimit(1); Spacer()
                            let l = n.live[t.id]
                            Text(l?.state == .down ? "DOWN" : Fmt.ms(l?.lastRttMs) + " ms").font(Theme.mono(10)).foregroundStyle(l?.state == .down ? Theme.red : Theme.latency(l?.lastRttMs)) }
                    }
                }
            }
            Divider()
            HStack {
                Button("Open Dashboard") { openWindow(id: "main"); NSApp.activate(ignoringOtherApps: true) }
                Spacer()
                Button("Quit") { NSApplication.shared.terminate(nil) }
            }
        }.padding(12).frame(width: 300).background(Theme.bg).foregroundStyle(Theme.text).preferredColorScheme(.dark)
    }
}
