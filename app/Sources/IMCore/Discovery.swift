import Foundation
import Darwin

public struct LocalInterface: Sendable, Equatable {
    public var name: String
    public var ip: String
    public var netmask: String
    public var prefix: Int
    public var isTailscale: Bool
}

public struct Candidate: Identifiable, Sendable, Equatable {
    public var ip: String
    public var name: String
    public var network: String          // "LAN en0", "Tailscale", ...
    public var isTailscale: Bool
    public var pingMs: Double?
    public var agent: Identity?
    public var extraIPs: [String] = []
    public var os: String?
    public var id: String { ip }
    public init(ip: String, name: String, network: String, isTailscale: Bool, pingMs: Double? = nil, agent: Identity? = nil, extraIPs: [String] = [], os: String? = nil) {
        self.ip = ip; self.name = name; self.network = network; self.isTailscale = isTailscale; self.pingMs = pingMs; self.agent = agent; self.extraIPs = extraIPs; self.os = os
    }
}

public enum Discovery {
    public static let agentPort = 7878

    // MARK: Local interfaces
    public static func localInterfaces() -> [LocalInterface] {
        var out: [LocalInterface] = []
        var head: UnsafeMutablePointer<ifaddrs>?
        guard getifaddrs(&head) == 0, let first = head else { return [] }
        defer { freeifaddrs(head) }
        for p in sequence(first: first, next: { $0.pointee.ifa_next }) {
            let ifa = p.pointee
            guard let sa = ifa.ifa_addr, sa.pointee.sa_family == UInt8(AF_INET),
                  (ifa.ifa_flags & UInt32(IFF_UP)) != 0, (ifa.ifa_flags & UInt32(IFF_LOOPBACK)) == 0,
                  let mk = ifa.ifa_netmask else { continue }
            let ip = ipString(sa), mask = ipString(mk)
            let name = String(cString: ifa.ifa_name)
            if ip.hasPrefix("169.254.") { continue }
            let prefix = maskToPrefix(mask)
            let ts = isTailscaleIP(ip)
            out.append(LocalInterface(name: name, ip: ip, netmask: mask, prefix: prefix, isTailscale: ts))
        }
        return out
    }

    static func ipString(_ sa: UnsafeMutablePointer<sockaddr>) -> String {
        var buf = [CChar](repeating: 0, count: Int(NI_MAXHOST))
        getnameinfo(sa, socklen_t(sa.pointee.sa_len), &buf, socklen_t(buf.count), nil, 0, NI_NUMERICHOST)
        return String(cString: buf)
    }
    public static func maskToPrefix(_ mask: String) -> Int {
        guard let v = ipv4ToUInt(mask) else { return 24 }
        return v.nonzeroBitCount
    }
    public static func ipv4ToUInt(_ s: String) -> UInt32? {
        let parts = s.split(separator: ".").compactMap { UInt32($0) }
        guard parts.count == 4, parts.allSatisfy({ $0 < 256 }) else { return nil }
        return parts[0] << 24 | parts[1] << 16 | parts[2] << 8 | parts[3]
    }
    public static func uintToIPv4(_ v: UInt32) -> String { "\(v >> 24 & 255).\(v >> 16 & 255).\(v >> 8 & 255).\(v & 255)" }
    public static func isTailscaleIP(_ ip: String) -> Bool {
        guard let v = ipv4ToUInt(ip) else { return false }
        return v & 0xFFC0_0000 == 0x6440_0000   // 100.64.0.0/10
    }

    /// Host addresses of the subnet that should be swept (excluding network/broadcast and `ip` itself).
    /// Subnets larger than /22 are clamped to the /24 around our address to keep a sweep quick and polite.
    public static func sweepAddresses(ip: String, prefix: Int) -> [String] {
        guard let me = ipv4ToUInt(ip) else { return [] }
        let p = prefix >= 22 ? prefix : 24
        guard p < 31 else { return [] }
        let mask: UInt32 = p == 0 ? 0 : ~UInt32(0) << UInt32(32 - p)
        let net = me & mask, bcast = net | ~mask
        guard bcast > net + 1 else { return [] }
        return ((net + 1)..<bcast).filter { $0 != me }.map(uintToIPv4)
    }

