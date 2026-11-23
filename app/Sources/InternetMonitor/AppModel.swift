import SwiftUI
import IMCore
import UserNotifications
import ServiceManagement

enum TimeRange: String, CaseIterable, Identifiable {
    case m5 = "5m", h1 = "1h", h6 = "6h", h24 = "24h", d7 = "7d", d30 = "30d", d60 = "60d"
    var id: String { rawValue }
    var seconds: Int { switch self { case .m5: 300; case .h1: 3600; case .h6: 21600; case .h24: 86400; case .d7: 604800; case .d30: 2592000; case .d60: 5184000 } }
    var refreshSec: TimeInterval { switch self { case .m5: 4; case .h1: 8; case .h6: 30; case .h24: 60; case .d7: 180; case .d30, .d60: 300 } }
}

enum Route: Hashable { case overview, node(String), incidents, settings }

@MainActor
final class NodeState: ObservableObject, Identifiable {
    @Published var saved: SavedNode
    @Published var identity: Identity?
    @Published var reachable: Bool?
    @Published var lastError: String?
    @Published var unauthorized = false
    @Published var config: AgentConfig?
    @Published var live: [String: LiveTarget] = [:]
    @Published var series: [String: Series] = [:]
    @Published var incidents: [Incident] = []
    @Published var system: SystemInfo?
    @Published var summaries: [String: [SummaryWindow]] = [:]
    @Published var speed: [SpeedResult] = []
    @Published var rssi: [(Date, Int)] = []
    var failures = 0
    var id: String { saved.nodeId }
    var client: AgentClient { AgentClient(host: saved.host, port: saved.port, token: saved.token) }
    var name: String { config?.nodeName.isEmpty == false ? config!.nodeName : (identity?.displayName ?? saved.name) }
    var targets: [Target] { (config?.targets ?? []).filter(\.enabled) }
    var worst: LinkState {
        if reachable == false { return .down }
        return live.values.map(\.state).max() ?? .unknown
    }
    init(_ s: SavedNode) { saved = s }
}

@MainActor
final class AppModel: ObservableObject {
    @Published var nodes: [NodeState] = []
    @Published var route: Route = .overview
    @Published var range: TimeRange = .h1 { didSet { zoom = nil; refreshSeries(force: true) } }
    @Published var zoom: ClosedRange<Date>?  { didSet { refreshSeries(force: true) } }
    @Published var hoverDate: Date?
    @Published var now = Date()
    @Published var showWizard = false
    @Published var wizardEditing = false
    @Published var icons: [String: String] = UserDefaults.standard.dictionary(forKey: "nodeIcons") as? [String: String] ?? [:]
    @Published var toast: String?
    @Published var expanded: Set<String> = Set(UserDefaults.standard.stringArray(forKey: "overviewExpanded") ?? [])
    @Published var removing: Set<String> = []
    private var lastSeriesFetch: [String: Date] = [:]
    static let historyDays = 60

    let store = NodeStore()
    let updater = Updater()
    private var liveTask: Task<Void, Never>?
    private var slowTask: Task<Void, Never>?
    private var previous: [String: LinkState] = [:]   // "node|target" → state
    private var started = false

    init() {
        nodes = store.load().map(NodeState.init)
        showWizard = nodes.isEmpty
        NotificationCenter.default.addObserver(forName: NSApplication.willTerminateNotification, object: nil, queue: .main) { [updater] _ in MainActor.assumeIsolated { updater.applyOnQuit() } }
        if UserDefaults.standard.object(forKey: "overviewExpanded") == nil { expanded = Set(nodes.map(\.id)) }
        if let n = nodes.first, route == .overview { _ = n }
    }

    var domain: ClosedRange<Date> {
        if let z = zoom { return z }
        return now.addingTimeInterval(-TimeInterval(range.seconds))...now
    }

    var selectedNode: NodeState? {
        if case .node(let id) = route { return nodes.first { $0.id == id } }
        return nil
    }
    var worst: LinkState { nodes.map(\.worst).max() ?? .unknown }

    func start() {
        guard !started else { return }
        started = true
        requestNotificationPermission()
        updater.start()
        liveTask = Task { [weak self] in
            while !Task.isCancelled {
                guard let self else { return }
                await self.pollLive()
                try? await Task.sleep(for: .seconds(2))
            }
        }
        slowTask = Task { [weak self] in
            var tick = 0
            while !Task.isCancelled {
                guard let self else { return }
                await self.pollSlow(tick: tick)
                tick += 1
                try? await Task.sleep(for: .seconds(5))
            }
        }
    }

    func persist() { store.save(nodes.map(\.saved)) }

