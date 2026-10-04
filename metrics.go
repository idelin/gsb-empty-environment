package cbitmap

import (
	"encoding/json"
	"io"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// ErrorCode classifies errors recorded by the metrics collector.
type ErrorCode int

const (
	ErrInvalidRange ErrorCode = iota
	ErrNilBitmap
	ErrEmptyBatch
	numErrorCodes
)

func (c ErrorCode) String() string {
	switch c {
	case ErrInvalidRange:
		return "invalid_range"
	case ErrNilBitmap:
		return "nil_bitmap"
	case ErrEmptyBatch:
		return "empty_batch"
	}
	return "unknown"
}

// Error is the error type returned by bitmap operations.
type Error struct {
	Code ErrorCode
	Msg  string
}

func (e *Error) Error() string { return e.Code.String() + ": " + e.Msg }

// conversion targets for container conversion counters.
type convTarget int

const (
	convArray convTarget = iota
	convBitmap
	convRun
	numConvTargets
)

// Metrics collects fine-grained, high-resolution runtime measurements for
// bitmap operations. All methods are safe for concurrent use.
type Metrics struct {
	inserts int64
	removes int64
	lookups int64
	hits    int64
	andOps  int64
	orOps   int64
	xorOps  int64
	convs   [numConvTargets]int64
	errs    [numErrorCodes]int64
	latency *histogram
	series  *timeSeries
}

// NewMetrics creates a Metrics collector with a 1s-resolution, 1h-capacity
// time series.
func NewMetrics() *Metrics {
	return NewMetricsWithSeries(time.Second, 3600)
}

// NewMetricsWithSeries creates a Metrics collector with a custom time-series
// bucket interval and capacity.
func NewMetricsWithSeries(interval time.Duration, capacity int) *Metrics {
	if capacity < 1 {
		capacity = 1
	}
	return &Metrics{
		latency: newHistogram(),
		series:  newTimeSeries(interval, capacity),
	}
}

func (m *Metrics) recordInserts(n int64) { atomic.AddInt64(&m.inserts, n) }
func (m *Metrics) recordRemoves(n int64) { atomic.AddInt64(&m.removes, n) }
func (m *Metrics) recordLookup(hit bool) {
	atomic.AddInt64(&m.lookups, 1)
	if hit {
		atomic.AddInt64(&m.hits, 1)
	}
}

func (m *Metrics) recordOp(kind opKind, d time.Duration) {
	switch kind {
	case opAnd:
		atomic.AddInt64(&m.andOps, 1)
	case opOr:
		atomic.AddInt64(&m.orOps, 1)
	case opXor:
		atomic.AddInt64(&m.xorOps, 1)
	}
	m.latency.record(int64(d))
	m.series.record(d, false, time.Now())
}

func (m *Metrics) recordError(code ErrorCode) {
	if code < 0 || int(code) >= len(m.errs) {
		return
	}
	atomic.AddInt64(&m.errs[code], 1)
	m.series.record(0, true, time.Now())
}

func (m *Metrics) recordConversion(t convTarget) {
	atomic.AddInt64(&m.convs[t], 1)
}

func (m *Metrics) recordConversionIfChanged(old, new container) {
	if containerKind(old) == containerKind(new) {
		return
	}
	switch new.(type) {
	case *arrayContainer:
		m.recordConversion(convArray)
	case *bitmapContainer:
		m.recordConversion(convBitmap)
	case *runContainer:
		m.recordConversion(convRun)
	}
}

// LatencyStats is an aggregated view of operation latencies (nanoseconds).
type LatencyStats struct {
	Count  int64   `json:"count"`
	MeanNs float64 `json:"mean_ns"`
	MinNs  int64   `json:"min_ns"`
	MaxNs  int64   `json:"max_ns"`
	P50Ns  int64   `json:"p50_ns"`
	P90Ns  int64   `json:"p90_ns"`
	P99Ns  int64   `json:"p99_ns"`
}

// BucketStats is one aggregated time-series bucket.
type BucketStats struct {
	Start  time.Time `json:"start"`
	Count  int64     `json:"count"`
	Errors int64     `json:"errors"`
	MeanNs float64   `json:"mean_ns"`
	MinNs  int64     `json:"min_ns"`
	MaxNs  int64     `json:"max_ns"`
}

// Snapshot is a point-in-time, exportable view of all collected metrics.
type Snapshot struct {
	Timestamp   time.Time        `json:"timestamp"`
	Inserts     int64            `json:"inserts"`
	Removes     int64            `json:"removes"`
	Lookups     int64            `json:"lookups"`
	Hits        int64            `json:"hits"`
	HitRate     float64          `json:"hit_rate"`
	AndOps      int64            `json:"and_ops"`
	OrOps       int64            `json:"or_ops"`
	XorOps      int64            `json:"xor_ops"`
	Conversions map[string]int64 `json:"conversions"`
	Errors      map[string]int64 `json:"errors"`
	Latency     LatencyStats     `json:"latency"`
	Series      []BucketStats    `json:"series"`
}

// Snapshot aggregates the current state of all metrics.
func (m *Metrics) Snapshot() Snapshot {
	lookups := atomic.LoadInt64(&m.lookups)
	hits := atomic.LoadInt64(&m.hits)
	hitRate := 0.0
	if lookups > 0 {
		hitRate = float64(hits) / float64(lookups)
	}
	convs := map[string]int64{
		"to_array":  atomic.LoadInt64(&m.convs[convArray]),
		"to_bitmap": atomic.LoadInt64(&m.convs[convBitmap]),
		"to_run":    atomic.LoadInt64(&m.convs[convRun]),
	}
	errs := map[string]int64{}
	for c := ErrorCode(0); c < numErrorCodes; c++ {
		if n := atomic.LoadInt64(&m.errs[c]); n > 0 {
			errs[c.String()] = n
		}
	}
	return Snapshot{
		Timestamp:   time.Now(),
		Inserts:     atomic.LoadInt64(&m.inserts),
		Removes:     atomic.LoadInt64(&m.removes),
		Lookups:     lookups,
		Hits:        hits,
		HitRate:     hitRate,
		AndOps:      atomic.LoadInt64(&m.andOps),
		OrOps:       atomic.LoadInt64(&m.orOps),
		XorOps:      atomic.LoadInt64(&m.xorOps),
		Conversions: convs,
		Errors:      errs,
		Latency:     m.latency.stats(),
		Series:      m.series.snapshot(),
	}
}

// ExportJSON writes an indented JSON snapshot of all aggregated metrics.
func (m *Metrics) ExportJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(m.Snapshot())
}

