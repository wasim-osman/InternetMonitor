package engine

import (
	"context"
	"sync"
	"testing"
	"time"

	"imonitord/internal/config"
	"imonitord/internal/probe"
	"imonitord/internal/store"
)

// blockProber never returns until the worker is cancelled, so tests drive
// the state machine via Record() only.
type blockProber struct{}

func (blockProber) Probe(ctx context.Context, _ *config.Target, _ time.Duration, _ *probe.State) probe.Result {
	<-ctx.Done()
	return probe.Result{}
}

type hook struct {
	mu   sync.Mutex
	evts []string
}

func newEngine(t *testing.T, dir string) (*Engine, *hook, *time.Time) {
	t.Helper()
	c := config.Default("pi")
	c.Targets = []config.Target{{ID: "a", Name: "A", Kind: config.KindICMP, Host: "1.1.1.1"}}
	c.Rules = config.Rules{DownAfterFailures: 3, DegradedLatencyMs: 100, DegradedForSec: 10, WebhookURL: "http://hook.invalid/x"}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1700000000, 0)
	e := New(c, blockProber{}, store.New(dir), store.NewIncidentLog(dir))
	e.SetClock(func() time.Time { return now })
	h := &hook{}
	e.Webhook = func(url string, payload []byte) {
		h.mu.Lock()
		h.evts = append(h.evts, string(payload))
		h.mu.Unlock()
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e.Start(ctx)
	return e, h, &now
}

func rec(e *Engine, at *time.Time, ok bool, rtt float64) {
	r := probe.Result{OK: ok, RttMs: rtt}
	if !ok {
		r.Err = "timeout"
	}
	e.Record("a", 0, *at, r)
	*at = at.Add(2 * time.Second)
}

func state(e *Engine) string {
	_, l := e.Live()
	return l[0].State
}

func TestDownAndRecovery(t *testing.T) {
	e, h, now := newEngine(t, t.TempDir())
	if s := state(e); s != "unknown" {
		t.Fatalf("initial state %s", s)
	}
	rec(e, now, true, 10)
	if s := state(e); s != "up" {
		t.Fatalf("state %s", s)
	}
	firstFail := now.Unix()
	rec(e, now, false, 0)
	rec(e, now, false, 0)
	if s := state(e); s != "up" {
		t.Fatalf("2 failures should still be up, got %s", s)
	}
	rec(e, now, false, 0)
	if s := state(e); s != "down" {
		t.Fatalf("state %s", s)
	}
	incs := e.Incidents(0, 10)
	if len(incs) != 1 || incs[0].Kind != "down" || incs[0].EndedAt != nil || incs[0].StartedAt != firstFail ||
		incs[0].Detail != "3 consecutive failures: timeout" || incs[0].TargetName != "A" {
		t.Fatalf("incidents %+v", incs)
	}
	rec(e, now, false, 0) // still one incident
	if n := len(e.Incidents(0, 10)); n != 1 {
		t.Fatalf("want 1 incident, got %d", n)
	}
	if e.OpenIncidents() != 1 {
		t.Fatal("open count")
	}
	recoverAt := now.Unix()
	rec(e, now, true, 12)
	if s := state(e); s != "up" {
		t.Fatalf("state after recovery %s", s)
	}
	incs = e.Incidents(0, 10)
	if incs[0].EndedAt == nil || *incs[0].EndedAt != recoverAt {
		t.Fatalf("not closed: %+v", incs[0])
	}
	if got := e.DownSeconds("a", 0, now.Unix()); got != float64(recoverAt-firstFail) {
		t.Fatalf("down seconds %v", got)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.evts) != 2 {
		t.Fatalf("webhook events %v", h.evts)
	}
}

func TestDegraded(t *testing.T) {
	e, _, now := newEngine(t, t.TempDir())
	rec(e, now, true, 50)
	start := now.Unix()
	for i := 0; i < 5; i++ { // 0,2,4,6,8 s above threshold: not yet 10 s
		rec(e, now, true, 150+float64(i))
	}
	if s := state(e); s != "up" {
		t.Fatalf("too early: %s", s)
	}
	rec(e, now, false, 0)  // a failure doesn't reset the latency streak
	rec(e, now, true, 300) // t=12s since start -> degraded
	if s := state(e); s != "degraded" {
		t.Fatalf("state %s", s)
	}
	incs := e.Incidents(0, 10)
	if len(incs) != 1 || incs[0].Kind != "degraded" || incs[0].StartedAt != start || incs[0].PeakMs == nil || *incs[0].PeakMs != 300 {
		t.Fatalf("incidents %+v", incs)
	}
	rec(e, now, true, 20)
	if s := state(e); s != "up" {
		t.Fatalf("state %s", s)
	}
	if incs := e.Incidents(0, 10); incs[0].EndedAt == nil {
		t.Fatal("degraded not closed")
	}
	// down supersedes degraded
	for i := 0; i < 7; i++ {
		rec(e, now, true, 200)
	}
	if state(e) != "degraded" {
		t.Fatal("expected degraded again")
	}
	rec(e, now, false, 0)
	rec(e, now, false, 0)
	rec(e, now, false, 0)
	if state(e) != "down" {
		t.Fatal("expected down")
	}
	for _, inc := range e.Incidents(0, 10) {
		if inc.Kind == "degraded" && inc.EndedAt == nil {
			t.Fatal("degraded should close when down opens")
		}
	}
}

func TestIncidentsPersistAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	e, _, now := newEngine(t, dir)
	for i := 0; i < 3; i++ {
		rec(e, now, false, 0)
	}
	e.Stop()
	e.incLog.Close()
	e2, _, _ := newEngine(t, dir)
	incs := e2.Incidents(0, 10)
	if len(incs) != 1 || incs[0].EndedAt != nil {
		t.Fatalf("incidents %+v", incs)
	}
	if s := state(e2); s != "down" {
		t.Fatalf("restored state %s", s)
	}
	at := time.Unix(1700000100, 0)
	rec(e2, &at, true, 5)
	if incs := e2.Incidents(0, 10); incs[0].EndedAt == nil {
		t.Fatal("restored incident should close on recovery")
	}
}

func TestRemovingTargetClosesIncidents(t *testing.T) {
	e, _, now := newEngine(t, t.TempDir())
	for i := 0; i < 3; i++ {
		rec(e, now, false, 0)
	}
	c := e.Config()
	c.Targets = []config.Target{}
	e.ApplyConfig(c)
	if e.OpenIncidents() != 0 {
		t.Fatal("incident should be closed when target removed")
	}
	if _, l := e.Live(); len(l) != 0 {
		t.Fatal("live should be empty")
	}
}

func TestIsTailscaleIP(t *testing.T) {
	for ip, want := range map[string]bool{"100.64.0.1": true, "100.127.255.1": true, "100.63.0.1": false, "100.128.0.1": false, "1.1.1.1": false, "x": false} {
		if IsTailscaleIP(ip) != want {
			t.Errorf("%s", ip)
		}
	}
}
