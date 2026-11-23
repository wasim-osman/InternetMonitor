//! SSH helpers using the system OpenSSH client (ships with Windows 10/11, Linux, macOS).
//! Uses the user's own keys/agent (BatchMode) — never asks for or stores passwords.
//! Mirrors app/Sources/IMCore/SSHInstaller.swift.

use crate::util::{self, shell_quote, CmdResult};
use serde::Deserialize;
use std::path::{Path, PathBuf};
use std::time::Duration;

const SSH_OPTS: [&str; 10] = [
    "-o", "BatchMode=yes",
    "-o", "ConnectTimeout=6",
    "-o", "StrictHostKeyChecking=accept-new",
    "-o", "ServerAliveInterval=5",
    "-o", "LogLevel=ERROR",
];

/// Verbatim from SSHInstaller.swift `uninstall`.
pub const UNINSTALL_SCRIPT: &str = r#"/usr/local/bin/imonitord uninstall --purge >/dev/null 2>&1
systemctl disable --now imonitord >/dev/null 2>&1
pkill -x imonitord >/dev/null 2>&1
rm -f /etc/systemd/system/imonitord.service /usr/local/bin/imonitord /tmp/imonitord.new /tmp/imonitord
rm -rf /var/lib/imonitor /var/lib/private/imonitor /var/cache/imonitor /var/log/imonitor
systemctl daemon-reload >/dev/null 2>&1
systemctl reset-failed imonitord >/dev/null 2>&1
for p in /usr/local/bin/imonitord /etc/systemd/system/imonitord.service /var/lib/imonitor /var/lib/private/imonitor /tmp/imonitord.new /tmp/imonitord; do [ -e "$p" ] && echo "LEFT:$p"; done
systemctl list-unit-files 2>/dev/null | grep -q '^imonitord' && echo "LEFT:systemd unit"
pgrep -x imonitord >/dev/null 2>&1 && echo "LEFT:running process"
echo DONE"#;

fn tool(name: &str) -> PathBuf {
    util::find_executable(name, &[&format!(r"C:\Windows\System32\OpenSSH\{name}.exe"), &format!("/usr/bin/{name}")])
        .unwrap_or_else(|| PathBuf::from(name))
}

fn opts() -> Vec<String> {
    SSH_OPTS.iter().map(|s| s.to_string()).collect()
}

pub async fn ssh(user: &str, host: &str, command: &str, timeout: Duration) -> CmdResult {
    let mut args = opts();
    args.push(format!("{user}@{host}"));
    args.push(command.to_string());
    util::run(&tool("ssh"), &args, timeout, None).await
}

async fn scp(local: &Path, user: &str, host: &str, remote: &str) -> CmdResult {
    // Run from the file's directory and pass a bare file name: avoids Windows drive-letter
    // colons being mistaken for a `host:` spec.
    let dir = local.parent().map(Path::to_path_buf);
    let file = local.file_name().map(|f| f.to_string_lossy().into_owned()).unwrap_or_default();
    let mut args = opts();
    args.push(file);
    args.push(format!("{user}@{host}:{remote}"));
    util::run(&tool("scp"), &args, Duration::from_secs(90), dir.as_deref()).await
}

/// Usernames to try, most likely first: saved one, the local user, then Pi defaults.
pub fn guess_users(saved: Option<&str>) -> Vec<String> {
    let mut u: Vec<String> = vec![];
    for x in [saved.map(str::to_string), util::current_user(), Some("pi".into()), Some("root".into())].into_iter().flatten() {
        let x = x.trim().to_string();
        if !x.is_empty() && !u.contains(&x) {
            u.push(x);
        }
    }
    u
}

pub async fn probe_login(host: &str, users: &[String]) -> Option<String> {
    for u in users {
        let r = ssh(u, host, "echo ok", Duration::from_secs(10)).await;
        if r.ok() && r.stdout.contains("ok") {
            return Some(u.clone());
        }
    }
    None
}

#[derive(Deserialize)]
struct PairCode {
    code: String,
}

/// Finds the `{"code": ...}` line in `imonitord pair-code --json` output (last match wins).
pub fn parse_pair_code(stdout: &str) -> Option<String> {
    stdout.lines().rev().find_map(|l| serde_json::from_str::<PairCode>(l.trim()).ok().map(|p| p.code))
}

pub async fn fetch_pair_code(user: &str, host: &str) -> Result<String, String> {
    let r = ssh(user, host, "(imonitord pair-code --json || /usr/local/bin/imonitord pair-code --json)", Duration::from_secs(15)).await;
    if !r.ok() {
        return Err(r.first_error_line("ssh failed"));
    }
    parse_pair_code(&r.stdout).ok_or_else(|| "Agent did not return a pairing code".into())
}

/// Maps `uname -m` to the bundled agent binary names to try (in order).
pub fn agent_binary_names(arch: &str) -> Vec<&'static str> {
    match arch.trim() {
        "x86_64" | "amd64" => vec!["imonitord-linux-amd64"],
        "aarch64" | "arm64" | "armv8l" => vec!["imonitord-linux-arm64"],
        a if a.starts_with("armv7") || a.starts_with("armv6") || a == "arm" => vec!["imonitord-linux-arm", "imonitord-linux-armv7"],
        "i686" | "i586" | "i486" | "i386" | "x86" => vec!["imonitord-linux-386"],
        _ => vec![],
    }
}

