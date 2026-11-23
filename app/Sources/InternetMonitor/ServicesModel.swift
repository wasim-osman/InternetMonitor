import SwiftUI
import IMCore

struct ServicePrefs: Codable {
    var selected: [String] = []
    var nodes: [String] = []
    var desktop = false
    var custom: [ServiceDef] = []
    var applied: [String: [String]] = [:]       // nodeId → service ids last written to that node
    var appliedDesktop: Bool = false
}

struct LocalProbeState {
    var samples: [(Date, Double?)] = []
    var failures = 0
    var down = false
    var lastMs: Double? { samples.last?.1 ?? nil }
}

@MainActor
final class ServicesModel: ObservableObject {
    @Published var prefs: ServicePrefs { didSet { save() } }
    @Published var status: [String: ServiceStatus] = [:]
    @Published var statusError: [String: String] = [:]
    @Published var local: [String: LocalProbeState] = [:]
    @Published var applying = false
    @Published var applyResults: [String: String] = [:]
    @Published var search = ""

    unowned let app: AppModel
    private let url: URL
    private var lastFetch: [String: Date] = [:]
    private var lastProbe: [String: Date] = [:]
    private var loop: Task<Void, Never>?
    private var notified: [String: Set<String>] = [:]        // service → incident ids already announced
    private var seenOnce = Set<String>()

    init(app: AppModel) {
        self.app = app
        let dir = app.store.url.deletingLastPathComponent()
        url = dir.appendingPathComponent("services.json")
        if let d = try? Data(contentsOf: url), let p = try? JSONDecoder().decode(ServicePrefs.self, from: d) { prefs = p } else { prefs = ServicePrefs() }
    }

