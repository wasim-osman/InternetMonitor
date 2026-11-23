package traceroute

import (
	"context"
	"errors"
	"net"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"

	"imonitord/internal/probe"
)

// trace uses IcmpSendEcho with increasing TTL (unprivileged on Windows) and
// falls back to tracert.exe if the API is unusable.
func trace(ctx context.Context, dst net.IP, maxHops int) ([]Hop, error) {
	hops, err := icmpTrace(ctx, dst, maxHops)
	if err != nil {
		return execTracert(ctx, dst, maxHops)
	}
	return hops, nil
}

type echoResult struct {
	r   probe.EchoReply
	err error
}

func icmpTrace(ctx context.Context, dst net.IP, maxHops int) ([]Hop, error) {
	hops := []Hop{}
	for ttl := 1; ttl <= maxHops; ttl++ {
		if ctx.Err() != nil {
			break
		}
		// The probes of one hop run concurrently (each IcmpSendEcho blocks).
		res := make([]echoResult, ProbesPerHop)
		var wg sync.WaitGroup
		for p := 0; p < ProbesPerHop; p++ {
			wg.Add(1)
			go func(p int) {
				defer wg.Done()
				r, err := probe.IcmpEcho(dst, ttl, ProbeTimeout)
				res[p] = echoResult{r, err}
			}(p)
		}
		wg.Wait()
		hop := Hop{TTL: ttl, RttMs: make([]*float64, 0, ProbesPerHop)}
		hardErrs := 0
		for _, e := range res {
			if e.err != nil {
				if e.r.Status != probe.IPReqTimedOut {
					hardErrs++
				}
				hop.RttMs = append(hop.RttMs, nil)
				continue
			}
			switch e.r.Status {
			case probe.IPSuccess, probe.IPTTLExpiredTransit, probe.IPTTLExpiredReassem,
				probe.IPDestNetUnreachable, probe.IPDestHostUnreachable,
				probe.IPDestProtUnreachable, probe.IPDestPortUnreachable:
				rtt := float64(e.r.RTT.Microseconds()) / 1000
				hop.RttMs = append(hop.RttMs, &rtt)
				if hop.IP == nil && e.r.From != nil {
					s := e.r.From.String()
					hop.IP = &s
				}
				if e.r.Status != probe.IPTTLExpiredTransit && e.r.Status != probe.IPTTLExpiredReassem {
					hop.Reached = true // echo reply or destination unreachable: stop here
				}
			default:
				hop.RttMs = append(hop.RttMs, nil)
			}
		}
		if ttl == 1 && hardErrs == ProbesPerHop {
			return nil, errors.New("IcmpSendEcho unusable: " + res[0].err.Error())
		}
		if hop.IP != nil && *hop.IP == dst.String() {
			hop.Reached = true
		}
		hops = append(hops, hop)
		if hop.Reached {
			break
		}
	}
	return hops, nil
}

func execTracert(ctx context.Context, dst net.IP, maxHops int) ([]Hop, error) {
	bin := "tracert.exe"
	if root, ok := syscall.Getenv("SystemRoot"); ok && root != "" {
		bin = filepath.Join(root, "System32", "tracert.exe")
	}
	cmd := exec.CommandContext(ctx, bin, "-d", "-4", "-h", strconv.Itoa(maxHops), "-w", "1000", dst.String())
	probe.HideWindow(cmd)
	out, err := cmd.Output()
	if len(out) == 0 && err != nil {
		return nil, errors.New("tracert failed: " + err.Error())
	}
	return ParseTracertOutput(string(out), dst.String()), nil
}
