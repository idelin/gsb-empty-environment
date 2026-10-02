// Package metrics provides fine-grained, concurrency-safe runtime
// instrumentation: counters, gauges, latency histograms with exact
// sliding-window percentiles, hit-rate trackers, error-code tallies and
// a text exporter. It uses only the standard library and starts no
// background goroutines, so it cannot leak.
package metrics

import (
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Counter is a monotonically increasing integer.
type Counter struct{ v int64 }

// Add increments the counter by delta (must be >= 0 for monotonicity).
func (c *Counter) Add(delta int64) { atomic.AddInt64(&c.v, delta) }

// Inc increments the counter by one.
func (c *Counter) Inc() { c.Add(1) }

// Value returns the current value.
func (c *Counter) Value() int64 { return atomic.LoadInt64(&c.v) }

// Gauge is an integer that can go up and down.
type Gauge struct{ v int64 }

// Set replaces the gauge value.
func (g *Gauge) Set(v int64) { atomic.StoreInt64(&g.v, v) }

// Add adjusts the gauge by delta.
func (g *Gauge) Add(delta int64) { atomic.AddInt64(&g.v, delta) }

// Value returns the current value.
func (g *Gauge) Value() int64 { return atomic.LoadInt64(&g.v) }

// BucketCount is one histogram bucket in a snapshot.
type BucketCount struct {
	LE    float64 // upper bound, inclusive
	Count uint64  // cumulative count of observations <= LE
}

// HistogramSnapshot is an immutable copy of a Histogram.
type HistogramSnapshot struct {
	Count   uint64
	Sum     float64
	Min     float64
	Max     float64
	Buckets []BucketCount
}

// Mean returns the average observation, or 0 when empty.
func (s HistogramSnapshot) Mean() float64 {
	if s.Count == 0 {
		return 0
	}
	return s.Sum / float64(s.Count)
}

// Histogram aggregates observations into fixed buckets. It is safe for
// concurrent use.
type Histogram struct {
	mu      sync.Mutex
	bounds  []float64 // sorted upper bounds
	counts  []uint64  // len(bounds)+1; last is +Inf
	count   uint64
	sum     float64
	min     float64
	max     float64
	started bool
}

// NewHistogram builds a Histogram with the given upper bounds. Bounds
// are sorted and deduplicated; +Inf is implied.
func NewHistogram(bounds ...float64) *Histogram {
	cp := append([]float64(nil), bounds...)
	sort.Float64s(cp)
	out := cp[:0]
	for _, b := range cp {
		if len(out) == 0 || b != out[len(out)-1] {
			out = append(out, b)
		}
	}
	return &Histogram{bounds: out, counts: make([]uint64, len(out)+1)}
}

// Observe records one observation.
func (h *Histogram) Observe(v float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	idx := sort.SearchFloat64s(h.bounds, v)
	h.counts[idx]++
	h.count++
	h.sum += v
	if !h.started || v < h.min {
		h.min = v
	}
	if !h.started || v > h.max {
		h.max = v
	}
	h.started = true
}

// Snapshot returns a consistent copy of the histogram state.
func (h *Histogram) Snapshot() HistogramSnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := HistogramSnapshot{
		Count: h.count, Sum: h.sum, Min: h.min, Max: h.max,
		Buckets: make([]BucketCount, len(h.bounds)+1),
	}
	var cum uint64
	for i, c := range h.counts {
		cum += c
		le := 0.0
		if i < len(h.bounds) {
			le = h.bounds[i]
		} else {
			le = math.Inf(1)
		}
		s.Buckets[i] = BucketCount{LE: le, Count: cum}
	}
	return s
}

// sample is one timed observation in a Window.
type sample struct {
	at time.Time
	v  float64
}

// Window keeps the most recent observations inside a sliding time
// window and computes exact percentiles over them. It is safe for
// concurrent use.
type Window struct {
	mu     sync.Mutex
	dur    time.Duration
	s      []sample
	now    func() time.Time // injectable for tests
	totalN uint64           // lifetime observation count
}

