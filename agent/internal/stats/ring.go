package stats

import "math"

// RingCap bounds the live window: 60 s at the minimum 500 ms interval is 120.
const RingCap = 128

type ringEntry struct {
	tMs int64
	rtt float32
	ok  bool
}

// Ring is a fixed-size buffer of the most recent probe results.
type Ring struct {
	buf  [RingCap]ringEntry
	head int // next write position
	n    int
}

func (r *Ring) Add(tMs int64, rtt float64, ok bool) {
	r.buf[r.head] = ringEntry{tMs: tMs, rtt: float32(rtt), ok: ok}
	r.head = (r.head + 1) % RingCap
	if r.n < RingCap {
		r.n++
	}
}

func (r *Ring) Reset() { *r = Ring{} }

// WindowStats summarises entries with t >= sinceMs (chronological order).
type WindowStats struct {
	Samples int
	Fails   int
	Min     float64
	Max     float64
	Avg     float64
	P95     float64
	Jitter  float64
	LossPct float64
}

// Window computes stats without heap allocation.
func (r *Ring) Window(sinceMs int64) WindowStats {
	var vals [RingCap]float32
	nv := 0
	ws := WindowStats{Min: math.NaN(), Max: math.NaN(), Avg: math.NaN(), P95: math.NaN(), Jitter: math.NaN(), LossPct: math.NaN()}
	start := (r.head - r.n + RingCap) % RingCap
	var sum, jit float64
	jitN := 0
	var prev float32
	hasPrev := false
	for i := 0; i < r.n; i++ {
		e := r.buf[(start+i)%RingCap]
		if e.tMs < sinceMs {
			continue
		}
		ws.Samples++
		if !e.ok {
			ws.Fails++
			continue
		}
		v := float64(e.rtt)
		if nv == 0 || v < ws.Min {
			ws.Min = v
		}
		if nv == 0 || v > ws.Max {
			ws.Max = v
		}
		sum += v
		if hasPrev {
			jit += math.Abs(v - float64(prev))
			jitN++
		}
		prev, hasPrev = e.rtt, true
		vals[nv] = e.rtt
		nv++
	}
	if ws.Samples > 0 {
		ws.LossPct = float64(ws.Fails) * 100 / float64(ws.Samples)
	}
	if nv > 0 {
		ws.Avg = sum / float64(nv)
		ws.P95 = Percentile32(vals[:nv], 95)
	}
	if jitN > 0 {
		ws.Jitter = jit / float64(jitN)
	}
	return ws
}
