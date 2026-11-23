import Foundation

public enum Fmt {
    public static func ms(_ v: Double?) -> String {
        guard let v else { return "—" }
        if v >= 1000 { return String(format: "%.1fs", v / 1000) }
        if v >= 100 { return String(format: "%.0f", v) }
        if v >= 10 { return String(format: "%.1f", v) }
        return String(format: "%.2f", v)
    }
    public static func pct(_ v: Double?) -> String {
        guard let v else { return "—" }
        return v == 0 || v >= 10 ? String(format: "%.0f%%", v) : String(format: "%.1f%%", v)
    }
    public static func uptime(_ v: Double?) -> String {
        guard let v else { return "—" }
        return v >= 99.995 ? "100%" : String(format: "%.2f%%", v)
    }
    public static func duration(_ s: TimeInterval) -> String {
        let t = Int(s)
        if t < 60 { return "\(t)s" }
        if t < 3600 { return "\(t / 60)m \(t % 60)s" }
        if t < 86400 { return "\(t / 3600)h \((t % 3600) / 60)m" }
        return "\(t / 86400)d \((t % 86400) / 3600)h"
    }
}

public enum Stats {
    /// Percentile with linear interpolation. `p` in 0...100.
    public static func percentile(_ values: [Double], _ p: Double) -> Double? {
        guard !values.isEmpty else { return nil }
        let s = values.sorted()
        if s.count == 1 { return s[0] }
        let rank = (p / 100) * Double(s.count - 1)
        let lo = Int(rank.rounded(.down)), hi = Int(rank.rounded(.up))
        return s[lo] + (s[hi] - s[lo]) * (rank - Double(lo))
    }
    public static func jitter(_ rtts: [Double]) -> Double? {
        guard rtts.count > 1 else { return nil }
        var sum = 0.0
        for i in 1..<rtts.count { sum += abs(rtts[i] - rtts[i - 1]) }
        return sum / Double(rtts.count - 1)
    }
    public static func ewma(_ values: [Double], alpha: Double = 0.3) -> [Double] {
        var out: [Double] = []; var acc: Double?
        for v in values { acc = acc.map { $0 + alpha * (v - $0) } ?? v; out.append(acc!) }
        return out
    }
}

public enum Palette {
    /// Deterministic colour slot for a string key (stable across launches).
    public static func slot(for key: String, count: Int) -> Int {
        var h: UInt64 = 1469598103934665603
        for b in key.utf8 { h = (h ^ UInt64(b)) &* 1099511628211 }
        return Int(h % UInt64(max(count, 1)))
    }
}

public enum CSV {
    public static func escape(_ s: String) -> String {
        s.contains(where: { $0 == "," || $0 == "\"" || $0 == "\n" }) ? "\"" + s.replacingOccurrences(of: "\"", with: "\"\"") + "\"" : s
    }
    /// rows: (node, target name, series)
    public static func series(_ rows: [(node: String, target: String, series: Series)]) -> String {
        var out = "node,target,time_iso,time_unix,samples,loss_pct,min_ms,avg_ms,max_ms,p95_ms\n"
        let iso = ISO8601DateFormatter()
        for r in rows {
            for p in r.series.points {
                func f(_ v: Double?) -> String { v.map { String(format: "%.3f", $0) } ?? "" }
                out += [escape(r.node), escape(r.target), iso.string(from: p.date), String(p.t), p.n.map(String.init) ?? "",
                        f(p.loss), f(p.min), f(p.avg), f(p.max), f(p.p95)].joined(separator: ",") + "\n"
            }
        }
        return out
    }
}
