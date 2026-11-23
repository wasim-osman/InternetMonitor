//go:build !windows

package install

import (
	"errors"
	"runtime"
)

// InstallMode is Install; forceTask (a Windows scheduled task) is Windows-only.
func InstallMode(listen string, forceTask bool) error {
	if forceTask {
		return errors.New("--task is only supported on Windows (this is " + runtime.GOOS + ")")
	}
	return Install(listen)
}