// ---------------------------------------------------------------------------
// latency histogram (exponential buckets, nanoseconds)
// ---------------------------------------------------------------------------

var latencyBounds = func() []int64 {
	bounds := []int64{100} // 100ns, 200ns, 400ns, ... up to >= 10s
	for bounds[len(bounds)-1] < int64(10*time.Second) {
		bounds = append(bounds, bounds[len(bounds)-1]*2)
	}
	return bounds
}()

type histogram struct {
	mu      sync.Mutex
	count   int64
	sum     int64
	min     int64
	max     int64
	buckets []int64 // len = len(latencyBounds)+1
}

func newHistogram() *histogram {
	return &histogram{buckets: make([]int64, len(latencyBounds)+1)}
}

func (h *histogram) record(v int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.count++
	h.sum += v
	if h.count == 1 || v < h.min {
		h.min = v
	}
	if v > h.max {
		h.max = v
	}
	i := sort.Search(len(latencyBounds), func(i int) bool { return v < latencyBounds[i] })
	h.buckets[i]++
}

// percentile returns the lower bound of the bucket containing the p-th
// percentile (p in [0,1]). Lock-free callers must hold nothing; safe.
func (h *histogram) percentile(p float64) int64 {
	if h.count == 0 {
		return 0
	}
	target := int64(math.Ceil(p * float64(h.count)))
	if target < 1 {
		target = 1
	}
	var cum int64
	for i, c := range h.buckets {
		cum += c
		if cum >= target {
			if i == 0 {
				return 0
			}
			return latencyBounds[i-1]
		}
	}
	return latencyBounds[len(latencyBounds)-1]
}

