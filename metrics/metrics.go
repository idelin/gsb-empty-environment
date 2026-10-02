// Package metrics provides fine-grained, concurrency-safe time-series
// metric collection: counters, gauges, latency-distribution histograms,
// error-code counters, plus windowed aggregation and JSON export.
package metrics

import (
	"encoding/json"
	"io"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// ErrorCode is a stable, low-cardinality classification of failures.
type ErrorCode string

const (
	ErrNotLeader    ErrorCode = "not_leader"
	ErrTimeout      ErrorCode = "timeout"
	ErrRejected     ErrorCode = "ballot_rejected"
	ErrLeaseExpired ErrorCode = "lease_expired"
	ErrUnreachable  ErrorCode = "unreachable"
)

// Counter is a monotonically increasing int64.
type Counter struct {
	v atomic.Int64
}

func (c *Counter) Inc()        { c.v.Add(1) }
func (c *Counter) Add(d int64) { c.v.Add(d) }
func (c *Counter) Load() int64 { return c.v.Load() }

// Gauge is an int64 that can go up and down.
type Gauge struct {
	v atomic.Int64
}

func (g *Gauge) Set(v int64) { g.v.Store(v) }
func (g *Gauge) Inc()        { g.v.Add(1) }
func (g *Gauge) Dec()        { g.v.Add(-1) }
func (g *Gauge) Load() int64 { return g.v.Load() }

// DefaultBuckets returns exponential latency bucket upper-bounds in
// nanoseconds, from 10us to ~10s.
func DefaultBuckets() []float64 {
	var b []float64
	for v := float64(10e3); v <= float64(10e9); v *= 2 {
		b = append(b, v)
	}
	return b
}

// BucketCount is one cumulative histogram bucket ("le" = less or equal).
type BucketCount struct {
	Le    float64 `json:"le_ns"`
	Count int64   `json:"count"`
}

// HistogramSnapshot is an immutable view of a Histogram.
type HistogramSnapshot struct {
	Count   int64         `json:"count"`
	SumNs   int64         `json:"sum_ns"`
	MeanNs  float64       `json:"mean_ns"`
	MinNs   int64         `json:"min_ns"`
	MaxNs   int64         `json:"max_ns"`
	P50Ns   float64       `json:"p50_ns"`
	P90Ns   float64       `json:"p90_ns"`
	P99Ns   float64       `json:"p99_ns"`
	Buckets []BucketCount `json:"buckets"`
}

// Histogram records latency distributions with nanosecond precision.
type Histogram struct {
	mu      sync.Mutex
	buckets []float64 // sorted upper bounds, nanoseconds
	counts  []int64   // per-bucket (non-cumulative)
	sum     int64
	count   int64
	min     int64
	max     int64
}

func NewHistogram(bounds []float64) *Histogram {
	cp := append([]float64(nil), bounds...)
	sort.Float64s(cp)
	return &Histogram{buckets: cp, counts: make([]int64, len(cp))}
}

// Observe records one latency sample.
func (h *Histogram) Observe(d time.Duration) {
	ns := d.Nanoseconds()
	if ns < 0 {
		ns = 0
	}
	h.mu.Lock()
	idx := sort.SearchFloat64s(h.buckets, float64(ns))
	if idx < len(h.counts) {
		h.counts[idx]++
	}
	h.count++
	h.sum += ns
	if h.count == 1 || ns < h.min {
		h.min = ns
	}
	if ns > h.max {
		h.max = ns
	}
	h.mu.Unlock()
}

// Since records the elapsed time since start. Typical use:
//
//	defer h.Since(time.Now())
func (h *Histogram) Since(start time.Time) { h.Observe(time.Since(start)) }

// percentile returns the interpolated percentile value in nanoseconds.
// Must be called with h.mu held.
func (h *Histogram) percentile(p float64) float64 {
	if h.count == 0 {
		return 0
	}
	rank := p * float64(h.count)
	var cum int64
	prevLe := 0.0
	for i, le := range h.buckets {
		c := h.counts[i]
		if float64(cum+c) >= rank {
			if c == 0 {
				return le
			}
			frac := (rank - float64(cum)) / float64(c)
			if frac < 0 {
				frac = 0
			}
			if frac > 1 {
				frac = 1
			}
			return prevLe + frac*(le-prevLe)
		}
		cum += c
		prevLe = le
	}
	return float64(h.max)
}

// Snapshot returns a consistent point-in-time view of the histogram.
func (h *Histogram) Snapshot() HistogramSnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	snap := HistogramSnapshot{
		Count: h.count,
		SumNs: h.sum,
		MinNs: h.min,
		MaxNs: h.max,
	}
	if h.count > 0 {
		snap.MeanNs = float64(h.sum) / float64(h.count)
		snap.P50Ns = h.percentile(0.50)
		snap.P90Ns = h.percentile(0.90)
		snap.P99Ns = h.percentile(0.99)
	}
	var cum int64
	for i, le := range h.buckets {
		cum += h.counts[i]
		snap.Buckets = append(snap.Buckets, BucketCount{Le: le, Count: cum})
	}
	return snap
}

