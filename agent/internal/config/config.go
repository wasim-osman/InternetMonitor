// Package config holds the agent configuration document, its validation and
// atomic persistence (config.json also carries the hashed client tokens).
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Target kinds.
const (
	KindICMP = "icmp"
	KindTCP  = "tcp"
	KindHTTP = "http"
	KindDNS  = "dns"

	GatewayHost = "@gateway"
)

// Limits applied by Validate.
const (
	MinIntervalMs   = 500
	MaxIntervalMs   = 3_600_000
	MinTimeoutMs    = 200
	MaxTargets      = 200
	MaxRawHours     = 168
	MaxRollupDays   = 365
	MinSpeedtestMin = 5
)

type Target struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Kind       string  `json:"kind"`
	Host       string  `json:"host"`
	Port       *int    `json:"port"`
	URL        *string `json:"url"`
	Resolver   *string `json:"resolver"`
	IntervalMs *int    `json:"intervalMs"`
	Enabled    *bool   `json:"enabled"` // absent/null == true
	Group      string  `json:"group"`
	NodeRef    *string `json:"nodeRef"`
}

// IsEnabled treats null/absent as enabled.
func (t *Target) IsEnabled() bool { return t.Enabled == nil || *t.Enabled }

// ProbeKey identifies what is probed; when it changes the live window is reset.
func (t *Target) ProbeKey() string {
	var b strings.Builder
	b.WriteString(t.Kind)
	b.WriteByte('|')
	b.WriteString(t.Host)
	b.WriteByte('|')
	if t.Port != nil {
		b.WriteString(strconv.Itoa(*t.Port))
	}
	b.WriteByte('|')
	if t.URL != nil {
		b.WriteString(*t.URL)
	}
	b.WriteByte('|')
	if t.Resolver != nil {
		b.WriteString(*t.Resolver)
	}
	return b.String()
}

type Rules struct {
	DownAfterFailures int    `json:"downAfterFailures"`
	DegradedLatencyMs int    `json:"degradedLatencyMs"` // 0 disables the latency rule
	DegradedForSec    int    `json:"degradedForSec"`
	WebhookURL        string `json:"webhookUrl"`
}

type Speedtest struct {
	Enabled     bool   `json:"enabled"`
	IntervalMin int    `json:"intervalMin"`
	URL         string `json:"url"`
}

type Config struct {
	NodeName            string    `json:"nodeName"`
	IntervalMs          int       `json:"intervalMs"`
	TimeoutMs           int       `json:"timeoutMs"`
	RetentionRawHours   int       `json:"retentionRawHours"`
	RetentionRollupDays int       `json:"retentionRollupDays"`
	MetricsPublic       bool      `json:"metricsPublic"`
	Targets             []Target  `json:"targets"`
	Rules               Rules     `json:"rules"`
	Speedtest           Speedtest `json:"speedtest"`
}

// EffectiveIntervalMs returns the target interval or the global default.
func (c *Config) EffectiveIntervalMs(t *Target) int {
	if t != nil && t.IntervalMs != nil && *t.IntervalMs > 0 {
		return *t.IntervalMs
	}
	return c.IntervalMs
}

// TargetByID returns a pointer into c.Targets or nil.
func (c *Config) TargetByID(id string) *Target {
	for i := range c.Targets {
		if c.Targets[i].ID == id {
			return &c.Targets[i]
		}
	}
	return nil
}

func boolPtr(b bool) *bool { return &b }

// Default returns the configuration of a fresh agent.
func Default(nodeName string) Config {
	return Config{
		NodeName:            nodeName,
		IntervalMs:          2000,
		TimeoutMs:           2000,
		RetentionRawHours:   6,
		RetentionRollupDays: 60,
		Targets: []Target{
			{ID: "t-gateway", Name: "Gateway", Kind: KindICMP, Host: GatewayHost, Enabled: boolPtr(true), Group: "baseline"},
			{ID: "t-cloudflare", Name: "Cloudflare DNS", Kind: KindICMP, Host: "1.1.1.1", Enabled: boolPtr(true), Group: "baseline"},
			{ID: "t-google", Name: "Google DNS", Kind: KindICMP, Host: "8.8.8.8", Enabled: boolPtr(true), Group: "baseline"},
		},
		Rules:     Rules{DownAfterFailures: 3, DegradedLatencyMs: 150, DegradedForSec: 60},
		Speedtest: Speedtest{Enabled: false, IntervalMin: 60, URL: "https://speed.cloudflare.com/__down?bytes=25000000"},
	}
}

