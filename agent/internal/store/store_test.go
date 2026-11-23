package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// base is 2023-11-14 22:00:00 UTC (hour and minute aligned).
const base = int64(1700000000 - 1700000000%3600)

func TestPlanStep(t *testing.T) {
	now := base + 7200
	// 1 h, 300 points, 2 s interval: 12 s raw step
	if src, step := PlanStep(now-3600, now, 300, 2000, 6, now); src != "raw" || step != 12 {
		t.Fatalf("got %s %d", src, step)
	}
	// rounding up to a multiple of a 5 s interval: 3600/300=12 -> 15
	if src, step := PlanStep(now-3600, now, 300, 5000, 6, now); src != "raw" || step != 15 {
		t.Fatalf("got %s %d", src, step)
	}
	// 500 ms interval, 1500 points over 10 min: 0.4 s -> 0.5 s -> 1 s
	if src, step := PlanStep(now-600, now, 1500, 500, 6, now); src != "raw" || step != 1 {
		t.Fatalf("got %s %d", src, step)
	}
	// 24 h: step >= 60 -> rollup, multiple of 60
	if src, step := PlanStep(now-86400, now, 300, 2000, 6, now); src != "rollup" || step != 300 {
		t.Fatalf("got %s %d", src, step)
	}
	// short range but outside raw retention -> rollup with 60 s
	if src, step := PlanStep(now-8*3600, now-8*3600+600, 300, 2000, 6, now); src != "rollup" || step != 60 {
		t.Fatalf("got %s %d", src, step)
	}
}

func fixedInterval(ms int) func(string) int { return func(string) int { return ms } }

func TestSeriesRawDownsampling(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	// 60 samples, 1 per second; every 10th fails; a second target interleaved.
	for i := int64(0); i < 60; i++ {
		ok := i%10 != 9
		s.AddSample("a", base+i, float64(10+i%10), ok, map[bool]string{true: "", false: "timeout"}[ok])
		s.AddSample("b", base+i, 5, true, "")
	}
	// a fully failed bucket at 100..109
	for i := int64(100); i < 110; i++ {
		s.AddSample("a", base+i, 0, false, "timeout")
	}
	out := s.Series(SeriesQuery{IDs: []string{"a", "missing"}, From: base, To: base + 119, Points: 12,
		IntervalMs: fixedInterval(1000), RawRetentionHours: 6, Now: base + 120})
	if len(out) != 2 || out[1].Target != "missing" || len(out[1].Points) != 0 {
		t.Fatalf("unexpected: %+v", out)
	}
	a := out[0]
	if a.Source != "raw" || a.StepSec != 10 {
		t.Fatalf("source/step %s %d", a.Source, a.StepSec)
	}
	// buckets 0..50 (6) + 100 (1); 60..90 empty -> omitted
	if len(a.Points) != 7 {
		t.Fatalf("points %d: %+v", len(a.Points), a.Points)
	}
	p := a.Points[0]
	if p.T != base || p.N != 10 || p.Loss != 10 || *p.Min != 10 || *p.Max != 18 || *p.Avg != 14 {
		t.Fatalf("bucket0 %+v", p)
	}
	last := a.Points[6]
	if last.T != base+100 || last.Loss != 100 || last.Min != nil || last.Avg != nil || last.P95 != nil {
		t.Fatalf("failed bucket should omit latency: %+v", last)
	}
	for i := 1; i < len(a.Points); i++ {
		if a.Points[i].T <= a.Points[i-1].T {
			t.Fatal("points not ascending")
		}
	}
}

