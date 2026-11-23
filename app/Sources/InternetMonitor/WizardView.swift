import SwiftUI
import IMCore

struct WizardView: View {
    @EnvironmentObject var model: AppModel
    @StateObject var w: WizardModel
    var onClose: () -> Void

    var body: some View {
        VStack(spacing: 0) {
            header
            Rectangle().fill(Theme.line).frame(height: 1)
            Group {
                switch w.step {
                case .scan: ScanStep(w: w)
                case .connect: ConnectStep(w: w)
                case .targets: TargetsStep(w: w)
                case .review: ReviewStep(w: w, onClose: onClose)
                }
            }
            Rectangle().fill(Theme.line).frame(height: 1)
            footer
        }
        .background(Theme.bg)
        .frame(minWidth: 900, minHeight: 640)
        .onAppear { if !w.editing && w.candidates.isEmpty { w.scan() } }
    }

    var header: some View {
        HStack(spacing: 0) {
            VStack(alignment: .leading, spacing: 2) {
                Text(w.editing ? "EDIT MONITORING" : "SETUP").font(Theme.mono(10, .semibold)).tracking(2).foregroundStyle(Theme.amber)
                Text(["Choose machines to monitor from", "Connect to the agents", "Choose what each node pings", "Review & apply"][w.step.rawValue])
                    .font(.system(size: 20, weight: .semibold)).foregroundStyle(Theme.text)
            }
            Spacer()
            HStack(spacing: 6) {
                ForEach(WizardStep.allCases, id: \.rawValue) { s in
                    HStack(spacing: 6) {
                        Text(String(format: "%02d", s.rawValue + 1)).font(Theme.mono(11, .bold))
                        Text(s.title.uppercased()).font(Theme.mono(10, .semibold)).tracking(1)
                    }
                    .padding(.horizontal, 10).padding(.vertical, 5)
                    .foregroundStyle(s == w.step ? Color.black : (s.rawValue < w.step.rawValue ? Theme.amber : Theme.faint))
                    .background(s == w.step ? Theme.amber : Color.clear)
                    .overlay(RoundedRectangle(cornerRadius: 2).stroke(s == w.step ? Theme.amber : Theme.line))
                }
            }
        }.padding(20)
    }

    var footer: some View {
        HStack {
            if !model.nodes.isEmpty || w.editing { Button("CANCEL") { onClose() }.buttonStyle(.industrial) }
            Spacer()
            if w.step != .scan {
                Button("BACK") { w.step = WizardStep(rawValue: w.step.rawValue - 1)! }.buttonStyle(.industrial)
            }
            switch w.step {
            case .scan:
                Button("CONNECT TO \(w.selected.count) NODE\(w.selected.count == 1 ? "" : "S") →") { w.beginConnect() }
                    .buttonStyle(.industrialPrimary).disabled(w.selected.isEmpty)
            case .connect:
                Button("CHOOSE TARGETS →") { w.buildRows(); w.step = .targets }
                    .buttonStyle(.industrialPrimary).disabled(w.readyIPs.isEmpty)
                    .help(w.allReady ? "" : "Continues with the nodes that are connected; the rest are skipped")
            case .targets:
                Button("REVIEW →") { w.step = .review }.buttonStyle(.industrialPrimary)
            case .review:
                EmptyView()
            }
        }.padding(14)
    }
}

