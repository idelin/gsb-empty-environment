package cbitmap

import (
	"bytes"
	"encoding/json"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestMetricsCountersAndHitRate(t *testing.T) {
	m := NewMetrics()
	b := New(WithMetrics(m))
	b.Add(1)
	b.Add(2)
	b.Add(2) // duplicate: not counted
	b.Contains(1)
	b.Contains(2)
	b.Contains(999) // miss
	b.Remove(1)

	o := New() // no metrics: only b's activity is measured
	o.Add(1)
	o.Add(3)
	b.And(o)
	b.Or(o)
	b.Xor(o)

	snap := m.Snapshot()
	if snap.Inserts != 2 {
		t.Fatalf("inserts = %d, want 2", snap.Inserts)
	}
	if snap.Removes != 1 {
		t.Fatalf("removes = %d, want 1", snap.Removes)
	}
	if snap.Lookups != 3 || snap.Hits != 2 {
		t.Fatalf("lookups/hits = %d/%d, want 3/2", snap.Lookups, snap.Hits)
	}
	if snap.HitRate < 0.66 || snap.HitRate > 0.67 {
		t.Fatalf("hit rate = %v, want ~2/3", snap.HitRate)
	}
	if snap.AndOps != 1 || snap.OrOps != 1 || snap.XorOps != 1 {
		t.Fatalf("op counters = %d/%d/%d, want 1/1/1", snap.AndOps, snap.OrOps, snap.XorOps)
	}
	if snap.Latency.Count != 3 {
		t.Fatalf("latency samples = %d, want 3", snap.Latency.Count)
	}
	// Percentiles are bucket lower bounds, so only their mutual ordering and
	// the min/max envelope are guaranteed.
	if snap.Latency.MinNs > snap.Latency.MaxNs ||
		snap.Latency.P50Ns > snap.Latency.P90Ns ||
		snap.Latency.P90Ns > snap.Latency.P99Ns || snap.Latency.P99Ns > snap.Latency.MaxNs {
		t.Fatalf("latency percentiles not ordered: %+v", snap.Latency)
	}
	if len(snap.Series) == 0 {
		t.Fatal("expected at least one time-series bucket")
	}
	var total int64
	for _, bkt := range snap.Series {
		total += bkt.Count
	}
	if total != 3 {
		t.Fatalf("time-series op count = %d, want 3", total)
	}
}

func TestMetricsConversionCounters(t *testing.T) {
	m := NewMetrics()
	b := New(WithMetrics(m))
	for i := 0; i < arrayMaxSize+1; i++ {
		b.Add(uint32(i))
	}
	if err := b.AddRange(1<<20, 1<<20+100000); err != nil {
		t.Fatal(err)
	}
	snap := m.Snapshot()
	if snap.Conversions["to_bitmap"] < 1 {
		t.Fatalf("expected array->bitmap conversions, got %v", snap.Conversions)
	}
	if snap.Conversions["to_run"] < 1 {
		t.Fatalf("expected run conversions for dense range, got %v", snap.Conversions)
	}
}

func TestMetricsTimeSeriesRotation(t *testing.T) {
	m := NewMetricsWithSeries(10*time.Millisecond, 8)
	b := New(WithMetrics(m))
	b.Add(1)
	for i := 0; i < 4; i++ {
		b.And(New(WithMetrics(m)))
		time.Sleep(15 * time.Millisecond)
	}
	snap := m.Snapshot()
	if len(snap.Series) < 2 {
		t.Fatalf("expected multiple time-series buckets, got %d", len(snap.Series))
	}
	for i := 1; i < len(snap.Series); i++ {
		if !snap.Series[i].Start.After(snap.Series[i-1].Start) {
			t.Fatal("time-series buckets must be chronological")
		}
	}
	// capacity is enforced
	m2 := NewMetricsWithSeries(time.Nanosecond, 3)
	b2 := New(WithMetrics(m2))
	for i := 0; i < 10; i++ {
		b2.And(New())
		time.Sleep(time.Microsecond)
	}
	if n := len(m2.Snapshot().Series); n > 3 {
		t.Fatalf("series capacity exceeded: %d buckets", n)
	}
}

func TestMetricsExportJSON(t *testing.T) {
	m := NewMetrics()
	b := New(WithMetrics(m))
	b.Add(1)
	b.Contains(1)
	b.Contains(2)
	b.AddRange(9, 3) // invalid -> error code recorded
	b.And(New(WithMetrics(m)))

	var buf bytes.Buffer
	if err := m.ExportJSON(&buf); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("export is not valid JSON: %v", err)
	}
	for _, key := range []string{"inserts", "removes", "lookups", "hits", "hit_rate",
		"and_ops", "or_ops", "xor_ops", "conversions", "errors", "latency", "series", "timestamp"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("exported JSON missing key %q", key)
		}
	}
	lat := decoded["latency"].(map[string]interface{})
	for _, key := range []string{"count", "mean_ns", "p50_ns", "p90_ns", "p99_ns"} {
		if _, ok := lat[key]; !ok {
			t.Fatalf("exported latency missing key %q", key)
		}
	}
	errs := decoded["errors"].(map[string]interface{})
	if errs[ErrInvalidRange.String()].(float64) != 1 {
		t.Fatalf("expected 1 invalid_range error in export, got %v", errs)
	}
}

