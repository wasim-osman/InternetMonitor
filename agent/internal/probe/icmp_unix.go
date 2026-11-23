//go:build !windows

package probe

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"net"
	"os"
	"sync"
	"syscall"
	"time"
)

// Pinger shares one ICMP socket between all icmp targets. It prefers a raw
// socket (root / CAP_NET_RAW) and falls back to the unprivileged datagram ICMP
// socket (macOS; Linux when ping_group_range allows it).
type Pinger struct {
	mu      sync.Mutex
	conn    net.PacketConn
	raw     bool
	id      uint16
	token   uint64
	seq     uint16
	pending map[uint16]*pendingPing
	lastErr error
	lastTry time.Time
}

type pendingPing struct {
	dst net.IP
	ch  chan time.Time
}

func NewPinger() *Pinger {
	var b [10]byte
	rand.Read(b[:])
	return &Pinger{
		id:      binary.BigEndian.Uint16(b[0:2]),
		token:   binary.BigEndian.Uint64(b[2:10]),
		pending: make(map[uint16]*pendingPing),
	}
}

// Mode reports "raw", "dgram" or "" (unavailable).
func (p *Pinger) Mode() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn == nil {
		return ""
	}
	if p.raw {
		return "raw"
	}
	return "dgram"
}

// OpenICMPDgram opens an unprivileged SOCK_DGRAM/IPPROTO_ICMP socket.
func OpenICMPDgram() (net.PacketConn, error) {
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM, syscall.IPPROTO_ICMP)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "icmp-dgram")
	defer f.Close()
	c, err := net.FilePacketConn(f)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (p *Pinger) ensureLocked() error {
	if p.conn != nil {
		return nil
	}
	if !p.lastTry.IsZero() && time.Since(p.lastTry) < 30*time.Second {
		return p.lastErr
	}
	p.lastTry = time.Now()
	if c, err := net.ListenPacket("ip4:icmp", "0.0.0.0"); err == nil {
		p.conn, p.raw = c, true
	} else if c, err2 := OpenICMPDgram(); err2 == nil {
		p.conn, p.raw = c, false
	} else {
		p.lastErr = errors.New("icmp unavailable (need CAP_NET_RAW or net.ipv4.ping_group_range): " + err2.Error())
		return p.lastErr
	}
	go p.readLoop(p.conn)
	return nil
}

func (p *Pinger) readLoop(c net.PacketConn) {
	buf := make([]byte, 1600)
	for {
		n, from, err := c.ReadFrom(buf)
		now := time.Now()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		b := StripIPv4Header(buf[:n])
		if len(b) < 8+16 || b[0] != 0 { // echo reply
			continue
		}
		if binary.BigEndian.Uint64(b[8:16]) != p.token {
			continue
		}
		seq := binary.BigEndian.Uint16(b[6:8])
		src := addrIP(from)
		p.mu.Lock()
		pd := p.pending[seq]
		if pd != nil && (src == nil || pd.dst.Equal(src)) {
			delete(p.pending, seq)
			select {
			case pd.ch <- now:
			default:
			}
		}
		p.mu.Unlock()
	}
}

// Ping sends one echo request and waits for the reply.
func (p *Pinger) Ping(ctx context.Context, ip net.IP, timeout time.Duration) (time.Duration, error) {
	ip4 := ip.To4()
	if ip4 == nil {
		return 0, errors.New("icmp: only IPv4 targets are supported")
	}
	p.mu.Lock()
	if err := p.ensureLocked(); err != nil {
		p.mu.Unlock()
		return 0, err
	}
	conn, raw := p.conn, p.raw
	p.seq++
	seq := p.seq
	pd := &pendingPing{dst: ip4, ch: make(chan time.Time, 1)}
	p.pending[seq] = pd
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.pending, seq)
		p.mu.Unlock()
	}()

	var pktBuf [24]byte
	pkt := BuildEcho(pktBuf[:], p.id, seq, p.token)
	var dst net.Addr
	if raw {
		dst = &net.IPAddr{IP: ip4}
	} else {
		dst = &net.UDPAddr{IP: ip4}
	}
	sent := time.Now()
	if _, err := conn.WriteTo(pkt, dst); err != nil {
		return 0, cleanErr(err)
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case at := <-pd.ch:
		return at.Sub(sent), nil
	case <-timer.C:
		return 0, errTimeout
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

// Close closes the socket.
func (p *Pinger) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn != nil {
		p.conn.Close()
		p.conn = nil
	}
}
