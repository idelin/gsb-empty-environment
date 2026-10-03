package metrics

import (
	"sort"
	"strings"
	"sync"
)

// Registry is a concurrency-safe collection of named, labeled metrics.
// It starts no background goroutines: all work happens on caller threads.
type Registry struct {
	mu         sync.RWMutex
	counters   map[string]*Counter
	gauges     map[string]*Gauge
	histograms map[string]*Histogram
	hitrates   map[string]*HitRate
	errcodes   map[string]*ErrorCodes
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		counters:   make(map[string]*Counter),
		gauges:     make(map[string]*Gauge),
		histograms: make(map[string]*Histogram),
		hitrates:   make(map[string]*HitRate),
		errcodes:   make(map[string]*ErrorCodes),
	}
}

// key joins a metric name with sorted labels: name{k="v",k2="v2"}.
func key(name string, labels map[string]string) string {
	if len(labels) == 0 {
		return name
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteString(name)
	sb.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(labels[k])
	}
	sb.WriteByte('}')
	return sb.String()
}

// Counter returns the counter for name+labels, creating it on first use.
func (r *Registry) Counter(name string, labels map[string]string) *Counter {
	k := key(name, labels)
	r.mu.RLock()
	c, ok := r.counters[k]
	r.mu.RUnlock()
	if ok {
		return c
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.counters[k]; ok {
		return c
	}
	c = &Counter{}
	r.counters[k] = c
	return c
}

// Gauge returns the gauge for name+labels, creating it on first use.
func (r *Registry) Gauge(name string, labels map[string]string) *Gauge {
	k := key(name, labels)
	r.mu.RLock()
	g, ok := r.gauges[k]
	r.mu.RUnlock()
	if ok {
		return g
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if g, ok := r.gauges[k]; ok {
		return g
	}
	g = &Gauge{}
	r.gauges[k] = g
	return g
}

// Histogram returns the histogram for name+labels, creating it with the
// given bounds on first use (bounds are ignored if it already exists).
func (r *Registry) Histogram(name string, labels map[string]string, bounds []float64) *Histogram {
	k := key(name, labels)
	r.mu.RLock()
	h, ok := r.histograms[k]
	r.mu.RUnlock()
	if ok {
		return h
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if h, ok := r.histograms[k]; ok {
		return h
	}
	h = NewHistogram(bounds)
	r.histograms[k] = h
	return h
}

// HitRate returns the hit-rate tracker for name+labels.
func (r *Registry) HitRate(name string, labels map[string]string) *HitRate {
	k := key(name, labels)
	r.mu.RLock()
	hr, ok := r.hitrates[k]
	r.mu.RUnlock()
	if ok {
		return hr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if hr, ok := r.hitrates[k]; ok {
		return hr
	}
	hr = &HitRate{}
	r.hitrates[k] = hr
	return hr
}

// ErrorCodes returns the error-code tally for name+labels.
func (r *Registry) ErrorCodes(name string, labels map[string]string) *ErrorCodes {
	k := key(name, labels)
	r.mu.RLock()
	ec, ok := r.errcodes[k]
	r.mu.RUnlock()
	if ok {
		return ec
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if ec, ok := r.errcodes[k]; ok {
		return ec
	}
	ec = NewErrorCodes()
	r.errcodes[k] = ec
	return ec
}

// Snapshot is a point-in-time, exportable copy of every metric.
type Snapshot struct {
	Counters   map[string]int64             `json:"counters"`
	Gauges     map[string]float64           `json:"gauges"`
	Histograms map[string]HistogramSnapshot `json:"histograms"`
	HitRates   map[string]HitRateSnapshot   `json:"hit_rates"`
	ErrorCodes map[string]map[string]int64  `json:"error_codes"`
}

// HistogramSnapshot is an exportable histogram view.
type HistogramSnapshot struct {
	Count   uint64   `json:"count"`
	Sum     float64  `json:"sum"`
	Mean    float64  `json:"mean"`
	Min     float64  `json:"min"`
	Max     float64  `json:"max"`
	P50     float64  `json:"p50"`
	P90     float64  `json:"p90"`
	P99     float64  `json:"p99"`
	Buckets []Bucket `json:"buckets"`
}

// HitRateSnapshot is an exportable hit-rate view.
type HitRateSnapshot struct {
	Hits   int64   `json:"hits"`
	Misses int64   `json:"misses"`
	Ratio  float64 `json:"ratio"`
}

// Snapshot copies all current metric state.
func (r *Registry) Snapshot() *Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	snap := &Snapshot{
		Counters:   make(map[string]int64, len(r.counters)),
		Gauges:     make(map[string]float64, len(r.gauges)),
		Histograms: make(map[string]HistogramSnapshot, len(r.histograms)),
		HitRates:   make(map[string]HitRateSnapshot, len(r.hitrates)),
		ErrorCodes: make(map[string]map[string]int64, len(r.errcodes)),
	}
	for k, c := range r.counters {
		snap.Counters[k] = c.Value()
	}
	for k, g := range r.gauges {
		snap.Gauges[k] = g.Value()
	}
	for k, h := range r.histograms {
		hs := HistogramSnapshot{
			Count:   h.Count(),
			Sum:     h.Sum(),
			Mean:    h.Mean(),
			Buckets: h.Buckets(),
		}
		if mn, mx, ok := h.MinMax(); ok {
			hs.Min, hs.Max = mn, mx
		}
		if v, ok := h.Quantile(0.50); ok {
			hs.P50 = v
		}
		if v, ok := h.Quantile(0.90); ok {
			hs.P90 = v
		}
		if v, ok := h.Quantile(0.99); ok {
			hs.P99 = v
		}
		snap.Histograms[k] = hs
	}
	for k, hr := range r.hitrates {
		snap.HitRates[k] = HitRateSnapshot{Hits: hr.Hits(), Misses: hr.Misses(), Ratio: hr.Ratio()}
	}
	for k, ec := range r.errcodes {
		snap.ErrorCodes[k] = ec.Snapshot()
	}
	return snap
}
