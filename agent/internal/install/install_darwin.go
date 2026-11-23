package install

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// SystemStateDir is the state directory of the installed daemon.
func SystemStateDir() string { return UnixStateDir }

// InstalledListen returns the --listen value of the installed plist, if any.
func InstalledListen() string {
	data, err := os.ReadFile(PlistPath)
	if err != nil {
		return ""
	}
	return ParseInstalledListen(string(data))
}

func checkRoot() error {
	if os.Geteuid() != 0 {
		return errors.New("must be run as root (use sudo)")
	}
	return nil
}

const launchctl = "/bin/launchctl"

func serviceTarget() string { return "system/" + LaunchdLabel }

func loaded() bool {
	return exec.Command(launchctl, "print", serviceTarget()).Run() == nil
}

// bootout unloads the daemon (stopping it) and waits until launchd forgot it,
// because an immediate bootstrap after bootout can fail with EIO.
func bootout() {
	if !loaded() {
		return
	}
	exec.Command(launchctl, "bootout", serviceTarget()).Run()
	for i := 0; i < 50 && loaded(); i++ {
		time.Sleep(100 * time.Millisecond)
	}
}

// Install copies the binary to /usr/local/bin, writes the LaunchDaemon plist
// and (re)loads it. Idempotent; the state directory is kept.
func Install(listen string) error {
	if err := checkRoot(); err != nil {
		return err
	}
	if listen == "" {
		listen = InstalledListen()
	}
	if listen == "" {
		listen = DefaultPort
	}
	if _, err := ListenPort(listen); err != nil {
		return err
	}
	bootout() // stop the old daemon before replacing the binary
	if err := os.MkdirAll(filepath.Dir(DarwinBinPath), 0o755); err != nil {
		return err
	}
	if err := copyBinary(DarwinBinPath); err != nil {
		return fmt.Errorf("copy binary: %w", err)
	}
	os.Chown(DarwinBinPath, 0, 0)
	fmt.Printf("installed %s\n", DarwinBinPath)
	if err := os.MkdirAll(UnixStateDir, 0o700); err != nil {
		return fmt.Errorf("state dir: %w", err)
	}
	os.Chmod(UnixStateDir, 0o700)
	if err := os.WriteFile(PlistPath, []byte(LaunchdPlist(DarwinBinPath, listen, UnixStateDir, DarwinLogPath)), 0o644); err != nil {
		return fmt.Errorf("write plist: %w", err)
	}
	// launchd refuses plists that are not root:wheel and not writable by others.
	os.Chown(PlistPath, 0, 0)
	os.Chmod(PlistPath, 0o644)
	fmt.Printf("wrote %s\n", PlistPath)
	exec.Command(launchctl, "enable", serviceTarget()).Run()
	var out []byte
	var err error
	for i := 0; i < 5; i++ {
		out, err = exec.Command(launchctl, "bootstrap", "system", PlistPath).CombinedOutput()
		if err == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		return fmt.Errorf("launchctl bootstrap system %s: %v: %s", PlistPath, err, strings.TrimSpace(string(out)))
	}
	fmt.Printf("imonitord is running on %s (logs: %s). Pair with: imonitord pair-code\n", listen, DarwinLogPath)
	return nil
}

// Uninstall unloads the daemon and removes the plist, binary and log; purge
// also deletes the state directory.
func Uninstall(purge bool) error {
	if err := checkRoot(); err != nil {
		return err
	}
	bootout()
	os.Remove(PlistPath)
	os.Remove(DarwinBinPath)
	os.Remove(DarwinLogPath)
	fmt.Println("removed daemon, binary and log")
	if purge {
		os.RemoveAll(UnixStateDir)
		fmt.Println("purged state directory")
	} else {
		fmt.Println("state kept in " + UnixStateDir + " (use --purge to delete)")
	}
	return nil
}

func canUseSystemDir(dir string) bool { return DirWritable(dir) }
