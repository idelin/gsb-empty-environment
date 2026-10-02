package metrics

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// Registry is a named collection of metrics. The zero value is ready
// to use and safe for concurrent access.
type Registry struct {
	mu         sync.Mutex
	counters   map[string]*Counter
	gauges     map[string]*Gauge
	histograms map[string]*Histogram
	windows    map[string]*Window
	hitRates   map[string]*HitRate
	errors     *ErrorCodes
}

func (r *Registry) init() {
	if r.counters == nil {
		r.counters = make(map[string]*Counter)
		r.gauges = make(map[string]*Gauge)
		r.histograms = make(map[string]*Histogram)
		r.windows = make(map[string]*Window)
		r.hitRates = make(map[string]*HitRate)
		r.errors = NewErrorCodes()
	}
}

// Counter returns (creating on first use) the named counter.
func (r *Registry) Counter(name string) *Counter {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	c, ok := r.counters[name]
	if !ok {
		c = &Counter{}
		r.counters[name] = c
	}
	return c
}

// Gauge returns (creating on first use) the named gauge.
func (r *Registry) Gauge(name string) *Gauge {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	g, ok := r.gauges[name]
	if !ok {
		g = &Gauge{}
		r.gauges[name] = g
	}
	return g
}

// Histogram returns the named histogram, creating it with the given
// bounds on first use. Later calls with different bounds keep the
// original bucket layout.
func (r *Registry) Histogram(name string, bounds ...float64) *Histogram {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	h, ok := r.histograms[name]
	if !ok {
		h = NewHistogram(bounds...)
		r.histograms[name] = h
	}
	return h
}

// Window returns the named sliding window, creating it with the given
// duration on first use. Later calls keep the original duration.
func (r *Registry) Window(name string, dur time.Duration) *Window {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	w, ok := r.windows[name]
	if !ok {
		w = NewWindow(dur)
		r.windows[name] = w
	}
	return w
}

// HitRate returns (creating on first use) the named hit-rate tracker.
func (r *Registry) HitRate(name string) *HitRate {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	h, ok := r.hitRates[name]
	if !ok {
		h = &HitRate{}
		r.hitRates[name] = h
	}
	return h
}

// Errors returns the registry-wide error-code tally.
func (r *Registry) Errors() *ErrorCodes {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	return r.errors
}

// Snapshot is a full, immutable export of a Registry.
type Snapshot struct {
	Counters   map[string]int64
	Gauges     map[string]int64
	Histograms map[string]HistogramSnapshot
	HitRates   map[string]float64
	Errors     map[string]int64
}

// Snapshot copies all metric values.
func (r *Registry) Snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	s := Snapshot{
		Counters:   make(map[string]int64, len(r.counters)),
		Gauges:     make(map[string]int64, len(r.gauges)),
		Histograms: make(map[string]HistogramSnapshot, len(r.histograms)),
		HitRates:   make(map[string]float64, len(r.hitRates)),
		Errors:     r.errors.Snapshot(),
	}
	for n, c := range r.counters {
		s.Counters[n] = c.Value()
	}
	for n, g := range r.gauges {
		s.Gauges[n] = g.Value()
	}
	for n, h := range r.histograms {
		s.Histograms[n] = h.Snapshot()
	}
	for n, hr := range r.hitRates {
		s.HitRates[n] = hr.Ratio()
	}
	return s
}

// Export renders the registry in a Prometheus-inspired text format.
func (r *Registry) Export() string {
	s := r.Snapshot()
	var b strings.Builder

	for _, n := range sortedKeysI(s.Counters) {
		fmt.Fprintf(&b, "# TYPE %s counter\n%s %d\n", n, n, s.Counters[n])
	}
	for _, n := range sortedKeysI(s.Gauges) {
		fmt.Fprintf(&b, "# TYPE %s gauge\n%s %d\n", n, n, s.Gauges[n])
	}
	for _, n := range sortedKeysH(s.Histograms) {
		h := s.Histograms[n]
		fmt.Fprintf(&b, "# TYPE %s histogram\n", n)
		for _, bk := range h.Buckets {
			le := "+Inf"
			if !math.IsInf(bk.LE, 1) {
				le = fmt.Sprintf("%g", bk.LE)
			}
			fmt.Fprintf(&b, "%s_bucket{le=\"%s\"} %d\n", n, le, bk.Count)
		}
		fmt.Fprintf(&b, "%s_sum %g\n%s_count %d\n", n, h.Sum, n, h.Count)
	}
	for _, n := range sortedKeysF(s.HitRates) {
		fmt.Fprintf(&b, "# TYPE %s gauge\n%s %g\n", n, n, s.HitRates[n])
	}
	for _, code := range sortedKeysI(s.Errors) {
		fmt.Fprintf(&b, "decode_errors_total{code=\"%s\"} %d\n", code, s.Errors[code])
	}
	return b.String()
}

func sortedKeysI(m map[string]int64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedKeysF(m map[string]float64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedKeysH(m map[string]HistogramSnapshot) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
