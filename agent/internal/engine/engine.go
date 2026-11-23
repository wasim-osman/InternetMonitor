// Package engine runs one probe goroutine per target, keeps the live windows,
// evaluates the down/degraded rules and manages incidents.
package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand/v2"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"imonitord/internal/config"
	"imonitord/internal/probe"
	"imonitord/internal/stats"
	"imonitord/internal/store"
)

const (
	LiveWindowSec   = 60
	maxIncidentsMem = 10000
)

type targetState struct {
	def         config.Target
	gen         uint64
	cancel      context.CancelFunc
	workerKey   string
	ring        stats.Ring
	consecFails int
	failSince   int64 // unix s of first failure in the current streak
	lastErrStr  string
	hasLast     bool
	lastOK      bool
	lastRtt     float64
	lastAt      int64
	lastErr     string
	resolved    string
	highSinceMs int64
	peak        float64
	down        *store.Incident
	degraded    *store.Incident
}

type event struct {
	kind string // opened | closed
	inc  store.Incident
}

// Engine is the core of the agent.
type Engine struct {
	mu        sync.Mutex
	cfg       config.Config
	targets   map[string]*targetState
	gen       uint64
	prober    probe.Prober
	store     *store.Store
	incLog    *store.IncidentLog
	incidents []*store.Incident // ascending by StartedAt
	lastIncID int64
	ctx       context.Context
	now       func() time.Time

	// PathHint returns "direct"/"relay"/nil for a Tailscale IP (must not block).
	PathHint func(ip string) *string
	// Webhook is called for opened/closed events (nil = use HTTP POST).
	Webhook func(url string, payload []byte)

	sumMu    sync.Mutex
	sumCache map[string]sumEntry
}

type sumEntry struct {
	at  time.Time
	res []store.TargetSummary
}

func New(cfg config.Config, pr probe.Prober, st *store.Store, il *store.IncidentLog) *Engine {
	e := &Engine{
		cfg:      cfg.Clone(),
		targets:  make(map[string]*targetState),
		prober:   pr,
		store:    st,
		incLog:   il,
		now:      time.Now,
		sumCache: make(map[string]sumEntry),
	}
	return e
}

// SetClock overrides time.Now (tests).
func (e *Engine) SetClock(f func() time.Time) { e.now = f }

// Config returns a copy of the current config.
func (e *Engine) Config() config.Config {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cfg.Clone()
}

// Start loads incidents, starts workers and the housekeeping loop.
func (e *Engine) Start(ctx context.Context) {
	e.mu.Lock()
	e.ctx = ctx
	now := e.now().Unix()
	var closeEvents []event
	if e.incLog != nil {
		for _, inc := range e.incLog.Load() {
			inc := inc
			e.incidents = append(e.incidents, &inc)
			e.bumpIncID(inc.ID)
		}
	}
	for _, inc := range e.incidents {
		if inc.EndedAt != nil {
			continue
		}
		t := e.cfg.TargetByID(inc.TargetID)
		if t == nil || !t.IsEnabled() {
			inc.EndedAt = &now
			closeEvents = append(closeEvents, event{"closed", *inc})
			continue
		}
		ts := e.stateLocked(t.ID)
		switch inc.Kind {
		case "down":
			if ts.down == nil {
				ts.down = inc
				ts.consecFails = e.cfg.Rules.DownAfterFailures
				ts.failSince = inc.StartedAt
			}
		case "degraded":
			if ts.degraded == nil {
				ts.degraded = inc
				ts.highSinceMs = inc.StartedAt * 1000
				if inc.PeakMs != nil {
					ts.peak = *inc.PeakMs
				}
			}
		}
	}
	e.reconcileLocked()
	e.mu.Unlock()
	e.emit(closeEvents, false)
	go e.housekeeping(ctx)
}

func (e *Engine) stateLocked(id string) *targetState {
	ts := e.targets[id]
	if ts == nil {
		ts = &targetState{}
		if t := e.cfg.TargetByID(id); t != nil {
			ts.def = *t
		}
		e.targets[id] = ts
	}
	return ts
}

func (e *Engine) bumpIncID(id string) {
	if n, err := strconv.ParseInt(strings.TrimPrefix(id, "i-"), 10, 64); err == nil && n > e.lastIncID {
		e.lastIncID = n
	}
}

func (e *Engine) newIncIDLocked() string {
	n := e.now().UnixMilli()
	if n <= e.lastIncID {
		n = e.lastIncID + 1
	}
	e.lastIncID = n
	return "i-" + strconv.FormatInt(n, 10)
}

