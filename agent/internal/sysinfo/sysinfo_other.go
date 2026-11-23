//go:build !linux && !darwin && !windows

package sysinfo

func collectOS(*System)                       {}
func diskFreeMB(string) (int64, bool)         { return 0, false }
func isPhysical(string) bool                  { return false }
func isWireless(string) bool                  { return false }
func ifaceCounters(string) (*uint64, *uint64) { return nil, nil }