    // MARK: ICMP sweep (unprivileged datagram ICMP socket; works without root on macOS)
    /// Pings all addresses concurrently over a single socket. Returns RTT in ms for each responder.
    public static func icmpSweep(_ ips: [String], timeout: TimeInterval = 1.2) async -> [String: Double] {
        await Task.detached(priority: .userInitiated) { icmpSweepBlocking(ips, timeout: timeout) }.value
    }

    static func checksum(_ data: [UInt8]) -> UInt16 {
        var sum: UInt32 = 0
        var i = 0
        while i + 1 < data.count { sum += UInt32(data[i]) << 8 | UInt32(data[i + 1]); i += 2 }
        if i < data.count { sum += UInt32(data[i]) << 8 }
        while sum >> 16 != 0 { sum = (sum & 0xFFFF) + (sum >> 16) }
        return ~UInt16(sum & 0xFFFF)
    }

    static func icmpSweepBlocking(_ ips: [String], timeout: TimeInterval) -> [String: Double] {
        let fd = socket(AF_INET, SOCK_DGRAM, IPPROTO_ICMP)
        guard fd >= 0 else { return [:] }
        defer { close(fd) }
        _ = fcntl(fd, F_SETFL, fcntl(fd, F_GETFL) | O_NONBLOCK)
        var big: Int32 = 1 << 20
        setsockopt(fd, SOL_SOCKET, SO_RCVBUF, &big, socklen_t(MemoryLayout<Int32>.size))

        var sent: [String: Date] = [:]
        var results: [String: Double] = [:]
        let ident = UInt16(truncatingIfNeeded: getpid())

        func sendOne(_ ip: String, seq: UInt16) {
            var pkt: [UInt8] = [8, 0, 0, 0, UInt8(ident >> 8), UInt8(ident & 255), UInt8(seq >> 8), UInt8(seq & 255)]
            pkt += Array("InternetMon".utf8)
            let ck = checksum(pkt); pkt[2] = UInt8(ck >> 8); pkt[3] = UInt8(ck & 255)
            var addr = sockaddr_in()
            addr.sin_len = UInt8(MemoryLayout<sockaddr_in>.size); addr.sin_family = sa_family_t(AF_INET)
            inet_pton(AF_INET, ip, &addr.sin_addr)
            _ = withUnsafePointer(to: &addr) {
                $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { sendto(fd, pkt, pkt.count, 0, $0, socklen_t(MemoryLayout<sockaddr_in>.size)) }
            }
            sent[ip] = Date()
        }

        func drain() {
            var buf = [UInt8](repeating: 0, count: 1500)
            while true {
                var from = sockaddr_in(); var len = socklen_t(MemoryLayout<sockaddr_in>.size)
                let n = withUnsafeMutablePointer(to: &from) {
                    $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { recvfrom(fd, &buf, buf.count, 0, $0, &len) }
                }
                if n <= 0 { return }
                var off = 0
                if buf[0] >> 4 == 4 { off = Int(buf[0] & 0x0F) * 4 }   // macOS includes the IP header
                guard n > off + 1, buf[off] == 0 else { continue }       // echo reply only
                var ipbuf = [CChar](repeating: 0, count: Int(INET_ADDRSTRLEN))
                var a = from.sin_addr
                inet_ntop(AF_INET, &a, &ipbuf, socklen_t(ipbuf.count))
                let ip = String(cString: ipbuf)
                if let t = sent[ip], results[ip] == nil { results[ip] = Date().timeIntervalSince(t) * 1000 }
            }
        }

        // Send in small bursts so the kernel buffer never overflows, draining between bursts.
        var seq: UInt16 = 1
        for chunk in stride(from: 0, to: ips.count, by: 32) {
            for ip in ips[chunk..<min(chunk + 32, ips.count)] { sendOne(ip, seq: seq); seq &+= 1 }
            drain(); usleep(8_000)
        }
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline && results.count < ips.count { drain(); usleep(10_000) }
        drain()
        return results
    }

    // MARK: Tailscale
    public struct TSPeerInfo: Sendable { public var name: String; public var ips: [String]; public var online: Bool; public var os: String? }
    public struct TSSnapshot: Sendable { public var selfIPs: [String]; public var peers: [TSPeerInfo] }

    public static func tailscaleBinary() -> String? {
        let candidates = ["/Applications/Tailscale.app/Contents/MacOS/Tailscale", "/opt/homebrew/bin/tailscale", "/usr/local/bin/tailscale",
                          NSHomeDirectory() + "/.homebrew/bin/tailscale"]
        return candidates.first { FileManager.default.isExecutableFile(atPath: $0) }
    }