// MARK: - Step 1
struct ScanStep: View {
    @ObservedObject var w: WizardModel
    @State private var confirmRemove = false
    @State private var pendingRemove: [Candidate] = []
    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                if w.scanning { ProgressView().controlSize(.small) }
                Text(w.progress.uppercased()).font(Theme.mono(11)).foregroundStyle(Theme.dim)
                Spacer()
                Text("THIS MAC IS EXCLUDED").font(Theme.mono(10, .semibold)).tracking(1).foregroundStyle(Theme.faint)
                Button("REMOVE AGENT FROM SELECTED (\(w.removableSelected.count))") { pendingRemove = w.removableSelected; confirmRemove = true }
                    .buttonStyle(.industrialDanger).disabled(w.removableSelected.isEmpty || !w.removingIPs.isEmpty)
                Button(w.scanning ? "SCANNING…" : "RESCAN") { w.scan() }.buttonStyle(.industrial).disabled(w.scanning)
            }
            Panel {
                if w.candidates.isEmpty {
                    VStack(spacing: 8) {
                        Text(w.scanning ? "Looking for machines on your networks…" : "No other machines found.").foregroundStyle(Theme.dim)
                        Text("Scans every active interface (Wi-Fi, Ethernet, Tailscale…) for hosts that answer ping or run the agent.").font(.caption).foregroundStyle(Theme.faint)
                    }.frame(maxWidth: .infinity, minHeight: 220)
                } else {
                    ScrollView {
                        LazyVStack(spacing: 0) {
                            HStack {
                                Text("").frame(width: 28)
                                Text("HOST").frame(width: 190, alignment: .leading)
                                Text("ADDRESS").frame(width: 130, alignment: .leading)
                                Text("NETWORK").frame(width: 150, alignment: .leading)
                                Text("PING").frame(width: 70, alignment: .trailing)
                                Text("AGENT").frame(maxWidth: .infinity, alignment: .leading).padding(.leading, 16)
                            }.font(Theme.mono(9, .semibold)).tracking(1).foregroundStyle(Theme.faint).padding(.bottom, 6)
                            ForEach(w.candidates) { c in
                                CandidateRow(c: c, on: w.selected.contains(c.ip), removable: w.canRemove(c), removing: w.removingIPs.contains(c.ip), status: w.removeResults[c.ip],
                                             toggle: { if w.selected.contains(c.ip) { w.selected.remove(c.ip) } else { w.selected.insert(c.ip) } },
                                             remove: { pendingRemove = [c]; confirmRemove = true })
                            }
                        }
                    }
                }
            }
            HStack {
                Text("ADD BY ADDRESS").font(Theme.mono(10, .semibold)).tracking(1).foregroundStyle(Theme.dim)
                TextField("IP or hostname", text: $w.manual).textFieldStyle(.industrial).frame(width: 240).onSubmit { w.addManual() }
                Button("ADD") { w.addManual() }.buttonStyle(.industrial)
                if let e = w.manualError { Text(e).font(.caption).foregroundStyle(Theme.red) }
                Spacer()
            }
        }.padding(20)
        .confirmationDialog("Remove the agent from \(pendingRemove.map(\.name).joined(separator: ", "))?", isPresented: $confirmRemove, titleVisibility: .visible) {
            Button("Uninstall agent & delete all its data", role: .destructive) { let l = pendingRemove; Task { await w.removeAgents(l) } }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("Over SSH this stops the service and deletes the binary, systemd unit, config, tokens and all stored history on those machines. Stored history cannot be recovered.")
        }
    }
}

struct CandidateRow: View {
    let c: Candidate, on: Bool, removable: Bool, removing: Bool, status: String?, toggle: () -> Void, remove: () -> Void
    var body: some View {
        Button(action: toggle) {
            HStack {
                Image(systemName: on ? "checkmark.square.fill" : "square").foregroundStyle(on ? Theme.amber : Theme.faint).frame(width: 28)
                HStack(spacing: 6) {
                    Image(systemName: c.agent != nil ? "cpu" : "desktopcomputer").foregroundStyle(c.agent != nil ? Theme.cyan : Theme.dim)
                    Text(c.name).lineLimit(1)
                }.frame(width: 190, alignment: .leading)
                Text(c.ip).frame(width: 130, alignment: .leading).foregroundStyle(Theme.dim)
                Text(c.network.uppercased()).font(Theme.mono(10)).foregroundStyle(c.isTailscale ? Theme.cyan : Theme.dim).frame(width: 150, alignment: .leading)
                Text(c.pingMs.map { Fmt.ms($0) + " ms" } ?? "—").foregroundStyle(Theme.latency(c.pingMs)).frame(width: 70, alignment: .trailing)
                HStack {
                    if let a = c.agent {
                        Text("AGENT v\(a.version ?? "?")").font(Theme.mono(10, .semibold)).foregroundStyle(Theme.cyan)
                        Text(a.paired == true ? "PAIRED" : "UNPAIRED").font(Theme.mono(9)).foregroundStyle(Theme.dim)
                    } else { Text("—").foregroundStyle(Theme.faint) }
                    if let st = status { Text(st).font(Theme.mono(10)).foregroundStyle(st.hasPrefix("FAILED") ? Theme.red : (st == "Removing…" ? Theme.amber : Theme.green)).lineLimit(2) }
                    Spacer()
                    if removing { ProgressView().controlSize(.small) }
                    else if removable { Button("REMOVE") { remove() }.buttonStyle(.plain).font(Theme.mono(10, .bold)).foregroundStyle(Theme.red) }
                }.padding(.leading, 16)
            }
            .font(Theme.mono(12)).padding(.vertical, 7).padding(.horizontal, 4)
            .background(on ? Theme.amber.opacity(0.07) : .clear)
            .overlay(alignment: .bottom) { Rectangle().fill(Theme.line.opacity(0.6)).frame(height: 1) }
            .contentShape(Rectangle())
        }.buttonStyle(.plain).foregroundStyle(Theme.text)
    }
}

