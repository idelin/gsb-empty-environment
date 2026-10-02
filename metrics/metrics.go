// Package metrics provides fine-grained, concurrency-safe time-series
// metric collection: counters, gauges, bucketed latency timers, sliding
// windows, error-code accounting, and aggregated export (JSON / text).
//
// All types are safe for concurrent use. No background goroutines are
// started, so the package is leak-free by construction.
package metrics

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ---------------------------------------------------------------------------
// Counter
// ---------------------------------------------------------------------------

// Counter is a monotonically increasing integer.
type Counter struct {
	value int64
}

// Inc adds 1 to the counter.
func (c *Counter) Inc() { atomic.AddInt64(&c.value, 1) }

// Add adds delta to the counter.
func (c *Counter) Add(delta int64) { atomic.AddInt64(&c.value, delta) }

// Value returns the current value.
func (c *Counter) Value() int64 { return atomic.LoadInt64(&c.value) }

// ---------------------------------------------------------------------------
// Gauge
// ---------------------------------------------------------------------------

// Gauge is an integer that can go up and down.
type Gauge struct {
	value int64
}

// Set replaces the gauge value.
func (g *Gauge) Set(v int64) { atomic.StoreInt64(&g.value, v) }

// Add adds delta to the gauge.
func (g *Gauge) Add(delta int64) { atomic.AddInt64(&g.value, delta) }

// Value returns the current value.
func (g *Gauge) Value() int64 { return atomic.LoadInt64(&g.value) }

// ---------------------------------------------------------------------------
// Timer (bucketed latency distribution)
// ---------------------------------------------------------------------------

// DefaultBuckets are latency bucket upper bounds, in seconds.
var DefaultBuckets = []float64{
	0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5,
}

// Timer records a distribution of durations in fixed buckets, plus exact
// count/sum/min/max so means and bucket-approximated percentiles can be
// computed at snapshot time.
type Timer struct {
	mu      sync.Mutex
	buckets []float64 // sorted upper bounds in seconds
	counts  []uint64  // len(buckets)+1, last is +Inf
	count   uint64
	sumNs   int64
	minNs   int64 // -1 while empty
	maxNs   int64
}

// NewTimer builds a Timer with the given bucket upper bounds (seconds).
// A nil or empty slice selects DefaultBuckets.
func NewTimer(buckets []float64) *Timer {
	if len(buckets) == 0 {
		buckets = DefaultBuckets
	}
	b := append([]float64(nil), buckets...)
	sort.Float64s(b)
	return &Timer{buckets: b, counts: make([]uint64, len(b)+1), minNs: -1}
}

// Observe records one observation. Negative durations are clamped to zero.
func (t *Timer) Observe(d time.Duration) {
	if d < 0 {
		d = 0
	}
	ns := d.Nanoseconds()
	sec := d.Seconds()
	t.mu.Lock()
	idx := sort.SearchFloat64s(t.buckets, sec) // first bucket with bound >= sec
	t.counts[idx]++
	t.count++
	t.sumNs += ns
	if t.minNs < 0 || ns < t.minNs {
		t.minNs = ns
	}
	if ns > t.maxNs {
		t.maxNs = ns
	}
	t.mu.Unlock()
}

// ObserveSince records the elapsed time since start.
func (t *Timer) ObserveSince(start time.Time) { t.Observe(time.Since(start)) }

// Bucket is one histogram bucket in a TimerSnapshot.
type Bucket struct {
	UpperBound      float64 `json:"le"` // seconds; +Inf for the last bucket
	CumulativeCount uint64  `json:"cumulative_count"`
}

// TimerSnapshot is an immutable aggregated view of a Timer.
type TimerSnapshot struct {
	Count   uint64   `json:"count"`
	SumNs   int64    `json:"sum_ns"`
	MinNs   int64    `json:"min_ns"`
	MaxNs   int64    `json:"max_ns"`
	MeanNs  int64    `json:"mean_ns"`
	P50Ns   int64    `json:"p50_ns"`
	P90Ns   int64    `json:"p90_ns"`
	P99Ns   int64    `json:"p99_ns"`
	Buckets []Bucket `json:"buckets"`
}

// Snapshot aggregates the current state without blocking observations for
// longer than a mutex hold.
func (t *Timer) Snapshot() TimerSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	snap := TimerSnapshot{Count: t.count, SumNs: t.sumNs, MinNs: t.minNs, MaxNs: t.maxNs}
	if t.count == 0 {
		snap.MinNs = 0
	} else {
		snap.MeanNs = t.sumNs / int64(t.count)
	}
	var cum uint64
	for i, c := range t.counts {
		cum += c
		upper := math.Inf(1)
		if i < len(t.buckets) {
			upper = t.buckets[i]
		}
		snap.Buckets = append(snap.Buckets, Bucket{UpperBound: upper, CumulativeCount: cum})
	}
	snap.P50Ns = t.percentileLocked(0.50)
	snap.P90Ns = t.percentileLocked(0.90)
	snap.P99Ns = t.percentileLocked(0.99)
	return snap
}

