import SwiftUI
import IMCore

enum WizardStep: Int, CaseIterable { case scan, connect, targets, review
    var title: String { ["Discover", "Connect", "Targets", "Review"][rawValue] }
}

struct Conn: Equatable {
    enum Phase: Equatable { case checking, noAgent, needsCode, working, ready, failed }
    var phase: Phase = .checking
    var identity: Identity?
    var token: String?
    var sshUser = ""
    var code = ""
    var message = ""
    var log: [String] = []
    var recognised = false      // an agent was already running here
    var config: AgentConfig?
}

struct TargetRow: Identifiable, Equatable {
    var id: String
    var template: Target
    var section: String            // "Nodes", "Baseline", "Custom", "Existing"
    var forNodeIP: String?         // node rows are not selectable on their own column
}

@MainActor
final class WizardModel: ObservableObject {
    @Published var step: WizardStep = .scan
    @Published var candidates: [Candidate] = []
    @Published var scanning = false
    @Published var progress = ""
    @Published var selected: Set<String> = []
    @Published var conns: [String: Conn] = [:]
    @Published var rows: [TargetRow] = []
    @Published var grid: Set<String> = []      // "rowId@@ip"
    @Published var applyResults: [String: String] = [:]
    @Published var applying = false
    @Published var manual = ""
    @Published var manualError: String?
    @Published var removeResults: [String: String] = [:]
    @Published var removingIPs: Set<String> = []
    let model: AppModel
    let selfIPs: Set<String>
    let editing: Bool

    init(model: AppModel, editing: Bool) {
        self.model = model; self.editing = editing
        selfIPs = Set(Discovery.localInterfaces().map(\.ip))
        if editing {
            for n in model.nodes {
                let c = Candidate(ip: n.saved.host, name: n.name, network: "Saved", isTailscale: Discovery.isTailscaleIP(n.saved.host), agent: n.identity)
                candidates.append(c); selected.insert(c.ip)
                conns[c.ip] = Conn(phase: .ready, identity: n.identity, token: n.saved.token, sshUser: n.saved.sshUser ?? "", recognised: true, config: n.config)
            }
            buildRows()
            step = .targets
        }
    }

    // MARK: Step 1 – scan
    func scan() {
        guard !scanning else { return }
        scanning = true; progress = "Starting…"
        Task {
            _ = await Discovery.scan(progress: { p in Task { @MainActor in self.progress = p } },
                                     onUpdate: { list in Task { @MainActor in self.merge(list) } })
            scanning = false; progress = "Scan complete"
        }
    }
    private func merge(_ list: [Candidate]) {
        var byIP = Dictionary(uniqueKeysWithValues: candidates.map { ($0.ip, $0) })
        for c in list { byIP[c.ip] = c }
        candidates = byIP.values.sorted { (Discovery.ipv4ToUInt($0.ip) ?? 0) < (Discovery.ipv4ToUInt($1.ip) ?? 0) }
    }

    func addManual() {
        let host = manual.trimmingCharacters(in: .whitespaces)
        guard !host.isEmpty else { return }
        manualError = nil
        if selfIPs.contains(host) || host == "127.0.0.1" || host == "localhost" { manualError = "That is this machine — only other machines can be monitored."; return }
        Task {
            let id = await Discovery.probeAgent(host, timeout: 2.5)
            let ping = (await Discovery.icmpSweep([host], timeout: 1.5))[host]
            if id == nil && ping == nil { manualError = "\(host) did not answer ping or the agent port (7878)." ; return }
            var c = Candidate(ip: host, name: id?.displayName ?? host, network: Discovery.isTailscaleIP(host) ? "Tailscale" : "Manual", isTailscale: Discovery.isTailscaleIP(host), pingMs: ping, agent: id)
            if id == nil, let rd = Discovery.reverseDNS(host) { c.name = rd }
            merge([c]); selected.insert(host); manual = ""
        }
    }

    // MARK: Removal (uninstall agent + delete its files over SSH)
    func savedNode(for c: Candidate) -> NodeState? {
        model.nodes.first { $0.saved.host == c.ip || (c.agent != nil && $0.id == c.agent?.nodeId) }
    }
    func canRemove(_ c: Candidate) -> Bool { c.agent != nil || savedNode(for: c) != nil }
    var removableSelected: [Candidate] { candidates.filter { selected.contains($0.ip) && canRemove($0) } }

