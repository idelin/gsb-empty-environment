package metrics

import (
	"math"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCounterGauge(t *testing.T) {
	var c Counter
	c.Inc()
	c.Add(41)
	if c.Value() != 42 {
		t.Fatalf("counter = %d, want 42", c.Value())
	}
	var g Gauge
	g.Set(10)
	g.Add(-4)
	if g.Value() != 6 {
		t.Fatalf("gauge = %d, want 6", g.Value())
	}
}

func TestHistogramBucketsAndStats(t *testing.T) {
	h := NewHistogram(5, 1, 3, 3, 1) // unsorted, duplicated bounds
	for _, v := range []float64{0.5, 1, 2, 4, 100} {
		h.Observe(v)
	}
	s := h.Snapshot()
	if s.Count != 5 {
		t.Fatalf("count = %d, want 5", s.Count)
	}
	if s.Min != 0.5 || s.Max != 100 {
		t.Fatalf("min/max = %v/%v", s.Min, s.Max)
	}
	if s.Mean() != (0.5+1+2+4+100)/5 {
		t.Fatalf("mean = %v", s.Mean())
	}
	// Cumulative buckets: <=1 -> 2, <=3 -> 3, <=5 -> 4, +Inf -> 5.
	want := []struct {
		le    float64
		count uint64
	}{{1, 2}, {3, 3}, {5, 4}, {math.Inf(1), 5}}
	if len(s.Buckets) != len(want) {
		t.Fatalf("buckets = %v", s.Buckets)
	}
	for i, w := range want {
		if s.Buckets[i].LE != w.le || s.Buckets[i].Count != w.count {
			t.Errorf("bucket %d = %+v, want %+v", i, s.Buckets[i], w)
		}
	}
}

func TestHistogramEmpty(t *testing.T) {
	s := NewHistogram(1, 2).Snapshot()
	if s.Count != 0 || s.Mean() != 0 {
		t.Fatalf("empty snapshot: %+v", s)
	}
}

func TestWindowExactPercentiles(t *testing.T) {
	w := NewWindow(time.Minute)
	base := time.Unix(1000, 0)
	now := base
	w.now = func() time.Time { return now }
	for i := 1; i <= 100; i++ {
		w.Observe(float64(i))
	}
	s := w.Snapshot()
	if s.Count != 100 || s.Total != 100 {
		t.Fatalf("count/total = %d/%d", s.Count, s.Total)
	}
	if s.Min != 1 || s.Max != 100 || s.Mean != 50.5 {
		t.Fatalf("stats = %+v", s)
	}
	if s.P50 != 50 || s.P90 != 90 || s.P99 != 99 {
		t.Fatalf("percentiles = %v/%v/%v", s.P50, s.P90, s.P99)
	}
	if got := w.Percentile(100); got != 100 {
		t.Fatalf("p100 = %v", got)
	}
}

func TestWindowEviction(t *testing.T) {
	w := NewWindow(10 * time.Second)
	now := time.Unix(0, 0)
	w.now = func() time.Time { return now }
	for i := 0; i < 5; i++ {
		w.Observe(float64(i))
		now = now.Add(time.Second)
	}
	now = now.Add(20 * time.Second) // everything expires
	w.Observe(99)
	s := w.Snapshot()
	if s.Count != 1 || s.Total != 6 {
		t.Fatalf("after eviction: %+v", s)
	}
	if s.Min != 99 || s.Max != 99 {
		t.Fatalf("survivor: %+v", s)
	}
	// The backing slice must not retain evicted entries (no leak).
	w.mu.Lock()
	live := len(w.s)
	w.mu.Unlock()
	if live != 1 {
		t.Fatalf("live samples = %d, want 1", live)
	}
}

func TestWindowEmpty(t *testing.T) {
	w := NewWindow(time.Second)
	if got := w.Percentile(50); got != 0 {
		t.Fatalf("empty percentile = %v", got)
	}
	if s := w.Snapshot(); s.Count != 0 || s.Total != 0 || s.P99 != 0 {
		t.Fatalf("empty snapshot: %+v", s)
	}
}

func TestHitRate(t *testing.T) {
	var h HitRate
	if h.Ratio() != 0 {
		t.Fatal("empty ratio must be 0")
	}
	h.Hit()
	h.Hit()
	h.Hit()
	h.Miss()
	if h.Ratio() != 0.75 {
		t.Fatalf("ratio = %v, want 0.75", h.Ratio())
	}
	hits, misses := h.Counts()
	if hits != 3 || misses != 1 {
		t.Fatalf("counts = %d/%d", hits, misses)
	}
}

func TestErrorCodes(t *testing.T) {
	e := NewErrorCodes()
	e.Inc("truncated")
	e.Inc("truncated")
	e.Inc("overflow")
	if e.Count("truncated") != 2 || e.Count("overflow") != 1 || e.Count("nope") != 0 {
		t.Fatalf("tallies: %v", e.Snapshot())
	}
	snap := e.Snapshot()
	snap["truncated"] = 99 // mutating the copy must not affect the source
	if e.Count("truncated") != 2 {
		t.Fatal("snapshot is not isolated")
	}
}

func TestRegistryAndExport(t *testing.T) {
	var r Registry
	r.Counter("decode_calls_total").Add(7)
	r.Gauge("inflight").Set(2)
	r.Histogram("decode_latency_ms", 1, 10).Observe(0.5)
	r.HitRate("schema_cache").Hit()
	r.Window("lat_win", time.Minute).Observe(1)
	r.Errors().Inc("truncated")

	s := r.Snapshot()
	if s.Counters["decode_calls_total"] != 7 || s.Gauges["inflight"] != 2 {
		t.Fatalf("snapshot: %+v", s)
	}
	if s.HitRates["schema_cache"] != 1 {
		t.Fatalf("hit rate: %+v", s.HitRates)
	}
	out := r.Export()
	for _, want := range []string{
		"decode_calls_total 7",
		"inflight 2",
		`decode_latency_ms_bucket{le="1"} 1`,
		`decode_latency_ms_bucket{le="+Inf"} 1`,
		"decode_latency_ms_count 1",
		"schema_cache 1",
		`decode_errors_total{code="truncated"} 1`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("export missing %q:\n%s", want, out)
		}
	}
}

