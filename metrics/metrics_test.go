package metrics

import (
	"bytes"
	"encoding/json"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCounterConcurrent(t *testing.T) {
	c := &Counter{}
	var wg sync.WaitGroup
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				c.Inc()
			}
		}()
	}
	wg.Wait()
	if got := c.Value(); got != 64000 {
		t.Fatalf("counter = %d, want 64000", got)
	}
}

func TestGaugeConcurrent(t *testing.T) {
	g := &Gauge{}
	var wg sync.WaitGroup
	for w := 0; w < 32; w++ {
		wg.Add(2)
		go func() { defer wg.Done(); g.Add(1) }()
		go func() { defer wg.Done(); g.Add(-1) }()
	}
	wg.Wait()
	if got := g.Value(); got != 0 {
		t.Fatalf("gauge = %d, want 0", got)
	}
	g.Set(42)
	if got := g.Value(); got != 42 {
		t.Fatalf("gauge = %d, want 42", got)
	}
}

func TestTimerEmptySnapshot(t *testing.T) {
	snap := NewTimer(nil).Snapshot()
	if snap.Count != 0 || snap.MinNs != 0 || snap.MaxNs != 0 ||
		snap.MeanNs != 0 || snap.P50Ns != 0 || snap.P90Ns != 0 || snap.P99Ns != 0 {
		t.Fatalf("empty timer snapshot not zero: %+v", snap)
	}
	if len(snap.Buckets) != len(DefaultBuckets)+1 {
		t.Fatalf("buckets = %d, want %d", len(snap.Buckets), len(DefaultBuckets)+1)
	}
	for _, b := range snap.Buckets {
		if b.CumulativeCount != 0 {
			t.Fatalf("empty bucket has count %d", b.CumulativeCount)
		}
	}
}

func TestTimerBucketBoundaries(t *testing.T) {
	// Buckets at 1ms and 10ms.
	tm := NewTimer([]float64{0.001, 0.01})
	tm.Observe(500 * time.Microsecond)  // -> bucket 0 (<=1ms)
	tm.Observe(1 * time.Millisecond)    // exactly on bound -> bucket 0
	tm.Observe(1500 * time.Microsecond) // -> bucket 1 (<=10ms)
	tm.Observe(20 * time.Millisecond)   // -> +Inf bucket
	tm.Observe(-5 * time.Millisecond)   // clamped to 0 -> bucket 0
	snap := tm.Snapshot()
	if snap.Count != 5 {
		t.Fatalf("count = %d, want 5", snap.Count)
	}
	wantCum := []uint64{3, 4, 5}
	for i, want := range wantCum {
		if snap.Buckets[i].CumulativeCount != want {
			t.Fatalf("bucket %d cumulative = %d, want %d", i, snap.Buckets[i].CumulativeCount, want)
		}
	}
	if snap.MinNs != 0 {
		t.Fatalf("min = %d, want 0 (clamped negative)", snap.MinNs)
	}
	if snap.MaxNs != int64(20*time.Millisecond) {
		t.Fatalf("max = %d, want %d", snap.MaxNs, int64(20*time.Millisecond))
	}
	wantMean := int64(500*time.Microsecond+time.Millisecond+1500*time.Microsecond+20*time.Millisecond) / 5
	if snap.MeanNs != wantMean {
		t.Fatalf("mean = %d, want %d", snap.MeanNs, wantMean)
	}
	// p50 of 5 -> rank 3 -> bucket 0 (1ms); p99 -> rank 5 -> +Inf -> max.
	if snap.P50Ns != int64(time.Millisecond) {
		t.Fatalf("p50 = %d, want %d", snap.P50Ns, int64(time.Millisecond))
	}
	if snap.P99Ns != int64(20*time.Millisecond) {
		t.Fatalf("p99 = %d, want %d", snap.P99Ns, int64(20*time.Millisecond))
	}
}

func TestTimerConcurrent(t *testing.T) {
	tm := NewTimer(nil)
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				tm.Observe(time.Duration(i) * time.Microsecond)
			}
		}()
	}
	wg.Wait()
	snap := tm.Snapshot()
	if snap.Count != 16000 {
		t.Fatalf("count = %d, want 16000", snap.Count)
	}
	var total uint64
	for _, b := range snap.Buckets {
		total = b.CumulativeCount
	}
	if total != 16000 {
		t.Fatalf("bucket total = %d, want 16000", total)
	}
}

