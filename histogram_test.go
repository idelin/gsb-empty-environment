package swhist

import (
	"math"
	"math/rand"
	"sort"
	"testing"
)

// exactQuantile computes the nearest-rank q-quantile over a fully
// retained sample set — the ground truth the estimator is compared to.
func exactQuantile(samples []float64, q float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	s := make([]float64, len(samples))
	copy(s, samples)
	sort.Float64s(s)
	rank := int(math.Ceil(q * float64(len(s))))
	if rank < 1 {
		rank = 1
	}
	return s[rank-1]
}

// longTail returns a deterministic lognormal-ish latency stream in ms:
// median ~20ms with a heavy right tail (P99 ~130ms).
func longTail(n int, seed int64) []float64 {
	r := rand.New(rand.NewSource(seed))
	out := make([]float64, n)
	for i := range out {
		out[i] = math.Exp(math.Log(20) + 0.8*r.NormFloat64())
	}
	return out
}

func relErr(est, exact float64) float64 {
	if exact == 0 {
		return 0
	}
	return math.Abs(est-exact) / exact
}

// TestPercentileAccuracy streams a long-tail workload through a
// 1000-sample window and checks P90/P99 against full-retention ground
// truth at every step once the window is warm.
func TestPercentileAccuracy(t *testing.T) {
	const (
		capacity = 1000
		stream   = 10000
		maxErr   = 0.005 // 0.5% acceptance bound
	)
	cfg := DefaultConfig()
	h, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	data := longTail(stream, 42)
	window := make([]float64, 0, capacity)
	var worst90, worst99 float64
	for i, v := range data {
		h.Add(v)
		window = append(window, v)
		if len(window) > capacity {
			window = window[1:]
		}
		if i+1 < capacity {
			continue
		}
		if got := h.Size(); got != capacity {
			t.Fatalf("step %d: window size = %d, want %d", i, got, capacity)
		}
		if i%200 == 0 {
			if got := h.Sum(); got != int64(capacity) {
				t.Fatalf("step %d: bucket sum = %d, want %d", i, got, capacity)
			}
		}
		e90 := relErr(h.P90(), exactQuantile(window, 0.90))
		e99 := relErr(h.P99(), exactQuantile(window, 0.99))
		if e90 > worst90 {
			worst90 = e90
		}
		if e99 > worst99 {
			worst99 = e99
		}
	}
	t.Logf("worst relative error over %d steps: P90=%.4f%% P99=%.4f%%",
		stream-capacity+1, worst90*100, worst99*100)
	if worst90 > maxErr {
		t.Errorf("P90 worst relative error %.4f%% exceeds 0.5%%", worst90*100)
	}
	if worst99 > maxErr {
		t.Errorf("P99 worst relative error %.4f%% exceeds 0.5%%", worst99*100)
	}
}

// TestSpikeEviction injects extreme spikes, confirms P99 reacts, then
// confirms it falls back within 0.5% of ground truth once the spikes
// slide out of the window.
func TestSpikeEviction(t *testing.T) {
	const capacity = 1000
	cfg := DefaultConfig()
	h, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	base := longTail(2*capacity+20, 7)
	window := make([]float64, 0, capacity)
	push := func(v float64) {
		h.Add(v)
		window = append(window, v)
		if len(window) > capacity {
			window = window[1:]
		}
	}
	for _, v := range base[:capacity] {
		push(v)
	}
	baselineEst, baselineExact := h.P99(), exactQuantile(window, 0.99)
	t.Logf("baseline P99: est=%.3fms exact=%.3fms", baselineEst, baselineExact)

	// Inject 20 extreme spikes (30s) — 2% of the window, so P99 must jump.
	for i := 0; i < 20; i++ {
		push(30000)
	}
	spikedEst, spikedExact := h.P99(), exactQuantile(window, 0.99)
	t.Logf("spiked  P99: est=%.3fms exact=%.3fms", spikedEst, spikedExact)
	if spikedEst < 10*baselineEst {
		t.Errorf("P99 did not react to spikes: est=%.3f baseline=%.3f", spikedEst, baselineEst)
	}
	if e := relErr(spikedEst, spikedExact); e > 0.005 {
		t.Errorf("spiked P99 relative error %.4f%% exceeds 0.5%%", e*100)
	}

	// Stream a full window of fresh baseline samples: the spikes slide
	// out of the window (FIFO) and P99 must fall back.
	for _, v := range base[capacity+20:] {
		push(v)
	}
	backEst, backExact := h.P99(), exactQuantile(window, 0.99)
	t.Logf("recovered P99: est=%.3fms exact=%.3fms", backEst, backExact)
	if backEst > 2*baselineEst {
		t.Errorf("P99 did not fall back after spikes expired: est=%.3f baseline=%.3f", backEst, baselineEst)
	}
	if e := relErr(backEst, backExact); e > 0.005 {
		t.Errorf("recovered P99 relative error %.4f%% exceeds 0.5%%", e*100)
	}
}

