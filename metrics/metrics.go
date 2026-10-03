// Package metrics provides fine-grained, concurrency-safe instrumentation:
// counters, gauges, latency histograms with exact sliding-window quantiles,
// hit-rate trackers and per-error-code tallies. All state is kept in memory
// with no background goroutines, so nothing can leak; snapshots can be
// exported as text or JSON.
package metrics

import (
	"encoding/json"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Counter is a monotonically increasing int64, safe for concurrent use.
type Counter struct {
	v int64
}

// Inc adds 1.
func (c *Counter) Inc() { atomic.AddInt64(&c.v, 1) }

// Add adds n (must be >= 0 for counter semantics; not enforced).
func (c *Counter) Add(n int64) { atomic.AddInt64(&c.v, n) }

// Value returns the current value.
func (c *Counter) Value() int64 { return atomic.LoadInt64(&c.v) }

// Gauge is a settable float64, safe for concurrent use.
type Gauge struct {
	bits uint64
}

// Set stores v.
func (g *Gauge) Set(v float64) { atomic.StoreUint64(&g.bits, math.Float64bits(v)) }

// Add atomically adds delta via CAS.
func (g *Gauge) Add(delta float64) {
	for {
		old := atomic.LoadUint64(&g.bits)
		nv := math.Float64frombits(old) + delta
		if atomic.CompareAndSwapUint64(&g.bits, old, math.Float64bits(nv)) {
			return
		}
	}
}

// Value returns the current value.
func (g *Gauge) Value() float64 { return math.Float64frombits(atomic.LoadUint64(&g.bits)) }

// sample is one timestamped observation in the sliding window.
type sample struct {
	t time.Time
	v float64
}

// DefaultWindowSize bounds the per-histogram time-series ring buffer.
const DefaultWindowSize = 2048

// Histogram tracks a latency/size distribution: bucket counts, sum, min,
// max, plus a sliding window of timestamped samples for exact quantiles.
type Histogram struct {
	mu     sync.Mutex
	bounds []float64
	counts []uint64
	sum    float64
	count  uint64
	min    float64
	max    float64
	win    []sample
	head   int
	filled int
}

// NewHistogram returns a Histogram with the given upper bounds (sorted,
// exclusive-ish: a value v lands in the first bucket with v <= bound; values
// above the last bound land in an implicit +Inf bucket).
func NewHistogram(bounds []float64) *Histogram {
	b := make([]float64, len(bounds))
	copy(b, bounds)
	sort.Float64s(b)
	return &Histogram{
		bounds: b,
		counts: make([]uint64, len(b)+1),
		win:    make([]sample, DefaultWindowSize),
	}
}

// DefaultLatencyBuckets are exponential buckets in seconds from 10us to ~10s.
var DefaultLatencyBuckets = []float64{
	0.00001, 0.00005, 0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5, 10,
}

// Observe records one observation.
func (h *Histogram) Observe(v float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.observeLocked(v, time.Now())
}

func (h *Histogram) observeLocked(v float64, now time.Time) {
	idx := sort.SearchFloat64s(h.bounds, v)
	h.counts[idx]++
	h.sum += v
	h.count++
	if h.count == 1 || v < h.min {
		h.min = v
	}
	if h.count == 1 || v > h.max {
		h.max = v
	}
	h.win[h.head] = sample{t: now, v: v}
	h.head = (h.head + 1) % len(h.win)
	if h.filled < len(h.win) {
		h.filled++
	}
}

// ObserveDuration records the elapsed time since start, in seconds.
func (h *Histogram) ObserveDuration(start time.Time) {
	h.Observe(time.Since(start).Seconds())
}

// Timer measures one operation's latency with nanosecond precision.
type Timer struct {
	h     *Histogram
	start time.Time
}

// NewTimer starts a timer that will record into h.
func (h *Histogram) NewTimer() *Timer {
	return &Timer{h: h, start: time.Now()}
}

// Observe stops the timer and records the elapsed seconds. It returns the
// recorded duration so callers can branch on it.
func (t *Timer) Observe() time.Duration {
	d := time.Since(t.start)
	t.h.Observe(d.Seconds())
	return d
}

// Count returns the total number of observations.
func (h *Histogram) Count() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.count
}

// Sum returns the sum of all observations.
func (h *Histogram) Sum() float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sum
}

// Mean returns the arithmetic mean, or 0 when empty.
func (h *Histogram) Mean() float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.count == 0 {
		return 0
	}
	return h.sum / float64(h.count)
}

// MinMax returns the smallest and largest observations; ok is false when empty.
func (h *Histogram) MinMax() (min, max float64, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.count == 0 {
		return 0, 0, false
	}
	return h.min, h.max, true
}

