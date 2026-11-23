//! Process + path helpers shared by discovery, SSH and the updater.

use std::path::{Path, PathBuf};
use std::process::Stdio;
use std::time::Duration;

#[derive(Debug, Clone, Default)]
pub struct CmdResult {
    pub status: i32,
    pub stdout: String,
    pub stderr: String,
}

impl CmdResult {
    pub fn ok(&self) -> bool {
        self.status == 0
    }
    /// First non-empty line of stderr (or stdout), else `fallback`.
    pub fn first_error_line(&self, fallback: &str) -> String {
        let src = if self.stderr.trim().is_empty() { &self.stdout } else { &self.stderr };
        first_line(src, fallback)
    }
}

pub fn first_line(s: &str, fallback: &str) -> String {
    s.lines()
        .map(str::trim)
        .find(|l| !l.is_empty())
        .map(str::to_string)
        .unwrap_or_else(|| fallback.to_string())
}

/// Builds a tokio Command that never pops a console window on Windows.
pub fn command(exe: &Path) -> tokio::process::Command {
    #[allow(unused_mut)]
    let mut c = tokio::process::Command::new(exe);
    #[cfg(windows)]
    {
        const CREATE_NO_WINDOW: u32 = 0x0800_0000;
        c.creation_flags(CREATE_NO_WINDOW);
    }
    c
}

/// Runs a process with a hard timeout; stdin is closed. Never panics.
pub async fn run(exe: &Path, args: &[String], timeout: Duration, cwd: Option<&Path>) -> CmdResult {
    let mut c = command(exe);
    c.args(args).stdin(Stdio::null()).stdout(Stdio::piped()).stderr(Stdio::piped()).kill_on_drop(true);
    if let Some(d) = cwd {
        c.current_dir(d);
    }
    let child = match c.spawn() {
        Ok(ch) => ch,
        Err(e) => return CmdResult { status: 127, stdout: String::new(), stderr: format!("{}: {e}", exe.display()) },
    };
    match tokio::time::timeout(timeout, child.wait_with_output()).await {
        Ok(Ok(o)) => CmdResult {
            status: o.status.code().unwrap_or(-1),
            stdout: String::from_utf8_lossy(&o.stdout).into_owned(),
            stderr: String::from_utf8_lossy(&o.stderr).into_owned(),
        },
        Ok(Err(e)) => CmdResult { status: -1, stdout: String::new(), stderr: e.to_string() },
        Err(_) => CmdResult { status: -2, stdout: String::new(), stderr: format!("timed out after {}s", timeout.as_secs()) },
    }
}

fn exe_name(name: &str) -> String {
    if cfg!(windows) && !name.to_ascii_lowercase().ends_with(".exe") {
        format!("{name}.exe")
    } else {
        name.to_string()
    }
}

fn is_executable(p: &Path) -> bool {
    if !p.is_file() {
        return false;
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        p.metadata().map(|m| m.permissions().mode() & 0o111 != 0).unwrap_or(false)
    }
    #[cfg(not(unix))]
    {
        true
    }
}

/// Looks for `name` on PATH, then in `extra` absolute locations.
pub fn find_executable(name: &str, extra: &[&str]) -> Option<PathBuf> {
    let file = exe_name(name);
    if let Some(path) = std::env::var_os("PATH") {
        for dir in std::env::split_paths(&path) {
            let p = dir.join(&file);
            if is_executable(&p) {
                return Some(p);
            }
        }
    }
    extra.iter().map(PathBuf::from).find(|p| is_executable(p))
}

/// POSIX single-quote shell quoting.
pub fn shell_quote(s: &str) -> String {
    format!("'{}'", s.replace('\'', "'\\''"))
}

/// Name this machine uses when pairing with an agent.
pub fn client_name() -> String {
    let h = gethostname::gethostname().to_string_lossy().trim().to_string();
    let h = h.strip_suffix(".local").unwrap_or(&h).to_string();
    if h.is_empty() {
        "Internet Monitor desktop".into()
    } else {
        h
    }
}

/// The local user name (used as the first SSH login guess).
pub fn current_user() -> Option<String> {
    ["USER", "USERNAME", "LOGNAME"]
        .iter()
        .filter_map(|k| std::env::var(k).ok())
        .map(|s| s.trim().to_string())
        .find(|s| !s.is_empty())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn quoting() {
        assert_eq!(shell_quote("a'b"), "'a'\\''b'");
        assert_eq!(shell_quote("plain"), "'plain'");
    }

    #[test]
    fn first_line_skips_blank() {
        assert_eq!(first_line("\n  \n hello \nworld", "x"), "hello");
        assert_eq!(first_line("   ", "fallback"), "fallback");
    }
}
