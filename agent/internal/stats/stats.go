// Package stats contains allocation-free math helpers: percentiles, jitter,
// a log-scale histogram and the fixed-size live ring buffer.
package stats

import (
	"math"
	"slices"
)

// Percentile returns the p-th percentile (0..100) of vals using linear
// interpolation between closest ranks. vals is sorted in place.
func Percentile(vals []float64, p float64) float64 {
	n := len(vals)
	if n == 0 {
		return math.NaN()
	}
	slices.Sort(vals)
	if n == 1 {
		return vals[0]
	}
	if p <= 0 {
		return vals[0]
	}
	if p >= 100 {
		return vals[n-1]
	}
	rank := p / 100 * float64(n-1)
	lo := int(math.Floor(rank))
	hi := lo + 1
	if hi >= n {
		return vals[n-1]
	}
	frac := rank - float64(lo)
	return vals[lo] + (vals[hi]-vals[lo])*frac
}

// Percentile32 is Percentile for float32 slices (sorted in place).
func Percentile32(vals []float32, p float64) float64 {
	n := len(vals)
	if n == 0 {
		return math.NaN()
	}
	slices.Sort(vals)
	if n == 1 || p <= 0 {
		return float64(vals[0])
	}
	if p >= 100 {
		return float64(vals[n-1])
	}
	rank := p / 100 * float64(n-1)
	lo := int(math.Floor(rank))
	if lo+1 >= n {
		return float64(vals[n-1])
	}
	frac := rank - float64(lo)
	return float64(vals[lo]) + float64(vals[lo+1]-vals[lo])*frac
}

// WeightedPercentile returns the value at which the cumulative weight first
// reaches p% of the total. Both slices are reordered together.
func WeightedPercentile(vals, weights []float64, p float64) float64 {
	n := len(vals)
	if n == 0 || n != len(weights) {
		return math.NaN()
	}
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	slices.SortFunc(idx, func(a, b int) int {
		switch {
		case vals[a] < vals[b]:
			return -1
		case vals[a] > vals[b]:
			return 1
		}
		return 0
	})
	total := 0.0
	for _, w := range weights {
		total += w
	}
	if total <= 0 {
		return vals[idx[n-1]]
	}
	target := p / 100 * total
	cum := 0.0
	for _, i := range idx {
		cum += weights[i]
		if cum >= target-1e-9 {
			return vals[i]
		}
	}
	return vals[idx[n-1]]
}

// Jitter is the mean absolute difference between consecutive values.
// Returns NaN with fewer than two values.
func Jitter(vals []float64) float64 {
	if len(vals) < 2 {
		return math.NaN()
	}
	s := 0.0
	for i := 1; i < len(vals); i++ {
		s += math.Abs(vals[i] - vals[i-1])
	}
	return s / float64(len(vals)-1)
}

// Agg accumulates count/fails/min/max/sum and jitter incrementally.
type Agg struct {
	N, Fails      int
	Sum, Min, Max float64
	JitSum        float64
	JitN          int
	prev          float64
	hasPrev       bool
}

// Add records one probe result. Failures do not break the jitter chain.
func (a *Agg) Add(rtt float64, ok bool) {
	a.N++
	if !ok {
		a.Fails++
		return
	}
	succ := a.N - a.Fails
	if succ == 1 || rtt < a.Min {
		a.Min = rtt
	}
	if succ == 1 || rtt > a.Max {
		a.Max = rtt
	}
	a.Sum += rtt
	if a.hasPrev {
		a.JitSum += math.Abs(rtt - a.prev)
		a.JitN++
	}
	a.prev, a.hasPrev = rtt, true
}

func (a *Agg) Successes() int { return a.N - a.Fails }

func (a *Agg) Avg() float64 {
	if a.Successes() == 0 {
		return math.NaN()
	}
	return a.Sum / float64(a.Successes())
}

func (a *Agg) Jitter() float64 {
	if a.JitN == 0 {
		return math.NaN()
	}
	return a.JitSum / float64(a.JitN)
}

func (a *Agg) LossPct() float64 {
	if a.N == 0 {
		return math.NaN()
	}
	return float64(a.Fails) * 100 / float64(a.N)
}

// LogHist is a fixed log-scale histogram (5% bucket width, 0.05 ms .. ~1.1e5 ms).
const (
	histBins  = 300
	histBase  = 0.05
	histRatio = 1.05
)

type LogHist struct {
	Bins  [histBins]float64
	Total float64
}

var logRatio = math.Log(histRatio)

func histBin(v float64) int {
	if v <= histBase {
		return 0
	}
	b := int(math.Log(v/histBase) / logRatio)
	if b >= histBins {
		b = histBins - 1
	}
	return b
}

// Add adds value v with weight w.
func (h *LogHist) Add(v, w float64) {
	if w <= 0 || math.IsNaN(v) {
		return
	}
	h.Bins[histBin(v)] += w
	h.Total += w
}

// Percentile returns the geometric centre of the bucket containing p%.
func (h *LogHist) Percentile(p float64) float64 {
	if h.Total <= 0 {
		return math.NaN()
	}
	target := p / 100 * h.Total
	cum := 0.0
	for i, c := range h.Bins {
		cum += c
		if cum >= target-1e-9 && c > 0 {
			lo := histBase * math.Pow(histRatio, float64(i))
			return lo * math.Sqrt(histRatio)
		}
	}
	return math.NaN()
}

// Round2 rounds to 2 decimals (NaN passes through).
func Round2(v float64) float64 { return math.Round(v*100) / 100 }

// F returns a *float64 rounded to 2 decimals, or nil for NaN/Inf.
func F(v float64) *float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	r := Round2(v)
	return &r
}
