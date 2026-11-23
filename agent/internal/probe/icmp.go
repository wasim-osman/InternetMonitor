package probe

import (
	"encoding/binary"
	"net"
	"time"
)

// StripIPv4Header removes a leading IPv4 header if present (raw sockets on
// some platforms and macOS datagram ICMP sockets include it).
func StripIPv4Header(b []byte) []byte {
	if len(b) >= 20 && b[0]>>4 == 4 {
		ihl := int(b[0]&0x0f) * 4
		if ihl >= 20 && len(b) >= ihl {
			return b[ihl:]
		}
	}
	return b
}

func addrIP(a net.Addr) net.IP {
	switch v := a.(type) {
	case *net.UDPAddr:
		return v.IP
	case *net.IPAddr:
		return v.IP
	}
	return nil
}

// Checksum is the internet checksum (RFC 1071).
func Checksum(b []byte) uint16 {
	var s uint32
	for i := 0; i+1 < len(b); i += 2 {
		s += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 == 1 {
		s += uint32(b[len(b)-1]) << 8
	}
	for s>>16 != 0 {
		s = (s & 0xffff) + (s >> 16)
	}
	return ^uint16(s)
}

// BuildEcho builds an ICMP echo request with a 16-byte payload.
func BuildEcho(pkt []byte, id, seq uint16, token uint64) []byte {
	pkt = pkt[:24]
	pkt[0], pkt[1] = 8, 0
	pkt[2], pkt[3] = 0, 0
	binary.BigEndian.PutUint16(pkt[4:], id)
	binary.BigEndian.PutUint16(pkt[6:], seq)
	binary.BigEndian.PutUint64(pkt[8:], token)
	binary.BigEndian.PutUint64(pkt[16:], uint64(time.Now().UnixNano()))
	cs := Checksum(pkt)
	binary.BigEndian.PutUint16(pkt[2:], cs)
	return pkt
}