// MARK: - Step 2
struct ConnectStep: View {
    @ObservedObject var w: WizardModel
    var body: some View {
        ScrollView {
            VStack(spacing: 12) {
                ForEach(w.candidates.filter { w.selected.contains($0.ip) }) { c in ConnectCard(w: w, c: c) }
                Text("Pairing codes come from the agent itself (`imonitord pair-code`). Over SSH this happens automatically using your existing keys; no passwords are asked for or stored.")
                    .font(.caption).foregroundStyle(Theme.faint).frame(maxWidth: .infinity, alignment: .leading)
            }.padding(20)
        }
    }
}

struct ConnectCard: View {
    @ObservedObject var w: WizardModel
    let c: Candidate
    var conn: Conn { w.conns[c.ip] ?? Conn() }
    var body: some View {
        Panel {
            VStack(alignment: .leading, spacing: 10) {
                HStack {
                    badge
                    VStack(alignment: .leading, spacing: 1) {
                        Text(c.name).font(.system(size: 14, weight: .semibold))
                        Text(c.ip + (conn.identity.map { " · \($0.os ?? "")/\($0.arch ?? "") · agent v\($0.version ?? "?")" } ?? "")).font(Theme.mono(11)).foregroundStyle(Theme.dim)
                    }
                    Spacer()
                    if conn.recognised && conn.phase != .ready { Text("EXISTING AGENT RECOGNISED").font(Theme.mono(9, .bold)).tracking(1).foregroundStyle(Theme.cyan) }
                }
                if !conn.message.isEmpty { Text(conn.message).font(.callout).foregroundStyle(conn.phase == .failed ? Theme.red : Theme.dim) }
                switch conn.phase {
                case .checking, .working:
                    HStack { ProgressView().controlSize(.small); Text(conn.log.last ?? "Working…").font(Theme.mono(11)).foregroundStyle(Theme.dim) }
                case .ready:
                    Label("Connected — controlled from this Mac", systemImage: "checkmark.seal.fill").foregroundStyle(Theme.green).font(.callout)
                case .needsCode:
                    HStack {
                        TextField("6-digit code", text: Binding(get: { w.conns[c.ip]?.code ?? "" }, set: { w.conns[c.ip]?.code = $0 }))
                            .textFieldStyle(.industrial).frame(width: 140).onSubmit { Task { await w.pair(c.ip, code: conn.code) } }
                        Button("PAIR") { Task { await w.pair(c.ip, code: conn.code) } }.buttonStyle(.industrialPrimary).disabled(conn.code.count < 6)
                        Divider().frame(height: 18)
                        sshField
                        Button("CONNECT VIA SSH") { Task { await w.connectViaSSH(c.ip) } }.buttonStyle(.industrial)
                    }
                case .noAgent, .failed:
                    HStack {
                        sshField
                        Button("INSTALL AGENT VIA SSH") { Task { await w.installAgent(c.ip) } }.buttonStyle(.industrialPrimary)
                        Button("RECHECK") { Task { await w.check(c.ip) } }.buttonStyle(.industrial)
                    }
                }
                if !conn.log.isEmpty && conn.phase != .ready {
                    Text(conn.log.joined(separator: "\n")).font(Theme.mono(10)).foregroundStyle(Theme.faint).textSelection(.enabled)
                }
            }
        }
    }
    var sshField: some View {
        HStack(spacing: 4) {
            Text("SSH USER").font(Theme.mono(9, .semibold)).foregroundStyle(Theme.faint)
            TextField(NSUserName(), text: Binding(get: { w.conns[c.ip]?.sshUser ?? "" }, set: { w.conns[c.ip]?.sshUser = $0 }))
                .textFieldStyle(.industrial).frame(width: 110)
        }
    }
    var badge: some View {
        let (icon, col): (String, Color) = switch conn.phase {
        case .ready: ("checkmark.circle.fill", Theme.green)
        case .needsCode: ("key.fill", Theme.amber)
        case .noAgent, .failed: ("exclamationmark.triangle.fill", Theme.red)
        default: ("ellipsis.circle", Theme.dim)
        }
        return Image(systemName: icon).font(.system(size: 20)).foregroundStyle(col).frame(width: 30)
    }
}