    func add(_ s: SavedNode) {
        if let i = nodes.firstIndex(where: { $0.id == s.nodeId }) { nodes[i].saved = s; nodes[i].unauthorized = false }
        else { nodes.append(NodeState(s)) }
        persist()
        Task { await pollSlow(tick: 0); await pollLive() }
    }
    func remove(_ n: NodeState) {
        nodes.removeAll { $0.id == n.id }; persist()
        if case .node(let id) = route, id == n.id { route = .overview }
    }
    func setIcon(_ icon: String, for n: NodeState) {
        icons[n.id] = icon; UserDefaults.standard.set(icons, forKey: "nodeIcons")
    }

    // MARK: Polling
    func pollLive() async {
        now = Date()
        await withTaskGroup(of: Void.self) { g in
            for n in nodes { g.addTask { await self.liveOne(n) } }
        }
    }

    private func liveOne(_ n: NodeState) async {
        let client = n.client
        do {
            let r = try await client.live()
            n.failures = 0
            if n.reachable != true { n.reachable = true; n.lastError = nil; n.unauthorized = false; notify("\(n.name) reachable", "Agent is back online.", id: "reach-\(n.id)") }
            var m: [String: LiveTarget] = [:]
            for t in r.targets { m[t.id] = t }
            n.live = m
            evaluate(n, m)
        } catch let e as AgentError {
            n.failures += 1
            n.lastError = e.localizedDescription
            if e.isUnauthorized { n.unauthorized = true; n.reachable = true }
            else if n.failures >= 3 {
                if n.reachable != false { notify("\(n.name) unreachable", "Cannot reach the agent: \(e.message)", id: "reach-\(n.id)") }
                n.reachable = false
            }
        } catch { n.failures += 1 }
    }

    private func evaluate(_ n: NodeState, _ m: [String: LiveTarget]) {
        for (id, t) in m {
            let key = "\(n.id)|\(id)"
            let old = previous[key]; previous[key] = t.state
            guard let old, old != t.state, old != .unknown || t.state == .down else { continue }
            let tname = n.config?.targets.first { $0.id == id }?.name ?? id
            switch t.state {
            case .down: notify("\(n.name) → \(tname) DOWN", t.lastError ?? "No response", id: key)
            case .degraded: notify("\(n.name) → \(tname) degraded", "Latency \(Fmt.ms(t.avgMs)) ms avg", id: key)
            case .up: notify("\(n.name) → \(tname) recovered", "Back to normal (\(Fmt.ms(t.lastRttMs)) ms)", id: key)
            case .unknown: break
            }
        }
    }

    func pollSlow(tick: Int) async {
        await withTaskGroup(of: Void.self) { g in
            for n in nodes {
                g.addTask { await self.slowOne(n, tick: tick) }
            }
        }
        refreshSeries(force: false)
    }

    private func slowOne(_ n: NodeState, tick: Int) async {
        let c = n.client
        if n.identity == nil || tick % 6 == 0 { if let id = try? await c.identity() { n.identity = id } }
        if n.config == nil || tick % 6 == 0, var cfg = try? await c.config() {
            // Keep two months of history on every node.
            if cfg.retentionRollupDays < Self.historyDays {
                cfg.retentionRollupDays = Self.historyDays
                if let saved = try? await c.putConfig(cfg) { cfg = saved }
            }
            n.config = cfg
        }
        if tick % 3 == 0, let inc = try? await c.incidents(since: Int(Date().timeIntervalSince1970) - 30 * 86400) { n.incidents = inc }
        if tick % 3 == 0, let sys = try? await c.system() {
            n.system = sys
            if let r = sys.wifi?.rssiDbm { n.rssi.append((Date(), r)); if n.rssi.count > 720 { n.rssi.removeFirst(60) } }
        }
        if tick % 12 == 0, let s = try? await c.summary() {
            var m: [String: [SummaryWindow]] = [:]
            for x in s.summaries { m[x.target] = x.windows }
            n.summaries = m
        }
    }

    private var seriesBusy = Set<String>()

    /// Nodes whose graphs are currently on screen.
    var visibleNodes: [NodeState] {
        switch route {
        case .node(let id): return nodes.filter { $0.id == id }
        case .overview: return nodes.filter { expanded.contains($0.id) }
        default: return []
        }
    }

    func toggleExpanded(_ n: NodeState) {
        if expanded.contains(n.id) { expanded.remove(n.id) } else { expanded.insert(n.id); fetchSeries(n) }
        UserDefaults.standard.set(Array(expanded), forKey: "overviewExpanded")
    }

    func refreshSeries(force: Bool) {
        if zoom != nil && !force { return }     // zoomed window is static
        for n in visibleNodes {
            if !force, let t = lastSeriesFetch[n.id], Date().timeIntervalSince(t) < range.refreshSec { continue }
            fetchSeries(n)
        }
    }

