// Package probe implements icmp/tcp/http/dns probes and default-gateway lookup.
package probe

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"imonitord/internal/config"
)

var errTimeout = errors.New("timeout")

// Result of a single probe.
type Result struct {
	OK       bool
	RttMs    float64
	Err      string // set on failure; for http also on success with status >= 500
	Resolved string // IP actually probed (or first DNS answer)
}

// State is per-target scratch kept by the scheduler between probes.
type State struct {
	key      string
	ip       net.IP
	resolved time.Time
	resolver *net.Resolver
}

// Prober runs probes. One instance is shared by all targets.
type Prober interface {
	Probe(ctx context.Context, t *config.Target, timeout time.Duration, st *State) Result
}

type RealProber struct {
	Pinger    *Pinger
	transport *http.Transport
	client    *http.Client
}

func NewProber() *RealProber {
	tr := &http.Transport{
		Proxy:                 nil,
		DisableKeepAlives:     true, // measure a full fresh request each time
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		MaxIdleConns:          0,
		ForceAttemptHTTP2:     false,
		TLSClientConfig:       &tls.Config{},
	}
	return &RealProber{
		Pinger:    NewPinger(),
		transport: tr,
		client: &http.Client{
			Transport: tr,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

const dnsCacheTTL = 60 * time.Second

// resolve returns the IPv4 (preferred) address to probe for host.
func resolve(ctx context.Context, host string, st *State) (net.IP, error) {
	if host == config.GatewayHost {
		gw, _, err := DefaultRoute()
		if err != nil {
			return nil, fmt.Errorf("gateway: %v", err)
		}
		return gw, nil
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip, nil
	}
	if st != nil && st.ip != nil && st.key == host && time.Since(st.resolved) < dnsCacheTTL {
		return st.ip, nil
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("dns: %s", cleanErr(err).Error())
	}
	var pick net.IP
	for _, ip := range ips {
		if ip.To4() != nil {
			pick = ip
			break
		}
	}
	if pick == nil && len(ips) > 0 {
		pick = ips[0]
	}
	if pick == nil {
		return nil, errors.New("dns: no addresses")
	}
	if st != nil {
		st.key, st.ip, st.resolved = host, pick, time.Now()
	}
	return pick, nil
}

func (p *RealProber) Probe(ctx context.Context, t *config.Target, timeout time.Duration, st *State) Result {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	switch t.Kind {
	case config.KindICMP:
		ip, err := resolve(ctx, t.Host, st)
		if err != nil {
			return Result{Err: err.Error()}
		}
		d, err := p.Pinger.Ping(ctx, ip, timeout)
		if err != nil {
			return Result{Err: cleanErr(err).Error(), Resolved: ip.String()}
		}
		return Result{OK: true, RttMs: ms(d), Resolved: ip.String()}
	case config.KindTCP:
		ip, err := resolve(ctx, t.Host, st)
		if err != nil {
			return Result{Err: err.Error()}
		}
		port := 0
		if t.Port != nil {
			port = *t.Port
		}
		var d net.Dialer
		start := time.Now()
		c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), strconv.Itoa(port)))
		el := time.Since(start)
		if err != nil {
			return Result{Err: cleanErr(err).Error(), Resolved: ip.String()}
		}
		c.Close()
		return Result{OK: true, RttMs: ms(el), Resolved: ip.String()}
	case config.KindHTTP:
		return p.probeHTTP(ctx, t)
	case config.KindDNS:
		return probeDNS(ctx, t, st)
	}
	return Result{Err: "unknown kind"}
}

func (p *RealProber) probeHTTP(ctx context.Context, t *config.Target) Result {
	if t.URL == nil {
		return Result{Err: "no url"}
	}
	var remote string
	var first time.Time
	trace := &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			if a, ok := info.Conn.RemoteAddr().(*net.TCPAddr); ok {
				remote = a.IP.String()
			}
		},
		GotFirstResponseByte: func() { first = time.Now() },
	}
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodGet, *t.URL, nil)
	if err != nil {
		return Result{Err: err.Error()}
	}
	req.Header.Set("User-Agent", "imonitord/"+Version)
	start := time.Now()
	resp, err := p.client.Do(req)
	if err != nil {
		return Result{Err: cleanErr(err).Error(), Resolved: remote}
	}
	io.CopyN(io.Discard, resp.Body, 512)
	resp.Body.Close()
	if first.IsZero() {
		first = time.Now()
	}
	r := Result{OK: true, RttMs: ms(first.Sub(start)), Resolved: remote}
	if resp.StatusCode >= 500 {
		r.Err = "HTTP " + strconv.Itoa(resp.StatusCode)
	}
	return r
}

// Version is set by main for the User-Agent.
var Version = "dev"

func probeDNS(ctx context.Context, t *config.Target, st *State) Result {
	res := net.DefaultResolver
	if t.Resolver != nil && *t.Resolver != "" {
		addr, err := config.ParseResolver(*t.Resolver)
		if err != nil {
			return Result{Err: "bad resolver"}
		}
		key := "resolver|" + addr
		if st != nil && st.resolver != nil && st.key == key {
			res = st.resolver
		} else {
			res = &net.Resolver{
				PreferGo: true,
				Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, network, addr)
				},
			}
			if st != nil {
				st.key, st.resolver = key, res
			}
		}
	}
	start := time.Now()
	addrs, err := res.LookupHost(ctx, t.Host)
	el := time.Since(start)
	if err != nil {
		return Result{Err: cleanErr(err).Error()}
	}
	r := Result{OK: true, RttMs: ms(el)}
	if len(addrs) > 0 {
		r.Resolved = addrs[0]
	}
	return r
}

func ms(d time.Duration) float64 { return float64(d.Nanoseconds()) / 1e6 }

// cleanErr turns verbose net errors into short messages.
func cleanErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) || errors.Is(err, errTimeout) {
		return errTimeout
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return errTimeout
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		if dnsErr.IsNotFound {
			return errors.New("NXDOMAIN")
		}
		if dnsErr.IsTimeout {
			return errTimeout
		}
		return errors.New(dnsErr.Err)
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errors.New(errno.Error())
	}
	var oe *net.OpError
	if errors.As(err, &oe) && oe.Err != nil {
		return errors.New(oe.Err.Error())
	}
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 && len(s)-i < 80 && strings.HasPrefix(s, "Get ") {
		s = s[i+2:]
	}
	return errors.New(s)
}

// gateway cache shared across targets (macOS shells out to route(8)).
var gwCache struct {
	sync.Mutex
	ip    net.IP
	iface string
	err   error
	at    time.Time
}

// DefaultRoute returns the default gateway and interface (Linux: read fresh
// from /proc every call; macOS: cached 5 s because it shells out).
func DefaultRoute() (net.IP, string, error) {
	gwCache.Lock()
	defer gwCache.Unlock()
	if gwCacheTTL > 0 && !gwCache.at.IsZero() && time.Since(gwCache.at) < gwCacheTTL {
		return gwCache.ip, gwCache.iface, gwCache.err
	}
	ip, iface, err := defaultRoute()
	gwCache.ip, gwCache.iface, gwCache.err, gwCache.at = ip, iface, err, time.Now()
	return ip, iface, err
}