// Quantile returns the exact q-quantile (0<=q<=1) over the sliding window of
// recent samples. ok is false when no samples have been recorded.
func (h *Histogram) Quantile(q float64) (v float64, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.filled == 0 {
		return 0, false
	}
	vals := make([]float64, h.filled)
	for i := 0; i < h.filled; i++ {
		vals[i] = h.win[i].v
	}
	sort.Float64s(vals)
	if q <= 0 {
		return vals[0], true
	}
	if q >= 1 {
		return vals[len(vals)-1], true
	}
	pos := q * float64(len(vals)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	frac := pos - float64(lo)
	return vals[lo] + (vals[hi]-vals[lo])*frac, true
}

// Buckets returns a copy of (upperBound, cumulativeCount) pairs plus a final
// +Inf bucket with the total count.
func (h *Histogram) Buckets() []Bucket {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Bucket, len(h.bounds)+1)
	var cum uint64
	for i, b := range h.bounds {
		cum += h.counts[i]
		out[i] = Bucket{UpperBound: b, CumulativeCount: cum}
	}
	cum += h.counts[len(h.bounds)]
	out[len(h.bounds)] = Bucket{UpperBound: math.Inf(1), CumulativeCount: cum}
	return out
}

// Bucket is one histogram bucket row.
type Bucket struct {
	UpperBound      float64 `json:"upper_bound"`
	CumulativeCount uint64  `json:"cumulative_count"`
}

// MarshalJSON renders a +Inf upper bound as the string "+Inf" so snapshots
// remain valid JSON.
func (b Bucket) MarshalJSON() ([]byte, error) {
	type alias struct {
		UpperBound      interface{} `json:"upper_bound"`
		CumulativeCount uint64      `json:"cumulative_count"`
	}
	var ub interface{} = b.UpperBound
	if math.IsInf(b.UpperBound, 1) {
		ub = "+Inf"
	}
	return json.Marshal(alias{UpperBound: ub, CumulativeCount: b.CumulativeCount})
}

// WindowSamples returns up to maxN most recent timestamped samples, oldest
// first. maxN <= 0 returns everything in the window.
func (h *Histogram) WindowSamples(maxN int) []Sample {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := h.filled
	if maxN > 0 && maxN < n {
		n = maxN
	}
	out := make([]Sample, 0, n)
	// oldest sample index within the ring
	start := (h.head - h.filled + len(h.win)) % len(h.win)
	for i := h.filled - n; i < h.filled; i++ {
		s := h.win[(start+i)%len(h.win)]
		out = append(out, Sample{Time: s.t, Value: s.v})
	}
	return out
}

// Sample is an exported timestamped observation.
type Sample struct {
	Time  time.Time
	Value float64
}

// HitRate tracks hits and misses (e.g. cache lookups).
type HitRate struct {
	hits   int64
	misses int64
}

// Hit records one hit.
func (hr *HitRate) Hit() { atomic.AddInt64(&hr.hits, 1) }

// Miss records one miss.
func (hr *HitRate) Miss() { atomic.AddInt64(&hr.misses, 1) }

// Hits returns the hit count.
func (hr *HitRate) Hits() int64 { return atomic.LoadInt64(&hr.hits) }

// Misses returns the miss count.
func (hr *HitRate) Misses() int64 { return atomic.LoadInt64(&hr.misses) }

// Total returns hits+misses.
func (hr *HitRate) Total() int64 { return hr.Hits() + hr.Misses() }

// Ratio returns hits/(hits+misses), or 0 when there were no lookups.
func (hr *HitRate) Ratio() float64 {
	h := hr.Hits()
	m := hr.Misses()
	if h+m == 0 {
		return 0
	}
	return float64(h) / float64(h+m)
}

// ErrorCodes tallies occurrences per error code string.
type ErrorCodes struct {
	mu sync.Mutex
	m  map[string]int64
}

// NewErrorCodes returns an empty ErrorCodes.
func NewErrorCodes() *ErrorCodes {
	return &ErrorCodes{m: make(map[string]int64)}
}

// Record increments the tally for code.
func (ec *ErrorCodes) Record(code string) {
	ec.mu.Lock()
	ec.m[code]++
	ec.mu.Unlock()
}

// Count returns the tally for one code.
func (ec *ErrorCodes) Count(code string) int64 {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	return ec.m[code]
}

// Total returns the sum of all tallies.
func (ec *ErrorCodes) Total() int64 {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	var t int64
	for _, v := range ec.m {
		t += v
	}
	return t
}

// Snapshot returns a copy of the code->count map.
func (ec *ErrorCodes) Snapshot() map[string]int64 {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	out := make(map[string]int64, len(ec.m))
	for k, v := range ec.m {
		out[k] = v
	}
	return out
}
