import SwiftUI
import Charts
import IMCore
import UniformTypeIdentifiers

enum NodeTab: String, CaseIterable { case latency = "Latency", incidents = "Incidents", diagnostics = "Diagnostics", control = "Control" }

struct NodeView: View {
    @EnvironmentObject var model: AppModel
    @ObservedObject var node: NodeState
    @State var tab: NodeTab = .latency

    var body: some View {
        VStack(spacing: 0) {
            header
            Rectangle().fill(Theme.line).frame(height: 1)
            if node.unauthorized {
                Banner(text: "This Mac's pairing was revoked or the agent was reset. Re-pair from Control → Re-pair.", color: Theme.red)
            } else if node.reachable == false {
                Banner(text: "Agent unreachable at \(node.saved.host):\(node.saved.port) — showing last known data. \(node.lastError ?? "")", color: Theme.red)
            }
            switch tab {
            case .latency: LatencyTab(node: node)
            case .incidents: IncidentsList(items: node.incidents.map { (node.name, $0) }, showNode: false)
            case .diagnostics: DiagnosticsTab(node: node)
            case .control: ControlTab(node: node)
            }
        }
        .background(Theme.bg)
    }

    var header: some View {
        HStack(spacing: 14) {
            Menu {
                ForEach(["cpu": "Raspberry Pi", "laptopcomputer": "Mac", "wifi.router": "Router", "server.rack": "Server", "cloud": "Cloud"].sorted { $0.value < $1.value }, id: \.key) { k, v in
                    Button { model.setIcon(k, for: node) } label: { Label(v, systemImage: k) }
                }
            } label: {
                Image(systemName: model.icons[node.id] ?? "cpu").font(.system(size: 20)).foregroundStyle(Theme.amber).frame(width: 36, height: 36)
                    .overlay(RoundedRectangle(cornerRadius: 3).stroke(Theme.line))
            }.menuStyle(.borderlessButton).menuIndicator(.hidden).fixedSize()
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 8) { StatusDot(state: node.worst); Text(node.name).font(.system(size: 18, weight: .semibold)) }
                Text("\(node.saved.host) · agent v\(node.identity?.version ?? "?") · up \(Fmt.duration(TimeInterval(node.identity?.uptimeSec ?? node.system?.uptimeSec ?? 0)))"
                     + (node.system?.tempC.map { String(format: " · %.0f°C", $0) } ?? ""))
                    .font(Theme.mono(11)).foregroundStyle(Theme.dim)
            }
            Spacer()
            Picker("", selection: $tab) { ForEach(NodeTab.allCases, id: \.self) { Text($0.rawValue.uppercased()).tag($0) } }
                .pickerStyle(.segmented).frame(width: 380).labelsHidden()
        }.padding(.horizontal, 20).padding(.vertical, 12)
    }
}

struct Banner: View {
    let text: String; let color: Color
    var body: some View {
        Text(text).font(Theme.mono(11)).foregroundStyle(color).frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, 20).padding(.vertical, 7).background(color.opacity(0.1))
    }
}

// MARK: - Latency
struct NodeChartStack: View {
    @EnvironmentObject var model: AppModel
    @ObservedObject var node: NodeState
    var body: some View {
        VStack(spacing: 1) {
            ForEach(Array(node.targets.enumerated()), id: \.element.id) { i, t in
                TargetChartRow(target: t, live: node.live[t.id], series: node.series[t.id], summary: node.summaries[t.id],
                               domain: model.domain, showAxis: i == node.targets.count - 1)
            }
        }
        .background(Theme.line)
        .overlay(RoundedRectangle(cornerRadius: 3).stroke(Theme.line))
        .clipShape(RoundedRectangle(cornerRadius: 3))
    }
}

struct RangeBar: View {
    @EnvironmentObject var model: AppModel
    var body: some View {
        HStack(spacing: 10) {
            ForEach(TimeRange.allCases) { r in
                Button(r == .d60 ? "2 MONTHS" : r.rawValue.uppercased()) { model.range = r }
                    .buttonStyle(.plain).font(Theme.mono(11, .bold))
                    .padding(.horizontal, 10).padding(.vertical, 5)
                    .foregroundStyle(model.range == r && model.zoom == nil ? Color.black : Theme.dim)
                    .background(model.range == r && model.zoom == nil ? Theme.amber : Theme.panel2)
                    .clipShape(RoundedRectangle(cornerRadius: 2))
            }
            if let z = model.zoom {
                Text("ZOOM \(z.lowerBound.formatted(date: .abbreviated, time: .shortened)) → \(z.upperBound.formatted(date: .omitted, time: .shortened))")
                    .font(Theme.mono(10, .semibold)).foregroundStyle(Theme.amber)
                Button("RESET") { model.zoom = nil }.buttonStyle(.industrial)
            } else {
                Text("DRAG ON A GRAPH TO ZOOM · DOUBLE-CLICK TO RESET").font(Theme.mono(9)).tracking(0.5).foregroundStyle(Theme.faint)
            }
        }
    }
}

