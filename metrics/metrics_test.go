package metrics

import (
	"bytes"
	"encoding/json"
	"math"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCounterConcurrency(t *testing.T) {
	var c Counter
	const workers = 64
	const perWorker = 1000
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				c.Inc()
			}
		}()
	}
	wg.Wait()
	if got := c.Value(); got != workers*perWorker {
		t.Fatalf("counter = %d, want %d", got, workers*perWorker)
	}
}

func TestGaugeSetAdd(t *testing.T) {
	var g Gauge
	g.Set(1.5)
	g.Add(0.25)
	g.Add(-0.75)
	if got := g.Value(); got != 1.0 {
		t.Fatalf("gauge = %v, want 1.0", got)
	}
}

func TestGaugeConcurrentAdd(t *testing.T) {
	var g Gauge
	var wg sync.WaitGroup
	for w := 0; w < 32; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				g.Add(1)
				g.Add(-0.5)
			}
		}()
	}
	wg.Wait()
	if got := g.Value(); got != 32*500*0.5 {
		t.Fatalf("gauge = %v, want %v", got, 32*500*0.5)
	}
}

func TestHistogramStats(t *testing.T) {
	h := NewHistogram([]float64{0.1, 0.5, 1.0})
	vals := []float64{0.05, 0.2, 0.7, 2.0, 0.3}
	for _, v := range vals {
		h.Observe(v)
	}
	if h.Count() != 5 {
		t.Fatalf("count = %d", h.Count())
	}
	var sum float64
	for _, v := range vals {
		sum += v
	}
	if h.Sum() != sum {
		t.Fatalf("sum = %v, want %v", h.Sum(), sum)
	}
	if h.Mean() != sum/5 {
		t.Fatalf("mean = %v", h.Mean())
	}
	mn, mx, ok := h.MinMax()
	if !ok || mn != 0.05 || mx != 2.0 {
		t.Fatalf("minmax = %v,%v,%v", mn, mx, ok)
	}
	buckets := h.Buckets()
	if len(buckets) != 4 {
		t.Fatalf("buckets = %d, want 4", len(buckets))
	}
	// cumulative: <=0.1:1, <=0.5:3, <=1.0:4, +Inf:5
	want := []uint64{1, 3, 4, 5}
	for i, b := range buckets {
		if b.CumulativeCount != want[i] {
			t.Fatalf("bucket %d = %d, want %d", i, b.CumulativeCount, want[i])
		}
	}
	if !math.IsInf(buckets[3].UpperBound, 1) {
		t.Fatal("last bucket must be +Inf")
	}
}

func TestHistogramQuantiles(t *testing.T) {
	h := NewHistogram(nil)
	for i := 1; i <= 100; i++ {
		h.Observe(float64(i))
	}
	if v, ok := h.Quantile(0.5); !ok || v < 50 || v > 51 {
		t.Fatalf("p50 = %v", v)
	}
	if v, ok := h.Quantile(0.99); !ok || v < 99 || v > 100 {
		t.Fatalf("p99 = %v", v)
	}
	if v, ok := h.Quantile(0); !ok || v != 1 {
		t.Fatalf("q0 = %v", v)
	}
	if v, ok := h.Quantile(1); !ok || v != 100 {
		t.Fatalf("q1 = %v", v)
	}
}

func TestHistogramEmpty(t *testing.T) {
	h := NewHistogram(nil)
	if h.Count() != 0 || h.Sum() != 0 || h.Mean() != 0 {
		t.Fatal("empty histogram must report zeros")
	}
	if _, _, ok := h.MinMax(); ok {
		t.Fatal("empty histogram MinMax must be !ok")
	}
	if _, ok := h.Quantile(0.5); ok {
		t.Fatal("empty histogram Quantile must be !ok")
	}
	if got := len(h.WindowSamples(0)); got != 0 {
		t.Fatalf("empty window = %d samples", got)
	}
}

