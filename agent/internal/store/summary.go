package store

import (
	"math"

	"imonitord/internal/stats"
)

type WindowDef struct {
	Label string
	Sec   int64
}

var SummaryWindows = []WindowDef{{"1h", 3600}, {"24h", 86400}, {"7d", 7 * 86400}, {"30d", 30 * 86400}, {"60d", 60 * 86400}}

type SummaryWindow struct {
	Label     string   `json:"label"`
	Seconds   int64    `json:"seconds"`
	UptimePct *float64 `json:"uptimePct"`
	LossPct   *float64 `json:"lossPct"`
	AvgMs     *float64 `json:"avgMs"`
	P95Ms     *float64 `json:"p95Ms"`
	MinMs     *float64 `json:"minMs"`
	MaxMs     *float64 `json:"maxMs"`
	JitterMs  *float64 `json:"jitterMs"`
	Samples   int      `json:"samples"`
}

type TargetSummary struct {
	Target  string          `json:"target"`
	Windows []SummaryWindow `json:"windows"`
}

type winAcc struct {
	n, fails, succ int
	sum            float64
	min, max       float64
	jitSum, jitW   float64
	hist           stats.LogHist
	first          int64
}

// Summary aggregates 1-minute rollups into the 1h/24h/7d/30d windows.
// downSec(id, from, to) returns seconds spent in "down" incidents in [from,to].
// p95 is approximated as the success-weighted 95th percentile of per-minute p95s.
func (s *Store) Summary(ids []string, now int64, rollupDays int, downSec func(id string, from, to int64) float64) []TargetSummary {
	s.Flush()
	accs := make(map[string]*[5]winAcc, len(ids))
	for _, id := range ids {
		a := new([5]winAcc)
		for i := range a {
			a[i].min, a[i].max, a[i].first = math.Inf(1), math.Inf(-1), -1
		}
		accs[id] = a
	}
	maxWin := SummaryWindows[len(SummaryWindows)-1].Sec
	if r := int64(rollupDays) * 86400; r < maxWin {
		maxWin = r
	}
	from := now - maxWin
	match := targetMatcher(ids)
	for _, path := range dayFiles(s.dir, from, now) {
		scanFile(path, func(line []byte) {
			id, r, ok := parseRollup(line, match)
			if !ok || r.t < from || r.t > now {
				return
			}
			a := accs[id]
			for i, w := range SummaryWindows {
				if r.t < now-w.Sec {
					continue
				}
				wa := &a[i]
				if wa.first < 0 || r.t < wa.first {
					wa.first = r.t
				}
				wa.n += r.n
				wa.fails += r.fails
				succ := r.n - r.fails
				if succ <= 0 || math.IsNaN(r.avg) {
					continue
				}
				wa.succ += succ
				wa.sum += r.avg * float64(succ)
				if !math.IsNaN(r.min) && r.min < wa.min {
					wa.min = r.min
				}
				if !math.IsNaN(r.max) && r.max > wa.max {
					wa.max = r.max
				}
				if !math.IsNaN(r.jit) && succ > 1 {
					wa.jitSum += r.jit * float64(succ-1)
					wa.jitW += float64(succ - 1)
				}
				if !math.IsNaN(r.p95) {
					wa.hist.Add(r.p95, float64(succ))
				}
			}
		})
	}
	out := make([]TargetSummary, 0, len(ids))
	for _, id := range ids {
		a := accs[id]
		ts := TargetSummary{Target: id, Windows: make([]SummaryWindow, 0, len(SummaryWindows))}
		for i, w := range SummaryWindows {
			wa := &a[i]
			sw := SummaryWindow{Label: w.Label, Samples: wa.n}
			if wa.n > 0 {
				start := now - w.Sec
				if wa.first > start {
					start = wa.first
				}
				sw.Seconds = now - start
				if sw.Seconds > 0 {
					down := 0.0
					if downSec != nil {
						down = downSec(id, start, now)
					}
					up := 100 * (1 - down/float64(sw.Seconds))
					sw.UptimePct = stats.F(math.Max(0, math.Min(100, up)))
				} else {
					sw.UptimePct = stats.F(100)
				}
				sw.LossPct = stats.F(float64(wa.fails) * 100 / float64(wa.n))
			}
			if wa.succ > 0 {
				sw.AvgMs = stats.F(wa.sum / float64(wa.succ))
				sw.MinMs, sw.MaxMs = stats.F(wa.min), stats.F(wa.max)
				sw.P95Ms = stats.F(wa.hist.Percentile(95))
			}
			if wa.jitW > 0 {
				sw.JitterMs = stats.F(wa.jitSum / wa.jitW)
			}
			ts.Windows = append(ts.Windows, sw)
		}
		out = append(out, ts)
	}
	return out
}
