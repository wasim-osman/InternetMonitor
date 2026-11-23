package sysinfo

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

type TailscalePeer struct {
	Name    string  `json:"name"`
	IP      string  `json:"ip"`
	Online  bool    `json:"online"`
	Path    *string `json:"path"`
	Relay   *string `json:"relay"`
	CurAddr *string `json:"curAddr"`
}

type Tailscale struct {
	SelfIP *string         `json:"selfIp"`
	Peers  []TailscalePeer `json:"peers"`
}

type tsStatus struct {
	Self *struct {
		TailscaleIPs []string `json:"TailscaleIPs"`
	} `json:"Self"`
	Peer map[string]struct {
		HostName     string   `json:"HostName"`
		DNSName      string   `json:"DNSName"`
		TailscaleIPs []string `json:"TailscaleIPs"`
		Online       bool     `json:"Online"`
		CurAddr      string   `json:"CurAddr"`
		Relay        string   `json:"Relay"`
	} `json:"Peer"`
}

// ParseTailscaleStatus converts `tailscale status --json` output.
func ParseTailscaleStatus(data []byte) (*Tailscale, error) {
	var st tsStatus
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, err
	}
	ts := &Tailscale{Peers: []TailscalePeer{}}
	if st.Self != nil {
		for _, ip := range st.Self.TailscaleIPs {
			if !strings.Contains(ip, ":") {
				v := ip
				ts.SelfIP = &v
				break
			}
		}
	}
	for _, p := range st.Peer {
		ip := ""
		for _, a := range p.TailscaleIPs {
			if !strings.Contains(a, ":") {
				ip = a
				break
			}
		}
		name := p.HostName
		if name == "" {
			name = strings.Split(p.DNSName, ".")[0]
		}
		peer := TailscalePeer{Name: name, IP: ip, Online: p.Online}
		switch {
		case p.CurAddr != "":
			peer.Path = sp("direct")
			peer.CurAddr = sp(p.CurAddr)
		case p.Relay != "":
			peer.Path = sp("relay")
			peer.Relay = sp(p.Relay)
		}
		ts.Peers = append(ts.Peers, peer)
	}
	sort.Slice(ts.Peers, func(i, j int) bool { return ts.Peers[i].Name < ts.Peers[j].Name })
	return ts, nil
}

func sp(s string) *string { return &s }

// TailscaleCache runs `tailscale status --json` at most every 10 s.
type TailscaleCache struct {
	mu       sync.Mutex
	val      *Tailscale
	at       time.Time
	running  bool
	bin      string
	binFound bool
	done     chan struct{}
}

const tsTTL = 10 * time.Second

func NewTailscaleCache() *TailscaleCache { return &TailscaleCache{} }

func findTailscale() string {
	if p, err := exec.LookPath("tailscale"); err == nil {
		return p
	}
	for _, p := range tailscaleBins() {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

func (c *TailscaleCache) refresh() {
	c.mu.Lock()
	if !c.binFound {
		c.bin, c.binFound = findTailscale(), true
	}
	bin := c.bin
	c.mu.Unlock()
	var val *Tailscale
	if bin != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		out, err := exec.CommandContext(ctx, bin, "status", "--json").Output()
		cancel()
		if err == nil {
			val, _ = ParseTailscaleStatus(out)
		}
	}
	c.mu.Lock()
	c.val, c.at, c.running = val, time.Now(), false
	if c.done != nil {
		close(c.done)
		c.done = nil
	}
	c.mu.Unlock()
}

// Get returns the cached status (nil when unavailable). With wait=true it
// blocks for a refresh when the cache is stale; otherwise it refreshes in the
// background and returns the previous value.
func (c *TailscaleCache) Get(wait bool) *Tailscale {
	c.mu.Lock()
	fresh := !c.at.IsZero() && time.Since(c.at) < tsTTL
	if fresh {
		v := c.val
		c.mu.Unlock()
		return v
	}
	if !c.running {
		c.running = true
		c.done = make(chan struct{})
		go c.refresh()
	}
	done := c.done
	v := c.val
	c.mu.Unlock()
	if !wait {
		return v
	}
	<-done
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.val
}

// PathHint returns "direct"/"relay"/nil for a Tailscale peer IP (non-blocking).
func (c *TailscaleCache) PathHint(ip string) *string {
	ts := c.Get(false)
	if ts == nil {
		return nil
	}
	for _, p := range ts.Peers {
		if p.IP == ip {
			return p.Path
		}
	}
	return nil
}

// tailscaleBins lists well-known install locations (after $PATH).
func tailscaleBins() []string {
	if runtime.GOOS == "windows" {
		var out []string
		for _, env := range []string{"ProgramFiles", "ProgramW6432", "ProgramFiles(x86)"} {
			if d := os.Getenv(env); d != "" {
				out = append(out, filepath.Join(d, "Tailscale", "tailscale.exe"))
			}
		}
		return append(out, `C:\Program Files\Tailscale\tailscale.exe`)
	}
	return []string{"/usr/bin/tailscale", "/usr/local/bin/tailscale", "/opt/homebrew/bin/tailscale",
		"/Applications/Tailscale.app/Contents/MacOS/Tailscale"}
}
