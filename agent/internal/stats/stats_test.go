package stats

import (
	"math"
	"testing"
)

func near(a, b, eps float64) bool { return math.Abs(a-b) <= eps }

func TestPercentile(t *testing.T) {
	v := []float64{10, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	if p := Percentile(v, 50); !near(p, 5.5, 1e-9) {
		t.Fatalf("p50 = %v", p)
	}
	v = []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21}
	if p := Percentile(v, 95); !near(p, 20, 1e-9) {
		t.Fatalf("p95 = %v", p)
	}
	if p := Percentile([]float64{7}, 95); p != 7 {
		t.Fatal(p)
	}
	if !math.IsNaN(Percentile(nil, 95)) {
		t.Fatal("empty should be NaN")
	}
	if p := Percentile32([]float32{3, 1, 2}, 100); p != 3 {
		t.Fatal(p)
	}
}

func TestWeightedPercentile(t *testing.T) {
	vals := []float64{100, 10, 20}
	w := []float64{1, 90, 9}
	if p := WeightedPercentile(vals, w, 50); p != 10 {
		t.Fatalf("p50 = %v", p)
	}
	if p := WeightedPercentile(vals, w, 95); p != 20 {
		t.Fatalf("p95 = %v", p)
	}
	if p := WeightedPercentile(vals, w, 100); p != 100 {
		t.Fatalf("p100 = %v", p)
	}
}

func TestJitterAndAgg(t *testing.T) {
	if j := Jitter([]float64{10, 12, 9, 9}); !near(j, (2+3+0)/3.0, 1e-9) {
		t.Fatalf("jitter %v", j)
	}
	var a Agg
	a.Add(10, true)
	a.Add(0, false)
	a.Add(14, true)
	a.Add(12, true)
	if a.N != 4 || a.Fails != 1 || a.Min != 10 || a.Max != 14 || !near(a.Avg(), 12, 1e-9) {
		t.Fatalf("agg %+v avg %v", a, a.Avg())
	}
	if !near(a.LossPct(), 25, 1e-9) || !near(a.Jitter(), 3, 1e-9) {
		t.Fatalf("loss %v jitter %v", a.LossPct(), a.Jitter())
	}
}

func TestRingWindow(t *testing.T) {
	var r Ring
	// 200 samples: only the last 128 are kept
	for i := 0; i < 200; i++ {
		r.Add(int64(i)*1000, float64(i), i%10 != 0)
	}
	w := r.Window(0)
	if w.Samples != RingCap {
		t.Fatalf("samples %d", w.Samples)
	}
	w = r.Window(190 * 1000) // t=190..199 -> 10 samples, 190 failed
	if w.Samples != 10 || w.Fails != 1 || w.Min != 191 || w.Max != 199 || !near(w.Avg, 195, 1e-9) || !near(w.LossPct, 10, 1e-9) || !near(w.Jitter, 1, 1e-9) {
		t.Fatalf("window %+v", w)
	}
	var empty Ring
	if w := empty.Window(0); w.Samples != 0 || !math.IsNaN(w.Avg) {
		t.Fatalf("empty %+v", w)
	}
}

func TestLogHist(t *testing.T) {
	var h LogHist
	for i := 1; i <= 1000; i++ {
		h.Add(float64(i), 1)
	}
	p := h.Percentile(95)
	if math.Abs(p-950)/950 > 0.05 {
		t.Fatalf("hist p95 %v", p)
	}
}
