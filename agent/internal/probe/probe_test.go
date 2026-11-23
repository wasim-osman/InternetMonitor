package probe

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"imonitord/internal/config"
)

func TestParseProcNetRoute(t *testing.T) {
	data := `Iface	Destination	Gateway 	Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
wlan0	00000000	0132A8C0	0003	0	0	600	00000000	0	0	0
eth0	00000000	0132A8C0	0003	0	0	100	00000000	0	0	0
eth0	0032A8C0	00000000	0001	0	0	100	00FFFFFF	0	0	0
`
	gw, iface, err := ParseProcNetRoute(bufio.NewScanner(strings.NewReader(data)))
	if err != nil || gw.String() != "192.168.50.1" || iface != "eth0" {
		t.Fatalf("%v %v %v", gw, iface, err)
	}
}

func TestChecksumEcho(t *testing.T) {
	var b [24]byte
	pkt := BuildEcho(b[:], 1, 2, 3)
	if Checksum(pkt) != 0 {
		t.Fatal("checksum over a valid packet must be 0")
	}
}

func TestTCPProbe(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	p := NewProber()
	r := p.Probe(context.Background(), &config.Target{Kind: config.KindTCP, Host: "127.0.0.1", Port: &port}, time.Second, &State{})
	if !r.OK || r.Resolved != "127.0.0.1" {
		t.Fatalf("%+v", r)
	}
	ln.Close()
	r = p.Probe(context.Background(), &config.Target{Kind: config.KindTCP, Host: "127.0.0.1", Port: &port}, time.Second, &State{})
	if r.OK || r.Err == "" {
		t.Fatalf("closed port should fail: %+v", r)
	}
}

func TestParseRoutePrint(t *testing.T) {
	out := `===========================================================================
Interface List
 12...00 15 5d 01 02 03 ......Intel(R) Ethernet Connection
 18...3c 22 fb 11 22 33 ......Intel(R) Wi-Fi 6 AX201
  1...........................Software Loopback Interface 1
===========================================================================

IPv4 Route Table
===========================================================================
Active Routes:
Network Destination        Netmask          Gateway       Interface  Metric
          0.0.0.0          0.0.0.0      192.168.1.1    192.168.1.50     35
          0.0.0.0          0.0.0.0     192.168.50.1   192.168.50.23     25
          0.0.0.0          0.0.0.0         On-link       100.64.1.2      5
===========================================================================
Persistent Routes:
  Network Address          Netmask  Gateway Address  Metric
          0.0.0.0          0.0.0.0      10.0.0.1  Default
===========================================================================
`
	gw, ifa, err := ParseRoutePrint(out)
	if err != nil || gw.String() != "192.168.50.1" || ifa.String() != "192.168.50.23" {
		t.Fatalf("%v %v %v", gw, ifa, err)
	}
	// Localized headers, CRLF line endings.
	de := "Aktive Routen:\r\nNetzwerkziel    Netzwerkmaske   Gateway    Schnittstelle Metrik\r\n          0.0.0.0          0.0.0.0     10.1.2.1    10.1.2.33     50\r\n"
	gw, ifa, err = ParseRoutePrint(de)
	if err != nil || gw.String() != "10.1.2.1" || ifa.String() != "10.1.2.33" {
		t.Fatalf("de: %v %v %v", gw, ifa, err)
	}
	if _, _, err := ParseRoutePrint("Persistent Routes:\n  None\n"); err == nil {
		t.Fatal("expected no default route")
	}
}