func (h *histogram) stats() LatencyStats {
	h.mu.Lock()
	count, sum, mn, mx := h.count, h.sum, h.min, h.max
	h.mu.Unlock()
	// percentiles computed under a consistent-enough view; re-check count
	s := LatencyStats{Count: count, MinNs: mn, MaxNs: mx}
	if count > 0 {
		s.MeanNs = float64(sum) / float64(count)
		s.P50Ns = h.percentile(0.50)
		s.P90Ns = h.percentile(0.90)
		s.P99Ns = h.percentile(0.99)
	}
	return s
}

// ---------------------------------------------------------------------------
// time series (fixed-interval aggregated buckets, lazy rotation, no goroutine)
// ---------------------------------------------------------------------------

type seriesBucket struct {
	start time.Time
	count int64
	sum   int64
	errs  int64
	min   int64
	max   int64
}

type timeSeries struct {
	mu       sync.Mutex
	interval time.Duration
	capacity int
	buckets  []seriesBucket
}

func newTimeSeries(interval time.Duration, capacity int) *timeSeries {
	if interval <= 0 {
		interval = time.Second
	}
	return &timeSeries{interval: interval, capacity: capacity}
}

func (ts *timeSeries) record(d time.Duration, isErr bool, now time.Time) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	n := len(ts.buckets)
	if n == 0 || !now.Before(ts.buckets[n-1].start.Add(ts.interval)) {
		start := now.Truncate(ts.interval)
		if n > 0 && !start.After(ts.buckets[n-1].start) {
			start = ts.buckets[n-1].start.Add(ts.interval)
		}
		ts.buckets = append(ts.buckets, seriesBucket{start: start, min: -1})
		if len(ts.buckets) > ts.capacity {
			ts.buckets = append([]seriesBucket(nil), ts.buckets[len(ts.buckets)-ts.capacity:]...)
		}
		n = len(ts.buckets)
	}
	b := &ts.buckets[n-1]
	if isErr {
		b.errs++
		return
	}
	b.count++
	b.sum += int64(d)
	if b.min < 0 || int64(d) < b.min {
		b.min = int64(d)
	}
	if int64(d) > b.max {
		b.max = int64(d)
	}
}

func (ts *timeSeries) snapshot() []BucketStats {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	out := make([]BucketStats, len(ts.buckets))
	for i, b := range ts.buckets {
		bs := BucketStats{
			Start:  b.start,
			Count:  b.count,
			Errors: b.errs,
			MaxNs:  b.max,
		}
		if b.count > 0 {
			bs.MeanNs = float64(b.sum) / float64(b.count)
			bs.MinNs = b.min
		}
		out[i] = bs
	}
	return out
}

// ---------------------------------------------------------------------------
// background flusher
// ---------------------------------------------------------------------------

// Flusher periodically pushes metrics snapshots to a sink. Stop must be
// called to release its goroutine.
type Flusher struct {
	done chan struct{}
	wg   sync.WaitGroup
}

// StartFlusher launches a goroutine that calls sink with a fresh Snapshot
// every interval until Stop is called.
func (m *Metrics) StartFlusher(interval time.Duration, sink func(Snapshot)) *Flusher {
	f := &Flusher{done: make(chan struct{})}
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-f.done:
				return
			case <-t.C:
				sink(m.Snapshot())
			}
		}
	}()
	return f
}

// Stop terminates the flusher and waits for its goroutine to exit.
func (f *Flusher) Stop() {
	close(f.done)
	f.wg.Wait()
}
