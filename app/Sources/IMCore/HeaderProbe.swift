import Foundation

/// Desktop-side reachability probe: time until response headers arrive. Any HTTP response counts as reachable.
public final class HeaderProbe: NSObject, URLSessionDataDelegate, @unchecked Sendable {
    private var cont: CheckedContinuation<Double?, Never>?
    private let start = Date()
    private var finished = false
    private let lock = NSLock()

    public static func run(_ url: URL, timeout: TimeInterval = 8) async -> Double? {
        let p = HeaderProbe()
        let cfg = URLSessionConfiguration.ephemeral
        cfg.timeoutIntervalForRequest = timeout; cfg.timeoutIntervalForResource = timeout + 2
        cfg.requestCachePolicy = .reloadIgnoringLocalCacheData
        let s = URLSession(configuration: cfg, delegate: p, delegateQueue: nil)
        var req = URLRequest(url: url); req.setValue("InternetMonitor", forHTTPHeaderField: "User-Agent")
        defer { s.finishTasksAndInvalidate() }
        return await withCheckedContinuation { c in p.cont = c; s.dataTask(with: req).resume() }
    }
    private func finish(_ v: Double?) {
        lock.lock(); defer { lock.unlock() }
        guard !finished else { return }; finished = true; cont?.resume(returning: v); cont = nil
    }
    public func urlSession(_ s: URLSession, dataTask: URLSessionDataTask, didReceive r: URLResponse, completionHandler: @escaping (URLSession.ResponseDisposition) -> Void) {
        finish(Date().timeIntervalSince(start) * 1000); completionHandler(.cancel)
    }
    public func urlSession(_ s: URLSession, task: URLSessionTask, didCompleteWithError e: Error?) { finish(nil) }
}