    private func save() {
        guard let d = try? JSONEncoder().encode(prefs) else { return }
        try? d.write(to: url, options: .atomic)
        try? FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: url.path)
    }

    // MARK: Catalog & selection
    var all: [ServiceDef] { ServiceCatalog.builtIn + prefs.custom }
    var selectedServices: [ServiceDef] { all.filter { prefs.selected.contains($0.id) } }
    func isSelected(_ s: ServiceDef) -> Bool { prefs.selected.contains(s.id) }
    func toggle(_ s: ServiceDef) {
        if let i = prefs.selected.firstIndex(of: s.id) { prefs.selected.remove(at: i); status[s.id] = nil; local[s.id] = nil }
        else { prefs.selected.append(s.id); Task { await refreshStatus(s, force: true) } }
    }
    func setNode(_ id: String, on: Bool) {
        if on { if !prefs.nodes.contains(id) { prefs.nodes.append(id) } } else { prefs.nodes.removeAll { $0 == id } }
    }
    func setDesktop(_ on: Bool) { prefs.desktop = on }
    func addCustom(name: String, probe: String, statusBase: String) {
        let p = probe.hasPrefix("http") ? probe : "https://\(probe)"
        guard URL(string: p)?.host != nil else { return }
        var base = statusBase.trimmingCharacters(in: .whitespaces)
        var adapter = "none"
        if !base.isEmpty {
            if !base.hasPrefix("http") { base = "https://" + base }
            if !base.contains("/api/") { base = base.trimmingCharacters(in: CharacterSet(charactersIn: "/")) + "/api/v2/summary.json" }
            adapter = "statuspage"
        }
        let id = "custom-" + String(UInt32(truncatingIfNeeded: (name + p).hashValue), radix: 16)
        let svc = ServiceDef(id: id, name: name.isEmpty ? (URL(string: p)?.host ?? p) : name, category: "Custom", adapter: adapter,
                             statusUrl: adapter == "none" ? nil : base, pageUrl: adapter == "none" ? p : (URL(string: base)?.host.map { "https://\($0)" } ?? p), probeUrl: p, custom: true)
        prefs.custom.append(svc); toggle(svc)
    }
    func removeCustom(_ s: ServiceDef) {
        prefs.selected.removeAll { $0 == s.id }; prefs.custom.removeAll { $0.id == s.id }; status[s.id] = nil
    }

    var problemCount: Int { selectedServices.filter { status[$0.id]?.state.isProblem == true }.count }
    var filtered: [ServiceDef] {
        let q = search.trimmingCharacters(in: .whitespaces).lowercased()
        return q.isEmpty ? all : all.filter { $0.name.lowercased().contains(q) || $0.category.lowercased().contains(q) }
    }

    // MARK: Polling
    func start() {
        guard loop == nil else { return }
        loop = Task { [weak self] in
            while !Task.isCancelled {
                guard let self else { return }
                await self.tick()
                try? await Task.sleep(for: .seconds(5))
            }
        }
    }

    private func tick() async {
        let now = Date()
        for s in selectedServices {
            if now.timeIntervalSince(lastFetch[s.id] ?? .distantPast) >= s.pollSeconds { await refreshStatus(s, force: true) }
            if prefs.desktop, now.timeIntervalSince(lastProbe[s.id] ?? .distantPast) >= 30 { lastProbe[s.id] = now; Task { await self.probeLocal(s) } }
        }
    }

    func refreshStatus(_ s: ServiceDef, force: Bool) async {
        lastFetch[s.id] = Date()
        do {
            let st = try await ServiceStatusFetcher.fetch(s)
            statusError[s.id] = nil
            let old = status[s.id]; status[s.id] = st
            announce(s, old: old, new: st)
        } catch { statusError[s.id] = error.localizedDescription }
    }

    /// Notify on state changes and on newly appeared incidents; stay silent for the first healthy observation.
    private func announce(_ s: ServiceDef, old: ServiceStatus?, new: ServiceStatus) {
        defer { seenOnce.insert(s.id) }
        if new.state == .unknown { return }
        let first = !seenOnce.contains(s.id)
        let seen = notified[s.id] ?? []
        let fresh = new.incidents.filter { !seen.contains($0.id) }
        notified[s.id] = Set(new.incidents.map(\.id))
        if first {
            if new.state.isProblem { app.notify("\(s.name): \(new.state.rawValue)", new.incidents.first?.title ?? new.summary, id: "svc-\(s.id)") }
            return
        }
        guard let old else { return }
        if old.state != new.state {
            if new.state == .operational { app.notify("\(s.name) is back to operational", new.summary, id: "svc-\(s.id)") }
            else if new.state.isProblem { app.notify("\(s.name): \(new.state.rawValue)", new.incidents.first?.title ?? new.summary, id: "svc-\(s.id)") }
        } else if new.state.isProblem, let f = fresh.first {
            app.notify("\(s.name): new incident", f.title, id: "svc-\(s.id)-\(f.id)")
        }
    }

    func probeLocal(_ s: ServiceDef) async {
        guard let u = URL(string: s.probeUrl) else { return }
        let ms = await HeaderProbe.run(u)
        var st = local[s.id] ?? LocalProbeState()
        st.samples.append((Date(), ms)); if st.samples.count > 2880 { st.samples.removeFirst(120) }
        if ms == nil {
            st.failures += 1
            if st.failures == 3 && !st.down { st.down = true; app.notify("\(s.name) unreachable from this computer", "3 consecutive probes failed.", id: "local-\(s.id)") }
        } else {
            if st.down { app.notify("\(s.name) reachable again", "From this computer (\(Fmt.ms(ms)) ms).", id: "local-\(s.id)") }
            st.failures = 0; st.down = false
        }
        local[s.id] = st
    }

    // MARK: Nodes
    func nodeTargetPresent(_ n: NodeState, _ s: ServiceDef) -> Bool { n.config?.targets.contains { $0.id == s.targetId } ?? false }

    var pendingChanges: Bool {
        for n in app.nodes {
            let want = Set(prefs.nodes.contains(n.id) ? prefs.selected : [])
            if want != Set(prefs.applied[n.id] ?? []) { return true }
        }
        return false
    }

    /// Writes `svc-*` HTTP targets to the chosen nodes (and removes them from nodes no longer chosen). Other targets are untouched.
    func apply() async {
        applying = true; applyResults = [:]
        for n in app.nodes {
            let want = prefs.nodes.contains(n.id) ? selectedServices : []
            let hadApplied = Set(prefs.applied[n.id] ?? [])
            guard let cur = try? await n.client.config() else { applyResults[n.id] = "Unreachable — not changed"; continue }
            let cfg = ServiceTargets.merged(cur, wanted: want)
            if cfg == cur { prefs.applied[n.id] = want.map(\.id); if !want.isEmpty || !hadApplied.isEmpty { applyResults[n.id] = "Already up to date" }; continue }
            do {
                n.config = try await n.client.putConfig(cfg)
                prefs.applied[n.id] = want.map(\.id)
                applyResults[n.id] = want.isEmpty ? "Services removed" : "\(want.count) service\(want.count == 1 ? "" : "s") now monitored"
            } catch { applyResults[n.id] = "Failed: \(error.localizedDescription)" }
        }
        prefs.appliedDesktop = prefs.desktop
        applying = false
    }
}