struct LatencyTab: View {
    @EnvironmentObject var model: AppModel
    @ObservedObject var node: NodeState

    var chartStack: some View { NodeChartStack(node: node) }

    var body: some View {
        VStack(spacing: 0) {
            toolbar
            Rectangle().fill(Theme.line).frame(height: 1)
            if node.targets.isEmpty {
                VStack(spacing: 10) {
                    Text("No targets configured on this node").foregroundStyle(Theme.dim)
                    Button("CHOOSE TARGETS") { model.wizardEditing = true; model.showWizard = true }.buttonStyle(.industrialPrimary)
                }.frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                ScrollView { chartStack.padding(20) }
            }
        }
    }

    var toolbar: some View {
        HStack(spacing: 10) {
            RangeBar()
            Spacer()
            Button { exportCSV() } label: { Label("CSV", systemImage: "tablecells") }.buttonStyle(.industrial)
            Button { exportPNG() } label: { Label("PNG", systemImage: "photo") }.buttonStyle(.industrial)
            Button { exportPDF() } label: { Label("REPORT", systemImage: "doc.richtext") }.buttonStyle(.industrial)
        }.padding(.horizontal, 20).padding(.vertical, 10)
    }

    func exportCSV() {
        let rows = node.targets.compactMap { t in node.series[t.id].map { (node: node.name, target: t.name, series: $0) } }
        Exporter.save(name: "\(node.name)-latency.csv", type: .commaSeparatedText, data: Data(CSV.series(rows).utf8))
    }
    func exportPNG() {
        let r = ImageRenderer(content: chartStack.frame(width: 1200).padding(20).background(Theme.bg).environmentObject(model).environment(\.colorScheme, .dark))
        r.scale = 2
        if let img = r.nsImage, let tiff = img.tiffRepresentation, let rep = NSBitmapImageRep(data: tiff), let png = rep.representation(using: .png, properties: [:]) {
            Exporter.save(name: "\(node.name)-latency.png", type: .png, data: png)
        }
    }
    func exportPDF() {
        let view = ReportView(node: node).environmentObject(model).environment(\.colorScheme, .dark)
        let r = ImageRenderer(content: view)
        var data = Data()
        r.render { size, draw in
            var box = CGRect(origin: .zero, size: size)
            let md = NSMutableData()
            guard let c = CGDataConsumer(data: md), let ctx = CGContext(consumer: c, mediaBox: &box, nil) else { return }
            ctx.beginPDFPage(nil); draw(ctx); ctx.endPDFPage(); ctx.closePDF()
            data = md as Data
        }
        Exporter.save(name: "\(node.name)-report.pdf", type: .pdf, data: data)
    }
}

enum Exporter {
    @MainActor static func save(name: String, type: UTType, data: Data) {
        let p = NSSavePanel(); p.nameFieldStringValue = name; p.allowedContentTypes = [type]
        if p.runModal() == .OK, let u = p.url { try? data.write(to: u) }
    }
}

/// Printable uptime/SLA report.
struct ReportView: View {
    @ObservedObject var node: NodeState
    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("INTERNET MONITOR · UPTIME REPORT").font(Theme.mono(11, .bold)).tracking(2).foregroundStyle(Theme.amber)
            Text(node.name).font(.system(size: 26, weight: .bold))
            Text("Generated \(Date().formatted()) · \(node.saved.host)").font(Theme.mono(11)).foregroundStyle(Theme.dim)
            SLATable(node: node)
            Text("INCIDENTS (30 DAYS)").font(Theme.mono(10, .bold)).foregroundStyle(Theme.dim).padding(.top, 8)
            ForEach(node.incidents.prefix(25)) { i in
                HStack {
                    Text(Date(timeIntervalSince1970: TimeInterval(i.startedAt)).formatted()).frame(width: 190, alignment: .leading)
                    Text(i.targetName ?? "").frame(width: 200, alignment: .leading)
                    Text(i.kind.uppercased()).frame(width: 90, alignment: .leading)
                    Text(Fmt.duration(i.duration)).frame(width: 80, alignment: .leading)
                }.font(Theme.mono(10))
            }
            if node.incidents.isEmpty { Text("No incidents recorded.").font(Theme.mono(10)).foregroundStyle(Theme.green) }
        }
        .padding(32).frame(width: 760, alignment: .leading).foregroundStyle(Theme.text).background(Theme.bg)
    }
}

