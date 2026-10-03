// Package metrics provides fine-grained, concurrency-safe telemetry:
// counters, gauges, latency histograms with percentile aggregation,
// error-code accounting, and JSON / text export of snapshots.
package metrics

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Counter is a monotonically increasing integer.
type Counter struct{ v atomic.Int64 }

// Inc adds 1 to the counter.
func (c *Counter) Inc() { c.v.Add(1) }

// Add adds n to the counter.
func (c *Counter) Add(n int64) { c.v.Add(n) }

// Value returns the current value.
func (c *Counter) Value() int64 { return c.v.Load() }

// Gauge is an integer that can go up and down.
type Gauge struct{ v atomic.Int64 }

// Set replaces the gauge value.
func (g *Gauge) Set(v int64) { g.v.Store(v) }

// Add adds delta to the gauge.
func (g *Gauge) Add(delta int64) { g.v.Add(delta) }

// Value returns the current value.
func (g *Gauge) Value() int64 { return g.v.Load() }

// Bucket is one histogram bucket: cumulative count of observations
// less than or equal to Le.
type Bucket struct {
	Le    float64 `json:"le"`
	Count uint64  `json:"count"`
}

// HistogramSnapshot is an immutable view of a Histogram.
type HistogramSnapshot struct {
	Count   uint64   `json:"count"`
	Sum     float64  `json:"sum"`
	Min     float64  `json:"min"`
	Max     float64  `json:"max"`
	Mean    float64  `json:"mean"`
	P50     float64  `json:"p50"`
	P90     float64  `json:"p90"`
	P99     float64  `json:"p99"`
	Buckets []Bucket `json:"buckets"`
}

// Histogram records a distribution of float64 observations with
// fixed upper-bound buckets plus exact percentile aggregation.
type Histogram struct {
	mu      sync.Mutex
	bounds  []float64
	counts  []uint64
	sum     float64
	min     float64
	max     float64
	samples []float64
}

// NewHistogram creates a Histogram with the given bucket upper bounds
// (sorted ascending). An implicit +Inf bucket is always present.
func NewHistogram(bounds []float64) *Histogram {
	b := make([]float64, len(bounds))
	copy(b, bounds)
	sort.Float64s(b)
	return &Histogram{bounds: b, counts: make([]uint64, len(b)+1)}
}

// Observe records one observation.
func (h *Histogram) Observe(v float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	i := sort.SearchFloat64s(h.bounds, v)
	h.counts[i]++
	h.sum += v
	if len(h.samples) == 0 || v < h.min {
		h.min = v
	}
	if len(h.samples) == 0 || v > h.max {
		h.max = v
	}
	h.samples = append(h.samples, v)
}

// Snapshot returns an immutable, consistently aggregated view.
func (h *Histogram) Snapshot() HistogramSnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := uint64(len(h.samples))
	snap := HistogramSnapshot{Count: n, Sum: h.sum, Buckets: make([]Bucket, 0, len(h.bounds)+1)}
	var cumulative uint64
	for i, b := range h.bounds {
		cumulative += h.counts[i]
		snap.Buckets = append(snap.Buckets, Bucket{Le: b, Count: cumulative})
	}
	cumulative += h.counts[len(h.bounds)]
	snap.Buckets = append(snap.Buckets, Bucket{Le: 1e308, Count: cumulative})
	if n == 0 {
		return snap
	}
	snap.Min = h.min
	snap.Max = h.max
	snap.Mean = h.sum / float64(n)
	sorted := make([]float64, n)
	copy(sorted, h.samples)
	sort.Float64s(sorted)
	snap.P50 = percentile(sorted, 0.50)
	snap.P90 = percentile(sorted, 0.90)
	snap.P99 = percentile(sorted, 0.99)
	return snap
}

// percentile returns the q-quantile of sorted samples using the
// nearest-rank method. Callers must pass a non-empty sorted slice.
func percentile(sorted []float64, q float64) float64 {
	rank := int(q*float64(len(sorted)) + 0.5)
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}

// ErrCode identifies a classified failure for error-rate metrics.
type ErrCode string

// Classified error codes tracked by the recovery engine.
const (
	ErrWALCorrupt  ErrCode = "WAL_CORRUPT"
	ErrPageMissing ErrCode = "PAGE_MISSING"
	ErrTxnUnknown  ErrCode = "TXN_UNKNOWN"
	ErrIO          ErrCode = "IO_ERROR"
)