func TestRollupsAndSeriesFromRollups(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	// 10 minutes, 30 samples/min (2 s), minute m has rtt 10+m, minute 5 fully down.
	for m := int64(0); m < 10; m++ {
		for k := int64(0); k < 30; k++ {
			ok := m != 5
			s.AddSample("a", base+m*60+k*2, float64(10+m), ok, "")
		}
	}
	s.Tick(time.Unix(base+3600, 0), 4) // finalise the last minute
	s.Flush()
	out := s.Series(SeriesQuery{IDs: []string{"a"}, From: base, To: base + 600, Points: 5,
		IntervalMs: fixedInterval(2000), RawRetentionHours: 6, Now: base + 100000})
	a := out[0]
	if a.Source != "rollup" || a.StepSec != 120 {
		t.Fatalf("source/step %s %d", a.Source, a.StepSec)
	}
	if len(a.Points) != 5 {
		t.Fatalf("points %+v", a.Points)
	}
	// bucket at +240 covers minutes 4 (ok, rtt 14) and 5 (down)
	p := a.Points[2]
	if p.T != base+240 || p.N != 60 || p.Loss != 50 || *p.Avg != 14 || *p.Min != 14 {
		t.Fatalf("bucket %+v", p)
	}
	// summary: 9 good minutes of 30 + 1 down minute
	sum := s.Summary([]string{"a"}, base+600, 14, func(string, int64, int64) float64 { return 60 })
	w := sum[0].Windows[0]
	if w.Label != "1h" || w.Samples != 300 || *w.LossPct != 10 || w.Seconds != 600 {
		t.Fatalf("summary %+v", w)
	}
	if *w.UptimePct != 90 || *w.MinMs != 10 || *w.MaxMs != 19 {
		t.Fatalf("summary %+v up=%v", w, *w.UptimePct)
	}
	if w.JitterMs == nil || *w.JitterMs != 0 {
		t.Fatalf("jitter %+v", w.JitterMs)
	}
}

func TestTornLinesTolerated(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Join(dir, RawName(base))
	os.WriteFile(name, []byte("{\"t\":"+itoa(base)+",\"target\":\"a\",\"ms\":5}\ngarbage\n{\"t\":"+itoa(base+1)+",\"target\":\"a\",\"ms\":7"), 0o600)
	s := New(dir)
	// appending after a torn line must start on a fresh line
	s.AddSample("a", base+2, 9, true, "")
	out := s.Series(SeriesQuery{IDs: []string{"a"}, From: base, To: base + 10, Points: 10,
		IntervalMs: fixedInterval(1000), RawRetentionHours: 6, Now: base + 10})
	if n := len(out[0].Points); n != 2 {
		t.Fatalf("want 2 valid samples (5 and 9), got %d: %+v", n, out[0].Points)
	}
}

func itoa(v int64) string { return string(appendInt(nil, v)) }

func TestPrune(t *testing.T) {
	dir := t.TempDir()
	now := base + 10*86400
	keep := []string{RawName(now - 3600), RollupName(now - 86400)}
	drop := []string{RawName(now - 8*3600), RollupName(now - 20*86400)}
	for _, n := range append(append([]string{}, keep...), drop...) {
		os.WriteFile(filepath.Join(dir, n), []byte("{}\n"), 0o600)
	}
	New(dir).Prune(now, 6, 14)
	for _, n := range keep {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Errorf("%s should be kept", n)
		}
	}
	for _, n := range drop {
		if _, err := os.Stat(filepath.Join(dir, n)); err == nil {
			t.Errorf("%s should be deleted", n)
		}
	}
}

func TestIncidentLogLastWins(t *testing.T) {
	dir := t.TempDir()
	l := NewIncidentLog(dir)
	inc := Incident{ID: "i-1", TargetID: "a", Kind: "down", StartedAt: 100}
	l.Write(inc)
	end := int64(200)
	inc.EndedAt = &end
	l.Write(inc)
	l.Write(Incident{ID: "i-2", TargetID: "a", Kind: "degraded", StartedAt: 300})
	l.Close()
	got := NewIncidentLog(dir).Load()
	if len(got) != 2 || got[0].EndedAt == nil || *got[0].EndedAt != 200 || got[1].EndedAt != nil {
		t.Fatalf("got %+v", got)
	}
}