func TestConcurrentSafety(t *testing.T) {
	var r Registry
	const workers = 8
	const iters = 2000
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				r.Counter("c").Inc()
				r.Gauge("g").Add(1)
				r.Histogram("h", 1, 10, 100).Observe(float64(i % 200))
				r.Window("w", time.Minute).Observe(float64(i))
				if i%2 == 0 {
					r.HitRate("hr").Hit()
				} else {
					r.HitRate("hr").Miss()
				}
				if i%100 == 0 {
					r.Errors().Inc("boom")
				}
				_ = r.Snapshot()
			}
		}(w)
	}
	wg.Wait()
	if got := r.Counter("c").Value(); got != workers*iters {
		t.Fatalf("counter = %d, want %d", got, workers*iters)
	}
	if got := r.Histogram("h").Snapshot().Count; got != workers*iters {
		t.Fatalf("histogram count = %d", got)
	}
	hits, misses := r.HitRate("hr").Counts()
	if hits+misses != workers*iters {
		t.Fatalf("hit rate total = %d", hits+misses)
	}
	if got := r.Errors().Count("boom"); got != workers*(iters/100) {
		t.Fatalf("errors = %d", got)
	}
	if got := r.Window("w", time.Minute).Snapshot().Total; got != workers*iters {
		t.Fatalf("window total = %d", got)
	}
}

func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	var r Registry
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				r.Counter("x").Inc()
				_ = r.Export()
			}
		}()
	}
	wg.Wait()
	// The package spawns no background goroutines, so after our own
	// workers exit the count must return to baseline.
	for i := 0; i < 50; i++ {
		if runtime.NumGoroutine() <= before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: before=%d after=%d", before, runtime.NumGoroutine())
}