// Decode parses a PUT body. Absent scalar fields keep their defaults (null and
// absent are equivalent); an absent target list means "no targets".
func Decode(data []byte, nodeName string) (Config, error) {
	c := Default(nodeName)
	c.Targets = nil
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("invalid JSON: %v", err)
	}
	if c.Targets == nil {
		c.Targets = []Target{}
	}
	return c, nil
}

// ValidationError is returned for 400 responses.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func verr(format string, a ...any) error { return &ValidationError{Msg: fmt.Sprintf(format, a...)} }

func validID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.', r == ':':
		default:
			return false
		}
	}
	return true
}

func validHTTPURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func validHost(h string) bool {
	if h == GatewayHost {
		return true
	}
	if h == "" || len(h) > 253 || strings.ContainsAny(h, " /\\\t\n\"") {
		return false
	}
	return true
}

// ParseResolver accepts "ip" or "ip:port" (IPv6 as "[::1]:53" or bare "::1").
func ParseResolver(s string) (string, error) {
	if ip := net.ParseIP(s); ip != nil {
		return net.JoinHostPort(ip.String(), "53"), nil
	}
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		return "", err
	}
	if net.ParseIP(host) == nil {
		return "", errors.New("resolver host must be an IP address")
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return "", errors.New("invalid resolver port")
	}
	return net.JoinHostPort(host, port), nil
}

// Validate checks and normalises c in place.
func (c *Config) Validate() error {
	c.NodeName = strings.TrimSpace(c.NodeName)
	if len(c.NodeName) > 100 {
		return verr("nodeName too long (max 100)")
	}
	if c.IntervalMs < MinIntervalMs || c.IntervalMs > MaxIntervalMs {
		return verr("intervalMs must be between %d and %d", MinIntervalMs, MaxIntervalMs)
	}
	if c.TimeoutMs < MinTimeoutMs {
		return verr("timeoutMs must be >= %d", MinTimeoutMs)
	}
	if c.TimeoutMs > c.IntervalMs*5 {
		return verr("timeoutMs must be <= intervalMs*5")
	}
	if c.RetentionRawHours < 1 || c.RetentionRawHours > MaxRawHours {
		return verr("retentionRawHours must be between 1 and %d", MaxRawHours)
	}
	if c.RetentionRollupDays < 1 || c.RetentionRollupDays > MaxRollupDays {
		return verr("retentionRollupDays must be between 1 and %d", MaxRollupDays)
	}
	if len(c.Targets) > MaxTargets {
		return verr("too many targets (max %d)", MaxTargets)
	}
	if c.Targets == nil {
		c.Targets = []Target{}
	}
	seen := make(map[string]bool, len(c.Targets))
	for i := range c.Targets {
		t := &c.Targets[i]
		if !validID(t.ID) {
			return verr("targets[%d].id invalid (1-64 chars of A-Z a-z 0-9 . _ - :)", i)
		}
		if seen[t.ID] {
			return verr("duplicate target id %q", t.ID)
		}
		seen[t.ID] = true
		t.Host = strings.TrimSpace(t.Host)
		t.Name = strings.TrimSpace(t.Name)
		if len(t.Name) > 100 || len(t.Group) > 64 {
			return verr("target %q: name/group too long", t.ID)
		}
		if t.IntervalMs != nil && (*t.IntervalMs < MinIntervalMs || *t.IntervalMs > MaxIntervalMs) {
			return verr("target %q: intervalMs must be null or between %d and %d", t.ID, MinIntervalMs, MaxIntervalMs)
		}
		switch t.Kind {
		case KindICMP:
			if !validHost(t.Host) {
				return verr("target %q: icmp requires host", t.ID)
			}
		case KindTCP:
			if !validHost(t.Host) {
				return verr("target %q: tcp requires host", t.ID)
			}
			if t.Port == nil || *t.Port < 1 || *t.Port > 65535 {
				return verr("target %q: tcp requires port 1-65535", t.ID)
			}
		case KindHTTP:
			if t.URL == nil || !validHTTPURL(*t.URL) {
				return verr("target %q: http requires an http(s) url", t.ID)
			}
		case KindDNS:
			if t.Host == "" || t.Host == GatewayHost || !validHost(t.Host) {
				return verr("target %q: dns requires host (name to resolve)", t.ID)
			}
			if t.Resolver != nil && *t.Resolver != "" {
				if _, err := ParseResolver(*t.Resolver); err != nil {
					return verr("target %q: resolver must be \"ip\" or \"ip:port\"", t.ID)
				}
			}
		default:
			return verr("target %q: kind must be icmp|tcp|http|dns", t.ID)
		}
		if t.Resolver != nil && *t.Resolver == "" {
			t.Resolver = nil
		}
		if t.Name == "" {
			if t.Kind == KindHTTP && t.URL != nil {
				t.Name = *t.URL
			} else {
				t.Name = t.Host
			}
		}
		if t.Enabled == nil {
			t.Enabled = boolPtr(true)
		}
	}
	r := &c.Rules
	if r.DownAfterFailures < 1 || r.DownAfterFailures > 1000 {
		return verr("rules.downAfterFailures must be between 1 and 1000")
	}
	if r.DegradedLatencyMs < 0 || r.DegradedForSec < 0 || r.DegradedForSec > 86400 {
		return verr("rules.degradedLatencyMs/degradedForSec must be >= 0")
	}
	if r.WebhookURL != "" && !validHTTPURL(r.WebhookURL) {
		return verr("rules.webhookUrl must be empty or an http(s) url")
	}
	s := &c.Speedtest
	if s.IntervalMin < MinSpeedtestMin {
		if s.Enabled {
			return verr("speedtest.intervalMin must be >= %d", MinSpeedtestMin)
		}
		s.IntervalMin = 60
	}
	if s.URL == "" {
		s.URL = Default("").Speedtest.URL
	}
	if !validHTTPURL(s.URL) {
		return verr("speedtest.url must be an http(s) url")
	}
	return nil
}

