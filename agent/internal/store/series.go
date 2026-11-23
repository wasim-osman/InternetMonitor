package store

import (
	"math"

	"imonitord/internal/stats"
)

// Point is one bucket of a series. Latency fields are nil when every probe in
// the bucket failed.
type Point struct {
	T    int64    `json:"t"`
	N    int      `json:"n"`
	Loss float64  `json:"loss"`
	Min  *float64 `json:"min,omitempty"`
	Avg  *float64 `json:"avg,omitempty"`
	Max  *float64 `json:"max,omitempty"`
	P95  *float64 `json:"p95,omitempty"`
}

type Series struct {
	Target  string  `json:"target"`
	StepSec int64   `json:"stepSec"`
	Source  string  `json:"source"`
	Points  []Point `json:"points"`
}

// SeriesQuery describes a /v1/series request.
type SeriesQuery struct {
	IDs               []string
	From, To          int64
	Points            int
	IntervalMs        func(id string) int // effective probe interval per target
	RawRetentionHours int
	Now               int64
}

// PlanStep decides source and step for one target.
func PlanStep(from, to int64, points, intervalMs int, rawRetentionHours int, now int64) (source string, step int64) {
	if points < 1 {
		points = 1
	}
	if intervalMs < 1 {
		intervalMs = 1000
	}
	spanMs := (to - from) * 1000
	if spanMs < 1000 {
		spanMs = 1000
	}
	want := (spanMs + int64(points) - 1) / int64(points) // ceil(span/points) in ms
	iv := int64(intervalMs)
	stepMs := ((want + iv - 1) / iv) * iv // round up to a multiple of the interval
	step = (stepMs + 999) / 1000
	if step < 1 {
		step = 1
	}
	if step < 60 && from >= now-int64(rawRetentionHours)*3600 {
		return "raw", step
	}
	wantSec := (want + 999) / 1000
	step = ((wantSec + 59) / 60) * 60
	if step < 60 {
		step = 60
	}
	return "rollup", step
}

type rawBucket struct {
	agg  stats.Agg
	vals []float32
}

type rollBucket struct {
	n, fails   int
	min, max   float64
	sum        float64 // avg * successes
	succ       int
	p95v, p95w []float64
}

func (s *Store) Series(q SeriesQuery) []Series {
	s.Flush()
	out := make([]Series, len(q.IDs))
	var rawIDs, rollIDs []string
	steps := make(map[string]int64, len(q.IDs))
	idx := make(map[string]int, len(q.IDs))
	for i, id := range q.IDs {
		src, step := PlanStep(q.From, q.To, q.Points, q.IntervalMs(id), q.RawRetentionHours, q.Now)
		out[i] = Series{Target: id, StepSec: step, Source: src, Points: []Point{}}
		steps[id] = step
		idx[id] = i
		if src == "raw" {
			rawIDs = append(rawIDs, id)
		} else {
			rollIDs = append(rollIDs, id)
		}
	}
	if len(rawIDs) > 0 {
		buckets := make(map[string]map[int64]*rawBucket, len(rawIDs))
		for _, id := range rawIDs {
			buckets[id] = make(map[int64]*rawBucket)
		}
		match := targetMatcher(rawIDs)
		for _, path := range hourFiles(s.dir, q.From, q.To) {
			scanFile(path, func(line []byte) {
				var t int64 = -1
				id := ""
				ms := math.NaN()
				hasMs := false
				ok := scanFlat(line, func(k, v []byte) {
					switch bstr(k) {
					case "t":
						t = intVal(v)
					case "target":
						id = match(v)
					case "ms":
						ms = numVal(v)
						hasMs = !math.IsNaN(ms)
					}
				})
				if !ok || id == "" || t < q.From || t > q.To {
					return
				}
				step := steps[id]
				bt := t - mod(t, step)
				m := buckets[id]
				b := m[bt]
				if b == nil {
					b = &rawBucket{}
					m[bt] = b
				}
				b.agg.Add(ms, hasMs)
				if hasMs {
					b.vals = append(b.vals, float32(ms))
				}
			})
		}
		for id, m := range buckets {
			pts := make([]Point, 0, len(m))
			for _, bt := range sortedKeys(m) {
				b := m[bt]
				p := Point{T: bt, N: b.agg.N, Loss: stats.Round2(b.agg.LossPct())}
				if b.agg.Successes() > 0 {
					p.Min, p.Avg, p.Max = stats.F(b.agg.Min), stats.F(b.agg.Avg()), stats.F(b.agg.Max)
					p.P95 = stats.F(stats.Percentile32(b.vals, 95))
				}
				pts = append(pts, p)
			}
			out[idx[id]].Points = pts
		}
	}
	if len(rollIDs) > 0 {
		buckets := make(map[string]map[int64]*rollBucket, len(rollIDs))
		for _, id := range rollIDs {
			buckets[id] = make(map[int64]*rollBucket)
		}
		match := targetMatcher(rollIDs)
		for _, path := range dayFiles(s.dir, q.From, q.To) {
			scanFile(path, func(line []byte) {
				id, r, ok := parseRollup(line, match)
				if !ok || r.t < q.From || r.t > q.To {
					return
				}
				step := steps[id]
				bt := r.t - mod(r.t, step)
				m := buckets[id]
				b := m[bt]
				if b == nil {
					b = &rollBucket{min: math.Inf(1), max: math.Inf(-1)}
					m[bt] = b
				}
				addRollup(b, r)
			})
		}
		for id, m := range buckets {
			pts := make([]Point, 0, len(m))
			for _, bt := range sortedKeys(m) {
				b := m[bt]
				p := Point{T: bt, N: b.n}
				if b.n > 0 {
					p.Loss = stats.Round2(float64(b.fails) * 100 / float64(b.n))
				}
				if b.succ > 0 {
					p.Min, p.Max = stats.F(b.min), stats.F(b.max)
					p.Avg = stats.F(b.sum / float64(b.succ))
					p.P95 = stats.F(stats.WeightedPercentile(b.p95v, b.p95w, 95))
				}
				pts = append(pts, p)
			}
			out[idx[id]].Points = pts
		}
	}
	return out
}

func addRollup(b *rollBucket, r rollupRec) {
	b.n += r.n
	b.fails += r.fails
	succ := r.n - r.fails
	if succ <= 0 || math.IsNaN(r.avg) {
		return
	}
	b.succ += succ
	b.sum += r.avg * float64(succ)
	if !math.IsNaN(r.min) && r.min < b.min {
		b.min = r.min
	}
	if !math.IsNaN(r.max) && r.max > b.max {
		b.max = r.max
	}
	if !math.IsNaN(r.p95) {
		b.p95v = append(b.p95v, r.p95)
		b.p95w = append(b.p95w, float64(succ))
	}
}
