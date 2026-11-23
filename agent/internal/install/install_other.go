//go:build !linux && !darwin && !windows

package install

import (
	"errors"
	"runtime"
)

func SystemStateDir() string  { return UnixStateDir }
func InstalledListen() string { return "" }

func Install(string) error {
	return errors.New("install is not supported on " + runtime.GOOS + "; run `imonitord run` from your init system")
}
func Uninstall(bool) error { return errors.New("uninstall is not supported on " + runtime.GOOS) }

func canUseSystemDir(dir string) bool { return DirWritable(dir) }
