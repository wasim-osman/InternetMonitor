import SwiftUI
import IMCore

/// Checks GitHub Releases for a newer build, downloads + verifies it (SHA-256 from the release's SHA256SUMS),
/// and swaps the app bundle in place when the app quits (or immediately on "Restart now").
@MainActor
final class Updater: ObservableObject {
    enum State: Equatable {
        case idle, checking, upToDate, available(String), downloading(String), ready(String), failed(String)
    }
    @Published var state: State = .idle
    @Published var lastChecked: Date?
    @Published var notes: String?
    @AppStorage("autoUpdates") var autoUpdates = true { didSet { objectWillChange.send(); if autoUpdates { Task { await check() } } } }

    static var currentVersion: String { Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "0.0.0" }
    private var staged: URL?          // extracted .app awaiting swap
    private var timer: Task<Void, Never>?
    private var canSelfUpdate: Bool { Bundle.main.bundlePath.hasSuffix(".app") }

    func start() {
        guard timer == nil else { return }
        timer = Task { [weak self] in
            try? await Task.sleep(for: .seconds(8))
            while !Task.isCancelled {
                if let self, self.autoUpdates { await self.check() }
                try? await Task.sleep(for: .seconds(6 * 3600))
            }
        }
    }

    func check() async {
        if case .downloading = state { return }
        state = .checking
        do {
            let rel = try await UpdateCheck.latest()
            lastChecked = Date(); notes = rel.body
            guard UpdateCheck.isNewer(rel, than: Self.currentVersion) else { state = .upToDate; return }
            let v = rel.version?.description ?? rel.tag_name
            state = .available(v)
            if autoUpdates { await download(rel) }
        } catch { state = .failed(error.localizedDescription) }
    }

    func installNow() async {
        if case .available = state, let rel = try? await UpdateCheck.latest() { await download(rel) }
        if case .ready = state { applyAndRelaunch() }
    }

    private func download(_ rel: Release) async {
        let v = rel.version?.description ?? rel.tag_name
        guard canSelfUpdate else { state = .failed("Updates install into the .app; this is a development run."); return }
        guard let asset = rel.assets.first(where: { $0.name == UpdateCheck.macAssetName }),
              let sums = rel.assets.first(where: { $0.name == UpdateCheck.sumsAssetName }) else {
            state = .failed("Release \(rel.tag_name) has no macOS build or checksums yet."); return
        }
        state = .downloading(v)
        do {
            let work = FileManager.default.temporaryDirectory.appendingPathComponent("im-update-\(UUID().uuidString)")
            try FileManager.default.createDirectory(at: work, withIntermediateDirectories: true)
            let (sumsData, _) = try await URLSession.shared.data(from: URL(string: sums.browser_download_url)!)
            guard let expected = UpdateCheck.parseSums(String(decoding: sumsData, as: UTF8.self))[asset.name] else { throw AgentError(status: 0, message: "Checksum for \(asset.name) missing") }
            let (tmp, _) = try await URLSession.shared.download(from: URL(string: asset.browser_download_url)!)
            let zip = work.appendingPathComponent(asset.name)
            try FileManager.default.moveItem(at: tmp, to: zip)
            guard try UpdateCheck.sha256(of: zip) == expected else { throw AgentError(status: 0, message: "Checksum mismatch — download discarded") }
            let out = work.appendingPathComponent("out")
            let r = await Shell.run("/usr/bin/ditto", ["-x", "-k", zip.path, out.path], timeout: 120)
            guard r.ok else { throw AgentError(status: 0, message: "Could not unpack update") }
            let app = out.appendingPathComponent("InternetMonitor.app")
            let info = NSDictionary(contentsOf: app.appendingPathComponent("Contents/Info.plist"))
            guard info?["CFBundleIdentifier"] as? String == Bundle.main.bundleIdentifier else { throw AgentError(status: 0, message: "Update bundle identity mismatch") }
            let cs = await Shell.run("/usr/bin/codesign", ["--verify", "--deep", app.path], timeout: 60)
            guard cs.ok else { throw AgentError(status: 0, message: "Update failed signature check") }
            staged = app; state = .ready(v)
        } catch { state = .failed(error.localizedDescription) }
    }

    /// Spawns a detached helper that waits for this process to exit, swaps the bundle, and reopens it.
    func spawnSwap(relaunch: Bool) {
        guard let staged else { return }
        let dest = Bundle.main.bundlePath
        let script = """
        while kill -0 \(getpid()) 2>/dev/null; do sleep 0.3; done
        rm -rf "\(dest).old"
        mv "\(dest)" "\(dest).old" && mv "\(staged.path)" "\(dest)" && rm -rf "\(dest).old" || { [ -d "\(dest).old" ] && [ ! -d "\(dest)" ] && mv "\(dest).old" "\(dest)"; }
        \(relaunch ? "open \"\(dest)\"" : "true")
        """
        let p = Process(); p.executableURL = URL(fileURLWithPath: "/bin/sh"); p.arguments = ["-c", script]
        p.standardOutput = FileHandle.nullDevice; p.standardError = FileHandle.nullDevice
        try? p.run()
        self.staged = nil
    }

    func applyAndRelaunch() { spawnSwap(relaunch: true); NSApp.terminate(nil) }
    /// Called from applicationWillTerminate: a staged update is applied silently on quit.
    func applyOnQuit() { if staged != nil { spawnSwap(relaunch: false) } }

    var statusText: String {
        switch state {
        case .idle: "Not checked yet"
        case .checking: "Checking GitHub…"
        case .upToDate: "Up to date (v\(Self.currentVersion))" + (lastChecked.map { " · checked \($0.formatted(date: .omitted, time: .shortened))" } ?? "")
        case .available(let v): "Version \(v) is available"
        case .downloading(let v): "Downloading \(v)…"
        case .ready(let v): "Version \(v) downloaded — installs when you quit, or restart now"
        case .failed(let e): "Update check failed: \(e)"
        }
    }
}