// ErrorCounter counts failures classified by ErrorCode.
type ErrorCounter struct {
	mu sync.Mutex
	m  map[ErrorCode]int64
}

func NewErrorCounter() *ErrorCounter {
	return &ErrorCounter{m: make(map[ErrorCode]int64)}
}

func (e *ErrorCounter) Record(code ErrorCode) {
	e.mu.Lock()
	e.m[code]++
	e.mu.Unlock()
}

func (e *ErrorCounter) Total() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	var t int64
	for _, v := range e.m {
		t += v
	}
	return t
}

// Snapshot returns a copy keyed by string code.
func (e *ErrorCounter) Snapshot() map[string]int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make(map[string]int64, len(e.m))
	for k, v := range e.m {
		out[string(k)] = v
	}
	return out
}

// Snapshot is a full point-in-time view of a Registry.
type Snapshot struct {
	Time       time.Time                    `json:"time"`
	Counters   map[string]int64             `json:"counters"`
	Gauges     map[string]int64             `json:"gauges"`
	Histograms map[string]HistogramSnapshot `json:"histograms"`
	Errors     map[string]map[string]int64  `json:"errors"`
}

// Registry owns a named set of metrics. All lookups are get-or-create.
type Registry struct {
	mu         sync.Mutex
	counters   map[string]*Counter
	gauges     map[string]*Gauge
	histograms map[string]*Histogram
	errors     map[string]*ErrorCounter
}

func NewRegistry() *Registry {
	return &Registry{
		counters:   make(map[string]*Counter),
		gauges:     make(map[string]*Gauge),
		histograms: make(map[string]*Histogram),
		errors:     make(map[string]*ErrorCounter),
	}
}

func (r *Registry) Counter(name string) *Counter {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.counters[name]
	if !ok {
		c = &Counter{}
		r.counters[name] = c
	}
	return c
}

func (r *Registry) Gauge(name string) *Gauge {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.gauges[name]
	if !ok {
		g = &Gauge{}
		r.gauges[name] = g
	}
	return g
}

func (r *Registry) Histogram(name string) *Histogram {
	r.mu.Lock()
	defer r.mu.Unlock()
	h, ok := r.histograms[name]
	if !ok {
		h = NewHistogram(DefaultBuckets())
		r.histograms[name] = h
	}
	return h
}

func (r *Registry) Errors(name string) *ErrorCounter {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.errors[name]
	if !ok {
		e = NewErrorCounter()
		r.errors[name] = e
	}
	return e
}

// Snapshot captures all metrics consistently (registry-level lock).
func (r *Registry) Snapshot() Snapshot {
	r.mu.Lock()
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
	errs := make(map[string]*ErrorCounter, len(r.errors))
	for k, v := range r.errors {
		errs[k] = v
	}
	r.mu.Unlock()

	snap := Snapshot{
		Time:       time.Now(),
		Counters:   make(map[string]int64, len(counters)),
		Gauges:     make(map[string]int64, len(gauges)),
		Histograms: make(map[string]HistogramSnapshot, len(histograms)),
		Errors:     make(map[string]map[string]int64, len(errs)),
	}
	for k, v := range counters {
		snap.Counters[k] = v.Load()
	}
	for k, v := range gauges {
		snap.Gauges[k] = v.Load()
	}
	for k, v := range histograms {
		snap.Histograms[k] = v.Snapshot()
	}
	for k, v := range errs {
		snap.Errors[k] = v.Snapshot()
	}
	return snap
}

// ExportJSON writes the current snapshot as indented JSON.
func (r *Registry) ExportJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r.Snapshot())
}

// HitRate computes hits/(hits+misses); 0 when there were no attempts.
func HitRate(hits, misses int64) float64 {
	total := hits + misses
	if total == 0 {
		return 0
	}
	return float64(hits) / float64(total)
}