pub fn find_agent_binary(dirs: &[PathBuf], arch: &str) -> Option<PathBuf> {
    for name in agent_binary_names(arch) {
        for d in dirs {
            let p = d.join(name);
            if p.is_file() {
                return Some(p);
            }
        }
    }
    None
}

/// Uploads the matching agent binary and runs `imonitord install` (needs passwordless sudo or root).
pub async fn install(user: &str, host: &str, dirs: &[PathBuf], log: &(dyn Fn(String) + Send + Sync)) -> Result<(), String> {
    log("Detecting architecture…".into());
    let uname = ssh(user, host, "uname -m", Duration::from_secs(10)).await;
    if !uname.ok() {
        return Err(uname.first_error_line("ssh failed"));
    }
    let arch = uname.stdout.trim().to_string();
    let Some(binary) = find_agent_binary(dirs, &arch) else {
        return Err(format!("No bundled agent for architecture {arch}"));
    };
    log(format!("Uploading agent ({arch})…"));
    let remote = "/tmp/imonitord.new";
    let up = scp(&binary, user, host, remote).await;
    if !up.ok() {
        return Err(up.first_error_line("scp failed"));
    }
    log("Installing service…".into());
    let sudo = if user == "root" { "" } else { "sudo -n " };
    let r = ssh(user, host, &format!("chmod +x {remote} && {sudo}{remote} install && rm -f {remote}"), Duration::from_secs(60)).await;
    if !r.ok() {
        let err = format!("{}{}", r.stderr, r.stdout);
        return Err(if err.contains("password") || err.contains("sudo") {
            format!("{user} needs passwordless sudo. Run on the node: sudo {remote} install")
        } else {
            r.first_error_line("install failed")
        });
    }
    log("Installed.".into());
    Ok(())
}

pub fn parse_leftovers(stdout: &str) -> Vec<String> {
    stdout.lines().filter_map(|l| l.trim().strip_prefix("LEFT:")).map(str::to_string).collect()
}

/// Removes the agent and all its files; returns leftovers (empty = clean).
pub async fn uninstall(user: &str, host: &str) -> Result<Vec<String>, String> {
    let wrapped = format!("{}{}", if user == "root" { "sh -c " } else { "sudo -n sh -c " }, shell_quote(UNINSTALL_SCRIPT));
    let r = ssh(user, host, &wrapped, Duration::from_secs(60)).await;
    if !r.stdout.contains("DONE") {
        let msg = if r.stderr.contains("sudo") || r.stderr.contains("password") {
            format!("{user} needs passwordless sudo on {host} to remove the agent.")
        } else {
            r.first_error_line("SSH failed")
        };
        return Err(format!("{msg} Manual removal: ssh {user}@{host} 'sudo imonitord uninstall --purge'"));
    }
    Ok(parse_leftovers(&r.stdout))
}

pub fn manual_install_command(host: &str, user: &str) -> String {
    format!("scp imonitord-linux-<arch> {user}@{host}:/tmp/imonitord && ssh -t {user}@{host} 'sudo /tmp/imonitord install'")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn arch_mapping() {
        assert_eq!(agent_binary_names("x86_64\n"), vec!["imonitord-linux-amd64"]);
        assert_eq!(agent_binary_names("aarch64"), vec!["imonitord-linux-arm64"]);
        assert_eq!(agent_binary_names("armv7l"), vec!["imonitord-linux-arm", "imonitord-linux-armv7"]);
        assert_eq!(agent_binary_names("armv6l"), vec!["imonitord-linux-arm", "imonitord-linux-armv7"]);
        assert_eq!(agent_binary_names("i686"), vec!["imonitord-linux-386"]);
        assert!(agent_binary_names("mips").is_empty());
    }

    #[test]
    fn find_binary_falls_back_to_armv7_name() {
        let d = std::env::temp_dir().join(format!("im-ssh-test-{}", std::process::id()));
        std::fs::create_dir_all(&d).unwrap();
        std::fs::write(d.join("imonitord-linux-armv7"), b"x").unwrap();
        assert_eq!(find_agent_binary(std::slice::from_ref(&d), "armv7l"), Some(d.join("imonitord-linux-armv7")));
        assert_eq!(find_agent_binary(std::slice::from_ref(&d), "x86_64"), None);
        std::fs::remove_dir_all(&d).ok();
    }

    #[test]
    fn pair_code_output() {
        let out = "Warning: something\n{\"code\":\"123456\",\"expiresAt\":1700000600}\n";
        assert_eq!(parse_pair_code(out).as_deref(), Some("123456"));
        assert_eq!(parse_pair_code("imonitord: command not found"), None);
    }

    #[test]
    fn users() {
        let u = guess_users(Some("alice"));
        assert_eq!(u[0], "alice");
        assert!(u.contains(&"pi".to_string()) && u.contains(&"root".to_string()));
        let mut d = u.clone();
        d.dedup();
        assert_eq!(d.len(), u.len());
    }

    #[test]
    fn uninstall_script_is_verbatim_and_quotable() {
        assert!(UNINSTALL_SCRIPT.starts_with("/usr/local/bin/imonitord uninstall --purge"));
        assert!(UNINSTALL_SCRIPT.ends_with("echo DONE"));
        assert!(UNINSTALL_SCRIPT.contains("grep -q '^imonitord'"));
        let q = shell_quote(UNINSTALL_SCRIPT);
        assert!(q.contains("'\\''^imonitord'\\''"));
        assert_eq!(parse_leftovers("LEFT:/var/lib/imonitor\nDONE\n"), vec!["/var/lib/imonitor"]);
    }
}
