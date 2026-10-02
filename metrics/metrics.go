// Package metrics 提供线程安全的细粒度度量采集与统计聚合导出。
//
// 支持的度量类型：
//   - Counter：单调递增计数器（如容器转换次数）
//   - Gauge：瞬时值（如当前容器数量、基数）
//   - Histogram：延迟分布直方图（微秒级，支持分位数估算）
//   - HitRate：命中率（如容器缓存命中）
//   - ErrorSet：按组件 + 错误码维度的错误计数
//
// 所有类型均可在多 goroutine 下并发安全使用，快照可导出为 JSON 或
// Prometheus 风格文本。
package metrics

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// LatencyBuckets 是默认的延迟直方图桶边界（单位：微秒）。
var LatencyBuckets = []float64{1, 2, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000}

// Counter 是单调递增计数器。
type Counter struct{ v atomic.Int64 }

// Inc 增加 1。
func (c *Counter) Inc() { c.v.Add(1) }

// Add 增加 delta。
func (c *Counter) Add(delta int64) { c.v.Add(delta) }

// Value 返回当前值。
func (c *Counter) Value() int64 { return c.v.Load() }

// Gauge 是瞬时值度量。
type Gauge struct{ v atomic.Int64 }

// Set 设置当前值。
func (g *Gauge) Set(v int64) { g.v.Store(v) }

// Inc 增加 1。
func (g *Gauge) Inc() { g.v.Add(1) }

// Dec 减少 1。
func (g *Gauge) Dec() { g.v.Add(-1) }

// Add 增加 delta（可为负）。
func (g *Gauge) Add(delta int64) { g.v.Add(delta) }

// Value 返回当前值。
func (g *Gauge) Value() int64 { return g.v.Load() }

// Histogram 是固定桶边界的延迟分布直方图。
// 桶语义：counts[i] 记录 bounds[i-1] < v <= bounds[i] 的观测数，
// 最后一个桶记录 v > bounds[last]（即 +Inf）的观测数。
type Histogram struct {
	bounds []float64
	counts []atomic.Int64
	count  atomic.Int64
	sum    atomic.Uint64 // float64 位表示
	min    atomic.Uint64 // float64 位表示
	max    atomic.Uint64 // float64 位表示
}

// NewHistogram 以给定升序桶边界创建直方图（边界会被排序）。
func NewHistogram(bounds []float64) *Histogram {
	cp := append([]float64(nil), bounds...)
	sort.Float64s(cp)
	h := &Histogram{bounds: cp, counts: make([]atomic.Int64, len(cp)+1)}
	h.reset()
	return h
}

func (h *Histogram) reset() {
	h.count.Store(0)
	h.sum.Store(0)
	h.min.Store(math.Float64bits(math.Inf(1)))
	h.max.Store(math.Float64bits(math.Inf(-1)))
	for i := range h.counts {
		h.counts[i].Store(0)
	}
}

// Observe 记录一次观测值。
func (h *Histogram) Observe(v float64) {
	idx := sort.SearchFloat64s(h.bounds, v)
	h.counts[idx].Add(1)
	h.count.Add(1)
	for {
		old := h.sum.Load()
		if h.sum.CompareAndSwap(old, math.Float64bits(math.Float64frombits(old)+v)) {
			break
		}
	}
	for {
		old := h.min.Load()
		if v >= math.Float64frombits(old) || h.min.CompareAndSwap(old, math.Float64bits(v)) {
			break
		}
	}
	for {
		old := h.max.Load()
		if v <= math.Float64frombits(old) || h.max.CompareAndSwap(old, math.Float64bits(v)) {
			break
		}
	}
}

// BucketSnapshot 是单个直方图桶的快照。
type BucketSnapshot struct {
	Le         string  `json:"le"`
	UpperBound float64 `json:"upper_bound"`
	Count      int64   `json:"count"`
}

// HistogramSnapshot 是直方图的聚合快照。
type HistogramSnapshot struct {
	Count   int64            `json:"count"`
	Sum     float64          `json:"sum"`
	Min     float64          `json:"min"`
	Max     float64          `json:"max"`
	Buckets []BucketSnapshot `json:"buckets"`
}