// Clone deep-copies the config (targets slice and pointers).
func (c Config) Clone() Config {
	out := c
	out.Targets = make([]Target, len(c.Targets))
	for i, t := range c.Targets {
		nt := t
		if t.Port != nil {
			v := *t.Port
			nt.Port = &v
		}
		if t.URL != nil {
			v := *t.URL
			nt.URL = &v
		}
		if t.Resolver != nil {
			v := *t.Resolver
			nt.Resolver = &v
		}
		if t.IntervalMs != nil {
			v := *t.IntervalMs
			nt.IntervalMs = &v
		}
		if t.Enabled != nil {
			v := *t.Enabled
			nt.Enabled = &v
		}
		if t.NodeRef != nil {
			v := *t.NodeRef
			nt.NodeRef = &v
		}
		out.Targets[i] = nt
	}
	return out
}

// ClientRecord is a paired client; only the sha256 of its token is stored.
type ClientRecord struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	TokenSHA256 string `json:"tokenSha256"`
	CreatedAt   int64  `json:"createdAt"`
	LastSeen    int64  `json:"lastSeen"`
}

// File is the on-disk layout of config.json.
type File struct {
	Config
	Clients []ClientRecord `json:"clients"`
}

// Load reads config.json. ok=false means the file does not exist.
func Load(path string) (f File, ok bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return File{}, false, nil
	}
	if err != nil {
		return File{}, false, err
	}
	f.Config = Default("")
	f.Config.Targets = nil
	if err := json.Unmarshal(data, &f); err != nil {
		return File{}, true, err
	}
	if f.Targets == nil {
		f.Targets = []Target{}
	}
	if f.Clients == nil {
		f.Clients = []ClientRecord{}
	}
	return f, true, nil
}

// Save writes config.json atomically with mode 0600.
func Save(path string, f File) error {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(path, append(data, '\n'), 0o600)
}

// WriteFileAtomic writes to a temp file in the same dir, fsyncs and renames.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	cleanup := func() { tmp.Close(); os.Remove(name) }
	if err := tmp.Chmod(perm); err != nil {
		cleanup()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}
