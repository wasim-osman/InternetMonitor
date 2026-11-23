package sysinfo

import "testing"

func TestParseProcNetWireless(t *testing.T) {
	data := `Inter-| sta-|   Quality        |   Discarded packets               | Missed | WE
 face | tus | link level noise |  nwid  crypt   frag  retry   misc | beacon | 22
 wlan0: 0000   49.  -61.  -256        0      0      0      0      0        0
`
	w := ParseProcNetWireless(data)
	if w == nil || w.Iface != "wlan0" || *w.RssiDbm != -61 || *w.LinkQuality != 70 {
		t.Fatalf("%+v", w)
	}
}

func TestParseTailscaleStatus(t *testing.T) {
	data := `{"Self":{"TailscaleIPs":["100.100.0.1","fd7a::1"]},"Peer":{
 "k1":{"HostName":"pi-b","TailscaleIPs":["100.100.0.2"],"Online":true,"CurAddr":"1.2.3.4:41641","Relay":"tor"},
 "k2":{"HostName":"phone","TailscaleIPs":["100.70.1.2"],"Online":true,"CurAddr":"","Relay":"fra"},
 "k3":{"HostName":"off","TailscaleIPs":["100.70.1.3"],"Online":false,"CurAddr":"","Relay":""}}}`
	ts, err := ParseTailscaleStatus([]byte(data))
	if err != nil || *ts.SelfIP != "100.100.0.1" || len(ts.Peers) != 3 {
		t.Fatalf("%+v %v", ts, err)
	}
	byName := map[string]TailscalePeer{}
	for _, p := range ts.Peers {
		byName[p.Name] = p
	}
	if p := byName["pi-b"]; *p.Path != "direct" || *p.CurAddr != "1.2.3.4:41641" || p.Relay != nil {
		t.Fatalf("direct peer %+v", p)
	}
	if p := byName["phone"]; *p.Path != "relay" || *p.Relay != "fra" {
		t.Fatalf("relay peer %+v", p)
	}
	if p := byName["off"]; p.Path != nil {
		t.Fatalf("unknown path %+v", p)
	}
}
