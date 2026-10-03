package paxos

import (
	"sort"
	"sync"
	"time"
)

// DefaultSeriesCap bounds the in-memory time-series ring buffer.
const DefaultSeriesCap = 4096

// Sample is one high-precision point in a metric time series.
type Sample struct {
	T     time.Time `json:"t"`
	Name  string    `json:"name"`
	Value float64   `json:"value"` // seconds for latencies, raw units otherwise
}

// Histogram accumulates latency observations and computes exact percentiles.
// It keeps raw samples (bounded by cap; 0 = unbounded) so p50/p90/p99 are
// exact rather than bucket-approximated. Safe for concurrent use.
type Histogram struct {
	mu    sync.Mutex
	vals  []float64
	count int64 // total observations, including evicted ones
	sum   float64
	min   float64
	max   float64
	cap   int
}

// NewHistogram creates a Histogram. cap <= 0 keeps every sample.
func NewHistogram(cap int) *Histogram {
	return &Histogram{cap: cap}
}

// Observe records one latency (in seconds).
func (h *Histogram) Observe(v float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.count == 0 || v < h.min {
		h.min = v
	}
	if h.count == 0 || v > h.max {
		h.max = v
	}
	h.count++
	h.sum += v
	if h.cap > 0 && len(h.vals) >= h.cap {
		// ring eviction: drop the oldest sample
		copy(h.vals, h.vals[1:])
		h.vals = h.vals[:h.cap-1]
	}
	h.vals = append(h.vals, v)
}

// HistSummary is an immutable statistical view of a Histogram.
type HistSummary struct {
	Count int64   `json:"count"`
	Min   float64 `json:"min"`
	Max   float64 `json:"max"`
	Mean  float64 `json:"mean"`
	P50   float64 `json:"p50"`
	P90   float64 `json:"p90"`
	P99   float64 `json:"p99"`
}

// Summary snapshots the histogram statistics.
func (h *Histogram) Summary() HistSummary {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := HistSummary{Count: h.count, Min: h.min, Max: h.max}
	if h.count > 0 {
		s.Mean = h.sum / float64(h.count)
	}
	if len(h.vals) == 0 {
		return s
	}
	sorted := make([]float64, len(h.vals))
	copy(sorted, h.vals)
	sort.Float64s(sorted)
	s.P50 = percentile(sorted, 0.50)
	s.P90 = percentile(sorted, 0.90)
	s.P99 = percentile(sorted, 0.99)
	return s
}

// percentile uses the nearest-rank method on a sorted slice.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(p*float64(len(sorted)) + 0.5) // ceil for p in (0,1]
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}

// rawValues returns a copy of retained samples (used by aggregation).
func (h *Histogram) rawValues() []float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]float64, len(h.vals))
	copy(out, h.vals)
	return out
}

// Metrics is the per-node fine-grained instrumentation hub. It tracks
// counters, error-code tallies, hit/miss ratios, latency histograms and a
// bounded high-precision time series. All methods are safe for concurrent
// use; no background goroutines are spawned.
type Metrics struct {
	mu        sync.Mutex
	counters  map[string]int64
	errs      map[ErrCode]int64
	hits      map[string]int64
	misses    map[string]int64
	hists     map[string]*Histogram
	series    []Sample
	head      int // ring write position when full
	seriesCap int
	start     time.Time
	now       func() time.Time // injectable clock for tests
}

// NewMetrics creates an empty Metrics with the default series capacity.
func NewMetrics() *Metrics { return NewMetricsWithCap(DefaultSeriesCap) }

// NewMetricsWithCap creates an empty Metrics with a custom series capacity.
func NewMetricsWithCap(seriesCap int) *Metrics {
	if seriesCap <= 0 {
		seriesCap = DefaultSeriesCap
	}
	return &Metrics{
		counters:  make(map[string]int64),
		errs:      make(map[ErrCode]int64),
		hits:      make(map[string]int64),
		misses:    make(map[string]int64),
		hists:     make(map[string]*Histogram),
		seriesCap: seriesCap,
		start:     time.Now(),
		now:       time.Now,
	}
}

