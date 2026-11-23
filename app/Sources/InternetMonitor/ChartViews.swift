import SwiftUI
import Charts
import IMCore

/// One stacked chart row: a single target measured from one node.
struct TargetChartRow: View {
    @EnvironmentObject var model: AppModel
    let target: Target
    let live: LiveTarget?
    let series: Series?
    let summary: [SummaryWindow]?
    let domain: ClosedRange<Date>
    var showAxis: Bool

    @State private var dragStart: Date?
    @State private var dragEnd: Date?

    var color: Color { Theme.seriesColor(target.endpointKey) }
    var pts: [SeriesPoint] { series?.points ?? [] }
    var yMax: Double {
        let m = pts.compactMap { $0.p95 ?? $0.avg }.max() ?? 20
        let top = pts.compactMap(\.max).max() ?? m
        return Swift.max(10, Swift.min(top, m * 2.5)) * 1.15
    }
    var step: TimeInterval { TimeInterval(series?.stepSec ?? 2) }

    var body: some View {
        HStack(spacing: 0) {
            info.frame(width: 190).padding(10)
            Rectangle().fill(Theme.line).frame(width: 1)
            chart.padding(.leading, 6).padding(.trailing, 10).padding(.vertical, 6)
        }
        .frame(height: showAxis ? 128 : 104)
        .background(Theme.panel)
    }

