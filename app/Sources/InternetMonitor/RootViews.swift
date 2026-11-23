import SwiftUI
import IMCore

struct RootView: View {
    @EnvironmentObject var model: AppModel
    var body: some View {
        HStack(spacing: 0) {
            Sidebar(services: model.services).frame(width: 230)
            Rectangle().fill(Theme.line).frame(width: 1)
            Group {
                switch model.route {
                case .overview: OverviewView()
                case .node(let id): if let n = model.nodes.first(where: { $0.id == id }) { NodeView(node: n).id(id) } else { OverviewView() }
                case .incidents: AllIncidentsView()
                case .services: ServicesView(sv: model.services)
                case .settings: SettingsView()
                }
            }.frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .background(Theme.bg).foregroundStyle(Theme.text)
        .overlay(alignment: .bottom) {
            if let t = model.toast { Text(t).font(Theme.mono(11)).padding(10).background(Theme.panel2).overlay(RoundedRectangle(cornerRadius: 3).stroke(Theme.amber)).padding(20) }
        }
        .sheet(isPresented: $model.showWizard) {
            WizardView(w: WizardModel(model: model, editing: model.wizardEditing)) { model.showWizard = false; model.wizardEditing = false }
                .environmentObject(model)
        }
        .preferredColorScheme(.dark)
    }
}

struct Sidebar: View {
    @EnvironmentObject var model: AppModel
    @ObservedObject var services: ServicesModel
    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 8) {
                Image(systemName: "waveform.path.ecg").foregroundStyle(Theme.amber)
                Text("INTERNET MONITOR").font(Theme.mono(11, .bold)).tracking(1.5)
            }.padding(16)
            item("Overview", "square.grid.3x3.fill", .overview)
            item("Incidents", "exclamationmark.triangle", .incidents, badge: model.nodes.flatMap(\.incidents).filter(\.isOpen).count)
            item("Services", "cloud", .services, badge: services.problemCount)
            Text("NODES").font(Theme.mono(9, .semibold)).tracking(1.5).foregroundStyle(Theme.faint).padding(.horizontal, 16).padding(.top, 16).padding(.bottom, 6)
            ScrollView {
                VStack(spacing: 0) {
                    ForEach(model.nodes) { n in NodeRow(node: n, selected: model.route == .node(n.id)) { model.selectRoute(.node(n.id)) } }
                }
            }
            Spacer(minLength: 0)
            Button { model.wizardEditing = false; model.showWizard = true } label: { Label("ADD NODES", systemImage: "plus") }
                .buttonStyle(.industrial).padding(12)
            item("Settings", "gearshape", .settings)
        }
        .background(Theme.panel)
    }
    func item(_ t: String, _ icon: String, _ r: Route, badge: Int = 0) -> some View {
        Button { model.selectRoute(r) } label: {
            HStack {
                Image(systemName: icon).frame(width: 18)
                Text(t.uppercased()).font(Theme.mono(11, .semibold)).tracking(1)
                Spacer()
                if badge > 0 { Text("\(badge)").font(Theme.mono(10, .bold)).padding(.horizontal, 6).background(Theme.red).foregroundStyle(.black).clipShape(Capsule()) }
            }
            .padding(.horizontal, 16).padding(.vertical, 8)
            .foregroundStyle(model.route == r ? Theme.amber : Theme.dim)
            .background(model.route == r ? Theme.amber.opacity(0.08) : .clear)
            .overlay(alignment: .leading) { Rectangle().fill(model.route == r ? Theme.amber : .clear).frame(width: 2) }
            .contentShape(Rectangle())
        }.buttonStyle(.plain)
    }
}

struct NodeRow: View {
    @ObservedObject var node: NodeState
    let selected: Bool
    let action: () -> Void
    @EnvironmentObject var model: AppModel
    var body: some View {
        Button(action: action) {
            HStack(spacing: 10) {
                StatusDot(state: node.worst)
                VStack(alignment: .leading, spacing: 1) {
                    Text(node.name).font(.system(size: 12, weight: .medium)).lineLimit(1)
                    Text(node.reachable == false ? "UNREACHABLE" : "\(node.targets.count) targets").font(Theme.mono(9)).foregroundStyle(node.reachable == false ? Theme.red : Theme.faint)
                }
                Spacer()
                let avg = node.live.values.compactMap(\.lastRttMs)
                if !avg.isEmpty { Text(Fmt.ms(avg.reduce(0, +) / Double(avg.count))).font(Theme.mono(10)).foregroundStyle(Theme.dim) }
            }
            .padding(.horizontal, 16).padding(.vertical, 8)
            .foregroundStyle(selected ? Theme.amber : Theme.text)
            .background(selected ? Theme.amber.opacity(0.08) : .clear)
            .overlay(alignment: .leading) { Rectangle().fill(selected ? Theme.amber : .clear).frame(width: 2) }
            .contentShape(Rectangle())
        }.buttonStyle(.plain)
    }
}