    func fetchSeries(_ n: NodeState) {
        let ids = n.targets.map(\.id)
        guard !ids.isEmpty, !seriesBusy.contains(n.id) else { return }
        let to: Int, from: Int
        if let z = zoom { from = Int(z.lowerBound.timeIntervalSince1970); to = Int(z.upperBound.timeIntervalSince1970) }
        else { to = Int(Date().timeIntervalSince1970); from = to - range.seconds }
        seriesBusy.insert(n.id); lastSeriesFetch[n.id] = Date()
        Task {
            defer { seriesBusy.remove(n.id) }
            if let r = try? await n.client.series(targets: ids, from: from, to: to, points: 500) {
                var m: [String: Series] = [:]
                for s in r.series { m[s.target] = s }
                n.series = m
            }
        }
    }

    // MARK: Agent upgrade
    func agentOutdated(_ n: NodeState) -> Bool {
        guard let v = n.identity?.version.flatMap(SemVer.init), let b = SemVer(AppInfo.bundledAgentVersion) else { return false }
        return v < b
    }
    /// Re-runs the idempotent installer over SSH with the bundled binary; state, tokens and history are preserved.
    func upgradeAgent(_ n: NodeState) async -> String {
        let host = n.saved.host
        guard let user = await SSHInstaller.probeLogin(host: host, users: SSHInstaller.guessUsers(saved: n.saved.sshUser)) else { return "SSH login failed — upgrade manually with `sudo ./imonitord install`." }
        let r = await SSHInstaller.install(user: user, host: host, binaryFor: { WizardModel.bundledAgent(arch: $0) }, log: { _ in })
        switch r {
        case .success: n.identity = try? await n.client.identity(); return "Agent upgraded to v\(n.identity?.version ?? "?")"
        case .failure(let e): return e.message
        }
    }

    // MARK: Removal
    /// Uninstalls the agent (and all its files) from the node over SSH, then forgets it here.
    func uninstallAndRemove(_ n: NodeState) async -> String? {
        removing.insert(n.id); defer { removing.remove(n.id) }
        let r = await Self.removeAgent(host: n.saved.host, sshUser: n.saved.sshUser)
        if let err = r.error { return err }
        remove(n)
        return r.leftovers.isEmpty ? nil : "Removed, but these files remain: " + r.leftovers.joined(separator: ", ")
    }

    nonisolated static func removeAgent(host: String, sshUser: String?) async -> (error: String?, leftovers: [String], user: String?) {
        guard let user = await SSHInstaller.probeLogin(host: host, users: SSHInstaller.guessUsers(saved: sshUser)) else {
            return ("SSH login to \(host) failed, so the agent could not be removed. Manual: ssh pi@\(host) 'sudo imonitord uninstall --purge'", [], nil)
        }
        switch await SSHInstaller.uninstall(user: user, host: host, log: { _ in }) {
        case .failure(let e): return (e.message, [], user)
        case .success(let left):
            // Independent check from this side: the agent port must be closed.
            if await Discovery.probeAgent(host, timeout: 2) != nil { return ("Agent still answers on \(host):7878 after removal.", left, user) }
            return (nil, left, user)
        }
    }

    func selectRoute(_ r: Route) {
        route = r; zoom = nil
        if r == .overview { refreshSeries(force: true) }
        if case .node(let id) = r, let n = nodes.first(where: { $0.id == id }) {
            n.series = [:]
            fetchSeries(n)
            Task { n.speed = (try? await n.client.speedHistory()) ?? [] }
        }
    }

    // MARK: Config helpers
    func updateConfig(_ n: NodeState, _ edit: (inout AgentConfig) -> Void) async -> String? {
        guard var cfg = n.config else { return "Config not loaded yet" }
        edit(&cfg)
        do { n.config = try await n.client.putConfig(cfg); return nil }
        catch { return error.localizedDescription }
    }

    // MARK: Notifications / login item
    private var canNotify: Bool { Bundle.main.bundleIdentifier != nil && Bundle.main.bundlePath.hasSuffix(".app") }
    func requestNotificationPermission() {
        guard canNotify else { return }
        UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound]) { _, _ in }
    }
    func notify(_ title: String, _ body: String, id: String) {
        guard canNotify, UserDefaults.standard.object(forKey: "notificationsEnabled") as? Bool ?? true else { return }
        let c = UNMutableNotificationContent(); c.title = title; c.body = body; c.sound = .default
        UNUserNotificationCenter.current().add(UNNotificationRequest(identifier: id + "-\(Int(Date().timeIntervalSince1970))", content: c, trigger: nil))
    }
    var launchAtLogin: Bool {
        get { SMAppService.mainApp.status == .enabled }
        set { objectWillChange.send(); try? (newValue ? SMAppService.mainApp.register() : SMAppService.mainApp.unregister()) }
    }

    func flash(_ s: String) { toast = s; Task { try? await Task.sleep(for: .seconds(3)); if toast == s { toast = nil } } }
}