// MARK: - Step 3
struct TargetsStep: View {
    @ObservedObject var w: WizardModel
    @State var kind: ProbeKind = .icmp
    @State var name = ""; @State var host = ""; @State var port = ""
    var nodes: [Candidate] { w.candidates.filter { w.readyIPs.contains($0.ip) } }

    var body: some View {
        VStack(spacing: 12) {
            Panel {
                ScrollView([.vertical]) {
                    VStack(spacing: 0) {
                        HStack(spacing: 0) {
                            Text("TARGET").frame(maxWidth: .infinity, alignment: .leading)
                            ForEach(nodes) { n in
                                VStack(spacing: 3) {
                                    Text(conn(n).identity?.displayName ?? n.name).lineLimit(1).font(Theme.mono(10, .bold)).foregroundStyle(Theme.text)
                                    Text("\(w.count(for: n.ip)) targets").font(Theme.mono(9)).foregroundStyle(Theme.faint)
                                    HStack(spacing: 6) {
                                        Button("all") { w.setColumn(n.ip, on: true) }; Button("none") { w.setColumn(n.ip, on: false) }
                                    }.buttonStyle(.plain).font(Theme.mono(9)).foregroundStyle(Theme.amber)
                                }.frame(width: 120)
                            }
                            Spacer().frame(width: 60)
                        }.font(Theme.mono(9, .semibold)).foregroundStyle(Theme.faint).padding(.bottom, 8)
                        ForEach(["Nodes", "Baseline", "Existing", "Custom"], id: \.self) { sec in
                            let rs = w.rows.filter { $0.section == sec }
                            if !rs.isEmpty {
                                Text(sec.uppercased()).font(Theme.mono(9, .bold)).tracking(1.5).foregroundStyle(Theme.amber)
                                    .frame(maxWidth: .infinity, alignment: .leading).padding(.top, 10).padding(.bottom, 4)
                                ForEach(rs) { r in rowView(r) }
                            }
                        }
                    }
                }
            }
            Panel("Add custom address") {
                HStack {
                    Picker("", selection: $kind) { ForEach(ProbeKind.allCases, id: \.self) { Text($0.label).tag($0) } }.pickerStyle(.segmented).frame(width: 220).labelsHidden()
                    TextField("Label (optional)", text: $name).textFieldStyle(.industrial).frame(width: 150)
                    TextField(kind == .http ? "https://example.com" : (kind == .dns ? "domain to resolve" : "host or IP"), text: $host).textFieldStyle(.industrial)
                    if kind == .tcp { TextField("port", text: $port).textFieldStyle(.industrial).frame(width: 70) }
                    Button("ADD") { w.addCustom(kind: kind, name: name, host: host, port: Int(port)); name = ""; host = ""; port = "" }.buttonStyle(.industrial)
                        .disabled(host.trimmingCharacters(in: .whitespaces).isEmpty)
                }
            }
        }.padding(20)
    }
    func conn(_ n: Candidate) -> Conn { w.conns[n.ip] ?? Conn() }