// percentileLocked returns the upper bound (in ns) of the first bucket whose
// cumulative count reaches the p-quantile rank. The +Inf bucket yields maxNs.
func (t *Timer) percentileLocked(p float64) int64 {
	if t.count == 0 {
		return 0
	}
	rank := uint64(math.Ceil(p * float64(t.count)))
	if rank == 0 {
		rank = 1
	}
	var cum uint64
	for i, c := range t.counts {
		cum += c
		if cum >= rank {
			if i < len(t.buckets) {
				return int64(t.buckets[i] * float64(time.Second))
			}
			return t.maxNs
		}
	}
	return t.maxNs
}

// ---------------------------------------------------------------------------
// Window (recent-observation sliding window)
// ---------------------------------------------------------------------------

// Window keeps the last N observed durations in a ring buffer and computes
// exact aggregate statistics over them.
type Window struct {
	mu       sync.Mutex
	vals     []int64 // ring of nanoseconds
	head     int     // next write position
	size     int     // number of valid entries
	capacity int
}

// NewWindow builds a Window holding up to capacity samples.
func NewWindow(capacity int) *Window {
	if capacity <= 0 {
		capacity = 1
	}
	return &Window{vals: make([]int64, capacity), capacity: capacity}
}

// Observe records one observation, evicting the oldest when full.
func (w *Window) Observe(d time.Duration) {
	if d < 0 {
		d = 0
	}
	w.mu.Lock()
	w.vals[w.head] = d.Nanoseconds()
	w.head = (w.head + 1) % w.capacity
	if w.size < w.capacity {
		w.size++
	}
	w.mu.Unlock()
}

// WindowSnapshot aggregates the samples currently in a Window.
type WindowSnapshot struct {
	Capacity int   `json:"capacity"`
	Count    int   `json:"count"`
	MinNs    int64 `json:"min_ns"`
	MaxNs    int64 `json:"max_ns"`
	MeanNs   int64 `json:"mean_ns"`
	P50Ns    int64 `json:"p50_ns"`
	P99Ns    int64 `json:"p99_ns"`
}

// Snapshot returns exact statistics over the retained samples.
func (w *Window) Snapshot() WindowSnapshot {
	w.mu.Lock()
	cp := make([]int64, w.size)
	copy(cp, w.vals[:w.size])
	snap := WindowSnapshot{Capacity: w.capacity, Count: w.size}
	w.mu.Unlock()
	if len(cp) == 0 {
		return snap
	}
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	snap.MinNs = cp[0]
	snap.MaxNs = cp[len(cp)-1]
	var sum int64
	for _, v := range cp {
		sum += v
	}
	snap.MeanNs = sum / int64(len(cp))
	snap.P50Ns = quantile(cp, 0.50)
	snap.P99Ns = quantile(cp, 0.99)
	return snap
}