func TestHistogramWindowSlides(t *testing.T) {
	h := NewHistogram(nil)
	total := DefaultWindowSize + 500
	for i := 0; i < total; i++ {
		h.Observe(float64(i))
	}
	samples := h.WindowSamples(0)
	if len(samples) != DefaultWindowSize {
		t.Fatalf("window = %d, want %d", len(samples), DefaultWindowSize)
	}
	// Oldest retained sample must be total-DefaultWindowSize.
	if samples[0].Value != float64(total-DefaultWindowSize) {
		t.Fatalf("oldest = %v, want %d", samples[0].Value, total-DefaultWindowSize)
	}
	if samples[len(samples)-1].Value != float64(total-1) {
		t.Fatalf("newest = %v", samples[len(samples)-1].Value)
	}
	// Timestamps must be non-decreasing, oldest first.
	for i := 1; i < len(samples); i++ {
		if samples[i].Time.Before(samples[i-1].Time) {
			t.Fatal("window samples not time-ordered")
		}
	}
	// maxN caps the result to the most recent N.
	last10 := h.WindowSamples(10)
	if len(last10) != 10 || last10[9].Value != float64(total-1) {
		t.Fatalf("last10 = %v", last10)
	}
}

func TestHistogramTimer(t *testing.T) {
	h := NewHistogram(DefaultLatencyBuckets)
	tm := h.NewTimer()
	time.Sleep(2 * time.Millisecond)
	d := tm.Observe()
	if d < 2*time.Millisecond {
		t.Fatalf("timer = %v", d)
	}
	if h.Count() != 1 {
		t.Fatalf("count = %d", h.Count())
	}
	v, ok := h.Quantile(0.5)
	if !ok || v < 0.002 {
		t.Fatalf("recorded = %v", v)
	}
}

func TestHitRate(t *testing.T) {
	var hr HitRate
	if hr.Ratio() != 0 {
		t.Fatal("empty hitrate ratio must be 0")
	}
	for i := 0; i < 3; i++ {
		hr.Hit()
	}
	hr.Miss()
	if hr.Hits() != 3 || hr.Misses() != 1 || hr.Total() != 4 {
		t.Fatalf("hits=%d misses=%d", hr.Hits(), hr.Misses())
	}
	if hr.Ratio() != 0.75 {
		t.Fatalf("ratio = %v", hr.Ratio())
	}
}

func TestHitRateConcurrency(t *testing.T) {
	var hr HitRate
	var wg sync.WaitGroup
	for w := 0; w < 50; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if (i+w)%2 == 0 {
					hr.Hit()
				} else {
					hr.Miss()
				}
			}
		}(w)
	}
	wg.Wait()
	if hr.Total() != 50*200 {
		t.Fatalf("total = %d", hr.Total())
	}
	if hr.Hits() != 5000 || hr.Misses() != 5000 {
		t.Fatalf("hits=%d misses=%d", hr.Hits(), hr.Misses())
	}
	if hr.Ratio() != 0.5 {
		t.Fatalf("ratio = %v", hr.Ratio())
	}
}

func TestErrorCodes(t *testing.T) {
	ec := NewErrorCodes()
	var wg sync.WaitGroup
	codes := []string{"TRUNCATED", "OVERFLOW", "INVALID_WIRE_TYPE"}
	for w := 0; w < 30; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				ec.Record(codes[(w+i)%len(codes)])
			}
		}(w)
	}
	wg.Wait()
	if ec.Total() != 3000 {
		t.Fatalf("total = %d", ec.Total())
	}
	snap := ec.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("codes = %v", snap)
	}
	for _, c := range codes {
		if snap[c] != 1000 {
			t.Fatalf("code %s = %d, want 1000", c, snap[c])
		}
	}
	// Snapshot must be a copy: mutating it must not affect the original.
	snap["TRUNCATED"] = -1
	if ec.Count("TRUNCATED") != 1000 {
		t.Fatal("snapshot aliases internal state")
	}
}

func TestRegistryGetOrCreate(t *testing.T) {
	r := NewRegistry()
	c1 := r.Counter("requests", map[string]string{"op": "decode"})
	c2 := r.Counter("requests", map[string]string{"op": "decode"})
	if c1 != c2 {
		t.Fatal("same name+labels must return the same counter")
	}
	c3 := r.Counter("requests", map[string]string{"op": "encode"})
	if c3 == c1 {
		t.Fatal("different labels must return different counters")
	}
	// Label order must not matter.
	c4 := r.Counter("multi", map[string]string{"a": "1", "b": "2"})
	c5 := r.Counter("multi", map[string]string{"b": "2", "a": "1"})
	if c4 != c5 {
		t.Fatal("label ordering must be canonicalized")
	}
}

