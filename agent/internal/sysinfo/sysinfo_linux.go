package sysinfo

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.TrimRight(string(b), "\x00"))
}

func collectOS(s *System) {
	s.Kernel = strPtr(readTrim("/proc/sys/kernel/osrelease"))
	s.Model = strPtr(readTrim("/sys/firmware/devicetree/base/model"))
	if s.Model == nil {
		s.Model = strPtr(readTrim("/sys/devices/virtual/dmi/id/product_name"))
	}
	if f := strings.Fields(readTrim("/proc/uptime")); len(f) > 0 {
		if v, err := strconv.ParseFloat(f[0], 64); err == nil {
			s.UptimeSec = i64(int64(v))
		}
	}
	if f := strings.Fields(readTrim("/proc/loadavg")); len(f) >= 3 {
		la := make([]float64, 0, 3)
		for _, x := range f[:3] {
			v, _ := strconv.ParseFloat(x, 64)
			la = append(la, v)
		}
		s.LoadAvg = la
	}
	if f, err := os.Open("/proc/meminfo"); err == nil {
		var total, avail int64 = -1, -1
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fs := strings.Fields(sc.Text())
			if len(fs) < 2 {
				continue
			}
			v, _ := strconv.ParseInt(fs[1], 10, 64)
			switch fs[0] {
			case "MemTotal:":
				total = v
			case "MemAvailable:":
				avail = v
			}
		}
		f.Close()
		if total > 0 {
			s.MemTotalMB = i64(total / 1024)
			if avail >= 0 {
				s.MemUsedMB = i64((total - avail) / 1024)
			}
		}
	}
	if t := readTrim("/sys/class/thermal/thermal_zone0/temp"); t != "" {
		if v, err := strconv.ParseFloat(t, 64); err == nil {
			s.TempC = f64(float64(int(v/100)) / 10)
		}
	}
	if data, err := os.ReadFile("/proc/net/wireless"); err == nil {
		if w := ParseProcNetWireless(string(data)); w != nil {
			w.SSID = ssid(w.Iface)
			s.Wifi = w
		}
	}
}

func ssid(iface string) *string {
	for _, bin := range []string{"/usr/sbin/iwgetid", "/sbin/iwgetid", "/usr/bin/iwgetid"} {
		if _, err := os.Stat(bin); err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		out, err := exec.CommandContext(ctx, bin, iface, "-r").Output()
		cancel()
		if err == nil {
			return strPtr(strings.TrimSpace(string(out)))
		}
		return nil
	}
	return nil
}

func diskFreeMB(dir string) (int64, bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, false
	}
	return int64(st.Bavail) * int64(st.Bsize) / (1024 * 1024), true
}

func isPhysical(name string) bool {
	_, err := os.Stat("/sys/class/net/" + name + "/device")
	return err == nil
}

func isWireless(name string) bool {
	if _, err := os.Stat("/sys/class/net/" + name + "/wireless"); err == nil {
		return true
	}
	return strings.HasPrefix(name, "wl")
}

func ifaceCounters(name string) (*uint64, *uint64) {
	rd := func(f string) *uint64 {
		v, err := strconv.ParseUint(readTrim("/sys/class/net/"+name+"/statistics/"+f), 10, 64)
		if err != nil {
			return nil
		}
		return &v
	}
	return rd("rx_bytes"), rd("tx_bytes")
}
