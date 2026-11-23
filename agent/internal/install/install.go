// Package install registers imonitord as a system service: a systemd unit on
// Linux, a launchd daemon on macOS and a Windows service (or, as a fallback, a
// SYSTEM scheduled task) on Windows. The text generators in this file are pure
// functions so they can be unit-tested on any OS.
package install

import (
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

const (
	ServiceName = "imonitord"
	DefaultPort = ":7878"

	// Linux (systemd)
	LinuxBinPath = "/usr/local/bin/imonitord"
	UnitPath     = "/etc/systemd/system/imonitord.service"

	// macOS (launchd)
	DarwinBinPath = "/usr/local/bin/imonitord"
	LaunchdLabel  = "com.wasim.imonitord"
	PlistPath     = "/Library/LaunchDaemons/com.wasim.imonitord.plist"
	DarwinLogPath = "/var/log/imonitord.log"

	// Linux and macOS state directory.
	UnixStateDir = "/var/lib/imonitor"

	// Windows
	WinDirName      = "InternetMonitor"
	WinExeName      = "imonitord.exe"
	WinDisplayName  = "InternetMonitor agent"
	WinDescription  = "InternetMonitor agent (imonitord): network probes and JSON API on TCP 7878."
	WinTaskName     = `InternetMonitor\imonitord`
	WinFirewallRule = "InternetMonitor agent (imonitord)"
)

// Unit renders the hardened systemd unit.
func Unit(listen string) string {
	return fmt.Sprintf(`[Unit]
Description=InternetMonitor agent (imonitord)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s run --listen %s --state-dir %s
Restart=always
RestartSec=2
DynamicUser=yes
StateDirectory=imonitor
StateDirectoryMode=0700
UMask=0077
AmbientCapabilities=CAP_NET_RAW
CapabilityBoundingSet=CAP_NET_RAW
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectKernelLogs=yes
ProtectControlGroups=yes
ProtectClock=yes
ProtectHostname=yes
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX AF_NETLINK
RestrictNamespaces=yes
RestrictRealtime=yes
RestrictSUIDSGID=yes
LockPersonality=yes
MemoryDenyWriteExecute=yes
SystemCallArchitectures=native
MemoryMax=64M
TasksMax=64

[Install]
WantedBy=multi-user.target
`, LinuxBinPath, listen, UnixStateDir)
}

// LaunchdPlist renders the macOS LaunchDaemon property list.
func LaunchdPlist(bin, listen, stateDir, logPath string) string {
	esc := func(s string) string {
		var b strings.Builder
		xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + LaunchdLabel + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + esc(bin) + `</string>
		<string>run</string>
		<string>--listen</string>
		<string>` + esc(listen) + `</string>
		<string>--state-dir</string>
		<string>` + esc(stateDir) + `</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ThrottleInterval</key>
	<integer>5</integer>
	<key>StandardOutPath</key>
	<string>` + esc(logPath) + `</string>
	<key>StandardErrorPath</key>
	<string>` + esc(logPath) + `</string>
</dict>
</plist>
`
}

var (
	listenRe      = regexp.MustCompile(`--listen[\s=]+"?([^\s"<]+)`)
	plistListenRe = regexp.MustCompile(`<string>--listen</string>\s*<string>([^<]+)</string>`)
)

// ParseInstalledListen extracts the --listen value from a systemd unit, a
// launchd plist, `sc qc` output or a scheduled-task XML ("" if absent).
func ParseInstalledListen(data string) string {
	if m := plistListenRe.FindStringSubmatch(data); m != nil {
		return strings.TrimSpace(m[1])
	}
	if m := listenRe.FindStringSubmatch(data); m != nil {
		return m[1]
	}
	return ""
}

// ListenPort returns the TCP port of a listen address such as ":7878".
func ListenPort(listen string) (int, error) {
	_, p, err := net.SplitHostPort(listen)
	if err != nil {
		return 0, fmt.Errorf("bad listen address %q", listen)
	}
	port, err := strconv.Atoi(p)
	if err != nil || port <= 0 || port > 65535 {
		return 0, fmt.Errorf("bad listen port %q", p)
	}
	return port, nil
}

// ---- Windows command lines (pure, so they can be tested anywhere) ----

// WinEscapeArg quotes s the way CommandLineToArgvW / the MS C runtime
// expect (same rules as Go's syscall.EscapeArg on Windows).
func WinEscapeArg(s string) string {
	if s == "" {
		return `""`
	}
	if !strings.ContainsAny(s, " \t\n\v\"") {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\':
			slashes++
		case '"':
			for ; slashes > 0; slashes-- {
				b.WriteByte('\\')
			}
			b.WriteByte('\\')
		default:
			slashes = 0
		}
		b.WriteByte(c)
	}
	for ; slashes > 0; slashes-- {
		b.WriteByte('\\')
	}
	b.WriteByte('"')
	return b.String()
}

// WinServiceArgs are the arguments the installed service/task runs with.
func WinServiceArgs(listen, stateDir string, service bool) []string {
	args := []string{"run"}
	if service {
		args = append(args, "--service")
	} else {
		args = append(args, "--self-restart", "--log-file", strings.TrimRight(stateDir, `\`)+`\imonitord.log`)
	}
	return append(args, "--listen", listen, "--state-dir", stateDir)
}

func winJoin(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = WinEscapeArg(a)
	}
	return strings.Join(q, " ")
}

// WinBinPath is the service's binPath (exe quoted, then its arguments).
func WinBinPath(exe, listen, stateDir string) string {
	return `"` + exe + `" ` + winJoin(WinServiceArgs(listen, stateDir, true))
}

// WinScCreate returns the sc.exe command line that creates (or, with
// update=true, reconfigures) the service.
func WinScCreate(sc, binPath string, update bool) string {
	verb := "create"
	if update {
		verb = "config"
	}
	return winJoin([]string{sc, verb, ServiceName, "binPath=", binPath, "start=", "auto",
		"DisplayName=", WinDisplayName})
}

// WinScFailure sets restart-on-failure actions (also when the service stops
// itself with an error, via failureflag).
func WinScFailure(sc string) []string {
	return []string{
		winJoin([]string{sc, "failure", ServiceName, "reset=", "86400", "actions=", "restart/2000/restart/5000/restart/10000"}),
		winJoin([]string{sc, "failureflag", ServiceName, "1"}),
		winJoin([]string{sc, "description", ServiceName, WinDescription}),
	}
}

// WinFirewallAdd / WinFirewallDelete build netsh command lines. netsh wants
// key="value with spaces" (quotes around the value only).
func WinFirewallAdd(netsh, exe string, port int) string {
	return fmt.Sprintf(`%s advfirewall firewall add rule name="%s" dir=in action=allow protocol=TCP localport=%d program="%s" profile=any enable=yes`,
		WinEscapeArg(netsh), WinFirewallRule, port, exe)
}

func WinFirewallDelete(netsh string) string {
	return fmt.Sprintf(`%s advfirewall firewall delete rule name="%s"`, WinEscapeArg(netsh), WinFirewallRule)
}

var scStateRe = regexp.MustCompile(`:\s*([1-7])\s+(STOPPED|START_PENDING|STOP_PENDING|RUNNING|CONTINUE_PENDING|PAUSE_PENDING|PAUSED)\b`)

// ParseScState returns the SERVICE_STATE number from `sc query` output
// (1 stopped, 2 start pending, 3 stop pending, 4 running, ...).
func ParseScState(out string) (int, bool) {
	m := scStateRe.FindStringSubmatch(out)
	if m == nil {
		return 0, false
	}
	n, _ := strconv.Atoi(m[1])
	return n, true
}

// WinTaskXML is the fallback scheduled task: runs as SYSTEM at boot, never
// times out, restarted by Task Scheduler if it fails to launch. API restarts
// are handled in-process (--self-restart), so the task never has to relaunch
// a process that exited.
func WinTaskXML(exe string, args []string) string {
	esc := func(s string) string {
		var b strings.Builder
		xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	return `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>` + esc(WinDescription) + `</Description>
  </RegistrationInfo>
  <Triggers>
    <BootTrigger>
      <Enabled>true</Enabled>
    </BootTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <UserId>S-1-5-18</UserId>
      <RunLevel>HighestAvailable</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <IdleSettings>
      <StopOnIdleEnd>false</StopOnIdleEnd>
      <RestartOnIdle>false</RestartOnIdle>
    </IdleSettings>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>false</Hidden>
    <RunOnlyIfIdle>false</RunOnlyIfIdle>
    <WakeToRun>false</WakeToRun>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>5</Priority>
    <RestartOnFailure>
      <Interval>PT1M</Interval>
      <Count>999</Count>
    </RestartOnFailure>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>` + esc(exe) + `</Command>
      <Arguments>` + esc(winJoin(args)) + `</Arguments>
    </Exec>
  </Actions>
</Task>
`
}

// ---- state directory selection ----

// ChooseStateDir returns system when it is usable (writable), else user.
func ChooseStateDir(system, user string, writable func(string) bool) string {
	if system != "" && writable(system) {
		return system
	}
	if user != "" {
		return user
	}
	return system
}

// DirWritable reports whether dir exists or can be created, and a file can be
// written in it.
func DirWritable(dir string) bool {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false
	}
	f, err := os.CreateTemp(dir, ".write-test-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}

// DefaultStateDir is the state directory used when --state-dir is not given:
// the system location when writable (root / Administrator / installed
// service), otherwise a per-user directory so a developer can just run
// ./imonitord unprivileged.
func DefaultStateDir() string {
	return ChooseStateDir(SystemStateDir(), UserStateDir(), canUseSystemDir)
}

// UserStateDir is the per-user fallback state directory.
func UserStateDir() string {
	switch runtime.GOOS {
	case "windows":
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, WinDirName)
		}
	case "darwin":
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, "Library", "Application Support", "imonitor")
		}
	default:
		if d := os.Getenv("XDG_STATE_HOME"); d != "" {
			return filepath.Join(d, "imonitor")
		}
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, ".local", "state", "imonitor")
		}
	}
	return filepath.Join(os.TempDir(), "imonitor")
}

// ---- shared helpers ----

func samePath(a, b string) bool {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

func copyBinary(dst string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if s, err := filepath.EvalSymlinks(self); err == nil {
		self = s
	}
	if d, err := filepath.EvalSymlinks(dst); err == nil && samePath(d, self) {
		return nil // already running the installed binary
	}
	in, err := os.Open(self)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst) // atomic replace; a running process keeps its old inode
}
