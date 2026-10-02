package slidinghist

import (
	"math"
	"math/rand"
	"slices"
	"testing"
)

// exactQuantile is the ground-truth reference: it retains all raw samples
// and sorts them, exactly what the histogram avoids doing.
func exactQuantile(samples []float64, q float64) float64 {
	s := slices.Clone(samples)
	slices.Sort(s)
	rank := int(math.Ceil(q * float64(len(s))))
	if rank < 1 {
		rank = 1
	}
	return s[rank-1]
}

func relErr(est, exact float64) float64 {
	if exact == 0 {
		return math.Abs(est)
	}
	return math.Abs(est-exact) / exact
}

func TestBucketLayoutBounds(t *testing.T) {
	cfg := DefaultConfig()
	lay := newLayout(cfg)

	// Bucket indexes must be monotonic and every value must land inside
	// the bounds of its own bucket.
	r := rand.New(rand.NewSource(1))
	prev := -1
	for i := 0; i < 100000; i++ {
		// Increasing values: bucket indexes must be non-decreasing.
		v := math.Exp(float64(i)/100000*math.Log(cfg.MaxValue*10)) - 1
		v += r.Float64() * 1e-9 // jitter to dodge exact boundaries
		idx := lay.index(v)
		if idx < prev {
			t.Fatalf("index not monotonic: v=%g idx=%d prev=%d", v, idx, prev)
		}
		prev = idx
		lo, hi := lay.bounds(idx)
		if v < cfg.MaxValue && (v < lo || v >= hi) {
			t.Fatalf("v=%g not in bucket %d bounds [%g,%g)", v, idx, lo, hi)
		}
		// Representative value must stay within theoretical error bound.
		rep := lay.representative(idx)
		if v >= cfg.FixedMax && v < cfg.MaxValue {
			if got := relErr(rep, v); got > (cfg.LogGrowth-1)/2+1e-12 {
				t.Fatalf("v=%g rep=%g rel err %g exceeds bound", v, rep, got)
			}
		}
	}
}

func TestWindowExpiryAndReverseCompensation(t *testing.T) {
	w, err := NewWindow(1000, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	// Push 3x capacity; live count must stay at capacity and the
	// per-bucket counts must exactly mirror the last 1000 samples.
	const n = 3000
	vals := make([]float64, 0, n)
	r := rand.New(rand.NewSource(2))
	for i := 0; i < n; i++ {
		v := 1 + r.Float64()*500
		vals = append(vals, v)
		w.Add(v)
	}
	if w.Count() != 1000 {
		t.Fatalf("Count()=%d, want 1000", w.Count())
	}
	var total uint64
	for _, c := range w.counts {
		total += c
	}
	if total != 1000 {
		t.Fatalf("sum of bucket counts=%d, want 1000", total)
	}
	// Rebuild the expected histogram from the last 1000 raw samples and
	// compare bucket by bucket.
	want := make([]uint64, w.lay.totalBuckets())
	for _, v := range vals[n-1000:] {
		want[w.lay.index(v)]++
	}
	for i := range want {
		if w.counts[i] != want[i] {
			t.Fatalf("bucket %d: got %d, want %d", i, w.counts[i], want[i])
		}
	}
}

// Acceptance: window=1000, continuous long-tail stream, estimated P90/P99
// must stay within 0.5% of the exact quantiles over all retained samples.
func TestQuantileAccuracyLongTail(t *testing.T) {
	const (
		capacity = 1000
		stream   = 20000
		maxErr   = 0.005 // 0.5%
	)
	w, err := NewWindow(capacity, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	r := rand.New(rand.NewSource(42))
	keep := make([]float64, 0, capacity)

	var worstP90, worstP99 float64
	for i := 0; i < stream; i++ {
		// Long-tail latency: lognormal body (~20ms median) plus rare
		// extreme spikes.
		v := math.Exp(3.0 + 0.9*r.NormFloat64())
		if r.Float64() < 0.005 {
			v *= 20 + 80*r.Float64()
		}
		w.Add(v)
		keep = append(keep, v)
		if len(keep) > capacity {
			keep = keep[1:]
		}
		if len(keep) < capacity {
			continue
		}
		if e := relErr(w.P90(), exactQuantile(keep, 0.90)); e > worstP90 {
			worstP90 = e
		}
		if e := relErr(w.P99(), exactQuantile(keep, 0.99)); e > worstP99 {
			worstP99 = e
		}
	}
	t.Logf("worst relative error over %d windows: P90=%.4f%% P99=%.4f%% (bound %.4f%%)",
		stream, worstP90*100, worstP99*100, maxErr*100)
	if worstP90 > maxErr {
		t.Errorf("P90 worst relative error %.4f%% exceeds 0.5%%", worstP90*100)
	}
	if worstP99 > maxErr {
		t.Errorf("P99 worst relative error %.4f%% exceeds 0.5%%", worstP99*100)
	}
}

// Acceptance: extreme spikes must inflate P99 while in the window, and P99
// must fall back sensitively (within 0.5% of exact) once they slide out.
func TestSpikeEvictionRecovery(t *testing.T) {
	const capacity = 1000
	w, err := NewWindow(capacity, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	r := rand.New(rand.NewSource(7))
	baseline := func() float64 { return 5 + r.Float64()*15 } // 5-20ms

	push := func(v float64) {
		w.Add(v)
	}
	for i := 0; i < capacity; i++ {
		push(baseline())
	}
	baseP99 := w.P99()

	// Spike storm: 30 extreme samples (3% of the window) at ~8s.
	for i := 0; i < 30; i++ {
		push(8000 + r.Float64()*1000)
	}
	spikeP99 := w.P99()
	if spikeP99 < 4000 {
		t.Fatalf("P99 did not react to spikes: got %g", spikeP99)
	}
	t.Logf("P99 baseline=%.2fms -> during spikes=%.2fms", baseP99, spikeP99)

	// Push a full window of baseline traffic; every spike slides out.
	keep := make([]float64, 0, capacity)
	for i := 0; i < capacity; i++ {
		v := baseline()
		push(v)
		keep = append(keep, v)
	}
	got, exact := w.P99(), exactQuantile(keep, 0.99)
	t.Logf("P99 after eviction: estimated=%.4fms exact=%.4fms relErr=%.4f%%",
		got, exact, relErr(got, exact)*100)
	if got > baseP99*1.5 {
		t.Errorf("P99=%.2f did not fall back near baseline %.2f", got, baseP99)
	}
	if e := relErr(got, exact); e > 0.005 {
		t.Errorf("post-eviction P99 relative error %.4f%% exceeds 0.5%%", e*100)
	}
}

func TestEmptyAndEdgeCases(t *testing.T) {
	w, err := NewWindow(10, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if got := w.P99(); got != 0 {
		t.Fatalf("empty window P99=%g, want 0", got)
	}
	w.Add(-5)   // clamped to bucket 0
	w.Add(1e12) // overflow bucket
	w.Add(0)    // lower edge
	if w.Count() != 3 {
		t.Fatalf("Count()=%d, want 3", w.Count())
	}
	if got := w.Quantile(1.0); got != DefaultConfig().MaxValue {
		t.Fatalf("max quantile=%g, want overflow representative %g", got, DefaultConfig().MaxValue)
	}
	if _, err := NewWindow(0, DefaultConfig()); err == nil {
		t.Fatal("expected error for zero capacity")
	}
	if _, err := NewWindow(10, Config{FixedWidth: 1, FixedMax: 1, LogGrowth: 1.1, MaxValue: 100}); err == nil {
		t.Fatal("expected error for invalid config")
	}
}