func workerKey(c *config.Config, t *config.Target) string {
	return fmt.Sprintf("%s|%d|%d|%v", t.ProbeKey(), c.EffectiveIntervalMs(t), c.TimeoutMs, t.IsEnabled())
}

// ApplyConfig swaps the config live: starts/stops/restarts workers as needed.
func (e *Engine) ApplyConfig(c config.Config) {
	e.mu.Lock()
	old := e.cfg
	e.cfg = c.Clone()
	var evs []event
	now := e.now().Unix()
	for i := range e.cfg.Targets {
		t := &e.cfg.Targets[i]
		ts := e.targets[t.ID]
		if ts == nil {
			continue
		}
		if ot := old.TargetByID(t.ID); ot != nil && ot.ProbeKey() != t.ProbeKey() {
			evs = append(evs, e.closeAllLocked(ts, now)...)
			ts.ring.Reset()
			ts.consecFails, ts.highSinceMs, ts.hasLast, ts.resolved = 0, 0, false, ""
		}
		if !t.IsEnabled() {
			evs = append(evs, e.closeAllLocked(ts, now)...)
			ts.consecFails, ts.highSinceMs = 0, 0
		}
		ts.def = *t
	}
	for id, ts := range e.targets {
		if e.cfg.TargetByID(id) == nil {
			if ts.cancel != nil {
				ts.cancel()
			}
			evs = append(evs, e.closeAllLocked(ts, now)...)
			delete(e.targets, id)
			e.store.Forget(id)
		}
	}
	e.reconcileLocked()
	e.mu.Unlock()
	e.invalidateSummary()
	e.emit(evs, true)
}

// reconcileLocked starts/restarts workers so they match the config.
func (e *Engine) reconcileLocked() {
	if e.ctx == nil {
		return
	}
	for i := range e.cfg.Targets {
		t := &e.cfg.Targets[i]
		ts := e.stateLocked(t.ID)
		ts.def = *t
		key := workerKey(&e.cfg, t)
		if ts.workerKey == key && (ts.cancel != nil || !t.IsEnabled()) {
			continue
		}
		if ts.cancel != nil {
			ts.cancel()
			ts.cancel = nil
		}
		ts.workerKey = key
		if !t.IsEnabled() {
			continue
		}
		e.gen++
		ts.gen = e.gen
		ctx, cancel := context.WithCancel(e.ctx)
		ts.cancel = cancel
		iv := time.Duration(e.cfg.EffectiveIntervalMs(t)) * time.Millisecond
		to := time.Duration(e.cfg.TimeoutMs) * time.Millisecond
		go e.worker(ctx, t.ID, ts.gen, *t, iv, to)
	}
}

