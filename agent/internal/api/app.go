// Package api wires state (identity, config, clients) to the HTTP API.
package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"imonitord/internal/config"
	"imonitord/internal/engine"
	"imonitord/internal/probe"
	"imonitord/internal/speedtest"
	"imonitord/internal/store"
	"imonitord/internal/sysinfo"
)

type Identity struct {
	NodeID    string `json:"nodeId"`
	CreatedAt int64  `json:"createdAt"`
}

func newUUID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// LoadIdentity reads identity.json or creates it (stable nodeId).
func LoadIdentity(dir string) (Identity, error) {
	path := filepath.Join(dir, "identity.json")
	var id Identity
	if data, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(data, &id) == nil && id.NodeID != "" {
			return id, nil
		}
		log.Printf("identity.json unreadable; regenerating")
	} else if !errors.Is(err, os.ErrNotExist) {
		return id, err
	}
	id = Identity{NodeID: newUUID(), CreatedAt: time.Now().Unix()}
	data, _ := json.MarshalIndent(id, "", "  ")
	return id, config.WriteFileAtomic(path, append(data, '\n'), 0o600)
}

// App holds all agent state.
type App struct {
	Version   string
	StateDir  string
	Hostname  string
	StartedAt time.Time
	Identity  Identity

	mu           sync.Mutex
	file         config.File // config + clients (persisted together)
	clientsDirty bool

	Engine *engine.Engine
	Store  *store.Store
	IncLog *store.IncidentLog
	Speed  *speedtest.Runner
	TS     *sysinfo.TailscaleCache
	Pair   *Pairing

	traceSem chan struct{}
	cancel   context.CancelFunc
	Exit     func(code int) // os.Exit by default
	shutOnce sync.Once
	now      func() time.Time
}

// Options for NewApp.
type Options struct {
	Version  string
	StateDir string
	Prober   probe.Prober // nil = real prober
	NoStart  bool         // don't start workers (tests)
}

// NewApp loads (or initialises) state and starts the engine.
func NewApp(opts Options) (*App, error) {
	if err := os.MkdirAll(opts.StateDir, 0o700); err != nil {
		return nil, err
	}
	os.Chmod(opts.StateDir, 0o700) // best effort; it holds token hashes
	host, _ := os.Hostname()
	id, err := LoadIdentity(opts.StateDir)
	if err != nil {
		return nil, fmt.Errorf("identity: %w", err)
	}
	cfgPath := filepath.Join(opts.StateDir, "config.json")
	f, ok, err := config.Load(cfgPath)
	if err == nil && ok {
		if verr := f.Config.Validate(); verr != nil {
			err = verr
		}
	}
	var keepClients []config.ClientRecord
	if err != nil {
		bak := fmt.Sprintf("%s.corrupt-%d", cfgPath, time.Now().Unix())
		log.Printf("config.json invalid (%v); copied to %s, starting with default config", err, bak)
		if data, rerr := os.ReadFile(cfgPath); rerr == nil {
			os.WriteFile(bak, data, 0o600)
		}
		var ve *config.ValidationError
		if errors.As(err, &ve) {
			keepClients = f.Clients // JSON was fine: keep pairings
		}
		ok = false
	}
	if !ok {
		if keepClients == nil {
			keepClients = []config.ClientRecord{}
		}
		f = config.File{Config: config.Default(host), Clients: keepClients}
		if err := config.Save(cfgPath, f); err != nil {
			return nil, fmt.Errorf("save config: %w", err)
		}
	}
	if f.NodeName == "" {
		f.NodeName = host
	}
	os.Chmod(cfgPath, 0o600)

	a := &App{
		Version:   opts.Version,
		StateDir:  opts.StateDir,
		Hostname:  host,
		StartedAt: time.Now(),
		Identity:  id,
		file:      f,
		Store:     store.New(opts.StateDir),
		IncLog:    store.NewIncidentLog(opts.StateDir),
		Speed:     speedtest.New(opts.StateDir),
		TS:        sysinfo.NewTailscaleCache(),
		Pair:      NewPairing(),
		traceSem:  make(chan struct{}, 2),
		Exit:      os.Exit,
		now:       time.Now,
	}
	pr := opts.Prober
	if pr == nil {
		pr = probe.NewProber()
	}
	a.Engine = engine.New(f.Config, pr, a.Store, a.IncLog)
	a.Engine.PathHint = a.TS.PathHint
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	if !opts.NoStart {
		a.Engine.Start(ctx)
		go a.background(ctx)
	}
	return a, nil
}