    func rowView(_ r: TargetRow) -> some View {
        HStack(spacing: 0) {
            HStack(spacing: 8) {
                Text(r.template.kind.label).font(Theme.mono(8, .bold)).padding(.horizontal, 4).padding(.vertical, 2)
                    .overlay(RoundedRectangle(cornerRadius: 2).stroke(Theme.line)).foregroundStyle(Theme.dim)
                VStack(alignment: .leading, spacing: 0) {
                    Text(r.template.name).font(.system(size: 12, weight: .medium)).lineLimit(1)
                    Text(r.template.endpointLabel).font(Theme.mono(10)).foregroundStyle(Theme.faint).lineLimit(1)
                }
                Spacer()
                Button("all") { w.setRow(r, on: true) }.buttonStyle(.plain).font(Theme.mono(9)).foregroundStyle(Theme.amber)
                Button("none") { w.setRow(r, on: false) }.buttonStyle(.plain).font(Theme.mono(9)).foregroundStyle(Theme.amber)
            }.frame(maxWidth: .infinity)
            ForEach(nodes) { n in
                let self_ = r.forNodeIP == n.ip
                Button { w.toggle(r, n.ip) } label: {
                    Image(systemName: self_ ? "minus" : (w.grid.contains("\(r.id)@@\(n.ip)") ? "checkmark.square.fill" : "square"))
                        .foregroundStyle(self_ ? Theme.line : (w.grid.contains("\(r.id)@@\(n.ip)") ? Theme.amber : Theme.faint))
                }.buttonStyle(.plain).disabled(self_).frame(width: 120)
            }
            Group {
                if r.section == "Custom" || r.section == "Existing" {
                    Button { w.removeRow(r) } label: { Image(systemName: "trash") }.buttonStyle(.plain).foregroundStyle(Theme.faint)
                } else { Color.clear }
            }.frame(width: 60)
        }
        .padding(.vertical, 5)
        .overlay(alignment: .bottom) { Rectangle().fill(Theme.line.opacity(0.5)).frame(height: 1) }
    }
}

// MARK: - Step 4
struct ReviewStep: View {
    @ObservedObject var w: WizardModel
    var onClose: () -> Void
    @State var done = false
    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            ForEach(w.candidates.filter { w.readyIPs.contains($0.ip) }) { n in
                Panel {
                    HStack {
                        VStack(alignment: .leading, spacing: 2) {
                            Text(w.conns[n.ip]?.identity?.displayName ?? n.name).font(.system(size: 14, weight: .semibold))
                            Text("\(n.ip) → \(w.count(for: n.ip)) targets").font(Theme.mono(11)).foregroundStyle(Theme.dim)
                        }
                        Spacer()
                        if let r = w.applyResults[n.ip] { Text(r).font(Theme.mono(11)).foregroundStyle(r.hasPrefix("OK") ? Theme.green : Theme.red) }
                    }
                    Text(w.rows.filter { w.grid.contains("\($0.id)@@\(n.ip)") }.map(\.template.name).joined(separator: "  ·  "))
                        .font(Theme.mono(10)).foregroundStyle(Theme.faint).padding(.top, 6)
                }
            }
            Text("Applying writes the target list to each agent's config; probing starts immediately and history is stored on the node.")
                .font(.caption).foregroundStyle(Theme.faint)
            Spacer()
            HStack {
                Spacer()
                Button(w.applying ? "APPLYING…" : (done ? "OPEN DASHBOARD" : "APPLY & START MONITORING")) {
                    if done { onClose(); return }
                    Task { let ok = await w.apply(); if ok { done = true; try? await Task.sleep(for: .seconds(0.6)); onClose() } else { done = false } }
                }.buttonStyle(.industrialPrimary).disabled(w.applying)
            }
        }.padding(20)
    }
}