// TestReverseCompensation hammers the window with 100x its capacity and
// verifies aggregate counts always match a fully retained reference.
func TestReverseCompensation(t *testing.T) {
	const capacity = 1000
	cfg := DefaultConfig()
	h, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	data := longTail(100000, 99)
	window := make([]float64, 0, capacity)
	for i, v := range data {
		h.Add(v)
		window = append(window, v)
		if len(window) > capacity {
			window = window[1:]
		}
		if i%5000 != 4999 {
			continue
		}
		if h.Sum() != int64(len(window)) {
			t.Fatalf("step %d: sum=%d want %d", i, h.Sum(), len(window))
		}
		// Cross-check every bucket count against the retained window.
		want := make(map[int]int64)
		for _, w := range window {
			want[h.bucketIndex(w)]++
		}
		for idx, c := range want {
			if h.counts[idx] != c {
				t.Fatalf("step %d: bucket %d count=%d want %d", i, idx, h.counts[idx], c)
			}
		}
	}
}

func TestEdgeCases(t *testing.T) {
	cfg := DefaultConfig()
	h, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := h.P99(); got != 0 {
		t.Errorf("empty P99 = %v, want 0", got)
	}
	h.Add(5)
	if got := h.Size(); got != 1 {
		t.Fatalf("size = %d, want 1", got)
	}
	if est := h.P99(); est < 5 || est > 5+cfg.LinearWidth {
		t.Errorf("single-sample P99 = %v, want within bucket [5, 5.05)", est)
	}
	// Overflow and negative values are clamped, NaN/Inf dropped.
	h.Add(cfg.MaxValue * 100)
	h.Add(-3)
	h.Add(math.NaN())
	h.Add(math.Inf(1))
	if got := h.Size(); got != 3 {
		t.Errorf("size after clamped adds = %d, want 3", got)
	}
	if got := h.BucketCount(cfg.MaxValue); got != 1 {
		t.Errorf("overflow bucket count = %d, want 1", got)
	}
	if got := h.BucketCount(0); got != 1 {
		t.Errorf("zero bucket count = %d, want 1", got)
	}
}

func TestInvalidConfig(t *testing.T) {
	bad := []Config{
		{Capacity: 0, LinearMax: 1, LinearWidth: 1, LogRatio: 2, MaxValue: 2},
		{Capacity: 1, LinearMax: 0, LinearWidth: 1, LogRatio: 2, MaxValue: 2},
		{Capacity: 1, LinearMax: 1, LinearWidth: 0, LogRatio: 2, MaxValue: 2},
		{Capacity: 1, LinearMax: 1, LinearWidth: 1, LogRatio: 1, MaxValue: 2},
		{Capacity: 1, LinearMax: 1, LinearWidth: 1, LogRatio: 2, MaxValue: 1},
	}
	for i, cfg := range bad {
		if _, err := New(cfg); err == nil {
			t.Errorf("bad config %d accepted", i)
		}
	}
}