func (e *Engine) worker(ctx context.Context, id string, gen uint64, t config.Target, iv, timeout time.Duration) {
	// Stagger the first probe so targets don't all fire at once.
	first := time.NewTimer(time.Duration(rand.Int64N(int64(iv)/2 + 1)))
	select {
	case <-ctx.Done():
		first.Stop()
		return
	case <-first.C:
	}
	var st probe.State
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	next := time.Now()
	for {
		start := time.Now()
		res := e.prober.Probe(ctx, &t, timeout, &st)
		if ctx.Err() != nil {
			return
		}
		e.Record(id, gen, start, res)
		next = next.Add(iv)
		d := time.Until(next)
		if d <= 0 {
			next = time.Now()
			d = 0
		}
		timer.Reset(d)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// Record applies one probe result. gen==0 bypasses the stale-worker check (tests).
func (e *Engine) Record(id string, gen uint64, start time.Time, res probe.Result) {
	e.mu.Lock()
	ts := e.targets[id]
	if ts == nil || (gen != 0 && ts.gen != gen) || !ts.def.IsEnabled() {
		e.mu.Unlock()
		return
	}
	tMs := start.UnixMilli()
	tSec := start.Unix()
	ts.ring.Add(tMs, res.RttMs, res.OK)
	ts.hasLast, ts.lastOK, ts.lastAt, ts.lastErr = true, res.OK, tSec, res.Err
	if res.OK {
		ts.lastRtt = res.RttMs
	}
	if res.Resolved != "" {
		ts.resolved = res.Resolved
	}
	e.store.AddSample(id, tSec, res.RttMs, res.OK, res.Err)

	rules := e.cfg.Rules
	var evs []event
	if res.OK {
		if ts.down != nil {
			evs = append(evs, e.closeLocked(ts, &ts.down, tSec))
		}
		ts.consecFails = 0
		thr := float64(rules.DegradedLatencyMs)
		if thr > 0 && res.RttMs > thr {
			if ts.highSinceMs == 0 {
				ts.highSinceMs = tMs
				ts.peak = 0
			}
			if res.RttMs > ts.peak {
				ts.peak = res.RttMs
			}
			if ts.degraded == nil && tMs-ts.highSinceMs >= int64(rules.DegradedForSec)*1000 {
				inc := &store.Incident{
					ID: e.newIncIDLocked(), TargetID: id, TargetName: ts.def.Name, Kind: "degraded",
					StartedAt: ts.highSinceMs / 1000,
					Detail:    fmt.Sprintf("latency above %d ms for %d s", rules.DegradedLatencyMs, rules.DegradedForSec),
					PeakMs:    stats.F(ts.peak),
				}
				ts.degraded = inc
				e.addIncidentLocked(inc)
				evs = append(evs, event{"opened", *inc})
			} else if ts.degraded != nil {
				ts.degraded.PeakMs = stats.F(ts.peak)
			}
		} else {
			ts.highSinceMs = 0
			if ts.degraded != nil {
				evs = append(evs, e.closeLocked(ts, &ts.degraded, tSec))
			}
		}
	} else {
		if ts.consecFails == 0 {
			ts.failSince = tSec
		}
		ts.consecFails++
		ts.lastErrStr = res.Err
		if ts.consecFails >= rules.DownAfterFailures && ts.down == nil {
			if ts.degraded != nil {
				evs = append(evs, e.closeLocked(ts, &ts.degraded, tSec))
			}
			ts.highSinceMs = 0
			reason := res.Err
			if reason == "" {
				reason = "failed"
			}
			inc := &store.Incident{
				ID: e.newIncIDLocked(), TargetID: id, TargetName: ts.def.Name, Kind: "down",
				StartedAt: ts.failSince,
				Detail:    fmt.Sprintf("%d consecutive failures: %s", ts.consecFails, reason),
			}
			ts.down = inc
			e.addIncidentLocked(inc)
			evs = append(evs, event{"opened", *inc})
		}
	}
	e.mu.Unlock()
	if len(evs) > 0 {
		e.invalidateSummary()
	}
	e.emit(evs, true)
}

func (e *Engine) addIncidentLocked(inc *store.Incident) {
	e.incidents = append(e.incidents, inc)
	if len(e.incidents) > maxIncidentsMem {
		// drop the oldest closed incident
		for i, x := range e.incidents {
			if x.EndedAt != nil {
				e.incidents = append(e.incidents[:i], e.incidents[i+1:]...)
				break
			}
		}
	}
}

func (e *Engine) closeLocked(ts *targetState, slot **store.Incident, at int64) event {
	inc := *slot
	end := at
	if end < inc.StartedAt {
		end = inc.StartedAt
	}
	inc.EndedAt = &end
	*slot = nil
	return event{"closed", *inc}
}

func (e *Engine) closeAllLocked(ts *targetState, at int64) []event {
	var evs []event
	if ts.down != nil {
		evs = append(evs, e.closeLocked(ts, &ts.down, at))
	}
	if ts.degraded != nil {
		evs = append(evs, e.closeLocked(ts, &ts.degraded, at))
	}
	ts.highSinceMs = 0
	return evs
}

// emit persists incident changes and fires webhooks (outside the lock).
func (e *Engine) emit(evs []event, webhook bool) {
	if len(evs) == 0 {
		return
	}
	e.mu.Lock()
	url := e.cfg.Rules.WebhookURL
	node := e.cfg.NodeName
	e.mu.Unlock()
	for _, ev := range evs {
		if e.incLog != nil {
			if err := e.incLog.Write(ev.inc); err != nil {
				log.Printf("incidents: %v", err)
			}
		}
		log.Printf("incident %s: %s %s (%s) %s", ev.kind, ev.inc.Kind, ev.inc.TargetName, ev.inc.TargetID, ev.inc.Detail)
		if webhook && url != "" {
			payload, _ := json.Marshal(map[string]any{"event": ev.kind, "node": node, "incident": ev.inc})
			if e.Webhook != nil {
				e.Webhook(url, payload)
			} else {
				go postWebhook(url, payload)
			}
		}
	}
}

var webhookClient = &http.Client{Timeout: 5 * time.Second}

func postWebhook(url string, payload []byte) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := webhookClient.Do(req)
	if err != nil {
		log.Printf("webhook: %v", err)
		return
	}
	resp.Body.Close()
}

func (e *Engine) housekeeping(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	e.prune()
	lastPrune := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			e.mu.Lock()
			grace := int64(e.cfg.TimeoutMs/1000) + 2
			e.mu.Unlock()
			e.store.Tick(now, grace)
			if now.Sub(lastPrune) >= 10*time.Minute {
				e.prune()
				lastPrune = now
			}
		}
	}
}

