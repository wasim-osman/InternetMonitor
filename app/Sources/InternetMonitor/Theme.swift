import SwiftUI
import IMCore

extension Color {
    init(hex: UInt32, opacity: Double = 1) {
        self.init(.sRGB, red: Double(hex >> 16 & 255) / 255, green: Double(hex >> 8 & 255) / 255, blue: Double(hex & 255) / 255, opacity: opacity)
    }
}

enum Theme {
    static let bg = Color(hex: 0x0D0F12)
    static let panel = Color(hex: 0x14181D)
    static let panel2 = Color(hex: 0x1A1F26)
    static let line = Color(hex: 0x2A313B)
    static let text = Color(hex: 0xE8EAED)
    static let dim = Color(hex: 0x8A94A0)
    static let faint = Color(hex: 0x56606B)
    static let amber = Color(hex: 0xFFB000)
    static let green = Color(hex: 0x39D98A)
    static let red = Color(hex: 0xFF4D4F)
    static let cyan = Color(hex: 0x2BD9FE)
    static let series: [Color] = [0xFFB000, 0x2BD9FE, 0xA3E635, 0xF472B6, 0xA78BFA, 0xFB923C, 0x2DD4BF, 0xF87171].map { Color(hex: $0) }

    static func seriesColor(_ key: String) -> Color { series[Palette.slot(for: key, count: series.count)] }
    static func color(_ s: LinkState) -> Color {
        switch s { case .up: green; case .degraded: amber; case .down: red; case .unknown: faint }
    }
    /// Latency heat colour: green → amber → red.
    static func latency(_ ms: Double?) -> Color {
        guard let ms else { return faint }
        if ms < 40 { return green }
        if ms < 100 { return Color(hex: 0xA3E635) }
        if ms < 200 { return amber }
        return red
    }
    static let mono = Font.system(.body, design: .monospaced)
    static func mono(_ size: CGFloat, _ weight: Font.Weight = .regular) -> Font { .system(size: size, weight: weight, design: .monospaced) }
}

struct Panel<Content: View>: View {
    var title: String?
    var trailing: AnyView?
    @ViewBuilder var content: Content
    init(_ title: String? = nil, trailing: AnyView? = nil, @ViewBuilder content: () -> Content) {
        self.title = title; self.trailing = trailing; self.content = content()
    }
    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            if let title {
                HStack {
                    Text(title.uppercased()).font(Theme.mono(10, .semibold)).tracking(1.2).foregroundStyle(Theme.dim)
                    Spacer(); trailing
                }
                .padding(.horizontal, 12).padding(.vertical, 8)
                Rectangle().fill(Theme.line).frame(height: 1)
            }
            content.padding(12)
        }
        .background(Theme.panel)
        .overlay(RoundedRectangle(cornerRadius: 3).stroke(Theme.line, lineWidth: 1))
        .clipShape(RoundedRectangle(cornerRadius: 3))
    }
}

struct StatusDot: View {
    var state: LinkState
    var size: CGFloat = 8
    var body: some View {
        Circle().fill(Theme.color(state)).frame(width: size, height: size)
            .shadow(color: Theme.color(state).opacity(state == .unknown ? 0 : 0.7), radius: 3)
    }
}

struct StatBlock: View {
    var label: String, value: String, unit: String = "", color: Color = Theme.text
    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(label.uppercased()).font(Theme.mono(9, .semibold)).tracking(1).foregroundStyle(Theme.faint)
            HStack(alignment: .firstTextBaseline, spacing: 2) {
                Text(value).font(Theme.mono(15, .semibold)).foregroundStyle(color)
                if !unit.isEmpty { Text(unit).font(Theme.mono(10)).foregroundStyle(Theme.dim) }
            }
        }
    }
}

struct IndustrialButtonStyle: ButtonStyle {
    var prominent = false
    var danger = false
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(Theme.mono(11, .semibold)).tracking(0.8)
            .padding(.horizontal, 12).padding(.vertical, 6)
            .foregroundStyle(prominent ? Color.black : (danger ? Theme.red : Theme.text))
            .background(prominent ? Theme.amber : Theme.panel2)
            .overlay(RoundedRectangle(cornerRadius: 3).stroke(prominent ? Theme.amber : (danger ? Theme.red.opacity(0.6) : Theme.line)))
            .clipShape(RoundedRectangle(cornerRadius: 3))
            .opacity(configuration.isPressed ? 0.7 : 1)
    }
}
extension ButtonStyle where Self == IndustrialButtonStyle {
    static var industrial: IndustrialButtonStyle { .init() }
    static var industrialPrimary: IndustrialButtonStyle { .init(prominent: true) }
    static var industrialDanger: IndustrialButtonStyle { .init(danger: true) }
}

struct FieldStyle: TextFieldStyle {
    func _body(configuration: TextField<_Label>) -> some View {
        configuration.textFieldStyle(.plain).font(Theme.mono(12))
            .padding(.horizontal, 8).padding(.vertical, 6)
            .background(Theme.bg)
            .overlay(RoundedRectangle(cornerRadius: 3).stroke(Theme.line))
    }
}
extension TextFieldStyle where Self == FieldStyle { static var industrial: FieldStyle { .init() } }
