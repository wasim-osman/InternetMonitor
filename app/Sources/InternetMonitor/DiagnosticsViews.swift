import SwiftUI
import Charts
import IMCore

struct DiagnosticsTab: View {
    @EnvironmentObject var model: AppModel
    @ObservedObject var node: NodeState
    @State var traceHost = "1.1.1.1"
    @State var trace: TraceResult?
    @State var tracing = false
    @State var traceError: String?
    @State var speeding = false
    @State var speedError: String?

    var body: some View {
        ScrollView {
            VStack(spacing: 16) {
                SLATable(node: node)
                HStack(alignment: .top, spacing: 16) {
                    systemPanel
                    interfacesPanel
                }
                tracePanel
                speedPanel
            }.padding(20)
        }
    }

    var systemPanel: some View {
        Panel("System") {
            let s = node.system
            VStack(alignment: .leading, spacing: 8) {
                kv("Model", s?.model ?? s?.hostname ?? "—"); kv("OS", "\(s?.os ?? "—") \(s?.kernel ?? "") \(s?.arch ?? "")")
                kv("Load", s?.loadAvg?.map { String(format: "%.2f", $0) }.joined(separator: " ") ?? "—")
                kv("Memory", s.flatMap { a in a.memTotalMB.map { "\(a.memUsedMB ?? 0) / \($0) MB" } } ?? "—")
                kv("Disk free", s?.diskFreeMB.map { "\($0) MB" } ?? "—")
                kv("Temperature", s?.tempC.map { String(format: "%.1f °C", $0) } ?? "—")
                kv("Gateway", "\(s?.gateway ?? "—") via \(s?.defaultInterface ?? "—")")
                if let w = s?.wifi {
                    kv("Wi-Fi", "\(w.ssid ?? w.iface ?? "wlan") · \(w.rssiDbm.map { "\($0) dBm" } ?? "—") · quality \(w.linkQuality.map { "\($0)%" } ?? "—")")
                    if node.rssi.count > 2 {
                        Chart { ForEach(Array(node.rssi.enumerated()), id: \.offset) { _, p in
                            LineMark(x: .value("t", p.0), y: .value("dBm", p.1)).foregroundStyle(Theme.cyan) } }
                            .chartYScale(domain: -100...(-30)).frame(height: 50)
                            .chartYAxis { AxisMarks(values: [-90, -60, -30]) { AxisValueLabel().font(Theme.mono(8)) } }
                    }
                }
            }
        }
    }

    var interfacesPanel: some View {
        Panel("Network paths") {
            VStack(alignment: .leading, spacing: 8) {
                ForEach(node.system?.interfaces ?? []) { i in
                    HStack {
                        StatusDot(state: i.up == true ? .up : .unknown, size: 6)
                        Text(i.name).font(Theme.mono(11, .bold)).frame(width: 70, alignment: .leading)
                        Text((i.kind ?? "").uppercased()).font(Theme.mono(9)).foregroundStyle(i.kind == "tailscale" ? Theme.cyan : Theme.dim).frame(width: 70, alignment: .leading)
                        Text((i.addrs ?? []).joined(separator: ", ")).font(Theme.mono(10)).foregroundStyle(Theme.dim).lineLimit(1)
                        if i.name == node.system?.defaultInterface { Text("DEFAULT").font(Theme.mono(8, .bold)).foregroundStyle(Theme.amber) }
                    }
                }
                if let ts = node.system?.tailscale {
                    Divider().overlay(Theme.line)
                    Text("TAILSCALE PEERS FROM THIS NODE").font(Theme.mono(9, .semibold)).foregroundStyle(Theme.faint)
                    ForEach(ts.peers ?? []) { p in
                        HStack {
                            StatusDot(state: p.online == true ? .up : .unknown, size: 6)
                            Text(p.name).font(Theme.mono(11)).frame(width: 150, alignment: .leading).lineLimit(1)
                            Text(p.ip ?? "").font(Theme.mono(10)).foregroundStyle(Theme.dim).frame(width: 110, alignment: .leading)
                            if p.online == true, let path = p.path ?? (p.relay != nil ? "relay" : nil) {
                                Text(path.uppercased() + (path == "relay" ? " · \(p.relay ?? "")" : "")).font(Theme.mono(9, .bold))
                                    .foregroundStyle(path == "relay" ? Theme.amber : Theme.green)
                            }
                        }
                    }
                }
                if node.system == nil { Text("Waiting for system info…").foregroundStyle(Theme.faint) }
            }
        }
    }

