package probe

import (
	"bufio"
	"encoding/hex"
	"errors"
	"net"
	"strconv"
	"strings"
)

// ParseProcNetRoute finds the lowest-metric default route in /proc/net/route.
func ParseProcNetRoute(sc *bufio.Scanner) (net.IP, string, error) {
	var best net.IP
	bestIface := ""
	bestMetric := int64(-1)
	first := true
	for sc.Scan() {
		if first {
			first = false
			continue
		}
		fs := strings.Fields(sc.Text())
		if len(fs) < 8 || fs[1] != "00000000" || fs[7] != "00000000" {
			continue
		}
		flags, _ := strconv.ParseInt(fs[3], 16, 64)
		if flags&0x1 == 0 || flags&0x2 == 0 { // RTF_UP, RTF_GATEWAY
			continue
		}
		b, err := hex.DecodeString(fs[2])
		if err != nil || len(b) != 4 {
			continue
		}
		metric, _ := strconv.ParseInt(fs[6], 10, 64)
		if best == nil || metric < bestMetric {
			best = net.IPv4(b[3], b[2], b[1], b[0]).To4()
			bestIface = fs[0]
			bestMetric = metric
		}
	}
	if best == nil {
		return nil, "", errors.New("no default route")
	}
	return best, bestIface, nil
}

// ParseRoutePrint parses Windows `route print -4 0.0.0.0` output and returns
// the lowest-metric default gateway and the local interface address it uses.
// Only the numeric data lines are looked at, so localized headers don't matter;
// "On-link" default routes (no gateway) are skipped, and the 4-column
// "Persistent Routes" lines are ignored.
func ParseRoutePrint(out string) (gw net.IP, ifaceAddr net.IP, err error) {
	bestMetric := int64(-1)
	for _, line := range strings.Split(out, "\n") {
		fs := strings.Fields(line)
		if len(fs) != 5 || fs[0] != "0.0.0.0" || fs[1] != "0.0.0.0" {
			continue
		}
		g := net.ParseIP(fs[2]).To4()
		ifa := net.ParseIP(fs[3]).To4()
		metric, perr := strconv.ParseInt(fs[4], 10, 64)
		if g == nil || ifa == nil || perr != nil || g.IsUnspecified() {
			continue
		}
		if gw == nil || metric < bestMetric {
			gw, ifaceAddr, bestMetric = g, ifa, metric
		}
	}
	if gw == nil {
		return nil, nil, errors.New("no default route")
	}
	return gw, ifaceAddr, nil
}
