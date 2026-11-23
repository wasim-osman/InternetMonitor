package traceroute

import "testing"

func TestParseTracerouteOutput(t *testing.T) {
	out := `traceroute to 1.1.1.1 (1.1.1.1), 20 hops max, 52 byte packets
 1  192.168.50.1  1.234 ms  1.100 ms  *
 2  * * *
 3  10.0.0.1  5.1 ms 10.0.0.2  6.2 ms !H  7.0 ms
 5  142.124.127.68  9.898 ms
    142.124.127.70  8.2 ms
    142.124.127.71  8.4 ms
 4  1.1.1.1  9.0 ms  9.1 ms  9.2 ms
`
	hops := ParseTracerouteOutput(out, "1.1.1.1")
	if len(hops) != 5 || len(hops[3].RttMs) != 3 || *hops[3].IP != "142.124.127.68" {
		t.Fatalf("continuation lines: %+v", hops[3])
	}
	hops = append(hops[:3], hops[4])
	if len(hops) != 4 {
		t.Fatalf("%d hops", len(hops))
	}
	if *hops[0].IP != "192.168.50.1" || len(hops[0].RttMs) != 3 || *hops[0].RttMs[0] != 1.234 || hops[0].RttMs[2] != nil {
		t.Fatalf("hop1 %+v", hops[0])
	}
	if hops[1].IP != nil || len(hops[1].RttMs) != 3 {
		t.Fatalf("hop2 %+v", hops[1])
	}
	if *hops[2].IP != "10.0.0.1" || len(hops[2].RttMs) != 3 {
		t.Fatalf("hop3 %+v", hops[2])
	}
	if !hops[3].Reached || hops[2].Reached {
		t.Fatal("reached flags")
	}
}

func TestParseTracertOutput(t *testing.T) {
	out := "\r\nTracing route to 1.1.1.1 over a maximum of 30 hops\r\n\r\n" +
		"  1    <1 ms    <1 ms    <1 ms  192.168.1.1\r\n" +
		"  2     8 ms     *        9 ms  10.0.0.1\r\n" +
		"  3     *        *        *     Request timed out.\r\n" +
		" 10    12 ms    11 ms    12 ms  1.1.1.1\r\n" +
		"\r\nTrace complete.\r\n"
	hops := ParseTracertOutput(out, "1.1.1.1")
	if len(hops) != 4 {
		t.Fatalf("%d hops: %+v", len(hops), hops)
	}
	if *hops[0].IP != "192.168.1.1" || len(hops[0].RttMs) != 3 || *hops[0].RttMs[0] != 0.5 {
		t.Fatalf("hop1 %+v", hops[0])
	}
	if *hops[1].IP != "10.0.0.1" || len(hops[1].RttMs) != 3 || hops[1].RttMs[1] != nil || *hops[1].RttMs[2] != 9 {
		t.Fatalf("hop2 %+v", hops[1])
	}
	if hops[2].IP != nil || len(hops[2].RttMs) != 3 || hops[2].Reached {
		t.Fatalf("hop3 %+v", hops[2])
	}
	if hops[3].TTL != 10 || !hops[3].Reached || hops[2].Reached {
		t.Fatalf("hop10 %+v", hops[3])
	}

	unreach := "  1    <1 ms    <1 ms    <1 ms  192.168.1.1\n" +
		"  2  192.168.1.1  reports: Destination host unreachable.\n" +
		"  3     *        *        *     Request timed out.\n"
	hops = ParseTracertOutput(unreach, "10.9.9.9")
	if len(hops) != 2 || !hops[1].Reached || *hops[1].IP != "192.168.1.1" || len(hops[1].RttMs) != 0 {
		t.Fatalf("unreachable: %+v", hops)
	}

	// German Windows: header text differs, data lines do not.
	de := "Routenverfolgung zu 8.8.8.8 über maximal 30 Hops\n\n  1     2 ms     1 ms     1 ms  192.168.178.1\n  2    15ms    14ms    16ms  8.8.8.8\n\nAblaufverfolgung beendet.\n"
	hops = ParseTracertOutput(de, "8.8.8.8")
	if len(hops) != 2 || !hops[1].Reached || len(hops[1].RttMs) != 3 || *hops[1].RttMs[0] != 15 {
		t.Fatalf("de: %+v", hops)
	}
}