    var tracePanel: some View {
        Panel("Traceroute", trailing: AnyView(Text("FROM \(node.name.uppercased())").font(Theme.mono(9)).foregroundStyle(Theme.faint))) {
            VStack(alignment: .leading, spacing: 10) {
                HStack {
                    TextField("host", text: $traceHost).textFieldStyle(.industrial).frame(width: 260).onSubmit { run() }
                    Menu("PICK TARGET") { ForEach(node.targets) { t in if let h = t.host, h != "@gateway" { Button(t.name) { traceHost = h } } } }.frame(width: 120)
                    Button(tracing ? "TRACING…" : "TRACE") { run() }.buttonStyle(.industrialPrimary).disabled(tracing || traceHost.isEmpty)
                    if tracing { ProgressView().controlSize(.small) }
                    if let e = traceError { Text(e).font(.caption).foregroundStyle(Theme.red) }
                }
                if let t = trace {
                    let maxRTT = t.hops.compactMap(\.avg).max() ?? 1
                    ForEach(t.hops) { h in
                        HStack(spacing: 10) {
                            Text("\(h.ttl)").font(Theme.mono(10, .bold)).foregroundStyle(Theme.faint).frame(width: 22, alignment: .trailing)
                            Text(hopLabel(h)).font(Theme.mono(11)).frame(width: 300, alignment: .leading).lineLimit(1)
                            GeometryReader { g in
                                Rectangle().fill(Theme.latency(h.avg)).frame(width: h.avg.map { max(2, g.size.width * $0 / maxRTT) } ?? 0, height: 6)
                            }.frame(height: 6)
                            Text(h.avg.map { Fmt.ms($0) + " ms" } ?? "—").font(Theme.mono(11)).frame(width: 80, alignment: .trailing)
                            Text(h.rttMs.map { $0.map { Fmt.ms($0) } ?? "*" }.joined(separator: " / ")).font(Theme.mono(9)).foregroundStyle(Theme.faint).frame(width: 140, alignment: .trailing)
                        }
                    }
                }
            }
        }
    }
    func hopLabel(_ h: Hop) -> String {
        guard let ip = h.ip else { return "* * *" }
        if let n = h.name, !n.isEmpty { return "\(n) (\(ip))" }
        return ip
    }
    func run() {
        tracing = true; traceError = nil
        Task {
            do { trace = try await node.client.traceroute(host: traceHost) } catch { traceError = error.localizedDescription }
            tracing = false
        }
    }

    var speedPanel: some View {
        Panel("Bandwidth", trailing: AnyView(Button(speeding ? "RUNNING…" : "RUN SPEED TEST") { runSpeed() }.buttonStyle(.industrial).disabled(speeding))) {
            VStack(alignment: .leading, spacing: 10) {
                if let e = speedError { Text(e).font(.caption).foregroundStyle(Theme.red) }
                if node.speed.isEmpty { Text("No speed tests yet. Enable scheduled tests under Control, or run one now.").font(.callout).foregroundStyle(Theme.faint) }
                else {
                    HStack(spacing: 30) {
                        if let l = node.speed.first(where: { $0.error == nil }) {
                            StatBlock(label: "Latest down", value: String(format: "%.1f", l.downMbps ?? 0), unit: "Mbps", color: Theme.cyan)
                            StatBlock(label: "Latency", value: Fmt.ms(l.latencyMs), unit: "ms")
                        }
                    }
                    Chart(node.speed.filter { $0.error == nil }) { r in
                        BarMark(x: .value("t", Date(timeIntervalSince1970: TimeInterval(r.at))), y: .value("Mbps", r.downMbps ?? 0), width: 6).foregroundStyle(Theme.cyan)
                    }.frame(height: 90)
                }
            }
        }
    }
    func runSpeed() {
        speeding = true; speedError = nil
        Task {
            do { _ = try await node.client.speedtest(); node.speed = (try? await node.client.speedHistory()) ?? node.speed } catch { speedError = error.localizedDescription }
            speeding = false
        }
    }