// Inc adds delta to a named counter.
func (m *Metrics) Inc(name string, delta int64) {
	m.mu.Lock()
	m.counters[name] += delta
	m.mu.Unlock()
}

// Counter reads a named counter.
func (m *Metrics) Counter(name string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.counters[name]
}

// RecordError tallies one occurrence of an error code.
func (m *Metrics) RecordError(c ErrCode) {
	m.mu.Lock()
	m.errs[c]++
	m.mu.Unlock()
}

// Errors returns the error-code tally.
func (m *Metrics) Errors() map[ErrCode]int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[ErrCode]int64, len(m.errs))
	for k, v := range m.errs {
		out[k] = v
	}
	return out
}

// Hit records a hit for a named ratio (e.g. "lease" fast-path).
func (m *Metrics) Hit(name string) {
	m.mu.Lock()
	m.hits[name]++
	m.mu.Unlock()
}

// Miss records a miss for a named ratio.
func (m *Metrics) Miss(name string) {
	m.mu.Lock()
	m.misses[name]++
	m.mu.Unlock()
}

// HitRate returns hits/(hits+misses) for a name; 0 when no samples exist.
func (m *Metrics) HitRate(name string) float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, mi := m.hits[name], m.misses[name]
	if h+mi == 0 {
		return 0
	}
	return float64(h) / float64(h+mi)
}

// Observe records a latency observation into a histogram and appends a
// high-precision point to the time series.
func (m *Metrics) Observe(name string, d time.Duration) {
	v := d.Seconds()
	m.mu.Lock()
	h, ok := m.hists[name]
	if !ok {
		h = NewHistogram(0)
		m.hists[name] = h
	}
	m.appendSeriesLocked(Sample{T: m.now(), Name: name, Value: v})
	m.mu.Unlock()
	h.Observe(v)
}

// ObserveValue records a non-latency scalar into the time series only.
func (m *Metrics) ObserveValue(name string, v float64) {
	m.mu.Lock()
	m.appendSeriesLocked(Sample{T: m.now(), Name: name, Value: v})
	m.mu.Unlock()
}

func (m *Metrics) appendSeriesLocked(s Sample) {
	if len(m.series) < m.seriesCap {
		m.series = append(m.series, s)
		return
	}
	m.series[m.head] = s
	m.head = (m.head + 1) % m.seriesCap
}

// Series returns the retained time-series points for a name, oldest first.
func (m *Metrics) Series(name string) []Sample {
	m.mu.Lock()
	defer m.mu.Unlock()
	ordered := m.orderedSeriesLocked()
	out := make([]Sample, 0, len(ordered))
	for _, s := range ordered {
		if s.Name == name {
			out = append(out, s)
		}
	}
	return out
}

// SeriesAll returns every retained point, oldest first.
func (m *Metrics) SeriesAll() []Sample {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.orderedSeriesLocked()
}

func (m *Metrics) orderedSeriesLocked() []Sample {
	if len(m.series) < m.seriesCap || m.head == 0 {
		out := make([]Sample, len(m.series))
		copy(out, m.series)
		return out
	}
	out := make([]Sample, 0, len(m.series))
	out = append(out, m.series[m.head:]...)
	out = append(out, m.series[:m.head]...)
	return out
}

// Histogram returns the summary of one latency histogram.
func (m *Metrics) Histogram(name string) (HistSummary, bool) {
	m.mu.Lock()
	h, ok := m.hists[name]
	m.mu.Unlock()
	if !ok {
		return HistSummary{}, false
	}
	return h.Summary(), true
}

// Uptime is the wall-clock age of this Metrics instance.
func (m *Metrics) Uptime() time.Duration { return time.Since(m.start) }