// prune applies retention to files and incidents.
func (e *Engine) prune() {
	e.mu.Lock()
	now := e.now().Unix()
	rawH, rollD := e.cfg.RetentionRawHours, e.cfg.RetentionRollupDays
	cut := now - int64(rollD)*86400
	kept := e.incidents[:0]
	dropped := false
	for _, inc := range e.incidents {
		if inc.EndedAt != nil && *inc.EndedAt < cut {
			dropped = true
			continue
		}
		kept = append(kept, inc)
	}
	e.incidents = kept
	var snapshot []store.Incident
	if dropped {
		for _, inc := range kept {
			snapshot = append(snapshot, *inc)
		}
	}
	e.mu.Unlock()
	e.store.Prune(now, rawH, rollD)
	if dropped && e.incLog != nil {
		e.incLog.Rewrite(snapshot)
	}
}

// ResetHistory wipes samples, rollups and incidents (config/clients kept).
func (e *Engine) ResetHistory() error {
	e.mu.Lock()
	e.incidents = nil
	for _, ts := range e.targets {
		ts.ring.Reset()
		ts.down, ts.degraded = nil, nil
		ts.consecFails, ts.highSinceMs, ts.hasLast = 0, 0, false
	}
	e.mu.Unlock()
	err := e.store.Reset()
	if e.incLog != nil {
		if err2 := e.incLog.Rewrite(nil); err == nil {
			err = err2
		}
	}
	e.invalidateSummary()
	return err
}

// Stop cancels all workers.
func (e *Engine) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, ts := range e.targets {
		if ts.cancel != nil {
			ts.cancel()
			ts.cancel = nil
		}
	}
}

// ---- read side ----

type LiveTarget struct {
	ID           string   `json:"id"`
	State        string   `json:"state"`
	LastRttMs    *float64 `json:"lastRttMs"`
	LastAt       *int64   `json:"lastAt"`
	LastError    *string  `json:"lastError"`
	ResolvedHost *string  `json:"resolvedHost"`
	WindowSec    int      `json:"windowSec"`
	AvgMs        *float64 `json:"avgMs"`
	MinMs        *float64 `json:"minMs"`
	MaxMs        *float64 `json:"maxMs"`
	P95Ms        *float64 `json:"p95Ms"`
	JitterMs     *float64 `json:"jitterMs"`
	LossPct      *float64 `json:"lossPct"`
	Samples      int      `json:"samples"`
	PathHint     *string  `json:"pathHint"`

	// extras for /metrics (not serialised)
	name, kind string
}

func stateOf(ts *targetState) string {
	switch {
	case !ts.def.IsEnabled():
		return "unknown"
	case ts.down != nil:
		return "down"
	case !ts.hasLast:
		return "unknown"
	case ts.degraded != nil:
		return "degraded"
	}
	return "up"
}

