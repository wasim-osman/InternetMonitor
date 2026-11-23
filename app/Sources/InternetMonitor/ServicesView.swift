import SwiftUI
import IMCore

struct ServicesView: View {
    @ObservedObject var sv: ServicesModel
    @EnvironmentObject var model: AppModel
    @State private var showCustom = false

    var body: some View {
        HStack(spacing: 0) {
            CatalogColumn(sv: sv, showCustom: $showCustom).frame(width: 320)
            Rectangle().fill(Theme.line).frame(width: 1)
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    HStack {
                        Text("SERVICES").font(Theme.mono(11, .bold)).tracking(1.5)
                        Spacer()
                        Text("\(sv.selectedServices.count) WATCHED · \(sv.problemCount) WITH PROBLEMS").font(Theme.mono(10)).foregroundStyle(sv.problemCount > 0 ? Theme.amber : Theme.faint)
                    }
                    MonitorFromPanel(sv: sv)
                    if sv.selectedServices.isEmpty {
                        Panel { VStack(spacing: 8) {
                            Text("Pick services on the left").foregroundStyle(Theme.dim)
                            Text("Official status pages are checked every 1–5 minutes and you get a desktop notification when a service goes down or comes back.")
                                .font(.caption).foregroundStyle(Theme.faint).multilineTextAlignment(.center)
                        }.frame(maxWidth: .infinity, minHeight: 140) }
                    } else {
                        LazyVGrid(columns: [GridItem(.adaptive(minimum: 360), spacing: 16, alignment: .top)], spacing: 16) {
                            ForEach(sv.selectedServices) { s in ServiceCard(sv: sv, svc: s) }
                        }
                    }
                }.padding(20)
            }
        }
        .sheet(isPresented: $showCustom) { CustomServiceSheet(sv: sv, isPresented: $showCustom) }
    }
}

// MARK: Catalog
struct CatalogColumn: View {
    @ObservedObject var sv: ServicesModel
    @Binding var showCustom: Bool
    var categories: [String] {
        var seen: [String] = []; for s in sv.filtered where !seen.contains(s.category) { seen.append(s.category) }; return seen
    }
    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 8) {
                Image(systemName: "magnifyingglass").foregroundStyle(Theme.faint)
                TextField("Search services", text: $sv.search).textFieldStyle(.plain).font(Theme.mono(12))
                Button { showCustom = true } label: { Image(systemName: "plus") }.buttonStyle(.plain).foregroundStyle(Theme.amber).help("Add a custom service")
            }.padding(12).background(Theme.panel)
            Rectangle().fill(Theme.line).frame(height: 1)
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 0, pinnedViews: []) {
                    ForEach(categories, id: \.self) { cat in
                        Text(cat.uppercased()).font(Theme.mono(9, .bold)).tracking(1.5).foregroundStyle(Theme.amber)
                            .padding(.horizontal, 14).padding(.top, 14).padding(.bottom, 4)
                        ForEach(sv.filtered.filter { $0.category == cat }) { s in
                            Button { sv.toggle(s) } label: {
                                HStack(spacing: 10) {
                                    Image(systemName: sv.isSelected(s) ? "checkmark.square.fill" : "square").foregroundStyle(sv.isSelected(s) ? Theme.amber : Theme.faint)
                                    Text(s.name).font(.system(size: 12.5)).lineLimit(1)
                                    Spacer()
                                    if sv.isSelected(s), let st = sv.status[s.id] { StateChip(state: st.state, small: true) }
                                    if s.custom == true { Button { sv.removeCustom(s) } label: { Image(systemName: "trash") }.buttonStyle(.plain).foregroundStyle(Theme.faint) }
                                }
                                .padding(.horizontal, 14).padding(.vertical, 6)
                                .background(sv.isSelected(s) ? Theme.amber.opacity(0.07) : .clear)
                                .contentShape(Rectangle())
                            }.buttonStyle(.plain).foregroundStyle(Theme.text)
                        }
                    }
                }
            }
        }.background(Theme.panel)
    }
}

struct StateChip: View {
    let state: ServiceState; var small = false
    var color: Color { switch state { case .operational: Theme.green; case .degraded: Theme.amber; case .outage: Theme.red; case .maintenance: Theme.cyan; case .unknown: Theme.faint } }
    var body: some View {
        Text(state.label).font(Theme.mono(small ? 8 : 9, .bold)).tracking(0.8)
            .padding(.horizontal, small ? 4 : 6).padding(.vertical, small ? 1 : 2)
            .foregroundStyle(color).overlay(RoundedRectangle(cornerRadius: 2).stroke(color.opacity(0.7)))
    }
}