    func kv(_ k: String, _ v: String) -> some View {
        HStack(alignment: .top) { Text(k.uppercased()).font(Theme.mono(9, .semibold)).foregroundStyle(Theme.faint).frame(width: 90, alignment: .leading); Text(v).font(Theme.mono(11)).textSelection(.enabled) }
    }
}

// MARK: - Control
struct ControlTab: View {
    @EnvironmentObject var model: AppModel
    @ObservedObject var node: NodeState
    @State var cfg: AgentConfig?
    @State var msg: String?
    @State var clients: [ClientInfo] = []
    @State var code: PairCode?
    @State var confirmReset = false
    @State var confirmRemove = false
    @State var confirmUninstall = false
    @State var uninstallMsg: String?

    var body: some View {
        ScrollView {
            VStack(spacing: 16) {
                if cfg != nil { editor }
                else { Text("Loading configuration…").foregroundStyle(Theme.dim) }
                clientsPanel
                dangerPanel
            }.padding(20)
        }
        .onAppear { cfg = node.config; Task { clients = (try? await node.client.clients()) ?? [] } }
        .onChange(of: node.config) { _, n in if cfg == nil { cfg = n } }
    }

    @ViewBuilder var editor: some View {
        let b = Binding(get: { cfg! }, set: { cfg = $0 })
        Panel("Probing", trailing: AnyView(HStack {
            if let m = msg { Text(m).font(Theme.mono(10)).foregroundStyle(m == "Saved" ? Theme.green : Theme.red) }
            Button("SAVE & APPLY") { save() }.buttonStyle(.industrialPrimary).disabled(cfg == node.config)
        })) {
            Grid(alignment: .leading, horizontalSpacing: 20, verticalSpacing: 10) {
                GridRow { label("Node name"); TextField("", text: b.nodeName).textFieldStyle(.industrial).frame(width: 240) }
                GridRow { label("Probe interval (ms)"); intField(b.intervalMs) }
                GridRow { label("Timeout (ms)"); intField(b.timeoutMs) }
                GridRow { label("Keep raw samples (h)"); intField(b.retentionRawHours) }
                GridRow { label("Keep rollups (days)"); intField(b.retentionRollupDays) }
                GridRow { label("Public /metrics"); Toggle("", isOn: b.metricsPublic).labelsHidden() }
            }
        }
        Panel("Alert rules") {
            Grid(alignment: .leading, horizontalSpacing: 20, verticalSpacing: 10) {
                GridRow { label("Down after N failures"); intField(b.rules.downAfterFailures) }
                GridRow { label("Degraded above (ms)"); intField(b.rules.degradedLatencyMs) }
                GridRow { label("…for at least (s)"); intField(b.rules.degradedForSec) }
                GridRow { label("Webhook URL"); TextField("https://hooks.slack.com/…  (optional)", text: b.rules.webhookUrl).textFieldStyle(.industrial).frame(width: 420) }
            }
        }
        Panel("Scheduled speed test") {
            Grid(alignment: .leading, horizontalSpacing: 20, verticalSpacing: 10) {
                GridRow { label("Enabled"); Toggle("", isOn: b.speedtest.enabled).labelsHidden() }
                GridRow { label("Every (min)"); intField(b.speedtest.intervalMin) }
                GridRow { label("Test URL"); TextField("", text: b.speedtest.url).textFieldStyle(.industrial).frame(width: 420) }
            }
        }
        Panel("Targets", trailing: AnyView(Button("EDIT TARGETS…") { model.wizardEditing = true; model.showWizard = true }.buttonStyle(.industrial))) {
            VStack(spacing: 0) {
                ForEach(b.targets) { $t in
                    HStack {
                        Toggle("", isOn: $t.enabled).labelsHidden().toggleStyle(.switch).controlSize(.mini)
                        Text(t.kind.label).font(Theme.mono(9, .bold)).foregroundStyle(Theme.dim).frame(width: 40, alignment: .leading)
                        Text(t.name).frame(width: 200, alignment: .leading).lineLimit(1)
                        Text(t.endpointLabel).font(Theme.mono(10)).foregroundStyle(Theme.faint).lineLimit(1)
                        Spacer()
                        Text("every").font(Theme.mono(9)).foregroundStyle(Theme.faint)
                        TextField("default", value: $t.intervalMs, format: .number).textFieldStyle(.industrial).frame(width: 80)
                        Text("ms").font(Theme.mono(9)).foregroundStyle(Theme.faint)
                    }.padding(.vertical, 5)
                }
            }
        }
    }

