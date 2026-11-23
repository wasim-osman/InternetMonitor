// Package sysinfo gathers host diagnostics for /v1/system.
package sysinfo

import (
	"bufio"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"

	"imonitord/internal/probe"
)

type Interface struct {
	Name    string   `json:"name"`
	Kind    string   `json:"kind"`
	Addrs   []string `json:"addrs"`
	Up      bool     `json:"up"`
	RxBytes *uint64  `json:"rxBytes"`
	TxBytes *uint64  `json:"txBytes"`
}

type Wifi struct {
	Iface       string   `json:"iface"`
	SSID        *string  `json:"ssid"`
	RssiDbm     *float64 `json:"rssiDbm"`
	LinkQuality *float64 `json:"linkQuality"`
}

type System struct {
	Hostname         string      `json:"hostname"`
	OS               string      `json:"os"`
	Kernel           *string     `json:"kernel"`
	Arch             string      `json:"arch"`
	Model            *string     `json:"model"`
	UptimeSec        *int64      `json:"uptimeSec"`
	LoadAvg          []float64   `json:"loadAvg"`
	MemTotalMB       *int64      `json:"memTotalMB"`
	MemUsedMB        *int64      `json:"memUsedMB"`
	DiskFreeMB       *int64      `json:"diskFreeMB"`
	TempC            *float64    `json:"tempC"`
	Interfaces       []Interface `json:"interfaces"`
	Gateway          *string     `json:"gateway"`
	DefaultInterface *string     `json:"defaultInterface"`
	Wifi             *Wifi       `json:"wifi"`
	Tailscale        *Tailscale  `json:"tailscale"`
}

func i64(v int64) *int64     { return &v }
func f64(v float64) *float64 { return &v }
func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Collect gathers everything; stateDir is used for diskFreeMB.
func Collect(stateDir string, ts *TailscaleCache) System {
	host, _ := os.Hostname()
	s := System{Hostname: host, OS: runtime.GOOS, Arch: runtime.GOARCH, LoadAvg: nil, Interfaces: []Interface{}}
	collectOS(&s)
	if free, ok := diskFreeMB(stateDir); ok {
		s.DiskFreeMB = i64(free)
	}
	if gw, iface, err := probe.DefaultRoute(); err == nil {
		s.Gateway = strPtr(gw.String())
		s.DefaultInterface = strPtr(iface)
	} else if iface != "" {
		s.DefaultInterface = strPtr(iface)
	}
	s.Interfaces = interfaces()
	if ts != nil {
		s.Tailscale = ts.Get(true)
	}
	if s.LoadAvg == nil {
		s.LoadAvg = []float64{}
	}
	return s
}

func interfaces() []Interface {
	out := []Interface{}
	ifs, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, ifc := range ifs {
		var addrs []string
		hasTS := false
		if as, err := ifc.Addrs(); err == nil {
			for _, a := range as {
				ipn, ok := a.(*net.IPNet)
				if !ok {
					continue
				}
				ip := ipn.IP
				if ip.IsLinkLocalUnicast() {
					continue
				}
				if ip4 := ip.To4(); ip4 != nil && ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
					hasTS = true
				}
				addrs = append(addrs, ip.String())
			}
		}
		physical := isPhysical(ifc.Name)
		if len(addrs) == 0 && !physical {
			continue
		}
		if addrs == nil {
			addrs = []string{}
		}
		it := Interface{Name: ifc.Name, Addrs: addrs, Up: ifc.Flags&net.FlagUp != 0}
		switch {
		case ifc.Flags&net.FlagLoopback != 0:
			it.Kind = "loopback"
		case strings.HasPrefix(strings.ToLower(ifc.Name), "tailscale") || hasTS:
			it.Kind = "tailscale"
		case isWireless(ifc.Name):
			it.Kind = "wifi"
		case strings.HasPrefix(strings.ToLower(ifc.Name), "eth") || strings.HasPrefix(ifc.Name, "en"):
			it.Kind = "ethernet"
		default:
			it.Kind = "other"
		}
		it.RxBytes, it.TxBytes = ifaceCounters(ifc.Name)
		out = append(out, it)
	}
	return out
}

// ParseProcNetWireless parses /proc/net/wireless; linkQuality is converted to
// a 0-100 percentage of the conventional 70 maximum.
func ParseProcNetWireless(data string) *Wifi {
	sc := bufio.NewScanner(strings.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" || strings.HasPrefix(name, "Inter") || strings.HasPrefix(name, "face") {
			continue
		}
		fs := strings.Fields(rest)
		if len(fs) < 3 {
			continue
		}
		w := &Wifi{Iface: name}
		if q, err := strconv.ParseFloat(strings.TrimRight(fs[1], "."), 64); err == nil {
			pct := q / 70 * 100
			if pct > 100 {
				pct = 100
			}
			w.LinkQuality = f64(float64(int(pct + 0.5)))
		}
		if lvl, err := strconv.ParseFloat(strings.TrimRight(fs[2], "."), 64); err == nil {
			if lvl > 0 { // some drivers report unsigned dBm
				lvl -= 256
			}
			w.RssiDbm = f64(lvl)
		}
		return w
	}
	return nil
}