func strp(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Live returns the live view in config order.
func (e *Engine) Live() (int64, []LiveTarget) {
	e.mu.Lock()
	now := e.now()
	since := now.UnixMilli() - LiveWindowSec*1000
	out := make([]LiveTarget, 0, len(e.cfg.Targets))
	for i := range e.cfg.Targets {
		t := &e.cfg.Targets[i]
		ts := e.targets[t.ID]
		lt := LiveTarget{ID: t.ID, State: "unknown", WindowSec: LiveWindowSec, name: t.Name, kind: t.Kind}
		if ts != nil {
			lt.State = stateOf(ts)
			if ts.hasLast {
				at := ts.lastAt
				lt.LastAt = &at
				if ts.lastOK {
					lt.LastRttMs = stats.F(ts.lastRtt)
				}
				lt.LastError = strp(ts.lastErr)
			}
			lt.ResolvedHost = strp(ts.resolved)
			w := ts.ring.Window(since)
			lt.Samples = w.Samples
			lt.AvgMs, lt.MinMs, lt.MaxMs, lt.P95Ms = stats.F(w.Avg), stats.F(w.Min), stats.F(w.Max), stats.F(w.P95)
			lt.JitterMs, lt.LossPct = stats.F(w.Jitter), stats.F(w.LossPct)
		}
		out = append(out, lt)
	}
	hint := e.PathHint
	e.mu.Unlock()
	if hint != nil {
		for i := range out {
			ip := ""
			if out[i].ResolvedHost != nil {
				ip = *out[i].ResolvedHost
			} else if t := e.cfgTargetHost(out[i].ID); t != "" {
				ip = t
			}
			if IsTailscaleIP(ip) {
				out[i].PathHint = hint(ip)
			}
		}
	}
	return now.Unix(), out
}

func (e *Engine) cfgTargetHost(id string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if t := e.cfg.TargetByID(id); t != nil {
		return t.Host
	}
	return ""
}

// IsTailscaleIP reports whether s is in 100.64.0.0/10.
func IsTailscaleIP(s string) bool {
	var a, b, c, d int
	if n, _ := fmt.Sscanf(s, "%d.%d.%d.%d", &a, &b, &c, &d); n != 4 {
		return false
	}
	return a == 100 && b >= 64 && b <= 127
}

// Incidents returns incidents overlapping [since, now], newest first.
func (e *Engine) Incidents(since int64, limit int) []store.Incident {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]store.Incident, 0)
	for i := len(e.incidents) - 1; i >= 0 && len(out) < limit; i-- {
		inc := e.incidents[i]
		if inc.EndedAt != nil && *inc.EndedAt < since {
			continue
		}
		out = append(out, *inc)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt > out[j].StartedAt })
	return out
}

// OpenIncidents counts incidents with endedAt == nil.
func (e *Engine) OpenIncidents() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, inc := range e.incidents {
		if inc.EndedAt == nil {
			n++
		}
	}
	return n
}

// DownSeconds returns seconds of "down" incidents for id overlapping [from,to].
func (e *Engine) DownSeconds(id string, from, to int64) float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now().Unix()
	total := 0.0
	for _, inc := range e.incidents {
		if inc.TargetID != id || inc.Kind != "down" {
			continue
		}
		s, en := inc.StartedAt, now
		if inc.EndedAt != nil {
			en = *inc.EndedAt
		}
		if s < from {
			s = from
		}
		if en > to {
			en = to
		}
		if en > s {
			total += float64(en - s)
		}
	}
	return total
}

func (e *Engine) invalidateSummary() {
	e.sumMu.Lock()
	clear(e.sumCache)
	e.sumMu.Unlock()
}

// Summary returns per-target window summaries (cached 30 s).
func (e *Engine) Summary(ids []string) []store.TargetSummary {
	e.mu.Lock()
	if len(ids) == 0 {
		for _, t := range e.cfg.Targets {
			ids = append(ids, t.ID)
		}
	}
	days := e.cfg.RetentionRollupDays
	e.mu.Unlock()
	key := strings.Join(ids, ",")
	e.sumMu.Lock()
	if c, ok := e.sumCache[key]; ok && time.Since(c.at) < 30*time.Second {
		e.sumMu.Unlock()
		return c.res
	}
	e.sumMu.Unlock()
	res := e.store.Summary(ids, e.now().Unix(), days, e.DownSeconds)
	e.sumMu.Lock()
	if len(e.sumCache) > 64 {
		clear(e.sumCache)
	}
	e.sumCache[key] = sumEntry{at: time.Now(), res: res}
	e.sumMu.Unlock()
	return res
}

// Series proxies to the store using each target's effective interval.
func (e *Engine) Series(ids []string, from, to int64, points int) []store.Series {
	e.mu.Lock()
	cfg := e.cfg.Clone()
	e.mu.Unlock()
	return e.store.Series(store.SeriesQuery{
		IDs: ids, From: from, To: to, Points: points,
		IntervalMs:        func(id string) int { return cfg.EffectiveIntervalMs(cfg.TargetByID(id)) },
		RawRetentionHours: cfg.RetentionRawHours,
		Now:               e.now().Unix(),
	})
}

// MetricTarget is used by /metrics.
type MetricTarget struct {
	ID, Name, Kind, State string
	LastRttMs, LossPct    *float64
}

func (e *Engine) Metrics() []MetricTarget {
	_, live := e.Live()
	out := make([]MetricTarget, 0, len(live))
	for _, l := range live {
		out = append(out, MetricTarget{ID: l.ID, Name: l.name, Kind: l.kind, State: l.State, LastRttMs: l.LastRttMs, LossPct: l.LossPct})
	}
	return out
}