// MARK: - Overview
struct OverviewView: View {
    @EnvironmentObject var model: AppModel
    var columns: [(key: String, label: String)] {
        var seen = Set<String>(); var out: [(String, String)] = []
        for n in model.nodes { for t in n.targets where seen.insert(t.endpointKey).inserted { out.append((t.endpointKey, t.name)) } }
        return out
    }
    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                kpis
                if model.nodes.isEmpty {
                    Panel { VStack(spacing: 10) { Text("No nodes yet").foregroundStyle(Theme.dim); Button("RUN SETUP WIZARD") { model.showWizard = true }.buttonStyle(.industrialPrimary) }.frame(maxWidth: .infinity, minHeight: 160) }
                } else {
                    Panel("Latency matrix — node × target (ms)") { matrix }
                    graphsHeader
                    ForEach(model.nodes) { n in OverviewNodeSection(node: n) }
                    LazyVGrid(columns: [GridItem(.adaptive(minimum: 300), spacing: 16)], spacing: 16) {
                        ForEach(model.nodes) { n in NodeCard(node: n) }
                    }
                }
            }.padding(20)
        }
    }

    var graphsHeader: some View {
        HStack(spacing: 12) {
            Text("GRAPHS").font(Theme.mono(10, .bold)).tracking(1.5).foregroundStyle(Theme.dim)
            RangeBar()
            Spacer()
            Button("EXPAND ALL") { model.nodes.forEach { if !model.expanded.contains($0.id) { model.toggleExpanded($0) } } }.buttonStyle(.industrial)
            Button("COLLAPSE ALL") { model.expanded = []; UserDefaults.standard.set([String](), forKey: "overviewExpanded") }.buttonStyle(.industrial)
        }
    }

    var kpis: some View {
        let live = model.nodes.flatMap { $0.live.values }
        let up = live.filter { $0.state == .up }.count
        let rtts = live.compactMap(\.avgMs)
        let open = model.nodes.flatMap(\.incidents).filter(\.isOpen).count
        return HStack(spacing: 16) {
            kpi("Nodes online", "\(model.nodes.filter { $0.reachable == true }.count)/\(model.nodes.count)", Theme.green)
            kpi("Links up", "\(up)/\(live.count)", up == live.count ? Theme.green : Theme.amber)
            kpi("Mean latency", rtts.isEmpty ? "—" : Fmt.ms(rtts.reduce(0, +) / Double(rtts.count)) + " ms", Theme.cyan)
            kpi("Open incidents", "\(open)", open == 0 ? Theme.green : Theme.red)
        }
    }
    func kpi(_ l: String, _ v: String, _ c: Color) -> some View {
        Panel { StatBlock(label: l, value: v, color: c).frame(maxWidth: .infinity, alignment: .leading) }
    }

    var matrix: some View {
        let cols = columns
        return ScrollView(.horizontal) {
            Grid(horizontalSpacing: 2, verticalSpacing: 2) {
                GridRow {
                    Color.clear.frame(width: 140, height: 1)
                    ForEach(cols, id: \.key) { c in Text(c.label).font(Theme.mono(9, .semibold)).foregroundStyle(Theme.dim).frame(width: 86).lineLimit(2).multilineTextAlignment(.center) }
                }
                ForEach(model.nodes) { n in
                    GridRow {
                        Button { model.selectRoute(.node(n.id)) } label: {
                            HStack(spacing: 6) { StatusDot(state: n.worst, size: 6); Text(n.name).font(.system(size: 12, weight: .medium)).lineLimit(1) }.frame(width: 140, alignment: .leading)
                        }.buttonStyle(.plain)
                        ForEach(cols, id: \.key) { c in
                            if let t = n.targets.first(where: { $0.endpointKey == c.key }) {
                                let l = n.live[t.id]
                                Text(l?.state == .down ? "DOWN" : Fmt.ms(l?.lastRttMs)).font(Theme.mono(12, .bold))
                                    .frame(width: 86, height: 38)
                                    .background((l?.state == .down ? Theme.red : Theme.latency(l?.lastRttMs)).opacity(l == nil ? 0.05 : 0.22))
                                    .foregroundStyle(l?.state == .down ? Theme.red : Theme.latency(l?.lastRttMs))
                                    .help("\(n.name) → \(t.name): avg \(Fmt.ms(l?.avgMs)) ms · loss \(Fmt.pct(l?.lossPct)) · jitter \(Fmt.ms(l?.jitterMs)) ms")
                            } else { Text("·").foregroundStyle(Theme.line).frame(width: 86, height: 38) }
                        }
                    }
                }
            }
        }
    }
}

