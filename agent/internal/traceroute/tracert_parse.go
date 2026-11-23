package traceroute

import (
	"bufio"
	"net"
	"strconv"
	"strings"
)

// ParseTracertOutput parses Windows `tracert -d` output, e.g.
//
//	1    <1 ms    <1 ms    <1 ms  192.168.1.1
//	2     *        8 ms     7 ms  10.0.0.1
//	3     *        *        *     Request timed out.
//	4  192.168.1.1  reports: Destination host unreachable.
//
// Header/footer lines (localized on non-English Windows) are skipped because
// only lines starting with a hop number are looked at. "<1 ms" is reported as
// 0.5 ms.
func ParseTracertOutput(out, dst string) []Hop {
	hops := []Hop{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		fs := strings.Fields(sc.Text())
		if len(fs) < 2 {
			continue
		}
		ttl, err := strconv.Atoi(fs[0])
		if err != nil || ttl <= 0 {
			continue
		}
		hop := Hop{TTL: ttl, RttMs: []*float64{}}
		unreachable := false
		for i := 1; i < len(fs); i++ {
			tok := fs[i]
			switch {
			case tok == "*":
				hop.RttMs = append(hop.RttMs, nil)
			case strings.HasPrefix(tok, "<") && i+1 < len(fs) && isMs(fs[i+1]):
				v := 0.5
				hop.RttMs = append(hop.RttMs, &v)
				i++
			case i+1 < len(fs) && isMs(fs[i+1]):
				if v, err := strconv.ParseFloat(tok, 64); err == nil {
					hop.RttMs = append(hop.RttMs, &v)
				}
				i++
			case strings.HasSuffix(tok, "ms") && len(tok) > 2: // "12ms" (some locales)
				num := strings.TrimSuffix(tok, "ms")
				if strings.HasPrefix(num, "<") {
					v := 0.5
					hop.RttMs = append(hop.RttMs, &v)
				} else if v, err := strconv.ParseFloat(num, 64); err == nil {
					hop.RttMs = append(hop.RttMs, &v)
				}
			default:
				ipTok := strings.Trim(tok, "[]")
				if ip := net.ParseIP(ipTok); ip != nil {
					if hop.IP == nil {
						s := ip.String()
						hop.IP = &s
					}
				} else if strings.HasSuffix(strings.ToLower(tok), "reports:") || strings.Contains(tok, ":") && hop.IP != nil {
					unreachable = true
				}
			}
		}
		if hop.IP != nil && (*hop.IP == dst || unreachable) {
			hop.Reached = true
		}
		hops = append(hops, hop)
		if hop.Reached {
			break
		}
	}
	return hops
}

func isMs(s string) bool { return s == "ms" || s == "мс" }
