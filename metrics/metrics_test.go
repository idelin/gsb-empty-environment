package metrics

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

func TestCounterGauge(t *testing.T) {
	var c Counter
	c.Inc()
	c.Add(4)
	if c.Value() != 5 {
		t.Fatalf("counter = %d, want 5", c.Value())
	}
	var g Gauge
	g.Set(10)
	g.Add(-3)
	if g.Value() != 7 {
		t.Fatalf("gauge = %d, want 7", g.Value())
	}
}

func TestHistogramEmpty(t *testing.T) {
	h := NewHistogram([]float64{1, 2, 3})
	s := h.Snapshot()
	if s.Count != 0 || s.Sum != 0 || s.Mean != 0 || s.P50 != 0 || s.P99 != 0 {
		t.Fatalf("empty snapshot not zero: %+v", s)
	}
	if len(s.Buckets) != 4 { // 3 bounds + implicit +Inf
		t.Fatalf("buckets = %d, want 4", len(s.Buckets))
	}
	for _, b := range s.Buckets {
		if b.Count != 0 {
			t.Fatalf("empty bucket count = %d", b.Count)
		}
	}
}

func TestHistogramBoundaries(t *testing.T) {
	h := NewHistogram([]float64{1, 2, 3})
	// Observations exactly on bounds fall into that bound's bucket.
	for _, v := range []float64{0.5, 1, 2, 3, 100} {
		h.Observe(v)
	}
	s := h.Snapshot()
	want := []uint64{2, 3, 4, 5} // cumulative: <=1, <=2, <=3, +Inf
	for i, b := range s.Buckets {
		if b.Count != want[i] {
			t.Fatalf("bucket %d = %d, want %d", i, b.Count, want[i])
		}
	}
	if s.Count != 5 || s.Min != 0.5 || s.Max != 100 {
		t.Fatalf("count/min/max = %d/%v/%v", s.Count, s.Min, s.Max)
	}
	if s.Mean != (0.5+1+2+3+100)/5 {
		t.Fatalf("mean = %v", s.Mean)
	}
}

func TestHistogramPercentiles(t *testing.T) {
	h := NewHistogram(nil)
	for i := 1; i <= 100; i++ {
		h.Observe(float64(i))
	}
	s := h.Snapshot()
	if s.P50 != 50 || s.P90 != 90 || s.P99 != 99 {
		t.Fatalf("p50/p90/p99 = %v/%v/%v, want 50/90/99", s.P50, s.P90, s.P99)
	}
}

func TestHistogramSingleSample(t *testing.T) {
	h := NewHistogram([]float64{1})
	h.Observe(42)
	s := h.Snapshot()
	if s.P50 != 42 || s.P90 != 42 || s.P99 != 42 || s.Mean != 42 {
		t.Fatalf("single sample snapshot wrong: %+v", s)
	}
}

func TestRegistryIdempotentAndTimer(t *testing.T) {
	r := NewRegistry()
	if r.Counter("a") != r.Counter("a") {
		t.Fatal("counter not idempotent")
	}
	if r.Histogram("h", []float64{1}) != r.Histogram("h", []float64{9, 9}) {
		t.Fatal("histogram not idempotent")
	}
	end := r.Timer("op.latency")
	end()
	snap := r.Snapshot()
	if snap.Histograms["op.latency"].Count != 1 {
		t.Fatal("timer did not record exactly one observation")
	}
}

func TestRecordError(t *testing.T) {
	r := NewRegistry()
	r.RecordError(ErrWALCorrupt)
	r.RecordError(ErrWALCorrupt)
	r.RecordError(ErrIO)
	snap := r.Snapshot()
	if snap.Errors[ErrWALCorrupt] != 2 || snap.Errors[ErrIO] != 1 {
		t.Fatalf("errors = %v", snap.Errors)
	}
}

func TestExportFormats(t *testing.T) {
	r := NewRegistry()
	r.Counter("requests").Add(7)
	r.Gauge("inflight").Set(2)
	r.Histogram("lat", []float64{0.1}).Observe(0.05)
	r.RecordError(ErrPageMissing)

	var js bytes.Buffer
	if err := r.ExportJSON(&js); err != nil {
		t.Fatal(err)
	}
	var snap Snapshot
	if err := json.Unmarshal(js.Bytes(), &snap); err != nil {
		t.Fatalf("invalid JSON export: %v", err)
	}
	if snap.Counters["requests"] != 7 || snap.Errors[ErrPageMissing] != 1 {
		t.Fatalf("JSON round trip wrong: %+v", snap)
	}

	var txt bytes.Buffer
	if err := r.ExportText(&txt); err != nil {
		t.Fatal(err)
	}
	out := txt.String()
	for _, want := range []string{
		"requests 7", "inflight 2", "lat_count 1",
		`lat{quantile="0.99"}`, `lat_bucket{le="+Inf"} 1`,
		`errors_total{code="PAGE_MISSING"} 1`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("text export missing %q:\n%s", want, out)
		}
	}
}

func TestConcurrentSafety(t *testing.T) {
	r := NewRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				r.Counter("c").Inc()
				r.Gauge("g").Add(1)
				r.Histogram("h", DefaultLatencyBounds).Observe(float64(j))
				r.RecordError(ErrIO)
				end := r.Timer("t")
				end()
				r.Snapshot()
			}
			var buf bytes.Buffer
			r.ExportJSON(&buf)
			r.ExportText(&buf)
		}(i)
	}
	wg.Wait()
	snap := r.Snapshot()
	if snap.Counters["c"] != 16*100 {
		t.Fatalf("counter = %d, want %d", snap.Counters["c"], 16*100)
	}
	if snap.Errors[ErrIO] != 16*100 {
		t.Fatalf("errors = %d, want %d", snap.Errors[ErrIO], 16*100)
	}
	if snap.Histograms["h"].Count != 16*100 {
		t.Fatalf("histogram count = %d", snap.Histograms["h"].Count)
	}
}