// DefaultLatencyBounds are histogram bounds (seconds) suited for
// recovery-phase and operation latencies.
var DefaultLatencyBounds = []float64{
	1e-6, 5e-6, 1e-5, 5e-5, 1e-4, 5e-4, 1e-3, 5e-3, 1e-2, 5e-2, 1e-1, 5e-1, 1,
}

// Registry is a concurrency-safe collection of named metrics.
type Registry struct {
	mu         sync.RWMutex
	counters   map[string]*Counter
	gauges     map[string]*Gauge
	histograms map[string]*Histogram
	errors     map[ErrCode]*Counter
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		counters:   make(map[string]*Counter),
		gauges:     make(map[string]*Gauge),
		histograms: make(map[string]*Histogram),
		errors:     make(map[ErrCode]*Counter),
	}
}

// Counter returns the named counter, creating it on first use.
func (r *Registry) Counter(name string) *Counter {
	r.mu.RLock()
	c, ok := r.counters[name]
	r.mu.RUnlock()
	if ok {
		return c
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok = r.counters[name]; !ok {
		c = &Counter{}
		r.counters[name] = c
	}
	return c
}

// Gauge returns the named gauge, creating it on first use.
func (r *Registry) Gauge(name string) *Gauge {
	r.mu.RLock()
	g, ok := r.gauges[name]
	r.mu.RUnlock()
	if ok {
		return g
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if g, ok = r.gauges[name]; !ok {
		g = &Gauge{}
		r.gauges[name] = g
	}
	return g
}

// Histogram returns the named histogram, creating it with bounds on
// first use. Bounds are ignored if the histogram already exists.
func (r *Registry) Histogram(name string, bounds []float64) *Histogram {
	r.mu.RLock()
	h, ok := r.histograms[name]
	r.mu.RUnlock()
	if ok {
		return h
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if h, ok = r.histograms[name]; !ok {
		h = NewHistogram(bounds)
		r.histograms[name] = h
	}
	return h
}

// RecordError increments the counter for a classified error code.
func (r *Registry) RecordError(code ErrCode) {
	r.mu.RLock()
	c, ok := r.errors[code]
	r.mu.RUnlock()
	if ok {
		c.Inc()
		return
	}
	r.mu.Lock()
	if c, ok = r.errors[code]; !ok {
		c = &Counter{}
		r.errors[code] = c
	}
	r.mu.Unlock()
	c.Inc()
}

// Timer starts a latency measurement for the named histogram
// (created with DefaultLatencyBounds if absent). The returned
// function stops the timer and records the elapsed seconds.
func (r *Registry) Timer(name string) (end func()) {
	h := r.Histogram(name, DefaultLatencyBounds)
	start := time.Now()
	return func() { h.Observe(time.Since(start).Seconds()) }
}

// Snapshot is an immutable, exportable view of the whole registry.
type Snapshot struct {
	Counters   map[string]int64             `json:"counters"`
	Gauges     map[string]int64             `json:"gauges"`
	Histograms map[string]HistogramSnapshot `json:"histograms"`
	Errors     map[ErrCode]int64            `json:"errors"`
}

// Snapshot captures a consistent view of all registered metrics.
func (r *Registry) Snapshot() Snapshot {
	r.mu.RLock()
	counters := make(map[string]*Counter, len(r.counters))
	for k, v := range r.counters {
		counters[k] = v
	}
	gauges := make(map[string]*Gauge, len(r.gauges))
	for k, v := range r.gauges {
		gauges[k] = v
	}
	histograms := make(map[string]*Histogram, len(r.histograms))
	for k, v := range r.histograms {
		histograms[k] = v
	}
	errs := make(map[ErrCode]*Counter, len(r.errors))
	for k, v := range r.errors {
		errs[k] = v
	}
	r.mu.RUnlock()

	snap := Snapshot{
		Counters:   make(map[string]int64, len(counters)),
		Gauges:     make(map[string]int64, len(gauges)),
		Histograms: make(map[string]HistogramSnapshot, len(histograms)),
		Errors:     make(map[ErrCode]int64, len(errs)),
	}
	for k, v := range counters {
		snap.Counters[k] = v.Value()
	}
	for k, v := range gauges {
		snap.Gauges[k] = v.Value()
	}
	for k, v := range histograms {
		snap.Histograms[k] = v.Snapshot()
	}
	for k, v := range errs {
		snap.Errors[k] = v.Value()
	}
	return snap
}