// MARK: Monitor-from panel
struct MonitorFromPanel: View {
    @ObservedObject var sv: ServicesModel
    @EnvironmentObject var model: AppModel
    var body: some View {
        Panel("Monitor selected services from", trailing: AnyView(HStack(spacing: 10) {
            if sv.applying { ProgressView().controlSize(.small) }
            Button("APPLY") { Task { await sv.apply() } }.buttonStyle(.industrialPrimary)
                .disabled(sv.applying || !sv.pendingChanges)
        })) {
            VStack(alignment: .leading, spacing: 10) {
                Text("Nodes keep history even when this app is closed. Each node measures reachability and latency to the service from its own network.")
                    .font(.caption).foregroundStyle(Theme.faint)
                if model.nodes.isEmpty { Text("No nodes yet — add some from the sidebar.").font(.callout).foregroundStyle(Theme.dim) }
                ForEach(model.nodes) { n in
                    HStack(spacing: 10) {
                        Toggle("", isOn: Binding(get: { sv.prefs.nodes.contains(n.id) }, set: { sv.setNode(n.id, on: $0) })).labelsHidden().toggleStyle(.switch).controlSize(.small)
                        StatusDot(state: n.worst, size: 7)
                        Text(n.name).font(.system(size: 12.5, weight: .medium))
                        Text(n.saved.host).font(Theme.mono(10)).foregroundStyle(Theme.faint)
                        Spacer()
                        if let r = sv.applyResults[n.id] { Text(r).font(Theme.mono(10)).foregroundStyle(r.hasPrefix("Failed") || r.hasPrefix("Unreachable") ? Theme.red : Theme.green) }
                    }
                }
                Divider().overlay(Theme.line)
                VStack(alignment: .leading, spacing: 4) {
                    HStack(spacing: 10) {
                        Toggle("", isOn: Binding(get: { sv.prefs.desktop }, set: { sv.setDesktop($0) })).labelsHidden().toggleStyle(.switch).controlSize(.small)
                        Image(systemName: "desktopcomputer").foregroundStyle(Theme.dim)
                        Text("This computer (desktop app)").font(.system(size: 12.5, weight: .medium))
                        Spacer()
                    }
                    if sv.prefs.desktop {
                        Label("Only collects data while this app is running", systemImage: "exclamationmark.triangle.fill")
                            .font(Theme.mono(9)).foregroundStyle(Theme.amber).padding(.leading, 44)
                    }
                }
                if sv.pendingChanges { Text("Changes to node selection are applied when you press APPLY.").font(Theme.mono(9)).foregroundStyle(Theme.amber) }
            }
        }
    }
}

// MARK: Card
struct ServiceCard: View {
    @ObservedObject var sv: ServicesModel
    @EnvironmentObject var model: AppModel
    let svc: ServiceDef
    var st: ServiceStatus? { sv.status[svc.id] }

    var body: some View {
        Panel {
            VStack(alignment: .leading, spacing: 10) {
                HStack(alignment: .firstTextBaseline) {
                    Text(svc.name).font(.system(size: 14, weight: .semibold)).lineLimit(1)
                    Spacer()
                    StateChip(state: st?.state ?? .unknown)
                    Link(destination: URL(string: svc.pageUrl)!) { Image(systemName: "arrow.up.right.square") }.foregroundStyle(Theme.dim).help("Open status page")
                }
                Text(sv.statusError[svc.id].map { "Status feed error: \($0)" } ?? st?.summary ?? "Checking…")
                    .font(Theme.mono(11)).foregroundStyle(sv.statusError[svc.id] != nil ? Theme.red : Theme.dim)
                ForEach((st?.incidents ?? []).prefix(3)) { i in
                    HStack(alignment: .top, spacing: 6) {
                        Rectangle().fill(i.impact == .outage ? Theme.red : Theme.amber).frame(width: 2)
                        Text(i.title).font(.system(size: 11)).lineLimit(2)
                    }
                }
                if let c = st?.components.prefix(5), !c.isEmpty {
                    Text(c.map { "\($0.name) (\($0.state.rawValue))" }.joined(separator: " · ")).font(Theme.mono(9)).foregroundStyle(Theme.faint).lineLimit(2)
                }
                reach
                if let t = st?.checkedAt { Text("CHECKED \(t.formatted(date: .omitted, time: .standard))").font(Theme.mono(8)).foregroundStyle(Theme.faint) }
            }
        }
    }