    func removeAgents(_ list: [Candidate]) async {
        for c in list {
            removingIPs.insert(c.ip); removeResults[c.ip] = "Removing…"
            let saved = savedNode(for: c)
            let r = await AppModel.removeAgent(host: c.ip, sshUser: saved?.saved.sshUser ?? conns[c.ip]?.sshUser)
            removingIPs.remove(c.ip)
            if let e = r.error { removeResults[c.ip] = "FAILED: " + e; continue }
            if let saved { model.remove(saved) }
            if let i = candidates.firstIndex(where: { $0.ip == c.ip }) { candidates[i].agent = nil }
            conns[c.ip] = nil; selected.remove(c.ip)
            removeResults[c.ip] = r.leftovers.isEmpty ? "Agent removed — no files left on the node" : "Removed, leftovers: " + r.leftovers.joined(separator: ", ")
        }
    }

    // MARK: Step 2 – connect
    func beginConnect() {
        step = .connect
        for ip in selected where conns[ip]?.phase != .ready { Task { await check(ip) } }
    }

    func check(_ ip: String) async {
        var c = conns[ip] ?? Conn()
        c.phase = .checking; c.message = ""; conns[ip] = c
        guard let cand = candidates.first(where: { $0.ip == ip }) else { return }
        var ident = cand.agent
        if ident == nil { ident = await Discovery.probeAgent(ip, timeout: 2.5) }
        c.identity = ident
        c.sshUser = c.sshUser.isEmpty ? (model.nodes.first { $0.saved.host == ip }?.saved.sshUser ?? "") : c.sshUser
        guard let ident else { c.phase = .noAgent; c.message = "No InternetMonitor agent found on this machine."; conns[ip] = c; return }
        c.recognised = true
        // Already known to this Mac? Re-use the stored token.
        if let saved = model.nodes.first(where: { $0.id == ident.nodeId }) {
            let cl = AgentClient(host: ip, port: saved.saved.port, token: saved.saved.token)
            if let cfg = try? await cl.config() {
                c.token = saved.saved.token; c.config = cfg; c.phase = .ready; c.message = "Already paired with this Mac."; conns[ip] = c; return
            }
        }
        c.phase = .needsCode
        c.message = "Agent v\(ident.version ?? "?") found (\(ident.hostname ?? ip)). Enter the pairing code shown by `imonitord pair-code` on that machine, or connect automatically over SSH."
        conns[ip] = c
        // Try SSH silently first: it is the zero-typing path.
        await connectViaSSH(ip, silent: true)
    }

    func log(_ ip: String, _ s: String) { conns[ip, default: Conn()].log.append(s) }

    func connectViaSSH(_ ip: String, silent: Bool = false) async {
        var c = conns[ip] ?? Conn()
        c.phase = .working; c.log = ["Trying SSH…"]; conns[ip] = c
        let users = SSHInstaller.guessUsers(saved: c.sshUser)
        guard let user = await SSHInstaller.probeLogin(host: ip, users: users) else {
            conns[ip]?.phase = c.identity == nil ? .noAgent : .needsCode
            conns[ip]?.message = silent ? (conns[ip]?.message ?? "") : "SSH login failed for \(users.joined(separator: ", ")). Enter a user name and retry, or paste the pairing code."
            if !silent { conns[ip]?.log.append("No SSH login worked (tried \(users.joined(separator: ", ")))") }
            return
        }
        conns[ip]?.sshUser = user; log(ip, "SSH ok as \(user)")
        if conns[ip]?.identity == nil { await installAgent(ip, user: user); return }
        switch await SSHInstaller.fetchPairCode(user: user, host: ip) {
        case .success(let code): log(ip, "Got pairing code"); await pair(ip, code: code)
        case .failure(let e): conns[ip]?.phase = .needsCode; conns[ip]?.message = "Could not fetch a code over SSH: \(e.message)"
        }
    }

