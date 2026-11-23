package install

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func winInstallDir() string {
	return filepath.Join(envOr("ProgramFiles", `C:\Program Files`), WinDirName)
}
func winExePath() string { return filepath.Join(winInstallDir(), WinExeName) }

// SystemStateDir is %ProgramData%\InternetMonitor.
func SystemStateDir() string {
	return filepath.Join(envOr("ProgramData", `C:\ProgramData`), WinDirName)
}

func sys32(exe string) string {
	return filepath.Join(envOr("SystemRoot", `C:\Windows`), "System32", exe)
}

// runLine runs exe with a verbatim command line (netsh/sc are picky about
// quoting, so the line is built by the pure functions in install.go).
func runLine(exe, line string, show bool) (string, error) {
	cmd := exec.Command(exe)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: line, HideWindow: true}
	out, err := cmd.CombinedOutput()
	if show && len(out) > 0 {
		os.Stdout.Write(out)
	}
	if err != nil {
		return string(out), fmt.Errorf("%s: %w: %s", line, err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func isElevated() bool {
	p, err := syscall.GetCurrentProcess()
	if err != nil {
		return false
	}
	var t syscall.Token
	if err := syscall.OpenProcessToken(p, syscall.TOKEN_QUERY, &t); err != nil {
		return false
	}
	defer t.Close()
	var elevated, n uint32
	if err := syscall.GetTokenInformation(t, syscall.TokenElevation, (*byte)(unsafe.Pointer(&elevated)), 4, &n); err != nil {
		return false
	}
	return elevated != 0
}

func checkAdmin() error {
	if !isElevated() {
		return errors.New("must be run from an elevated prompt (right-click PowerShell or Command Prompt > Run as administrator)")
	}
	return nil
}

// InstalledListen reads --listen from the service config (readable without
// admin rights) or, for the scheduled-task fallback, the task definition.
func InstalledListen() string {
	sc := sys32("sc.exe")
	if out, err := runLine(sc, WinEscapeArg(sc)+" qc "+ServiceName+" 4096", false); err == nil {
		if l := ParseInstalledListen(out); l != "" {
			return l
		}
	}
	st := sys32("schtasks.exe")
	if out, err := runLine(st, WinEscapeArg(st)+" /Query /TN "+WinEscapeArg(WinTaskName)+" /XML", false); err == nil {
		return ParseInstalledListen(out)
	}
	return ""
}

func serviceExists() bool {
	sc := sys32("sc.exe")
	_, err := runLine(sc, WinEscapeArg(sc)+" query "+ServiceName, false)
	return err == nil
}

func serviceState() (int, bool) {
	sc := sys32("sc.exe")
	out, err := runLine(sc, WinEscapeArg(sc)+" query "+ServiceName, false)
	if err != nil {
		return 0, false
	}
	return ParseScState(out)
}

func waitState(want int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if st, ok := serviceState(); ok && st == want {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func stopService() {
	if !serviceExists() {
		return
	}
	if st, ok := serviceState(); ok && st == 1 {
		return
	}
	sc := sys32("sc.exe")
	runLine(sc, WinEscapeArg(sc)+" stop "+ServiceName, false)
	if !waitState(1, 30*time.Second) {
		// Last resort: kill a hung process so the binary can be replaced.
		tk := sys32("taskkill.exe")
		runLine(tk, WinEscapeArg(tk)+` /F /FI "SERVICES eq `+ServiceName+`"`, false)
		time.Sleep(time.Second)
	}
}

func removeTask() {
	st := sys32("schtasks.exe")
	runLine(st, WinEscapeArg(st)+" /End /TN "+WinEscapeArg(WinTaskName), false)
	runLine(st, WinEscapeArg(st)+" /Delete /TN "+WinEscapeArg(WinTaskName)+" /F", false)
}

func taskExists() bool {
	st := sys32("schtasks.exe")
	_, err := runLine(st, WinEscapeArg(st)+" /Query /TN "+WinEscapeArg(WinTaskName), false)
	return err == nil
}

// restrictACL: SYSTEM + Administrators only (config.json holds token hashes;
// Unix file modes mean nothing on Windows). SIDs keep it locale-independent.
func restrictACL(dir string) {
	ic := sys32("icacls.exe")
	line := WinEscapeArg(ic) + " " + WinEscapeArg(dir) +
		" /inheritance:r /grant:r *S-1-5-18:(OI)(CI)F *S-1-5-32-544:(OI)(CI)F /T /C /Q"
	if _, err := runLine(ic, line, false); err != nil {
		fmt.Println("warning: could not restrict permissions on", dir+":", err)
	}
}

func installService(exe, listen, stateDir string) error {
	sc := sys32("sc.exe")
	if _, err := runLine(sc, WinScCreate(sc, WinBinPath(exe, listen, stateDir), serviceExists()), true); err != nil {
		return err
	}
	for _, l := range WinScFailure(sc) {
		if _, err := runLine(sc, l, false); err != nil {
			fmt.Println("warning:", err)
		}
	}
	if _, err := runLine(sc, WinEscapeArg(sc)+" start "+ServiceName, false); err != nil {
		if st, ok := serviceState(); !ok || st != 4 {
			return err
		}
	}
	if !waitState(4, 20*time.Second) {
		return errors.New("service did not reach RUNNING within 20 s")
	}
	return nil
}

func installTask(exe, listen, stateDir string) error {
	xmlText := WinTaskXML(exe, WinServiceArgs(listen, stateDir, false))
	// schtasks /XML is most reliable with a UTF-16LE file with BOM.
	u := utf16.Encode([]rune(xmlText))
	buf := make([]byte, 2+2*len(u))
	buf[0], buf[1] = 0xFF, 0xFE
	for i, c := range u {
		buf[2+2*i], buf[3+2*i] = byte(c), byte(c>>8)
	}
	tmp := filepath.Join(stateDir, "imonitord-task.xml")
	if err := os.WriteFile(tmp, buf, 0o600); err != nil {
		return err
	}
	defer os.Remove(tmp)
	st := sys32("schtasks.exe")
	if _, err := runLine(st, WinEscapeArg(st)+" /Create /TN "+WinEscapeArg(WinTaskName)+" /XML "+WinEscapeArg(tmp)+" /F", true); err != nil {
		return err
	}
	_, err := runLine(st, WinEscapeArg(st)+" /Run /TN "+WinEscapeArg(WinTaskName), true)
	return err
}

func deleteService() {
	sc := sys32("sc.exe")
	runLine(sc, WinEscapeArg(sc)+" delete "+ServiceName, false)
}

// Install copies the exe to %ProgramFiles%\InternetMonitor, registers the
// Windows service (falling back to a SYSTEM scheduled task if the service
// cannot be started) and opens TCP <port> in Windows Firewall. Idempotent.
func Install(listen string) error { return InstallMode(listen, false) }

// InstallMode is Install; forceTask skips the service and uses the task.
func InstallMode(listen string, forceTask bool) error {
	if err := checkAdmin(); err != nil {
		return err
	}
	if listen == "" {
		listen = InstalledListen()
	}
	if listen == "" {
		listen = DefaultPort
	}
	port, err := ListenPort(listen)
	if err != nil {
		return err
	}
	exe, stateDir := winExePath(), SystemStateDir()
	// Stop whatever runs the old binary so it can be replaced.
	stopService()
	if taskExists() {
		st := sys32("schtasks.exe")
		runLine(st, WinEscapeArg(st)+" /End /TN "+WinEscapeArg(WinTaskName), false)
		time.Sleep(time.Second)
	}
	if err := os.MkdirAll(winInstallDir(), 0o755); err != nil {
		return err
	}
	if err := copyBinary(exe); err != nil {
		return fmt.Errorf("copy binary: %w (is another imonitord.exe still running?)", err)
	}
	fmt.Printf("installed %s\n", exe)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("state dir: %w", err)
	}
	restrictACL(stateDir)

	netsh := sys32("netsh.exe")
	runLine(netsh, WinFirewallDelete(netsh), false)
	if _, err := runLine(netsh, WinFirewallAdd(netsh, exe, port), false); err != nil {
		fmt.Println("warning: could not add the firewall rule:", err)
	} else {
		fmt.Printf("firewall: allowed inbound TCP %d (rule %q)\n", port, WinFirewallRule)
	}

	mode := "service"
	if !forceTask {
		if err := installService(exe, listen, stateDir); err != nil {
			fmt.Println("warning: Windows service failed:", err)
			fmt.Println("falling back to a scheduled task (runs as SYSTEM at startup)")
			stopService()
			deleteService()
			forceTask = true
		} else {
			removeTask()
		}
	}
	if forceTask {
		mode = "scheduled task " + WinTaskName
		if serviceExists() {
			stopService()
			deleteService()
		}
		if err := installTask(exe, listen, stateDir); err != nil {
			return fmt.Errorf("scheduled task: %w", err)
		}
	}
	fmt.Printf("imonitord is running as %s on %s (state %s, log %s).\nPair with: \"%s\" pair-code\n",
		mode, listen, stateDir, filepath.Join(stateDir, "imonitord.log"), exe)
	return nil
}

var (
	modkernel32     = syscall.NewLazyDLL("kernel32.dll")
	procMoveFileExW = modkernel32.NewProc("MoveFileExW")
)

// removeOrSchedule deletes path, or schedules it for deletion at reboot when
// it is in use (e.g. uninstall run from the installed exe itself).
func removeOrSchedule(path string) {
	if err := os.Remove(path); err == nil || os.IsNotExist(err) {
		return
	}
	p, _ := syscall.UTF16PtrFromString(path)
	const moveFileDelayUntilReboot = 0x4
	if r, _, _ := procMoveFileExW.Call(uintptr(unsafe.Pointer(p)), 0, moveFileDelayUntilReboot); r != 0 {
		fmt.Printf("%s is in use; it will be deleted at the next reboot\n", path)
	} else {
		fmt.Printf("could not delete %s; delete it by hand\n", path)
	}
}

// Uninstall stops and removes the service / task, the firewall rule and the
// binary; purge also deletes %ProgramData%\InternetMonitor.
func Uninstall(purge bool) error {
	if err := checkAdmin(); err != nil {
		return err
	}
	stopService()
	deleteService()
	removeTask()
	netsh := sys32("netsh.exe")
	runLine(netsh, WinFirewallDelete(netsh), false)
	removeOrSchedule(winExePath())
	os.Remove(winExePath() + ".new")
	os.Remove(winInstallDir()) // only if empty
	fmt.Println("removed service, firewall rule and binary")
	stateDir := SystemStateDir()
	if purge {
		if err := os.RemoveAll(stateDir); err != nil {
			fmt.Println("warning:", err)
		} else {
			fmt.Println("purged state directory")
		}
	} else {
		fmt.Println("state kept in " + stateDir + " (use --purge to delete)")
	}
	return nil
}

// canUseSystemDir: only elevated processes use %ProgramData%; normal users
// could create it too, but it would then inherit a Users-readable ACL.
func canUseSystemDir(dir string) bool { return isElevated() && DirWritable(dir) }