    @ViewBuilder var reach: some View {
        let nodes = model.nodes.filter { sv.prefs.nodes.contains($0.id) }
        if !nodes.isEmpty || sv.prefs.desktop {
            VStack(alignment: .leading, spacing: 5) {
                Text("REACHABILITY").font(Theme.mono(8, .bold)).tracking(1).foregroundStyle(Theme.faint)
                ForEach(nodes) { n in
                    HStack(spacing: 8) {
                        if let l = n.live[svc.targetId] {
                            StatusDot(state: l.state == .degraded ? .up : l.state, size: 6)
                            Text(n.name).font(.system(size: 11)).lineLimit(1)
                            Spacer()
                            Text(l.state == .down ? "UNREACHABLE" : Fmt.ms(l.lastRttMs) + " ms").font(Theme.mono(10, .semibold)).foregroundStyle(l.state == .down ? Theme.red : Theme.latency(l.lastRttMs))
                        } else {
                            StatusDot(state: .unknown, size: 6)
                            Text(n.name).font(.system(size: 11)).lineLimit(1)
                            Spacer()
                            Text(sv.nodeTargetPresent(n, svc) ? "waiting for first sample" : "not applied — press APPLY").font(Theme.mono(9)).foregroundStyle(Theme.faint)
                        }
                    }
                }
                if sv.prefs.desktop {
                    let l = sv.local[svc.id]
                    HStack(spacing: 8) {
                        StatusDot(state: l == nil ? .unknown : (l!.down ? .down : .up), size: 6)
                        Image(systemName: "desktopcomputer").font(.system(size: 9)).foregroundStyle(Theme.dim)
                        Text("This computer").font(.system(size: 11))
                        Spacer()
                        if let l, l.samples.count > 1 { Sparkline(values: l.samples.suffix(60).map { $0.1 }).frame(width: 70, height: 14) }
                        Text(l == nil ? "…" : (l!.down ? "UNREACHABLE" : Fmt.ms(l!.lastMs) + " ms")).font(Theme.mono(10, .semibold))
                            .foregroundStyle(l?.down == true ? Theme.red : Theme.latency(l?.lastMs))
                    }
                }
            }
        }
    }
}

struct Sparkline: View {
    let values: [Double?]
    var body: some View {
        GeometryReader { g in
            let nums = values.compactMap { $0 }
            let hi = max(nums.max() ?? 1, 1)
            Path { p in
                var started = false
                for (i, v) in values.enumerated() {
                    let x = g.size.width * CGFloat(i) / CGFloat(max(values.count - 1, 1))
                    guard let v else { started = false; continue }
                    let y = g.size.height * (1 - CGFloat(v / hi))
                    if started { p.addLine(to: CGPoint(x: x, y: y)) } else { p.move(to: CGPoint(x: x, y: y)); started = true }
                }
            }.stroke(Theme.cyan, lineWidth: 1)
        }
    }
}

struct CustomServiceSheet: View {
    @ObservedObject var sv: ServicesModel
    @Binding var isPresented: Bool
    @State var name = ""; @State var probe = ""; @State var status = ""
    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("ADD CUSTOM SERVICE").font(Theme.mono(11, .bold)).tracking(1.5).foregroundStyle(Theme.amber)
            TextField("Name", text: $name).textFieldStyle(.industrial)
            TextField("Address to probe, e.g. https://app.example.com", text: $probe).textFieldStyle(.industrial)
            TextField("Status page (optional) — Atlassian Statuspage sites, e.g. https://status.example.com", text: $status).textFieldStyle(.industrial)
            Text("Reachability is measured from the nodes/desktop you choose. If you give a Statuspage address, its official status is shown and alerted on too.").font(.caption).foregroundStyle(Theme.faint)
            HStack { Spacer()
                Button("CANCEL") { isPresented = false }.buttonStyle(.industrial)
                Button("ADD") { sv.addCustom(name: name, probe: probe, statusBase: status); isPresented = false }.buttonStyle(.industrialPrimary).disabled(probe.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }.padding(20).frame(width: 520).background(Theme.bg)
    }
}