    func installAgent(_ ip: String, user: String? = nil) async {
        conns[ip]?.phase = .working
        let u = user ?? (conns[ip]?.sshUser.isEmpty == false ? conns[ip]!.sshUser : SSHInstaller.guessUsers(saved: nil).first!)
        conns[ip]?.sshUser = u
        let res = await SSHInstaller.install(user: u, host: ip, binaryFor: { Self.bundledAgent(arch: $0) }) { s in Task { @MainActor in self.log(ip, s) } }
        switch res {
        case .failure(let e):
            conns[ip]?.phase = .noAgent; conns[ip]?.message = e.message
            log(ip, "Manual alternative: \(SSHInstaller.manualInstallCommand(host: ip, user: u))")
        case .success:
            try? await Task.sleep(for: .seconds(1.5))
            let ident = await Discovery.probeAgent(ip, timeout: 3)
            conns[ip]?.identity = ident
            guard ident != nil else { conns[ip]?.phase = .noAgent; conns[ip]?.message = "Installed, but the agent is not answering on port 7878 yet."; return }
            switch await SSHInstaller.fetchPairCode(user: u, host: ip) {
            case .success(let code): await pair(ip, code: code)
            case .failure(let e): conns[ip]?.phase = .needsCode; conns[ip]?.message = e.message
            }
        }
    }

    nonisolated static func bundledAgent(arch: String) -> URL? {
        let map = ["aarch64": "arm64", "arm64": "arm64", "x86_64": "amd64", "amd64": "amd64", "i686": "386", "i386": "386", "i586": "386"]
        guard let suffix = map[arch] ?? (arch.hasPrefix("armv") ? "arm" : nil) else { return nil }
        let name = "imonitord-linux-\(suffix)"
        if let u = Bundle.main.url(forResource: name, withExtension: nil) { return u }
        // dev fallback: running from the repo
        let dev = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
            .deletingLastPathComponent().appendingPathComponent("agent/dist/\(name)")
        return FileManager.default.fileExists(atPath: dev.path) ? dev : nil
    }

    func pair(_ ip: String, code: String) async {
        conns[ip]?.phase = .working
        guard let ident = conns[ip]?.identity else { return }
        let client = AgentClient(host: ip)
        do {
            let r = try await client.pair(code: code.trimmingCharacters(in: .whitespaces), clientName: Host.current().localizedName ?? "Mac")
            let cl = AgentClient(host: ip, token: r.token)
            let cfg = try await cl.config()
            conns[ip]?.token = r.token; conns[ip]?.config = cfg; conns[ip]?.phase = .ready; conns[ip]?.message = "Paired."
            _ = ident
        } catch {
            conns[ip]?.phase = .needsCode; conns[ip]?.message = (error as? AgentError)?.message ?? error.localizedDescription
        }
    }

    var readyIPs: [String] { candidates.map(\.ip).filter { selected.contains($0) && conns[$0]?.phase == .ready } }
    var allReady: Bool { !selected.isEmpty && selected.allSatisfy { conns[$0]?.phase == .ready } }

