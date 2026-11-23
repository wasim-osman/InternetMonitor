import Foundation

/// Uses the user's own ssh setup (keys / agent / Tailscale SSH) — never asks for or stores passwords.
public enum SSHInstaller {
    static let sshOpts = ["-o", "BatchMode=yes", "-o", "ConnectTimeout=6", "-o", "StrictHostKeyChecking=accept-new",
                          "-o", "ServerAliveInterval=5", "-o", "LogLevel=ERROR"]

    public static func ssh(_ user: String, _ host: String, _ command: String, timeout: TimeInterval = 30) async -> Shell.Result {
        await Shell.run("/usr/bin/ssh", sshOpts + ["\(user)@\(host)", command], timeout: timeout)
    }

    /// Candidate usernames to try, most likely first (the current mac user, then Pi defaults).
    public static func guessUsers(saved: String?) -> [String] {
        var u: [String] = []
        if let saved, !saved.isEmpty { u.append(saved) }
        for x in [NSUserName(), "pi", "root"] where !u.contains(x) { u.append(x) }
        return u
    }

    /// Finds a working ssh login. Returns the username.
    public static func probeLogin(host: String, users: [String]) async -> String? {
        for u in users {
            let r = await ssh(u, host, "echo ok", timeout: 10)
            if r.ok && r.stdout.contains("ok") { return u }
        }
        return nil
    }

    /// Ask an already-installed agent for a fresh pairing code via SSH (it only hands them out on loopback).
    public static func fetchPairCode(user: String, host: String) async -> Result<String, AgentError> {
        // Try PATH first, then the standard install location (non-login shells often lack /usr/local/bin).
        let r = await ssh(user, host, "(imonitord pair-code --json || /usr/local/bin/imonitord pair-code --json)", timeout: 15)
        guard r.ok else { return .failure(AgentError(status: 0, message: firstLine(r.stderr.isEmpty ? r.stdout : r.stderr, "ssh failed"))) }
        for line in r.stdout.split(separator: "\n").reversed() {
            if let d = line.data(using: .utf8), let pc = try? JSONDecoder().decode(PairCode.self, from: d) { return .success(pc.code) }
        }
        return .failure(AgentError(status: 0, message: "Agent did not return a pairing code"))
    }

    /// Copies the bundled binary and runs `imonitord install`. Needs passwordless sudo (or root login).
    public static func install(user: String, host: String, binaryFor: @escaping @Sendable (String) -> URL?, log: @escaping @Sendable (String) -> Void) async -> Result<Void, AgentError> {
        log("Detecting architecture…")
        let uname = await ssh(user, host, "uname -m", timeout: 10)
        guard uname.ok else { return .failure(AgentError(status: 0, message: firstLine(uname.stderr, "ssh failed"))) }
        let arch = uname.stdout.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let binary = binaryFor(arch) else { return .failure(AgentError(status: 0, message: "No bundled agent for architecture \(arch)")) }
        log("Uploading agent (\(arch))…")
        let remote = "/tmp/imonitord.new"
        let scp = await Shell.run("/usr/bin/scp", sshOpts + [binary.path, "\(user)@\(host):\(remote)"], timeout: 60)
        guard scp.ok else { return .failure(AgentError(status: 0, message: firstLine(scp.stderr, "scp failed"))) }
        log("Installing service…")
        let sudo = user == "root" ? "" : "sudo -n "
        let r = await ssh(user, host, "chmod +x \(remote) && \(sudo)\(remote) install && rm -f \(remote)", timeout: 60)
        guard r.ok else {
            let msg = r.stderr.contains("password") || r.stderr.contains("sudo")
                ? "\(user) needs passwordless sudo. Run on the Pi: sudo /tmp/imonitord.new install"
                : firstLine(r.stderr.isEmpty ? r.stdout : r.stderr, "install failed")
            return .failure(AgentError(status: 0, message: msg))
        }
        log("Installed.")
        return .success(())
    }

    /// Removes the agent and every file it created, then verifies nothing is left. Returns leftovers (empty = clean).
    public static func uninstall(user: String, host: String, log: @escaping @Sendable (String) -> Void) async -> Result<[String], AgentError> {
        log("Stopping and removing agent…")
        let script = #"""
        /usr/local/bin/imonitord uninstall --purge >/dev/null 2>&1
        systemctl disable --now imonitord >/dev/null 2>&1
        pkill -x imonitord >/dev/null 2>&1
        rm -f /etc/systemd/system/imonitord.service /usr/local/bin/imonitord /tmp/imonitord.new /tmp/imonitord
        rm -rf /var/lib/imonitor /var/lib/private/imonitor /var/cache/imonitor /var/log/imonitor
        systemctl daemon-reload >/dev/null 2>&1
        systemctl reset-failed imonitord >/dev/null 2>&1
        for p in /usr/local/bin/imonitord /etc/systemd/system/imonitord.service /var/lib/imonitor /var/lib/private/imonitor /tmp/imonitord.new /tmp/imonitord; do [ -e "$p" ] && echo "LEFT:$p"; done
        systemctl list-unit-files 2>/dev/null | grep -q '^imonitord' && echo "LEFT:systemd unit"
        pgrep -x imonitord >/dev/null 2>&1 && echo "LEFT:running process"
        echo DONE
        """#
        let wrapped = (user == "root" ? "sh -c " : "sudo -n sh -c ") + shellQuote(script)
        let r = await ssh(user, host, wrapped, timeout: 60)
        guard r.stdout.contains("DONE") else {
            let msg = r.stderr.contains("sudo") || r.stderr.contains("password")
                ? "\(user) needs passwordless sudo on \(host) to remove the agent."
                : firstLine(r.stderr.isEmpty ? r.stdout : r.stderr, "SSH failed")
            return .failure(AgentError(status: 0, message: msg + " Manual removal: ssh \(user)@\(host) 'sudo imonitord uninstall --purge'"))
        }
        let left = r.stdout.split(separator: "\n").filter { $0.hasPrefix("LEFT:") }.map { String($0.dropFirst(5)) }
        log(left.isEmpty ? "Verified: no agent files remain." : "Leftovers: \(left.joined(separator: ", "))")
        return .success(left)
    }

    static func shellQuote(_ s: String) -> String { "'" + s.replacingOccurrences(of: "'", with: "'\\''") + "'" }

    public static func manualInstallCommand(host: String, user: String) -> String {
        "scp imonitord-linux-arm64 \(user)@\(host):/tmp/imonitord && ssh -t \(user)@\(host) 'sudo /tmp/imonitord install'"
    }

    static func firstLine(_ s: String, _ fallback: String) -> String {
        s.split(separator: "\n").first.map(String.init)?.trimmingCharacters(in: .whitespaces) ?? fallback
    }
}