// Snapshot 生成直方图快照。
func (h *Histogram) Snapshot() HistogramSnapshot {
	count := h.count.Load()
	sum := math.Float64frombits(h.sum.Load())
	minV := math.Float64frombits(h.min.Load())
	maxV := math.Float64frombits(h.max.Load())
	if count == 0 {
		minV, maxV = 0, 0
	}
	buckets := make([]BucketSnapshot, len(h.bounds)+1)
	for i, b := range h.bounds {
		buckets[i] = BucketSnapshot{
			Le:         strconv.FormatFloat(b, 'g', -1, 64),
			UpperBound: b,
			Count:      h.counts[i].Load(),
		}
	}
	// +Inf 桶的 UpperBound 用当前最大值代替，保证 JSON 可序列化且分位数可估算。
	buckets[len(h.bounds)] = BucketSnapshot{Le: "+Inf", UpperBound: maxV, Count: h.counts[len(h.bounds)].Load()}
	return HistogramSnapshot{Count: count, Sum: sum, Min: minV, Max: maxV, Buckets: buckets}
}

// Quantile 基于桶内线性插值估算分位数（q 取值 [0,1]）。无观测时返回 0。
func (s HistogramSnapshot) Quantile(q float64) float64 {
	if s.Count == 0 || q <= 0 {
		return 0
	}
	if q > 1 {
		q = 1
	}
	target := q * float64(s.Count)
	lower := 0.0
	var cum int64
	for _, b := range s.Buckets {
		prev := cum
		cum += b.Count
		if float64(cum) >= target && b.Count > 0 {
			frac := (target - float64(prev)) / float64(b.Count)
			return lower + (b.UpperBound-lower)*frac
		}
		lower = b.UpperBound
	}
	return s.Max
}

// HitRate 统计命中率。
type HitRate struct {
	hits   atomic.Int64
	misses atomic.Int64
}

// Hit 记录一次命中。
func (h *HitRate) Hit() { h.hits.Add(1) }

// Miss 记录一次未命中。
func (h *HitRate) Miss() { h.misses.Add(1) }

// HitRateSnapshot 是命中率快照。
type HitRateSnapshot struct {
	Hits   int64   `json:"hits"`
	Misses int64   `json:"misses"`
	Rate   float64 `json:"rate"`
}

// Snapshot 生成命中率快照。无事件时 Rate 为 0。
func (h *HitRate) Snapshot() HitRateSnapshot {
	hits := h.hits.Load()
	misses := h.misses.Load()
	total := hits + misses
	rate := 0.0
	if total > 0 {
		rate = float64(hits) / float64(total)
	}
	return HitRateSnapshot{Hits: hits, Misses: misses, Rate: rate}
}

// Snapshot 是整个注册表的聚合快照。
type Snapshot struct {
	UptimeSeconds float64                      `json:"uptime_seconds"`
	Counters      map[string]int64             `json:"counters"`
	Gauges        map[string]int64             `json:"gauges"`
	Histograms    map[string]HistogramSnapshot `json:"histograms"`
	HitRates      map[string]HitRateSnapshot   `json:"hit_rates"`
	Errors        map[string]map[string]int64  `json:"errors"`
}

// Registry 是度量注册表，按名称管理各类度量。
type Registry struct {
	mu         sync.RWMutex
	counters   map[string]*Counter
	gauges     map[string]*Gauge
	histograms map[string]*Histogram
	hitRates   map[string]*HitRate

	errMu  sync.Mutex
	errors map[string]map[string]int64

	started time.Time
}

// NewRegistry 创建空注册表。
func NewRegistry() *Registry {
	return &Registry{
		counters:   make(map[string]*Counter),
		gauges:     make(map[string]*Gauge),
		histograms: make(map[string]*Histogram),
		hitRates:   make(map[string]*HitRate),
		errors:     make(map[string]map[string]int64),
		started:    time.Now(),
	}
}

// Counter 返回（或创建）指定名称的计数器。
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

// Gauge 返回（或创建）指定名称的仪表。
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