    var clientsPanel: some View {
        Panel("Paired controllers", trailing: AnyView(Button("NEW PAIRING CODE") {
            Task { code = try? await node.client.newPairCode() }
        }.buttonStyle(.industrial))) {
            VStack(alignment: .leading, spacing: 6) {
                if let c = code { Text("Code \(c.code) — enter on another Mac within 10 minutes").font(Theme.mono(12, .bold)).foregroundStyle(Theme.amber) }
                ForEach(clients) { c in
                    HStack {
                        Image(systemName: "laptopcomputer").foregroundStyle(Theme.dim)
                        Text(c.name); Spacer()
                        Text(c.lastSeen.map { "seen " + Date(timeIntervalSince1970: TimeInterval($0)).formatted(.relative(presentation: .named)) } ?? "").font(Theme.mono(10)).foregroundStyle(Theme.faint)
                        Button { Task { try? await node.client.deleteClient(c.id); clients = (try? await node.client.clients()) ?? [] } } label: { Image(systemName: "xmark.circle") }.buttonStyle(.plain).foregroundStyle(Theme.red)
                    }.font(.callout)
                }
            }
        }
    }

    var dangerPanel: some View {
        Panel("Service") {
            HStack {
                if model.agentOutdated(node) {
                    Button("UPDATE AGENT TO v\(AppInfo.bundledAgentVersion)") { Task { model.flash(await model.upgradeAgent(node)) } }.buttonStyle(.industrialPrimary)
                }
                Button("RESTART AGENT") { Task { try? await node.client.restart(); model.flash("Restarting \(node.name)…") } }.buttonStyle(.industrial)
                Button("WIPE HISTORY") { confirmReset = true }.buttonStyle(.industrialDanger)
                Button("STOP MONITORING") { confirmRemove = true }.buttonStyle(.industrialDanger)
                Button(model.removing.contains(node.id) ? "REMOVING…" : "UNINSTALL AGENT & REMOVE") { confirmUninstall = true }
                    .buttonStyle(.industrialDanger).disabled(model.removing.contains(node.id))
                Spacer()
                Text(uninstallMsg ?? "Stop monitoring keeps the agent running on the node; Uninstall deletes it and all its data.").font(.caption)
                    .foregroundStyle(uninstallMsg == nil ? Theme.faint : Theme.red).lineLimit(3)
            }
        }
        .confirmationDialog("Wipe all stored history on \(node.name)?", isPresented: $confirmReset) {
            Button("Wipe history", role: .destructive) { Task { try? await node.client.resetHistory(); node.series = [:]; node.incidents = [] } }
        }
        .confirmationDialog("Uninstall the agent from \(node.name)?", isPresented: $confirmUninstall, titleVisibility: .visible) {
            Button("Uninstall & delete all data on the node", role: .destructive) { Task { let n = node.name; uninstallMsg = await model.uninstallAndRemove(node); model.flash(uninstallMsg ?? "\(n): agent uninstalled, no files left") } }
        } message: { Text("Stops the service and deletes the binary, config, tokens and all stored history on \(node.saved.host) over SSH. Cannot be undone.") }
        .confirmationDialog("Stop monitoring \(node.name) from this Mac?", isPresented: $confirmRemove) {
            Button("Remove from this Mac", role: .destructive) { model.remove(node) }
        }
    }

    func save() {
        guard let c = cfg else { return }
        Task {
            if let e = await model.updateConfig(node, { $0 = c }) { msg = e } else { msg = "Saved"; cfg = node.config }
            try? await Task.sleep(for: .seconds(3)); msg = nil
        }
    }
    func label(_ s: String) -> some View { Text(s.uppercased()).font(Theme.mono(9, .semibold)).foregroundStyle(Theme.faint).frame(width: 170, alignment: .leading) }
    func intField(_ b: Binding<Int>) -> some View { TextField("", value: b, format: .number.grouping(.never)).textFieldStyle(.industrial).frame(width: 100) }
}