    public static func tailscale() async -> TSSnapshot? {
        guard let bin = tailscaleBinary() else { return nil }
        guard let data = await Shell.run(bin, ["status", "--json"], timeout: 5).stdoutData,
              let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return nil }
        func v4(_ a: Any?) -> [String] { (a as? [String] ?? []).filter { $0.contains(".") } }
        let me = obj["Self"] as? [String: Any]
        var peers: [TSPeerInfo] = []
        for (_, v) in (obj["Peer"] as? [String: Any] ?? [:]) {
            guard let p = v as? [String: Any] else { continue }
            var name = (p["HostName"] as? String) ?? (p["DNSName"] as? String ?? "")
            if name.isEmpty { name = (p["DNSName"] as? String ?? "").split(separator: ".").first.map(String.init) ?? "peer" }
            peers.append(TSPeerInfo(name: name, ips: v4(p["TailscaleIPs"]), online: p["Online"] as? Bool ?? false, os: p["OS"] as? String))
        }
        return TSSnapshot(selfIPs: v4(me?["TailscaleIPs"]), peers: peers)
    }

    // MARK: Agent probe
    public static func probeAgent(_ ip: String, port: Int = agentPort, timeout: TimeInterval = 1.5) async -> Identity? {
        guard let id = try? await AgentClient(host: ip, port: port).identity(timeout: timeout), id.isAgent else { return nil }
        return id
    }

    public static func reverseDNS(_ ip: String) -> String? {
        var addr = sockaddr_in(); addr.sin_len = UInt8(MemoryLayout<sockaddr_in>.size); addr.sin_family = sa_family_t(AF_INET)
        inet_pton(AF_INET, ip, &addr.sin_addr)
        var host = [CChar](repeating: 0, count: Int(NI_MAXHOST))
        let r = withUnsafePointer(to: &addr) { $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
            getnameinfo($0, socklen_t(MemoryLayout<sockaddr_in>.size), &host, socklen_t(host.count), nil, 0, NI_NAMEREQD) } }
        guard r == 0 else { return nil }
        let s = String(cString: host)
        return s.hasSuffix(".local") || s.hasSuffix(".lan") ? String(s.split(separator: ".").first ?? "") : s
    }

    /// Full scan. `onUpdate` is called on arbitrary threads with the growing candidate list.
    /// This machine is never included: every local interface address and our own Tailscale addresses are excluded.
    public static func scan(progress: @escaping @Sendable (String) -> Void,
                            onUpdate: @escaping @Sendable ([Candidate]) -> Void) async -> [Candidate] {
        let ifaces = localInterfaces()
        let ts = await tailscale()
        var selfIPs = Set(ifaces.map(\.ip)); ts.map { selfIPs.formUnion($0.selfIPs) }

        var found: [String: Candidate] = [:]
        func publish() { onUpdate(found.values.sorted { sortKey($0.ip) < sortKey($1.ip) }) }

        // 1. Tailscale peers (online) — probe agent + ping
        if let ts {
            progress("Checking Tailscale peers…")
            let peers = ts.peers.filter { $0.online }
            await withTaskGroup(of: (TSPeerInfo, String, Identity?, Double?).self) { g in
                for p in peers { guard let ip = p.ips.first else { continue }
                    g.addTask {
                        async let id = probeAgent(ip)
                        async let ping = icmpSweep([ip], timeout: 1.5)
                        let r = await ping[ip]
                        return (p, ip, await id, r)
                    }
                }
                for await (p, ip, id, rtt) in g where !selfIPs.contains(ip) {
                    // Peers that answer neither ping nor agent probe are still reachable via Tailscale (ICMP is often filtered); keep them.
                    found[ip] = Candidate(ip: ip, name: p.name, network: "Tailscale", isTailscale: true, pingMs: rtt, agent: id, os: p.os)
                    publish()
                }
            }
        }

        // 2. LAN sweeps on every non-Tailscale interface
        var seenSubnets = Set<String>()
        for itf in ifaces where !itf.isTailscale {
            let key = "\(itf.ip)/\(itf.prefix)"
            let targets = sweepAddresses(ip: itf.ip, prefix: itf.prefix)
            let subnetKey = targets.first.map { "\($0)-\(targets.count)" } ?? key
            guard !targets.isEmpty, seenSubnets.insert(subnetKey).inserted else { continue }
            progress("Sweeping \(itf.name) (\(itf.ip)/\(itf.prefix))…")
            let alive = await icmpSweep(targets.filter { !selfIPs.contains($0) })
            // Also probe the agent port on everything (some hosts drop ICMP but run the agent).
            let agentHits = await withTaskGroup(of: (String, Identity?).self) { g -> [String: Identity] in
                var out: [String: Identity] = [:]
                var it = targets.filter { !selfIPs.contains($0) }.makeIterator(); var inflight = 0
                func next() { if let ip = it.next() { inflight += 1; g.addTask { (ip, await probeAgent(ip, timeout: 0.8)) } } }
                for _ in 0..<64 { next() }
                for await (ip, id) in g { inflight -= 1; if let id { out[ip] = id }; next() }
                return out
            }
            let net = "LAN \(itf.name)"
            for ip in Set(alive.keys).union(agentHits.keys) where !selfIPs.contains(ip) {
                if found[ip] != nil { continue }
                var name = agentHits[ip]?.displayName ?? ip
                if agentHits[ip] == nil, let rd = reverseDNS(ip) { name = rd }
                found[ip] = Candidate(ip: ip, name: name, network: net, isTailscale: false, pingMs: alive[ip], agent: agentHits[ip])
            }
            publish()
        }

        // 3. De-duplicate machines reachable through several networks (same agent nodeId): keep LAN as primary, list others
        var byNode: [String: [Candidate]] = [:]
        var result: [Candidate] = []
        for c in found.values { if let nid = c.agent?.nodeId { byNode[nid, default: []].append(c) } else { result.append(c) } }
        for (_, group) in byNode {
            let sorted = group.sorted { ($0.pingMs ?? 9e9) < ($1.pingMs ?? 9e9) }
            var primary = sorted[0]; primary.extraIPs = sorted.dropFirst().map(\.ip)
            if sorted.contains(where: \.isTailscale), !primary.isTailscale { primary.network += " + Tailscale" }
            result.append(primary)
        }
        result.sort { sortKey($0.ip) < sortKey($1.ip) }
        onUpdate(result)
        return result
    }

    static func sortKey(_ ip: String) -> UInt32 { ipv4ToUInt(ip) ?? 0 }
}