// Histogram 返回（或创建）指定名称的直方图。已存在时忽略 bounds 参数。
func (r *Registry) Histogram(name string, bounds []float64) *Histogram {
	r.mu.RLock()
	h, ok := r.histograms[name]
	r.mu.RUnlock()
	if ok {
		return h
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if h, ok = r.histograms[name]; ok {
		return h
	}
	h = NewHistogram(bounds)
	r.histograms[name] = h
	return h
}

// HitRate 返回（或创建）指定名称的命中率统计。
func (r *Registry) HitRate(name string) *HitRate {
	r.mu.RLock()
	h, ok := r.hitRates[name]
	r.mu.RUnlock()
	if ok {
		return h
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if h, ok = r.hitRates[name]; ok {
		return h
	}
	h = &HitRate{}
	r.hitRates[name] = h
	return h
}

// ObserveLatency 以默认延迟桶（微秒）记录一次延迟观测。
func (r *Registry) ObserveLatency(name string, microseconds float64) {
	r.Histogram(name, LatencyBuckets).Observe(microseconds)
}

// RecordError 记录一次 component 下 code 错误码的出现。
func (r *Registry) RecordError(component, code string) {
	r.errMu.Lock()
	defer r.errMu.Unlock()
	m, ok := r.errors[component]
	if !ok {
		m = make(map[string]int64)
		r.errors[component] = m
	}
	m[code]++
}

// Snapshot 生成注册表聚合快照。
func (r *Registry) Snapshot() Snapshot {
	s := Snapshot{
		UptimeSeconds: time.Since(r.started).Seconds(),
		Counters:      make(map[string]int64),
		Gauges:        make(map[string]int64),
		Histograms:    make(map[string]HistogramSnapshot),
		HitRates:      make(map[string]HitRateSnapshot),
		Errors:        make(map[string]map[string]int64),
	}
	r.mu.RLock()
	for name, c := range r.counters {
		s.Counters[name] = c.Value()
	}
	for name, g := range r.gauges {
		s.Gauges[name] = g.Value()
	}
	for name, h := range r.histograms {
		s.Histograms[name] = h.Snapshot()
	}
	for name, hr := range r.hitRates {
		s.HitRates[name] = hr.Snapshot()
	}
	r.mu.RUnlock()
	r.errMu.Lock()
	for comp, codes := range r.errors {
		cp := make(map[string]int64, len(codes))
		for code, n := range codes {
			cp[code] = n
		}
		s.Errors[comp] = cp
	}
	r.errMu.Unlock()
	return s
}

// ExportJSON 将聚合快照导出为 JSON。
func (r *Registry) ExportJSON() ([]byte, error) {
	return json.MarshalIndent(r.Snapshot(), "", "  ")
}

var nameSanitizer = strings.NewReplacer(".", "_", "-", "_", "/", "_", " ", "_")

// ExportText 将聚合快照导出为 Prometheus 风格文本。
func (r *Registry) ExportText() string {
	s := r.Snapshot()
	var sb strings.Builder

	for _, name := range sortedKeys(s.Counters) {
		n := nameSanitizer.Replace(name)
		fmt.Fprintf(&sb, "# TYPE %s counter\n%s %d\n", n, n, s.Counters[name])
	}
	for _, name := range sortedKeys(s.Gauges) {
		n := nameSanitizer.Replace(name)
		fmt.Fprintf(&sb, "# TYPE %s gauge\n%s %d\n", n, n, s.Gauges[name])
	}
	for _, name := range sortedKeys(s.Histograms) {
		n := nameSanitizer.Replace(name)
		h := s.Histograms[name]
		fmt.Fprintf(&sb, "# TYPE %s histogram\n", n)
		for _, b := range h.Buckets {
			fmt.Fprintf(&sb, "%s_bucket{le=\"%s\"} %d\n", n, b.Le, b.Count)
		}
		fmt.Fprintf(&sb, "%s_sum %g\n%s_count %d\n", n, h.Sum, n, h.Count)
	}
	for _, name := range sortedKeys(s.HitRates) {
		n := nameSanitizer.Replace(name)
		hr := s.HitRates[name]
		fmt.Fprintf(&sb, "# TYPE %s gauge\n%s_hits %d\n%s_misses %d\n%s_ratio %g\n", n, n, hr.Hits, n, hr.Misses, n, hr.Rate)
	}
	for _, comp := range sortedKeys(s.Errors) {
		n := nameSanitizer.Replace(comp)
		codes := s.Errors[comp]
		for _, code := range sortedKeys(codes) {
			fmt.Fprintf(&sb, "%s_errors_total{code=\"%s\"} %d\n", n, code, codes[code])
		}
	}
	return sb.String()
}

// Reset 将所有度量清零并重新开始计时。
func (r *Registry) Reset() {
	r.mu.Lock()
	for _, c := range r.counters {
		c.v.Store(0)
	}
	for _, g := range r.gauges {
		g.v.Store(0)
	}
	for _, h := range r.histograms {
		h.reset()
	}
	for _, hr := range r.hitRates {
		hr.hits.Store(0)
		hr.misses.Store(0)
	}
	r.mu.Unlock()
	r.errMu.Lock()
	r.errors = make(map[string]map[string]int64)
	r.errMu.Unlock()
	r.started = time.Now()
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