    // MARK: Step 3 – targets
    func buildRows() {
        var out: [TargetRow] = []
        let sel = candidates.filter { selected.contains($0.ip) }
        for c in sel {
            let nid = conns[c.ip]?.identity?.nodeId ?? c.ip
            out.append(TargetRow(id: "node-\(nid)", template: Target(id: "node-\(nid)", name: conns[c.ip]?.identity?.displayName ?? c.name, kind: .icmp, host: c.ip, group: "node", nodeRef: conns[c.ip]?.identity?.nodeId), section: "Nodes", forNodeIP: c.ip))
        }
        out.append(TargetRow(id: "base-gw", template: Target(id: "base-gw", name: "Gateway", kind: .icmp, host: "@gateway", group: "baseline"), section: "Baseline"))
        out.append(TargetRow(id: "base-cf", template: Target(id: "base-cf", name: "Cloudflare DNS", kind: .icmp, host: "1.1.1.1", group: "baseline"), section: "Baseline"))
        out.append(TargetRow(id: "base-g", template: Target(id: "base-g", name: "Google DNS", kind: .icmp, host: "8.8.8.8", group: "baseline"), section: "Baseline"))
        var g = Set<String>()
        var anyExisting = false
        for ip in sel.map(\.ip) {
            guard let cfg = conns[ip]?.config else { continue }
            for t in cfg.targets {
                anyExisting = true
                if let r = out.firstIndex(where: { $0.id == t.id || ($0.template.endpointKey == t.endpointKey) }) { g.insert("\(out[r].id)@@\(ip)") }
                else { out.append(TargetRow(id: t.id, template: t, section: t.group == "custom" ? "Custom" : "Existing")); g.insert("\(t.id)@@\(ip)") }
            }
        }
        if !anyExisting || !editing && sel.allSatisfy({ (conns[$0.ip]?.config?.targets.count ?? 0) <= 3 }) {
            // fresh agents (only built-in defaults) → sensible defaults: everyone pings everyone else + baselines
            g.removeAll()
            for r in out where r.section == "Nodes" || r.section == "Baseline" { for c in sel where r.forNodeIP != c.ip { g.insert("\(r.id)@@\(c.ip)") } }
            for ip in sel.map(\.ip) { for t in conns[ip]?.config?.targets ?? [] where !out.contains(where: { $0.template.endpointKey == t.endpointKey }) {
                out.append(TargetRow(id: t.id, template: t, section: "Existing")); g.insert("\(t.id)@@\(ip)") } }
        }
        rows = out; grid = g
    }

    func toggle(_ row: TargetRow, _ ip: String) {
        let k = "\(row.id)@@\(ip)"
        if grid.contains(k) { grid.remove(k) } else { grid.insert(k) }
    }
    func setColumn(_ ip: String, on: Bool) { for r in rows where r.forNodeIP != ip { let k = "\(r.id)@@\(ip)"; if on { grid.insert(k) } else { grid.remove(k) } } }
    func setRow(_ row: TargetRow, on: Bool) { for ip in selected where row.forNodeIP != ip { let k = "\(row.id)@@\(ip)"; if on { grid.insert(k) } else { grid.remove(k) } } }

    func addCustom(kind: ProbeKind, name: String, host: String, port: Int?) {
        let h = host.trimmingCharacters(in: .whitespaces)
        guard !h.isEmpty else { return }
        let id = "custom-" + String(UInt32(truncatingIfNeeded: "\(kind)\(h)\(port ?? 0)".hashValue) , radix: 16) + String(Int.random(in: 0..<999))
        var t = Target(id: id, name: name.isEmpty ? h : name, kind: kind, group: "custom")
        switch kind {
        case .http: t.url = h.hasPrefix("http") ? h : "https://\(h)"; t.host = URL(string: t.url!)?.host
        case .tcp: t.host = h; t.port = port ?? 443
        default: t.host = h
        }
        let row = TargetRow(id: id, template: t, section: "Custom")
        rows.append(row); setRow(row, on: true)
    }
    func removeRow(_ row: TargetRow) { rows.removeAll { $0.id == row.id }; grid = grid.filter { !$0.hasPrefix(row.id + "@@") } }

    // MARK: Step 4 – apply
    func count(for ip: String) -> Int { rows.filter { grid.contains("\($0.id)@@\(ip)") }.count }

    func apply() async -> Bool {
        applying = true; applyResults = [:]
        var ok = true
        for ip in readyIPs {
            guard let c = conns[ip], let token = c.token, let ident = c.identity else { continue }
            let cl = AgentClient(host: ip, token: token)
            do {
                var cfg = try await cl.config()
                cfg.targets = rows.filter { grid.contains("\($0.id)@@\(ip)") }.map { r in
                    var t = r.template
                    // keep existing probe interval overrides for targets that were already configured
                    if let old = cfg.targets.first(where: { $0.id == t.id }) { t.intervalMs = old.intervalMs }
                    return t
                }
                _ = try await cl.putConfig(cfg)
                model.add(SavedNode(nodeId: ident.nodeId, name: ident.displayName, host: ip, token: token, sshUser: c.sshUser.isEmpty ? nil : c.sshUser))
                applyResults[ip] = "OK — \(cfg.targets.count) targets"
            } catch {
                ok = false; applyResults[ip] = "Failed: \((error as? AgentError)?.message ?? error.localizedDescription)"
            }
        }
        applying = false
        return ok
    }
}
