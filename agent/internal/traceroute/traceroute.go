// Package traceroute implements ICMP TTL traceroute: over a raw socket with a
// fallback to the system traceroute(8) binary (macOS / no CAP_NET_RAW) on
// Unix, and through IcmpSendEcho with a fallback to tracert.exe on Windows.
package traceroute

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"imonitord/internal/probe"
)

const (
	ProbesPerHop = 3
	ProbeTimeout = time.Second
	MaxDuration  = 30 * time.Second
	DefaultHops  = 20
	MaxHops      = 30
)

type Hop struct {
	TTL     int        `json:"ttl"`
	IP      *string    `json:"ip"`
	Name    *string    `json:"name"`
	RttMs   []*float64 `json:"rttMs"`
	Reached bool       `json:"reached"`
}

type Result struct {
	Host string `json:"host"`
	IP   string `json:"ip"`
	Hops []Hop  `json:"hops"`
}

// Run resolves host and traces it.
func Run(ctx context.Context, host string, maxHops int) (*Result, error) {
	if maxHops <= 0 {
		maxHops = DefaultHops
	}
	if maxHops > MaxHops {
		maxHops = MaxHops
	}
	ctx, cancel := context.WithTimeout(ctx, MaxDuration)
	defer cancel()
	var ip net.IP
	if host == "@gateway" {
		gw, _, err := probe.DefaultRoute()
		if err != nil {
			return nil, err
		}
		ip = gw
	} else if ip = net.ParseIP(host); ip == nil {
		ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
		if err != nil || len(ips) == 0 {
			return nil, errors.New("cannot resolve host")
		}
		ip = ips[0]
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return nil, errors.New("only IPv4 is supported")
	}
	res := &Result{Host: host, IP: ip4.String()}
	hops, err := trace(ctx, ip4, maxHops)
	if err != nil {
		return nil, err
	}
	resolveNames(ctx, hops)
	res.Hops = hops
	return res, nil
}

// ParseTracerouteOutput parses `traceroute -n -q 3` output.
func ParseTracerouteOutput(out, dst string) []Hop {
	hops := []Hop{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		fs := strings.Fields(sc.Text())
		if len(fs) < 2 {
			continue
		}
		start := 1
		var hop Hop
		if ttl, err := strconv.Atoi(fs[0]); err == nil {
			hop = Hop{TTL: ttl, RttMs: []*float64{}}
		} else if net.ParseIP(fs[0]) != nil && len(hops) > 0 && !hops[len(hops)-1].Reached {
			// continuation line: another responder for the previous hop
			hop = hops[len(hops)-1]
			hops = hops[:len(hops)-1]
			start = 0
		} else {
			continue // header line
		}
		for i := start; i < len(fs); i++ {
			tok := fs[i]
			switch {
			case tok == "*":
				hop.RttMs = append(hop.RttMs, nil)
			case net.ParseIP(tok) != nil:
				if hop.IP == nil {
					s := tok
					hop.IP = &s
				}
			case i+1 < len(fs) && fs[i+1] == "ms":
				if v, err := strconv.ParseFloat(tok, 64); err == nil {
					hop.RttMs = append(hop.RttMs, &v)
				}
				i++
			}
		}
		if hop.IP != nil && *hop.IP == dst {
			hop.Reached = true
		}
		hops = append(hops, hop)
		if hop.Reached {
			break
		}
	}
	return hops
}

// resolveNames does reverse DNS for hop IPs in parallel (bounded to ~1.5 s).
func resolveNames(ctx context.Context, hops []Hop) {
	ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i := range hops {
		if hops[i].IP == nil {
			continue
		}
		wg.Add(1)
		go func(h *Hop, ip string) {
			defer wg.Done()
			names, err := net.DefaultResolver.LookupAddr(ctx, ip)
			if err == nil && len(names) > 0 {
				n := strings.TrimSuffix(names[0], ".")
				mu.Lock()
				h.Name = &n
				mu.Unlock()
			}
		}(&hops[i], *hops[i].IP)
	}
	wg.Wait()
}