// MARK: - Process helper
public enum Shell {
    public struct Result: Sendable {
        public var status: Int32
        public var stdout: String
        public var stderr: String
        public var stdoutData: Data?
        public var ok: Bool { status == 0 }
    }
    public static func run(_ exe: String, _ args: [String], timeout: TimeInterval = 20, stdin: Data? = nil) async -> Result {
        await Task.detached { runBlocking(exe, args, timeout: timeout, stdin: stdin) }.value
    }
    static func runBlocking(_ exe: String, _ args: [String], timeout: TimeInterval, stdin: Data?) -> Result {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: exe); p.arguments = args
        let out = Pipe(), err = Pipe(), inp = Pipe()
        p.standardOutput = out; p.standardError = err; p.standardInput = stdin == nil ? FileHandle.nullDevice : inp
        var outData = Data(), errData = Data()
        let lock = NSLock()
        out.fileHandleForReading.readabilityHandler = { h in let d = h.availableData; lock.lock(); outData.append(d); lock.unlock() }
        err.fileHandleForReading.readabilityHandler = { h in let d = h.availableData; lock.lock(); errData.append(d); lock.unlock() }
        let done = DispatchSemaphore(value: 0)
        p.terminationHandler = { _ in done.signal() }
        do { try p.run() } catch { return Result(status: 127, stdout: "", stderr: "\(error.localizedDescription)", stdoutData: nil) }
        if let stdin { inp.fileHandleForWriting.write(stdin); try? inp.fileHandleForWriting.close() }
        if done.wait(timeout: .now() + timeout) == .timedOut { p.terminate(); _ = done.wait(timeout: .now() + 2) }
        out.fileHandleForReading.readabilityHandler = nil; err.fileHandleForReading.readabilityHandler = nil
        lock.lock(); defer { lock.unlock() }
        outData.append(out.fileHandleForReading.readDataToEndOfFile()); errData.append(err.fileHandleForReading.readDataToEndOfFile())
        return Result(status: p.terminationStatus, stdout: String(decoding: outData, as: UTF8.self),
                      stderr: String(decoding: errData, as: UTF8.self), stdoutData: outData)
    }
}