struct OverviewNodeSection: View {
    @ObservedObject var node: NodeState
    @EnvironmentObject var model: AppModel
    var open: Bool { model.expanded.contains(node.id) }
    var body: some View {
        VStack(spacing: 0) {
            Button { withAnimation(.easeInOut(duration: 0.15)) { model.toggleExpanded(node) } } label: {
                HStack(spacing: 10) {
                    Image(systemName: "chevron.right").rotationEffect(.degrees(open ? 90 : 0)).foregroundStyle(Theme.amber).frame(width: 14)
                    StatusDot(state: node.worst)
                    Text(node.name).font(.system(size: 13, weight: .semibold))
                    Text(node.saved.host).font(Theme.mono(10)).foregroundStyle(Theme.faint)
                    Spacer()
                    let rtts = node.live.values.compactMap(\.lastRttMs)
                    Text("\(node.targets.count) TARGETS").font(Theme.mono(9, .semibold)).foregroundStyle(Theme.faint)
                    if !rtts.isEmpty { Text("AVG " + Fmt.ms(rtts.reduce(0, +) / Double(rtts.count)) + " ms").font(Theme.mono(10, .semibold)).foregroundStyle(Theme.dim) }
                    Button { model.selectRoute(.node(node.id)) } label: { Image(systemName: "arrow.up.right.square") }.buttonStyle(.plain).foregroundStyle(Theme.dim).help("Open node")
                }
                .padding(.horizontal, 12).padding(.vertical, 10).background(Theme.panel).contentShape(Rectangle())
            }.buttonStyle(.plain)
            if open {
                Rectangle().fill(Theme.line).frame(height: 1)
                if node.targets.isEmpty {
                    Text("No targets configured").font(.callout).foregroundStyle(Theme.faint).padding(20)
                } else {
                    ScrollView(.vertical) { NodeChartStack(node: node) }
                        .frame(height: min(CGFloat(node.targets.count) * 105 + 28, 460))
                }
            }
        }
        .overlay(RoundedRectangle(cornerRadius: 3).stroke(Theme.line))
        .clipShape(RoundedRectangle(cornerRadius: 3))
    }
}

struct NodeCard: View {
    @ObservedObject var node: NodeState
    @EnvironmentObject var model: AppModel
    var body: some View {
        Button { model.selectRoute(.node(node.id)) } label: {
            Panel {
                VStack(alignment: .leading, spacing: 10) {
                    HStack { StatusDot(state: node.worst); Text(node.name).font(.system(size: 14, weight: .semibold)); Spacer()
                        Text(node.saved.host).font(Theme.mono(10)).foregroundStyle(Theme.faint) }
                    ForEach(node.targets.prefix(6)) { t in
                        let l = node.live[t.id]
                        HStack {
                            StatusDot(state: l?.state ?? .unknown, size: 6)
                            Text(t.name).font(.system(size: 11)).lineLimit(1)
                            Spacer()
                            Text(l?.state == .down ? "DOWN" : Fmt.ms(l?.lastRttMs)).font(Theme.mono(11, .semibold)).foregroundStyle(l?.state == .down ? Theme.red : Theme.latency(l?.lastRttMs))
                        }
                    }
                    if node.targets.count > 6 { Text("+\(node.targets.count - 6) more").font(Theme.mono(9)).foregroundStyle(Theme.faint) }
                    if node.targets.isEmpty { Text(node.reachable == false ? "Unreachable" : "No targets").font(.caption).foregroundStyle(Theme.faint) }
                }
            }
        }.buttonStyle(.plain)
    }
}