struct SLATable: View {
    @ObservedObject var node: NodeState
    var body: some View {
        Panel("SLA / uptime per target") {
            VStack(spacing: 0) {
                HStack {
                    Text("TARGET").frame(maxWidth: .infinity, alignment: .leading)
                    ForEach(["1h", "24h", "7d", "30d", "60d"], id: \.self) { Text($0.uppercased()).frame(width: 120, alignment: .trailing) }
                }.font(Theme.mono(9, .semibold)).foregroundStyle(Theme.faint).padding(.bottom, 6)
                ForEach(node.targets) { t in
                    HStack {
                        Text(t.name).frame(maxWidth: .infinity, alignment: .leading).lineLimit(1)
                        ForEach(["1h", "24h", "7d", "30d", "60d"], id: \.self) { lab in
                            let w = node.summaries[t.id]?.first { $0.label == lab }
                            VStack(alignment: .trailing, spacing: 0) {
                                Text(Fmt.uptime(w?.uptimePct)).foregroundStyle((w?.uptimePct ?? 100) < 99 ? Theme.amber : Theme.green)
                                Text("p95 \(Fmt.ms(w?.p95Ms)) · avg \(Fmt.ms(w?.avgMs))").font(Theme.mono(8)).foregroundStyle(Theme.faint)
                            }.frame(width: 120, alignment: .trailing)
                        }
                    }.font(Theme.mono(11)).padding(.vertical, 5).overlay(alignment: .bottom) { Rectangle().fill(Theme.line.opacity(0.5)).frame(height: 1) }
                }
            }
        }
    }
}

// MARK: - Incidents
struct IncidentsList: View {
    let items: [(String, Incident)]
    var showNode: Bool
    var body: some View {
        ScrollView {
            if items.isEmpty {
                Text("No incidents in the last 30 days").foregroundStyle(Theme.green).font(Theme.mono(12)).padding(40).frame(maxWidth: .infinity)
            } else {
                VStack(spacing: 0) {
                    ForEach(items.sorted { $0.1.startedAt > $1.1.startedAt }, id: \.1.id) { node, i in
                        HStack(spacing: 12) {
                            Rectangle().fill(i.isOpen ? Theme.red : (i.kind == "down" ? Theme.red.opacity(0.5) : Theme.amber.opacity(0.7))).frame(width: 3)
                            VStack(alignment: .leading, spacing: 2) {
                                HStack(spacing: 8) {
                                    Text(i.kind.uppercased()).font(Theme.mono(10, .bold)).foregroundStyle(i.kind == "down" ? Theme.red : Theme.amber)
                                    Text((showNode ? "\(node) → " : "") + (i.targetName ?? i.targetId ?? "")).font(.system(size: 13, weight: .medium))
                                    if i.isOpen { Text("ONGOING").font(Theme.mono(9, .bold)).padding(.horizontal, 5).padding(.vertical, 1).background(Theme.red).foregroundStyle(.black).clipShape(RoundedRectangle(cornerRadius: 2)) }
                                }
                                Text(i.detail ?? "").font(Theme.mono(10)).foregroundStyle(Theme.dim)
                            }
                            Spacer()
                            VStack(alignment: .trailing, spacing: 2) {
                                Text(Date(timeIntervalSince1970: TimeInterval(i.startedAt)).formatted(date: .abbreviated, time: .standard)).font(Theme.mono(11))
                                Text("duration " + Fmt.duration(i.duration) + (i.peakMs.map { " · peak \(Fmt.ms($0)) ms" } ?? "")).font(Theme.mono(10)).foregroundStyle(Theme.dim)
                            }
                        }
                        .padding(12).background(Theme.panel).overlay(alignment: .bottom) { Rectangle().fill(Theme.line).frame(height: 1) }
                    }
                }.overlay(RoundedRectangle(cornerRadius: 3).stroke(Theme.line)).padding(20)
            }
        }
    }
}
