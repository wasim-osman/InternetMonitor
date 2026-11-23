//go:build !windows

package traceroute

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"net"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"imonitord/internal/probe"
)

func trace(ctx context.Context, dst net.IP, maxHops int) ([]Hop, error) {
	hops, err := rawTrace(ctx, dst, maxHops)
	if err != nil {
		return execTrace(ctx, dst, maxHops)
	}
	return hops, nil
}

// rawTrace sends ICMP echo requests with increasing TTL on a raw socket.
func rawTrace(ctx context.Context, dst net.IP, maxHops int) ([]Hop, error) {
	pc, err := net.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		return nil, err
	}
	defer pc.Close()
	conn := pc.(*net.IPConn)
	sc, err := conn.SyscallConn()
	if err != nil {
		return nil, err
	}
	var rb [10]byte
	rand.Read(rb[:])
	id := binary.BigEndian.Uint16(rb[0:2])
	token := binary.BigEndian.Uint64(rb[2:10])
	hops := []Hop{}
	buf := make([]byte, 1600)
	var seq uint16
	var pkt [24]byte
	for ttl := 1; ttl <= maxHops; ttl++ {
		if ctx.Err() != nil {
			break
		}
		var serr error
		if err := sc.Control(func(fd uintptr) {
			serr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_TTL, ttl)
		}); err != nil || serr != nil {
			return nil, errors.New("cannot set TTL")
		}
		hop := Hop{TTL: ttl, RttMs: make([]*float64, 0, ProbesPerHop)}
		for p := 0; p < ProbesPerHop; p++ {
			seq++
			b := probe.BuildEcho(pkt[:], id, seq, token)
			sent := time.Now()
			if _, err := conn.WriteTo(b, &net.IPAddr{IP: dst}); err != nil {
				hop.RttMs = append(hop.RttMs, nil)
				continue
			}
			deadline := sent.Add(ProbeTimeout)
			if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
				deadline = d
			}
			conn.SetReadDeadline(deadline)
			var got *float64
			for {
				n, from, err := conn.ReadFrom(buf)
				if err != nil {
					break
				}
				src, reached, ok := matchReply(probe.StripIPv4Header(buf[:n]), id, seq, token)
				if !ok {
					continue
				}
				if src == nil {
					if ia, ok2 := from.(*net.IPAddr); ok2 {
						src = ia.IP
					}
				}
				rtt := float64(time.Since(sent).Microseconds()) / 1000
				got = &rtt
				if hop.IP == nil && src != nil {
					s := src.String()
					hop.IP = &s
				}
				if reached {
					hop.Reached = true
				}
				break
			}
			hop.RttMs = append(hop.RttMs, got)
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

// matchReply checks an ICMP message (IP header stripped) against our probe.
// Echo replies (type 0) mean the destination was reached; time-exceeded (11)
// and destination-unreachable (3) carry the original header + 8 bytes.
func matchReply(b []byte, id, seq uint16, token uint64) (net.IP, bool, bool) {
	if len(b) < 8 {
		return nil, false, false
	}
	switch b[0] {
	case 0:
		if len(b) < 16 || binary.BigEndian.Uint16(b[4:6]) != id && binary.BigEndian.Uint64(b[8:16]) != token {
			return nil, false, false
		}
		if binary.BigEndian.Uint16(b[6:8]) != seq {
			return nil, false, false
		}
		return nil, true, true
	case 11, 3:
		inner := b[8:]
		if len(inner) < 20 {
			return nil, false, false
		}
		ihl := int(inner[0]&0x0f) * 4
		if len(inner) < ihl+8 || inner[9] != 1 { // protocol ICMP
			return nil, false, false
		}
		orig := inner[ihl:]
		if orig[0] != 8 || binary.BigEndian.Uint16(orig[4:6]) != id || binary.BigEndian.Uint16(orig[6:8]) != seq {
			return nil, false, false
		}
		return nil, b[0] == 3, true
	}
	return nil, false, false
}

var tracerouteBins = []string{"/usr/sbin/traceroute", "/usr/bin/traceroute", "/bin/traceroute", "/sbin/traceroute"}

func execTrace(ctx context.Context, dst net.IP, maxHops int) ([]Hop, error) {
	bin := ""
	if p, err := exec.LookPath("traceroute"); err == nil {
		bin = p
	} else {
		for _, p := range tracerouteBins {
			if _, err := os.Stat(p); err == nil {
				bin = p
				break
			}
		}
	}
	if bin == "" {
		return nil, errors.New("traceroute unavailable: needs CAP_NET_RAW or the traceroute binary")
	}
	cmd := exec.CommandContext(ctx, bin, "-n", "-q", "3", "-w", "1", "-m", strconv.Itoa(maxHops), dst.String())
	out, err := cmd.Output()
	if len(out) == 0 && err != nil {
		return nil, errors.New("traceroute failed: " + err.Error())
	}
	return ParseTracerouteOutput(string(out), dst.String()), nil
}