// NewWindow creates a Window covering the last dur of observations.
func NewWindow(dur time.Duration) *Window {
	return &Window{dur: dur, now: time.Now}
}

// Observe records v at the current time and evicts expired samples.
func (w *Window) Observe(v float64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.s = append(w.s, sample{at: w.now(), v: v})
	w.totalN++
	w.evictLocked()
}

func (w *Window) evictLocked() {
	cutoff := w.now().Add(-w.dur)
	i := 0
	for i < len(w.s) && w.s[i].at.Before(cutoff) {
		i++
	}
	if i > 0 {
		// Move survivors down so the backing array cannot grow without
		// bound (no memory leak under sustained load).
		copy(w.s, w.s[i:])
		w.s = w.s[:len(w.s)-i]
	}
}

// WindowSnapshot holds exact statistics over live samples.
type WindowSnapshot struct {
	Count    int
	Min, Max float64
	Mean     float64
	P50, P90 float64
	P99      float64
	Total    uint64 // lifetime observations, including evicted
}

// Percentile returns the exact p-th percentile (0..100) of live samples
// using nearest-rank, or 0 when empty.
func (w *Window) Percentile(p float64) float64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.s) == 0 {
		return 0
	}
	vals := make([]float64, len(w.s))
	for i, sm := range w.s {
		vals[i] = sm.v
	}
	sort.Float64s(vals)
	rank := int(p/100*float64(len(vals)) + 0.5)
	if rank < 1 {
		rank = 1
	}
	if rank > len(vals) {
		rank = len(vals)
	}
	return vals[rank-1]
}

// Snapshot returns exact statistics over the live window.
func (w *Window) Snapshot() WindowSnapshot {
	w.mu.Lock()
	defer w.mu.Unlock()
	var snap WindowSnapshot
	snap.Count = len(w.s)
	snap.Total = w.totalN
	if len(w.s) == 0 {
		return snap
	}
	vals := make([]float64, len(w.s))
	for i, sm := range w.s {
		vals[i] = sm.v
	}
	sort.Float64s(vals)
	snap.Min, snap.Max = vals[0], vals[len(vals)-1]
	var sum float64
	for _, v := range vals {
		sum += v
	}
	snap.Mean = sum / float64(len(vals))
	pick := func(p float64) float64 {
		rank := int(p/100*float64(len(vals)) + 0.5)
		if rank < 1 {
			rank = 1
		}
		if rank > len(vals) {
			rank = len(vals)
		}
		return vals[rank-1]
	}
	snap.P50, snap.P90, snap.P99 = pick(50), pick(90), pick(99)
	return snap
}

// HitRate tracks hit/miss outcomes.
type HitRate struct {
	hits   int64
	misses int64
}

// Hit records a hit.
func (h *HitRate) Hit() { atomic.AddInt64(&h.hits, 1) }

// Miss records a miss.
func (h *HitRate) Miss() { atomic.AddInt64(&h.misses, 1) }

// Counts returns (hits, misses).
func (h *HitRate) Counts() (int64, int64) {
	return atomic.LoadInt64(&h.hits), atomic.LoadInt64(&h.misses)
}

// Ratio returns hits/(hits+misses), or 0 when no events were recorded.
func (h *HitRate) Ratio() float64 {
	hits, misses := h.Counts()
	total := hits + misses
	if total == 0 {
		return 0
	}
	return float64(hits) / float64(total)
}

// ErrorCodes tallies occurrences per error code string.
type ErrorCodes struct {
	mu sync.Mutex
	m  map[string]int64
}

// NewErrorCodes creates an empty tally.
func NewErrorCodes() *ErrorCodes { return &ErrorCodes{m: make(map[string]int64)} }

// Inc records one occurrence of code.
func (e *ErrorCodes) Inc(code string) {
	e.mu.Lock()
	e.m[code]++
	e.mu.Unlock()
}

// Count returns the tally for one code.
func (e *ErrorCodes) Count(code string) int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.m[code]
}

// Snapshot returns a copy of all tallies.
func (e *ErrorCodes) Snapshot() map[string]int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make(map[string]int64, len(e.m))
	for k, v := range e.m {
		out[k] = v
	}
	return out
}