func (a *App) cfgPath() string { return filepath.Join(a.StateDir, "config.json") }

// background persists client lastSeen and runs scheduled speed tests.
func (a *App) background(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	lastSave := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			a.mu.Lock()
			dirty := a.clientsDirty && now.Sub(lastSave) >= 5*time.Minute
			a.mu.Unlock()
			if dirty {
				a.saveFile()
				lastSave = now
			}
			cfg := a.Engine.Config()
			if cfg.Speedtest.Enabled {
				last := a.Speed.LastAt()
				if last == 0 {
					last = a.StartedAt.Unix()
				}
				if now.Unix()-last >= int64(cfg.Speedtest.IntervalMin)*60 {
					go a.Speed.Run(ctx, cfg.Speedtest.URL)
				}
			}
		}
	}
}

func (a *App) saveFile() error {
	a.mu.Lock()
	f := config.File{Config: a.file.Config.Clone(), Clients: append([]config.ClientRecord(nil), a.file.Clients...)}
	a.clientsDirty = false
	a.mu.Unlock()
	if f.Clients == nil {
		f.Clients = []config.ClientRecord{}
	}
	return config.Save(a.cfgPath(), f)
}

// Paired reports whether any client token exists.
func (a *App) Paired() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.file.Clients) > 0
}

// Authenticate checks a bearer token (sha256 + constant-time compare).
func (a *App) Authenticate(token string) bool {
	if token == "" {
		return false
	}
	h := []byte(HashToken(token))
	a.mu.Lock()
	defer a.mu.Unlock()
	match := -1
	for i := range a.file.Clients {
		if subtle.ConstantTimeCompare(h, []byte(a.file.Clients[i].TokenSHA256)) == 1 {
			match = i
		}
	}
	if match < 0 {
		return false
	}
	now := a.now().Unix()
	if now-a.file.Clients[match].LastSeen >= 60 {
		a.file.Clients[match].LastSeen = now
		a.clientsDirty = true
	}
	return true
}

// AddClient creates a client and returns (id, token).
func (a *App) AddClient(name string) (string, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Unnamed client"
	}
	if len(name) > 100 {
		name = name[:100]
	}
	tok := NewToken()
	id := "c-" + randHex(6)
	now := a.now().Unix()
	a.mu.Lock()
	a.file.Clients = append(a.file.Clients, config.ClientRecord{ID: id, Name: name, TokenSHA256: HashToken(tok), CreatedAt: now, LastSeen: now})
	a.mu.Unlock()
	if err := a.saveFile(); err != nil {
		return "", "", err
	}
	return id, tok, nil
}

type ClientView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"createdAt"`
	LastSeen  int64  `json:"lastSeen"`
}

func (a *App) Clients() []ClientView {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]ClientView, 0, len(a.file.Clients))
	for _, c := range a.file.Clients {
		out = append(out, ClientView{ID: c.ID, Name: c.Name, CreatedAt: c.CreatedAt, LastSeen: c.LastSeen})
	}
	return out
}

// DeleteClient removes a client; false if not found.
func (a *App) DeleteClient(id string) (bool, error) {
	a.mu.Lock()
	idx := -1
	for i, c := range a.file.Clients {
		if c.ID == id {
			idx = i
		}
	}
	if idx < 0 {
		a.mu.Unlock()
		return false, nil
	}
	a.file.Clients = append(a.file.Clients[:idx:idx], a.file.Clients[idx+1:]...)
	a.mu.Unlock()
	return true, a.saveFile()
}

// SetConfig validates, persists atomically and applies live.
func (a *App) SetConfig(c config.Config) (config.Config, error) {
	if err := c.Validate(); err != nil {
		return config.Config{}, err
	}
	if c.NodeName == "" {
		c.NodeName = a.Hostname
	}
	a.mu.Lock()
	prev := a.file.Config
	a.file.Config = c.Clone()
	a.mu.Unlock()
	if err := a.saveFile(); err != nil {
		a.mu.Lock()
		a.file.Config = prev
		a.mu.Unlock()
		return config.Config{}, err
	}
	a.Engine.ApplyConfig(c)
	return c.Clone(), nil
}

// Shutdown flushes everything. Safe to call more than once.
func (a *App) Shutdown() {
	a.shutOnce.Do(func() {
		a.cancel()
		a.Engine.Stop()
		a.Store.Close()
		a.IncLog.Close()
		a.mu.Lock()
		dirty := a.clientsDirty
		a.mu.Unlock()
		if dirty {
			a.saveFile()
		}
	})
}
