package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ip(v int) *int         { return &v }
func sptr(s string) *string { return &s }

func TestDefaultIsValid(t *testing.T) {
	c := Default("pi")
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(c.Targets) != 3 || c.Targets[0].Host != GatewayHost || c.Targets[0].Group != "baseline" {
		t.Fatalf("unexpected default targets: %+v", c.Targets)
	}
}

func TestValidateErrors(t *testing.T) {
	cases := map[string]func(c *Config){
		"interval too small": func(c *Config) { c.IntervalMs = 499 },
		"timeout too small":  func(c *Config) { c.TimeoutMs = 199 },
		"timeout > 5x":       func(c *Config) { c.IntervalMs = 500; c.TimeoutMs = 2501 },
		"raw retention 0":    func(c *Config) { c.RetentionRawHours = 0 },
		"rollup retention 0": func(c *Config) { c.RetentionRollupDays = 0 },
		"dup id":             func(c *Config) { c.Targets[1].ID = c.Targets[0].ID },
		"bad id":             func(c *Config) { c.Targets[0].ID = "has space" },
		"bad kind":           func(c *Config) { c.Targets[0].Kind = "udp" },
		"tcp no port":        func(c *Config) { c.Targets[0].Kind = KindTCP },
		"tcp bad port":       func(c *Config) { c.Targets[0].Kind = KindTCP; c.Targets[0].Port = ip(70000) },
		"http no url":        func(c *Config) { c.Targets[0].Kind = KindHTTP },
		"http ftp url":       func(c *Config) { c.Targets[0].Kind = KindHTTP; c.Targets[0].URL = sptr("ftp://x") },
		"dns bad resolver":   func(c *Config) { c.Targets[1].Kind = KindDNS; c.Targets[1].Resolver = sptr("dns.google") },
		"dns gateway":        func(c *Config) { c.Targets[0].Kind = KindDNS },
		"icmp empty host":    func(c *Config) { c.Targets[0].Host = "" },
		"target interval":    func(c *Config) { c.Targets[0].IntervalMs = ip(100) },
		"downAfter 0":        func(c *Config) { c.Rules.DownAfterFailures = 0 },
		"bad webhook":        func(c *Config) { c.Rules.WebhookURL = "not a url" },
		"speedtest interval": func(c *Config) { c.Speedtest.Enabled = true; c.Speedtest.IntervalMin = 1 },
	}
	for name, mut := range cases {
		c := Default("pi")
		mut(&c)
		err := c.Validate()
		if err == nil {
			t.Errorf("%s: expected error", name)
			continue
		}
		if _, ok := err.(*ValidationError); !ok {
			t.Errorf("%s: want *ValidationError, got %T", name, err)
		}
	}
}

func TestValidateAcceptsKinds(t *testing.T) {
	c := Default("pi")
	c.Targets = []Target{
		{ID: "a", Kind: KindTCP, Host: "1.1.1.1", Port: ip(443)},
		{ID: "b", Kind: KindHTTP, URL: sptr("https://example.com")},
		{ID: "c", Kind: KindDNS, Host: "example.com", Resolver: sptr("1.1.1.1")},
		{ID: "d", Kind: KindDNS, Host: "example.com", Resolver: sptr("[2606:4700::1111]:53")},
		{ID: "e", Kind: KindICMP, Host: "@gateway", IntervalMs: ip(500)},
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, tg := range c.Targets {
		if !tg.IsEnabled() || tg.Name == "" {
			t.Errorf("target %s not normalised: %+v", tg.ID, tg)
		}
	}
	c.Targets = nil
	if err := c.Validate(); err != nil || c.Targets == nil {
		t.Fatalf("empty target list must be valid: %v", err)
	}
}

func TestDecodeDefaultsAndUnknownFields(t *testing.T) {
	c, err := Decode([]byte(`{"nodeName":"X","unknownField":1,"targets":[{"id":"t1","kind":"icmp","host":"1.1.1.1","enabled":null}],"rules":{"downAfterFailures":5}}`), "host")
	if err != nil {
		t.Fatal(err)
	}
	if c.IntervalMs != 2000 || c.TimeoutMs != 2000 || c.Rules.DownAfterFailures != 5 || c.Rules.DegradedLatencyMs != 150 {
		t.Fatalf("defaults not applied: %+v", c)
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if !c.Targets[0].IsEnabled() {
		t.Fatal("null enabled should mean enabled")
	}
	c2, _ := Decode([]byte(`{"nodeName":"X"}`), "host")
	if c2.Targets == nil || len(c2.Targets) != 0 {
		t.Fatal("absent targets should be an empty list")
	}
	if _, err := Decode([]byte(`{`), "h"); err == nil {
		t.Fatal("expected JSON error")
	}
}

func TestSaveAtomic0600(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	f := File{Config: Default("pi"), Clients: []ClientRecord{{ID: "c-1", Name: "mac", TokenSHA256: strings.Repeat("a", 64)}}}
	if err := Save(p, f); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode().Perm())
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Fatalf("temp files left behind: %v", ents)
	}
	g, ok, err := Load(p)
	if err != nil || !ok || len(g.Clients) != 1 || g.Clients[0].TokenSHA256 != f.Clients[0].TokenSHA256 || len(g.Targets) != 3 {
		t.Fatalf("roundtrip failed: %v %v %+v", ok, err, g)
	}
}
