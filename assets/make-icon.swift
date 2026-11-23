import AppKit
import CoreGraphics

// Renders the InternetMonitor icon: dark industrial tile, amber latency trace with a live status dot.
let S: CGFloat = 1024
let cs = CGColorSpace(name: CGColorSpace.sRGB)!
let ctx = CGContext(data: nil, width: Int(S), height: Int(S), bitsPerComponent: 8, bytesPerRow: 0, space: cs, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
func col(_ h: UInt32, _ a: CGFloat = 1) -> CGColor { CGColor(srgbRed: CGFloat(h >> 16 & 255) / 255, green: CGFloat(h >> 8 & 255) / 255, blue: CGFloat(h & 255) / 255, alpha: a) }

// macOS icon grid: 824pt tile centred in 1024 with ~185 corner radius
let tile = CGRect(x: 100, y: 100, width: 824, height: 824)
let path = CGPath(roundedRect: tile, cornerWidth: 185, cornerHeight: 185, transform: nil)
ctx.saveGState()
ctx.setShadow(offset: CGSize(width: 0, height: -14), blur: 28, color: col(0x000000, 0.45))
ctx.addPath(path); ctx.setFillColor(col(0x14181D)); ctx.fillPath()
ctx.restoreGState()
ctx.saveGState(); ctx.addPath(path); ctx.clip()
let g = CGGradient(colorsSpace: cs, colors: [col(0x222831), col(0x0B0D10)] as CFArray, locations: [0, 1])!
ctx.drawLinearGradient(g, start: CGPoint(x: 512, y: 924), end: CGPoint(x: 512, y: 100), options: [])
// grid
ctx.setStrokeColor(col(0xFFFFFF, 0.055)); ctx.setLineWidth(2)
for i in 1..<10 { let p = 100 + CGFloat(i) * 82.4
    ctx.move(to: CGPoint(x: p, y: 100)); ctx.addLine(to: CGPoint(x: p, y: 924))
    ctx.move(to: CGPoint(x: 100, y: p)); ctx.addLine(to: CGPoint(x: 924, y: p)) }
ctx.strokePath()
// faint area under the trace
let pts: [CGPoint] = [(150,470),(285,470),(340,470),(395,690),(470,300),(545,620),(610,470),(700,470),(760,545),(800,470)].map { CGPoint(x: $0.0 + 40, y: $0.1) }
let area = CGMutablePath(); area.move(to: CGPoint(x: pts[0].x, y: 190)); pts.forEach { area.addLine(to: $0) }; area.addLine(to: CGPoint(x: pts.last!.x, y: 190)); area.closeSubpath()
_ = area
// glow + trace
let trace = CGMutablePath(); trace.addLines(between: pts)
for (w, a) in [(46.0, 0.10), (26.0, 0.18)] as [(CGFloat, CGFloat)] {
    ctx.addPath(trace); ctx.setStrokeColor(col(0xFFB000, a)); ctx.setLineWidth(w); ctx.setLineJoin(.round); ctx.setLineCap(.round); ctx.strokePath() }
ctx.addPath(trace); ctx.setStrokeColor(col(0xFFB000)); ctx.setLineWidth(22); ctx.setLineJoin(.round); ctx.setLineCap(.round); ctx.strokePath()
// live status dot (green) at the end of the trace
let end = pts.last!
ctx.setFillColor(col(0x39D98A, 0.25)); ctx.fillEllipse(in: CGRect(x: end.x - 52, y: end.y - 52, width: 104, height: 104))
ctx.setFillColor(col(0x39D98A)); ctx.fillEllipse(in: CGRect(x: end.x - 30, y: end.y - 30, width: 60, height: 60))
ctx.setFillColor(col(0x0B0D10)); ctx.fillEllipse(in: CGRect(x: end.x - 11, y: end.y - 11, width: 22, height: 22))
ctx.restoreGState()
// inner highlight border
ctx.addPath(path); ctx.setStrokeColor(col(0xFFFFFF, 0.10)); ctx.setLineWidth(3); ctx.strokePath()

let img = ctx.makeImage()!
let rep = NSBitmapImageRep(cgImage: img)
try! rep.representation(using: .png, properties: [:])!.write(to: URL(fileURLWithPath: "icon-1024.png"))