func TestRegistryConcurrentAccess(t *testing.T) {
	r := NewRegistry()
	var wg sync.WaitGroup
	for w := 0; w < 32; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			name := []string{"a", "b", "c"}[w%3]
			for i := 0; i < 200; i++ {
				r.Counter("c_"+name, nil).Inc()
				r.Gauge("g_"+name, nil).Set(float64(i))
				r.Histogram("h_"+name, nil, DefaultLatencyBuckets).Observe(float64(i) * 0.001)
				r.HitRate("hr_"+name, nil).Hit()
				r.ErrorCodes("ec_"+name, nil).Record("E1")
			}
		}(w)
	}
	wg.Wait()
	snap := r.Snapshot()
	var total int64
	for _, v := range snap.Counters {
		total += v
	}
	if total != 32*200 {
		t.Fatalf("counter total = %d", total)
	}
	if len(snap.Histograms) != 3 || len(snap.HitRates) != 3 || len(snap.ErrorCodes) != 3 {
		t.Fatalf("snapshot sizes: %d %d %d", len(snap.Histograms), len(snap.HitRates), len(snap.ErrorCodes))
	}
	for _, ec := range snap.ErrorCodes {
		if ec["E1"] == 0 {
			t.Fatal("error code not aggregated")
		}
	}
}

func TestExportText(t *testing.T) {
	r := NewRegistry()
	r.Counter("requests_total", map[string]string{"op": "decode"}).Add(7)
	r.Gauge("inflight", nil).Set(3)
	h := r.Histogram("latency_seconds", nil, []float64{0.1, 1})
	h.Observe(0.05)
	h.Observe(0.5)
	r.HitRate("cache", nil).Hit()
	r.HitRate("cache", nil).Miss()
	r.ErrorCodes("decode_errors", nil).Record("TRUNCATED")

	var buf bytes.Buffer
	if err := r.Snapshot().ExportText(&buf); err != nil {
		t.Fatalf("ExportText: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`counter requests_total{op=decode} 7`,
		"gauge inflight 3",
		"histogram_bucket latency_seconds le=0.1 1",
		"histogram_bucket latency_seconds le=1 2",
		"histogram_bucket latency_seconds le=+Inf 2",
		"histogram_summary latency_seconds count=2",
		"hitrate cache hits=1 misses=1 ratio=0.5",
		"error_code decode_errors code=TRUNCATED 1",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("export missing %q\n---\n%s", want, out)
		}
	}
}

func TestExportJSON(t *testing.T) {
	r := NewRegistry()
	r.Counter("requests", nil).Add(42)
	h := r.Histogram("latency", nil, DefaultLatencyBuckets)
	h.Observe(0.001)
	h.Observe(0.002)

	var buf bytes.Buffer
	if err := r.Snapshot().ExportJSON(&buf); err != nil {
		t.Fatalf("ExportJSON: %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("exported JSON does not parse: %v", err)
	}
	counters, ok := decoded["counters"].(map[string]interface{})
	if !ok || counters["requests"] != float64(42) {
		t.Fatalf("counters = %v", decoded["counters"])
	}
	hists, ok := decoded["histograms"].(map[string]interface{})
	if !ok {
		t.Fatal("missing histograms")
	}
	lat, ok := hists["latency"].(map[string]interface{})
	if !ok || lat["count"] != float64(2) {
		t.Fatalf("latency = %v", hists["latency"])
	}
}

// TestNoGoroutineLeak proves the package starts no background goroutines and
// leaves none behind after heavy concurrent use.
func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()

	r := NewRegistry()
	var wg sync.WaitGroup
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				r.Counter("c", nil).Inc()
				r.Histogram("h", nil, DefaultLatencyBuckets).Observe(0.001)
				r.ErrorCodes("e", nil).Record("X")
			}
		}()
	}
	wg.Wait()
	_ = r.Snapshot()

	// Allow any transient runtime goroutines to settle.
	deadline := time.Now().Add(2 * time.Second)
	for {
		after := runtime.NumGoroutine()
		if after <= before {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutine leak: before=%d after=%d", before, after)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
