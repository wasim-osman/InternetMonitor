package probe

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"strconv"
	"syscall"
	"time"
	"unsafe"
)

// Windows has no unprivileged ICMP socket, so pings go through
// iphlpapi!IcmpSendEcho, which works for normal users and services alike
// (it is what ping.exe and tracert.exe use).

var (
	modiphlpapi         = syscall.NewLazyDLL("iphlpapi.dll")
	procIcmpCreateFile  = modiphlpapi.NewProc("IcmpCreateFile")
	procIcmpCloseHandle = modiphlpapi.NewProc("IcmpCloseHandle")
	procIcmpSendEcho    = modiphlpapi.NewProc("IcmpSendEcho")
	procGetBestRoute    = modiphlpapi.NewProc("GetBestRoute")
)

// IP_STATUS values returned in ICMP_ECHO_REPLY.Status / GetLastError.
const (
	IPSuccess             = 0
	IPDestNetUnreachable  = 11002
	IPDestHostUnreachable = 11003
	IPDestProtUnreachable = 11004
	IPDestPortUnreachable = 11005
	IPReqTimedOut         = 11010
	IPTTLExpiredTransit   = 11013
	IPTTLExpiredReassem   = 11014
)

// ipOptionInformation mirrors IP_OPTION_INFORMATION (native layout).
type ipOptionInformation struct {
	TTL         uint8
	Tos         uint8
	Flags       uint8
	OptionsSize uint8
	OptionsData uintptr
}

// icmpEchoReply mirrors ICMP_ECHO_REPLY (native layout; on 64-bit Windows
// IcmpSendEcho fills the 64-bit variant, on 32-bit the 32-bit one, and Go's
// struct layout matches the C one on each).
type icmpEchoReply struct {
	Address       uint32
	Status        uint32
	RoundTripTime uint32
	DataSize      uint16
	Reserved      uint16
	Data          uintptr
	Options       ipOptionInformation
}

const invalidHandle = ^uintptr(0)

// EchoReply is the outcome of one IcmpSendEcho call.
type EchoReply struct {
	From   net.IP        // responder (destination, or the router for TTL expired)
	Status uint32        // IP_STATUS code
	RTT    time.Duration // wall-clock round trip measured around the call
}

// IcmpEcho sends one ICMP echo request to ip4 with the given TTL (0 = system
// default) and waits up to timeout. It blocks the calling OS thread, so call
// it from its own goroutine when it has to be cancellable.
func IcmpEcho(ip4 net.IP, ttl int, timeout time.Duration) (EchoReply, error) {
	ip4 = ip4.To4()
	if ip4 == nil {
		return EchoReply{}, errors.New("icmp: only IPv4 targets are supported")
	}
	if err := modiphlpapi.Load(); err != nil {
		return EchoReply{}, err
	}
	h, _, e := procIcmpCreateFile.Call()
	if h == invalidHandle || h == 0 {
		return EchoReply{}, errors.New("IcmpCreateFile: " + e.Error())
	}
	defer procIcmpCloseHandle.Call(h)

	var req [32]byte
	copy(req[:], "imonitord-icmp-echo-payload-0000")
	// Reply buffer: one ICMP_ECHO_REPLY + request data + 8 bytes for an ICMP
	// error + 8 bytes slack (per the IcmpSendEcho docs), generously rounded up.
	var reply [256]uint64 // uint64 for 8-byte alignment
	var opts *ipOptionInformation
	if ttl > 0 {
		opts = &ipOptionInformation{TTL: uint8(ttl)}
	}
	ms := timeout.Milliseconds()
	if ms < 1 {
		ms = 1
	}
	dst := binary.LittleEndian.Uint32(ip4) // IPAddr is in network order in memory
	start := time.Now()
	n, _, callErr := procIcmpSendEcho.Call(h,
		uintptr(dst),
		uintptr(unsafe.Pointer(&req[0])), uintptr(len(req)),
		uintptr(unsafe.Pointer(opts)),
		uintptr(unsafe.Pointer(&reply[0])), uintptr(len(reply)*8),
		uintptr(ms))
	rtt := time.Since(start)
	r := (*icmpEchoReply)(unsafe.Pointer(&reply[0]))
	if n == 0 {
		// No reply parsed. GetLastError holds the IP_STATUS (e.g. 11010 timed
		// out); for TTL-expired some Windows builds still fill the buffer.
		code := uint32(0)
		if en, ok := callErr.(syscall.Errno); ok {
			code = uint32(en)
		}
		if r.Address != 0 && r.Status >= 11000 {
			code = r.Status
		}
		switch {
		case code == 0 || code == IPReqTimedOut:
			return EchoReply{Status: IPReqTimedOut, RTT: rtt}, errTimeout
		case code < 11000:
			return EchoReply{RTT: rtt}, errors.New("IcmpSendEcho: " + callErr.Error())
		}
		var from net.IP
		if r.Address != 0 {
			from = ipFromAddr(r.Address)
		}
		return EchoReply{From: from, Status: code, RTT: rtt}, nil
	}
	return EchoReply{From: ipFromAddr(r.Address), Status: r.Status, RTT: rtt}, nil
}

func ipFromAddr(a uint32) net.IP {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], a)
	return net.IPv4(b[0], b[1], b[2], b[3]).To4()
}

// IPStatusText describes an IP_STATUS code.
func IPStatusText(s uint32) string {
	switch s {
	case IPSuccess:
		return "ok"
	case IPDestNetUnreachable:
		return "network unreachable"
	case IPDestHostUnreachable:
		return "host unreachable"
	case IPDestProtUnreachable:
		return "protocol unreachable"
	case IPDestPortUnreachable:
		return "port unreachable"
	case IPReqTimedOut:
		return "timeout"
	case IPTTLExpiredTransit, IPTTLExpiredReassem:
		return "ttl expired"
	}
	return "icmp status " + strconv.FormatUint(uint64(s), 10)
}

// Pinger sends pings through IcmpSendEcho (no socket to share on Windows).
type Pinger struct{}

func NewPinger() *Pinger { return &Pinger{} }

// Mode reports "iphlpapi" ("" if the DLL cannot be loaded).
func (p *Pinger) Mode() string {
	if modiphlpapi.Load() != nil {
		return ""
	}
	return "iphlpapi"
}

// Ping sends one echo request and waits for the reply.
func (p *Pinger) Ping(ctx context.Context, ip net.IP, timeout time.Duration) (time.Duration, error) {
	ip4 := ip.To4()
	if ip4 == nil {
		return 0, errors.New("icmp: only IPv4 targets are supported")
	}
	if d, ok := ctx.Deadline(); ok {
		if left := time.Until(d); left < timeout {
			timeout = left
		}
	}
	if timeout <= 0 {
		return 0, errTimeout
	}
	type res struct {
		r   EchoReply
		err error
	}
	ch := make(chan res, 1)
	go func() {
		r, err := IcmpEcho(ip4, 0, timeout)
		ch <- res{r, err}
	}()
	select {
	case v := <-ch:
		if v.err != nil {
			return 0, v.err
		}
		if v.r.Status != IPSuccess {
			if v.r.Status == IPReqTimedOut {
				return 0, errTimeout
			}
			return 0, errors.New(IPStatusText(v.r.Status))
		}
		return v.r.RTT, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

// Close is a no-op on Windows (handles are per call).
func (p *Pinger) Close() {}