    var info: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 6) {
                StatusDot(state: live?.state ?? .unknown)
                Text(target.name).font(.system(size: 12, weight: .semibold)).lineLimit(1)
            }
            HStack(spacing: 4) {
                Text(target.kind.label).font(Theme.mono(8, .bold)).padding(.horizontal, 3).padding(.vertical, 1)
                    .overlay(RoundedRectangle(cornerRadius: 2).stroke(color.opacity(0.7))).foregroundStyle(color)
                Text(target.endpointLabel).font(Theme.mono(9)).foregroundStyle(Theme.faint).lineLimit(1)
            }
            HStack(alignment: .firstTextBaseline, spacing: 2) {
                Text(live?.state == .down ? "DOWN" : Fmt.ms(live?.lastRttMs)).font(Theme.mono(20, .bold))
                    .foregroundStyle(live?.state == .down ? Theme.red : Theme.latency(live?.lastRttMs))
                if live?.state != .down { Text("ms").font(Theme.mono(10)).foregroundStyle(Theme.dim) }
            }
            HStack(spacing: 10) {
                mini("AVG", Fmt.ms(live?.avgMs)); mini("P95", Fmt.ms(live?.p95Ms))
                mini("JIT", Fmt.ms(live?.jitterMs)); mini("LOSS", Fmt.pct(live?.lossPct), warn: (live?.lossPct ?? 0) > 0)
            }
            if let p = live?.pathHint { Text("TAILSCALE · \(p.uppercased())").font(Theme.mono(8, .bold)).foregroundStyle(p == "relay" ? Theme.amber : Theme.cyan) }
            else if let u = summary?.first(where: { $0.label == "24h" })?.uptimePct { Text("24H UPTIME \(Fmt.uptime(u))").font(Theme.mono(8, .bold)).foregroundStyle(Theme.faint) }
        }.frame(maxWidth: .infinity, alignment: .leading)
    }
    func mini(_ l: String, _ v: String, warn: Bool = false) -> some View {
        VStack(alignment: .leading, spacing: 0) {
            Text(l).font(Theme.mono(7, .bold)).foregroundStyle(Theme.faint)
            Text(v).font(Theme.mono(10, .semibold)).foregroundStyle(warn ? Theme.red : Theme.text)
        }
    }

    var chart: some View {
        Chart {
            ForEach(pts, id: \.t) { p in
                if let lo = p.min, let hi = p.max {
                    AreaMark(x: .value("t", p.date), yStart: .value("min", lo), yEnd: .value("max", Swift.min(hi, yMax * 1.5)), series: .value("s", "band"))
                        .foregroundStyle(color.opacity(0.14)).interpolationMethod(.monotone)
                }
                if let a = p.avg {
                    LineMark(x: .value("t", p.date), y: .value("avg", a), series: .value("s", "avg"))
                        .foregroundStyle(color).lineStyle(StrokeStyle(lineWidth: 1.6)).interpolationMethod(.monotone)
                }
                if (p.loss ?? 0) >= 99.9 {
                    RectangleMark(xStart: .value("a", p.date), xEnd: .value("b", p.date.addingTimeInterval(Swift.max(step, 1))), yStart: .value("y0", 0), yEnd: .value("y1", yMax))
                        .foregroundStyle(Theme.red.opacity(0.28))
                } else if let l = p.loss, l > 0 {
                    RectangleMark(xStart: .value("a", p.date), xEnd: .value("b", p.date.addingTimeInterval(Swift.max(step, 1))), yStart: .value("y0", 0), yEnd: .value("y1", yMax * 0.14))
                        .foregroundStyle(Theme.red.opacity(Swift.min(0.9, 0.3 + l / 100)))
                }
            }
            if let thr = thresholdLine { RuleMark(y: .value("thr", thr)).foregroundStyle(Theme.amber.opacity(0.35)).lineStyle(StrokeStyle(lineWidth: 1, dash: [3, 3])) }
            if let h = model.hoverDate, domain.contains(h) {
                RuleMark(x: .value("hover", h)).foregroundStyle(Theme.text.opacity(0.5)).lineStyle(StrokeStyle(lineWidth: 1))
                    .annotation(position: .top, alignment: .leading, spacing: 0, overflowResolution: .init(x: .fit(to: .chart), y: .disabled)) { tooltip(h) }
            }
            if let a = dragStart, let b = dragEnd {
                RectangleMark(xStart: .value("a", Swift.min(a, b)), xEnd: .value("b", Swift.max(a, b))).foregroundStyle(Theme.amber.opacity(0.18))
            }
        }
        .chartXScale(domain: domain)
        .chartYScale(domain: 0...yMax)
        .chartYAxis {
            AxisMarks(position: .trailing, values: .automatic(desiredCount: 3)) { v in
                AxisGridLine(stroke: StrokeStyle(lineWidth: 0.5)).foregroundStyle(Theme.line)
                AxisValueLabel().font(Theme.mono(9)).foregroundStyle(Theme.faint)
            }
        }
        .chartXAxis {
            AxisMarks(values: .automatic(desiredCount: 6)) { _ in
                AxisGridLine(stroke: StrokeStyle(lineWidth: 0.5)).foregroundStyle(Theme.line.opacity(0.6))
                if showAxis { AxisValueLabel(format: axisFormat).font(Theme.mono(9)).foregroundStyle(Theme.faint) }
            }
        }
        .chartOverlay { proxy in
            GeometryReader { geo in
                Rectangle().fill(.clear).contentShape(Rectangle())
                    .onContinuousHover { phase in
                        switch phase {
                        case .active(let loc): if let d: Date = proxy.value(atX: loc.x - geo[proxy.plotFrame!].origin.x) { model.hoverDate = d }
                        case .ended: model.hoverDate = nil
                        }
                    }
                    .gesture(DragGesture(minimumDistance: 6)
                        .onChanged { g in
                            let o = geo[proxy.plotFrame!].origin.x
                            dragStart = proxy.value(atX: g.startLocation.x - o); dragEnd = proxy.value(atX: g.location.x - o)
                        }
                        .onEnded { g in
                            defer { dragStart = nil; dragEnd = nil }
                            let o = geo[proxy.plotFrame!].origin.x
                            guard let a: Date = proxy.value(atX: g.startLocation.x - o), let b: Date = proxy.value(atX: g.location.x - o) else { return }
                            let lo = Swift.min(a, b), hi = Swift.max(a, b)
                            if hi.timeIntervalSince(lo) >= 10 { model.zoom = lo...hi }
                        })
                    .onTapGesture(count: 2) { model.zoom = nil }
            }
        }
        .overlay {
            if pts.isEmpty { Text(series == nil ? "LOADING…" : "NO DATA IN RANGE").font(Theme.mono(10, .semibold)).tracking(1).foregroundStyle(Theme.faint) }
        }
    }

    var thresholdLine: Double? { nil }
    var axisFormat: Date.FormatStyle {
        let span = domain.upperBound.timeIntervalSince(domain.lowerBound)
        return span > 2 * 86400 ? .dateTime.month(.abbreviated).day() : (span > 3600 * 6 ? .dateTime.hour().minute() : .dateTime.hour().minute().second())
    }

    @ViewBuilder func tooltip(_ d: Date) -> some View {
        if let p = nearest(d) {
            VStack(alignment: .leading, spacing: 1) {
                Text(p.date.formatted(date: .omitted, time: .standard)).foregroundStyle(Theme.dim)
                if let a = p.avg { Text("avg \(Fmt.ms(a))  max \(Fmt.ms(p.max)) ms") } else { Text("no response").foregroundStyle(Theme.red) }
                if let l = p.loss, l > 0 { Text("loss \(Fmt.pct(l))").foregroundStyle(Theme.red) }
            }
            .font(Theme.mono(9)).padding(5).background(Theme.bg.opacity(0.95)).overlay(RoundedRectangle(cornerRadius: 2).stroke(Theme.line))
        }
    }
    func nearest(_ d: Date) -> SeriesPoint? {
        pts.min { abs($0.date.timeIntervalSince(d)) < abs($1.date.timeIntervalSince(d)) }.flatMap { abs($0.date.timeIntervalSince(d)) <= Swift.max(step * 2, 5) ? $0 : nil }
    }
}