func TestWindowBoundaries(t *testing.T) {
	w := NewWindow(4)
	if snap := w.Snapshot(); snap.Count != 0 || snap.MinNs != 0 || snap.MaxNs != 0 {
		t.Fatalf("empty window not zero: %+v", snap)
	}
	for i := 1; i <= 6; i++ {
		w.Observe(time.Duration(i) * time.Millisecond)
	}
	snap := w.Snapshot()
	if snap.Count != 4 || snap.Capacity != 4 {
		t.Fatalf("count = %d capacity = %d, want 4/4", snap.Count, snap.Capacity)
	}
	// Retained: 3,4,5,6 ms (oldest evicted).
	if snap.MinNs != int64(3*time.Millisecond) || snap.MaxNs != int64(6*time.Millisecond) {
		t.Fatalf("min/max = %d/%d, want 3ms/6ms", snap.MinNs, snap.MaxNs)
	}
	if snap.MeanNs != int64(4500*time.Microsecond) {
		t.Fatalf("mean = %d, want 4.5ms", snap.MeanNs)
	}
	if snap.P50Ns != int64(4*time.Millisecond) {
		t.Fatalf("p50 = %d, want 4ms", snap.P50Ns)
	}
	if snap.P99Ns != int64(6*time.Millisecond) {
		t.Fatalf("p99 = %d, want 6ms", snap.P99Ns)
	}
}

func TestWindowConcurrent(t *testing.T) {
	w := NewWindow(128)
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				w.Observe(time.Duration(i) * time.Nanosecond)
			}
		}()
	}
	wg.Wait()
	if snap := w.Snapshot(); snap.Count != 128 {
		t.Fatalf("count = %d, want 128", snap.Count)
	}
}

func TestRegistryGetOrCreateConcurrent(t *testing.T) {
	r := New()
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				r.Counter("c").Inc()
				r.Gauge("g").Add(1)
				r.Timer("t").Observe(time.Microsecond)
				r.Window("w", 8).Observe(time.Microsecond)
				r.IncError("E_TEST")
			}
		}()
	}
	wg.Wait()
	snap := r.Snapshot()
	if snap.Counters["c"] != 3200 {
		t.Fatalf("counter = %d, want 3200", snap.Counters["c"])
	}
	if snap.Gauges["g"] != 3200 {
		t.Fatalf("gauge = %d, want 3200", snap.Gauges["g"])
	}
	if snap.Timers["t"].Count != 3200 {
		t.Fatalf("timer count = %d, want 3200", snap.Timers["t"].Count)
	}
	if snap.Windows["w"].Count != 8 {
		t.Fatalf("window count = %d, want 8", snap.Windows["w"].Count)
	}
	if snap.Errors["E_TEST"] != 3200 {
		t.Fatalf("errors = %d, want 3200", snap.Errors["E_TEST"])
	}
}

func TestHitRate(t *testing.T) {
	r := New()
	if got := r.HitRate("hits", "misses"); got != 0 {
		t.Fatalf("empty hit rate = %v, want 0", got)
	}
	r.Counter("hits").Add(3)
	r.Counter("misses").Add(1)
	if got := r.HitRate("hits", "misses"); got != 0.75 {
		t.Fatalf("hit rate = %v, want 0.75", got)
	}
}

func TestExportJSON(t *testing.T) {
	r := New()
	r.Counter("recovery.pages_redone").Add(7)
	r.Timer("recovery.redo").Observe(2 * time.Millisecond)
	r.IncError("WAL_CORRUPT")
	var buf bytes.Buffer
	if err := r.ExportJSON(&buf); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	counters := decoded["counters"].(map[string]interface{})
	if counters["recovery.pages_redone"].(float64) != 7 {
		t.Fatalf("exported counter = %v, want 7", counters["recovery.pages_redone"])
	}
	errs := decoded["errors"].(map[string]interface{})
	if errs["WAL_CORRUPT"].(float64) != 1 {
		t.Fatalf("exported error = %v, want 1", errs["WAL_CORRUPT"])
	}
	timers := decoded["timers"].(map[string]interface{})
	if timers["recovery.redo"].(map[string]interface{})["count"].(float64) != 1 {
		t.Fatal("exported timer count != 1")
	}
}

func TestExportText(t *testing.T) {
	r := New()
	r.Counter("recovery.pages_redone").Add(3)
	r.Timer("recovery.redo").Observe(time.Millisecond)
	r.IncError("WAL_CORRUPT")
	var buf bytes.Buffer
	if err := r.ExportText(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"aries_recovery_pages_redone 3",
		"aries_recovery_redo_seconds_count 1",
		`aries_errors_total{code="WAL_CORRUPT"} 1`,
		"# TYPE aries_recovery_pages_redone counter",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("text export missing %q:\n%s", want, out)
		}
	}
}

// TestNoGoroutineLeak verifies the package starts no background goroutines.
func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	r := New()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Counter("c").Inc()
			r.Timer("t").Observe(time.Microsecond)
		}()
	}
	wg.Wait()
	var buf bytes.Buffer
	r.ExportJSON(&buf)
	r.ExportText(&buf)
	// Allow the scheduler a moment, then compare.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: before=%d after=%d", before, runtime.NumGoroutine())
}
