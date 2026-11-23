package sysinfo

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func run(name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// collectOS is best-effort on macOS (development only).
func collectOS(s *System) {
	s.Kernel = strPtr(run("/usr/bin/uname", "-r"))
	s.Model = strPtr(run("/usr/sbin/sysctl", "-n", "hw.model"))
	if bt := run("/usr/sbin/sysctl", "-n", "kern.boottime"); bt != "" {
		// "{ sec = 1700000000, usec = 0 } ..."
		if i := strings.Index(bt, "sec = "); i >= 0 {
			rest := bt[i+6:]
			if j := strings.IndexByte(rest, ','); j > 0 {
				if v, err := strconv.ParseInt(rest[:j], 10, 64); err == nil {
					s.UptimeSec = i64(time.Now().Unix() - v)
				}
			}
		}
	}
	if la := run("/usr/sbin/sysctl", "-n", "vm.loadavg"); la != "" {
		f := strings.Fields(strings.Trim(la, "{} "))
		if len(f) >= 3 {
			out := make([]float64, 0, 3)
			for _, x := range f[:3] {
				v, _ := strconv.ParseFloat(x, 64)
				out = append(out, v)
			}
			s.LoadAvg = out
		}
	}
	if m := run("/usr/sbin/sysctl", "-n", "hw.memsize"); m != "" {
		if v, err := strconv.ParseInt(m, 10, 64); err == nil {
			s.MemTotalMB = i64(v / (1024 * 1024))
		}
	}
}

func diskFreeMB(dir string) (int64, bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, false
	}
	return int64(st.Bavail) * int64(st.Bsize) / (1024 * 1024), true
}

func isPhysical(string) bool                  { return false }
func isWireless(string) bool                  { return false }
func ifaceCounters(string) (*uint64, *uint64) { return nil, nil }
