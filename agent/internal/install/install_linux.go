package install

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// SystemStateDir is the state directory of the installed service.
func SystemStateDir() string { return UnixStateDir }

// InstalledListen returns the --listen value of the installed unit, if any.
func InstalledListen() string {
	data, err := os.ReadFile(UnitPath)
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

// hasSystemd: systemctl present and systemd actually running as PID 1
// (containers and OpenRC/SysV/BusyBox systems often have neither).
func hasSystemd() bool {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false
	}
	st, err := os.Stat("/run/systemd/system")
	return err == nil && st.IsDir()
}

// Install copies the binary, writes + enables the unit and (re)starts it.
// Idempotent: re-running upgrades the binary and keeps the state directory.
// Without systemd it installs the binary and prints how to run it manually.
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
	if err := os.MkdirAll(filepath.Dir(LinuxBinPath), 0o755); err != nil {
		return err
	}
	if err := copyBinary(LinuxBinPath); err != nil {
		return fmt.Errorf("copy binary: %w", err)
	}
	fmt.Printf("installed %s\n", LinuxBinPath)
	if !hasSystemd() {
		if err := os.MkdirAll(UnixStateDir, 0o700); err == nil {
			os.Chmod(UnixStateDir, 0o700)
		}
		fmt.Printf(`
systemd was not found (no systemctl, or systemd is not PID 1), so no service
was registered. Start the agent with your init system, or by hand:

    sudo %s run --listen %s --state-dir %s

(e.g. add that line to /etc/rc.local, an OpenRC/runit service, or a
@reboot crontab entry for root). Then pair with: %s pair-code --listen %s
`, LinuxBinPath, listen, UnixStateDir, LinuxBinPath, listen)
		return nil
	}
	if err := os.WriteFile(UnitPath, []byte(Unit(listen)), 0o644); err != nil {
		return fmt.Errorf("write unit: %w", err)
	}
	fmt.Printf("wrote %s\n", UnitPath)
	if err := run("systemctl", "daemon-reload"); err != nil {
		return err
	}
	if err := run("systemctl", "enable", ServiceName); err != nil {
		return err
	}
	if err := run("systemctl", "restart", ServiceName); err != nil {
		return err
	}
	fmt.Printf("imonitord is running on %s. Pair with: imonitord pair-code\n", listen)
	return nil
}

// Uninstall stops/disables the service and removes the unit and binary;
// purge also deletes the state directory (history, identity, clients).
func Uninstall(purge bool) error {
	if err := checkRoot(); err != nil {
		return err
	}
	if hasSystemd() {
		exec.Command("systemctl", "disable", "--now", ServiceName).Run()
		os.Remove(UnitPath)
		run("systemctl", "daemon-reload")
	} else {
		os.Remove(UnitPath)
	}
	os.Remove(LinuxBinPath)
	fmt.Println("removed service and binary")
	if purge {
		// DynamicUser keeps state in /var/lib/private/imonitor (+ symlink).
		os.RemoveAll(UnixStateDir)
		os.RemoveAll("/var/lib/private/imonitor")
		fmt.Println("purged state directory")
	} else {
		fmt.Println("state kept in " + UnixStateDir + " (use --purge to delete)")
	}
	return nil
}

func canUseSystemDir(dir string) bool { return DirWritable(dir) }