func TestMetricsConcurrentSafety(t *testing.T) {
	m := NewMetrics()
	b := New(WithMetrics(m))
	o := New() // unmeasured operand
	if err := o.AddRange(0, 5000); err != nil {
		t.Fatal(err)
	}

	const workers = 8
	const perWorker = 10000
	var wg sync.WaitGroup
	var readers sync.WaitGroup
	stop := make(chan struct{})
	// concurrent readers running set operations while writers add
	for r := 0; r < 2; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = b.And(o)
					_ = b.Or(o)
					_ = b.Xor(o)
				}
			}
		}()
	}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			base := uint32(w) * perWorker
			for i := 0; i < perWorker; i++ {
				b.Add(base + uint32(i))
			}
			for i := 0; i < 100; i++ {
				b.Contains(base + uint32(i))
			}
		}(w)
	}
	wg.Wait()
	close(stop)
	readers.Wait()

	if n := b.Cardinality(); n != workers*perWorker {
		t.Fatalf("cardinality = %d, want %d", n, workers*perWorker)
	}
	snap := m.Snapshot()
	if snap.Inserts != workers*perWorker {
		t.Fatalf("inserts = %d, want %d", snap.Inserts, workers*perWorker)
	}
	if snap.Lookups != workers*100 {
		t.Fatalf("lookups = %d, want %d", snap.Lookups, workers*100)
	}
}

func TestFlusherNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	m := NewMetrics()
	var mu sync.Mutex
	flushed := 0
	f := m.StartFlusher(5*time.Millisecond, func(s Snapshot) {
		mu.Lock()
		flushed++
		mu.Unlock()
	})
	time.Sleep(40 * time.Millisecond)
	f.Stop()

	mu.Lock()
	n := flushed
	mu.Unlock()
	if n == 0 {
		t.Fatal("flusher never fired")
	}
	// the flusher goroutine must be gone
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, got)
	}
}

func TestHistogramPercentiles(t *testing.T) {
	h := newHistogram()
	for i := 1; i <= 100; i++ {
		h.record(int64(i * 100)) // 100ns .. 10000ns
	}
	s := h.stats()
	if s.Count != 100 {
		t.Fatalf("count = %d, want 100", s.Count)
	}
	if s.MeanNs != 5050 {
		t.Fatalf("mean = %v, want 5050", s.MeanNs)
	}
	if s.MinNs != 100 || s.MaxNs != 10000 {
		t.Fatalf("min/max = %d/%d, want 100/10000", s.MinNs, s.MaxNs)
	}
	if s.P50Ns > 5000 || s.P90Ns > 9000 || s.P99Ns > 9900 {
		t.Fatalf("percentile upper bounds exceeded: %+v", s)
	}
	if !(s.P50Ns <= s.P90Ns && s.P90Ns <= s.P99Ns) {
		t.Fatalf("percentiles not ordered: %+v", s)
	}
}