func quantile(sorted []int64, p float64) int64 {
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// ---------------------------------------------------------------------------
// Registry
// ---------------------------------------------------------------------------

// Registry is a named collection of metrics plus error-code accounting.
type Registry struct {
	mu       sync.RWMutex
	counters map[string]*Counter
	gauges   map[string]*Gauge
	timers   map[string]*Timer
	windows  map[string]*Window
	errors   map[string]*Counter
}

// New returns an empty Registry.
func New() *Registry {
	return &Registry{
		counters: make(map[string]*Counter),
		gauges:   make(map[string]*Gauge),
		timers:   make(map[string]*Timer),
		windows:  make(map[string]*Window),
		errors:   make(map[string]*Counter),
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
	if c, ok = r.counters[name]; ok {
		return c
	}
	c = &Counter{}
	r.counters[name] = c
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
	if g, ok = r.gauges[name]; ok {
		return g
	}
	g = &Gauge{}
	r.gauges[name] = g
	return g
}

// Timer returns the named timer, creating it (with DefaultBuckets) on first use.
func (r *Registry) Timer(name string) *Timer {
	r.mu.RLock()
	t, ok := r.timers[name]
	r.mu.RUnlock()
	if ok {
		return t
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok = r.timers[name]; ok {
		return t
	}
	t = NewTimer(nil)
	r.timers[name] = t
	return t
}

// Window returns the named sliding window, creating it on first use.
func (r *Registry) Window(name string, capacity int) *Window {
	r.mu.RLock()
	w, ok := r.windows[name]
	r.mu.RUnlock()
	if ok {
		return w
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if w, ok = r.windows[name]; ok {
		return w
	}
	w = NewWindow(capacity)
	r.windows[name] = w
	return w
}

// IncError increments the counter for the given error code.
func (r *Registry) IncError(code string) {
	r.mu.RLock()
	c, ok := r.errors[code]
	r.mu.RUnlock()
	if !ok {
		r.mu.Lock()
		if c, ok = r.errors[code]; !ok {
			c = &Counter{}
			r.errors[code] = c
		}
		r.mu.Unlock()
	}
	c.Inc()
}

// HitRate computes hits/(hits+misses) for the named counters; 0 if no events.
func (r *Registry) HitRate(hitsName, missesName string) float64 {
	r.mu.RLock()
	var hits, misses int64
	if c, ok := r.counters[hitsName]; ok {
		hits = c.Value()
	}
	if c, ok := r.counters[missesName]; ok {
		misses = c.Value()
	}
	r.mu.RUnlock()
	total := hits + misses
	if total == 0 {
		return 0
	}
	return float64(hits) / float64(total)
}

// Snapshot is a full aggregated view of a Registry.
type Snapshot struct {
	Counters map[string]int64          `json:"counters"`
	Gauges   map[string]int64          `json:"gauges"`
	Timers   map[string]TimerSnapshot  `json:"timers"`
	Windows  map[string]WindowSnapshot `json:"windows"`
	Errors   map[string]int64          `json:"errors"`
}

// Snapshot aggregates every registered metric.
func (r *Registry) Snapshot() Snapshot {
	snap := Snapshot{
		Counters: make(map[string]int64),
		Gauges:   make(map[string]int64),
		Timers:   make(map[string]TimerSnapshot),
		Windows:  make(map[string]WindowSnapshot),
		Errors:   make(map[string]int64),
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for name, c := range r.counters {
		snap.Counters[name] = c.Value()
	}
	for name, g := range r.gauges {
		snap.Gauges[name] = g.Value()
	}
	for name, t := range r.timers {
		snap.Timers[name] = t.Snapshot()
	}
	for name, w := range r.windows {
		snap.Windows[name] = w.Snapshot()
	}
	for name, c := range r.errors {
		snap.Errors[name] = c.Value()
	}
	return snap
}

// ExportJSON writes the aggregated snapshot as indented JSON.
func (r *Registry) ExportJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r.Snapshot())
}

// ExportText writes the aggregated snapshot in a Prometheus-like text format.
func (r *Registry) ExportText(w io.Writer) error {
	snap := r.Snapshot()
	var b strings.Builder
	for _, name := range sortedKeysInt64(snap.Counters) {
		fmt.Fprintf(&b, "# TYPE aries_%s counter\n", sanitize(name))
		fmt.Fprintf(&b, "aries_%s %d\n", sanitize(name), snap.Counters[name])
	}
	for _, name := range sortedKeysInt64(snap.Gauges) {
		fmt.Fprintf(&b, "# TYPE aries_%s gauge\n", sanitize(name))
		fmt.Fprintf(&b, "aries_%s %d\n", sanitize(name), snap.Gauges[name])
	}
	for _, name := range sortedKeysTimer(snap.Timers) {
		ts := snap.Timers[name]
		n := sanitize(name)
		fmt.Fprintf(&b, "# TYPE aries_%s_seconds summary\n", n)
		fmt.Fprintf(&b, "aries_%s_seconds{quantile=\"0.5\"} %s\n", n, sec(ts.P50Ns))
		fmt.Fprintf(&b, "aries_%s_seconds{quantile=\"0.9\"} %s\n", n, sec(ts.P90Ns))
		fmt.Fprintf(&b, "aries_%s_seconds{quantile=\"0.99\"} %s\n", n, sec(ts.P99Ns))
		fmt.Fprintf(&b, "aries_%s_seconds_count %d\n", n, ts.Count)
		fmt.Fprintf(&b, "aries_%s_seconds_sum %s\n", n, sec(ts.SumNs))
	}
	for _, name := range sortedKeysInt64(snap.Errors) {
		fmt.Fprintf(&b, "aries_errors_total{code=%q} %d\n", name, snap.Errors[name])
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func sec(ns int64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.9f", float64(ns)/1e9), "0"), ".")
}

func sanitize(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			return r
		default:
			return '_'
		}
	}, name)
}

func sortedKeysInt64(m map[string]int64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedKeysTimer(m map[string]TimerSnapshot) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// MarshalJSON renders the +Inf upper bound as the string "+Inf" so the
// export stays valid JSON.
func (b Bucket) MarshalJSON() ([]byte, error) {
	le := interface{}(b.UpperBound)
	if math.IsInf(b.UpperBound, 1) {
		le = "+Inf"
	}
	return json.Marshal(struct {
		LE              interface{} `json:"le"`
		CumulativeCount uint64      `json:"cumulative_count"`
	}{LE: le, CumulativeCount: b.CumulativeCount})
}