struct AllIncidentsView: View {
    @EnvironmentObject var model: AppModel
    var body: some View {
        VStack(spacing: 0) {
            HStack { Text("INCIDENT TIMELINE").font(Theme.mono(11, .bold)).tracking(1.5); Spacer(); Text("LAST 30 DAYS · ALL NODES").font(Theme.mono(10)).foregroundStyle(Theme.faint) }.padding(20)
            Rectangle().fill(Theme.line).frame(height: 1)
            IncidentsList(items: model.nodes.flatMap { n in n.incidents.map { (n.name, $0) } }, showNode: true)
        }
    }
}

// MARK: - Settings
struct SettingsView: View {
    @EnvironmentObject var model: AppModel
    @AppStorage("notificationsEnabled") var notifications = true
    @AppStorage("menuBarEnabled") var menuBar = true
    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                Panel("Application") {
                    VStack(alignment: .leading, spacing: 12) {
                        Toggle("Notifications for outages, degradation and recovery", isOn: $notifications)
                        Toggle("Show status in the menu bar", isOn: $menuBar)
                        Toggle("Launch at login (keeps monitoring in the menu bar)", isOn: Binding(get: { model.launchAtLogin }, set: { model.launchAtLogin = $0 }))
                    }.toggleStyle(.switch).font(.callout)
                }
                UpdatesPanel(up: model.updater)
                Panel("About") {
                    VStack(alignment: .leading, spacing: 10) {
                        HStack(spacing: 14) {
                            Image(nsImage: NSApp.applicationIconImage).resizable().frame(width: 56, height: 56)
                            VStack(alignment: .leading, spacing: 3) {
                                Text("Internet Monitor").font(.system(size: 16, weight: .semibold))
                                Text("Version \(Updater.currentVersion) · MIT License").font(Theme.mono(11)).foregroundStyle(Theme.dim)
                                Text("Developed By \(AppInfo.author)").font(Theme.mono(11, .semibold)).foregroundStyle(Theme.amber)
                            }
                        }
                        Link(destination: URL(string: AppInfo.githubURL)!) {
                            Label(AppInfo.githubURL.replacingOccurrences(of: "https://", with: ""), systemImage: "link")
                                .font(Theme.mono(12)).foregroundStyle(Theme.cyan)
                        }
                    }
                }
                Panel("How it works") {
                    Text("Each node runs a tiny agent (imonitord) that does the probing and stores history locally. This app only displays and controls them, so graphs keep their history even when this Mac is closed or asleep. Agents are paired with a token stored in ~/Library/Application Support/InternetMonitor/nodes.json (owner-only permissions).")
                        .font(.callout).foregroundStyle(Theme.dim)
                }
            }.padding(20)
        }
    }
}

struct UpdatesPanel: View {
    @ObservedObject var up: Updater
    var body: some View {
        Panel("Updates") {
            VStack(alignment: .leading, spacing: 12) {
                Toggle("Automatically download and install updates from GitHub", isOn: Binding(get: { up.autoUpdates }, set: { up.autoUpdates = $0 }))
                    .toggleStyle(.switch).font(.callout)
                HStack(spacing: 12) {
                    Text(up.statusText).font(Theme.mono(11)).foregroundStyle(up.state.isFailure ? Theme.red : Theme.dim)
                    Spacer()
                    if case .ready = up.state { Button("RESTART & UPDATE") { up.applyAndRelaunch() }.buttonStyle(.industrialPrimary) }
                    else if case .available = up.state { Button("INSTALL UPDATE") { Task { await up.installNow() } }.buttonStyle(.industrialPrimary) }
                    Button("CHECK NOW") { Task { await up.check() } }.buttonStyle(.industrial)
                }
                if let n = up.notes, !n.isEmpty, case .available = up.state { Text(n).font(.caption).foregroundStyle(Theme.faint).lineLimit(6) }
                else if let n = up.notes, !n.isEmpty, case .ready = up.state { Text(n).font(.caption).foregroundStyle(Theme.faint).lineLimit(6) }
            }
        }
    }
}
extension Updater.State { var isFailure: Bool { if case .failed = self { return true }; return false } }

