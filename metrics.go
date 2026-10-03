// Package cachebench 提供内置高精度打点的缓存审计组件：
// 无锁原子采集 Hit/Miss/Eviction 计数、微秒级访问延迟分布，
// 并支持输出 JSON 格式的缓存健康度评估报告与内存碎片率。
package cachebench

import (
	"math"
	"sync/atomic"
	"time"
)

// 直方图分桶布局：
//
//	[0, 1000)      us 级，1us 分辨率（微秒级精度）
//	[1ms, 1000ms)  ms 级，1ms 分辨率
//	[1s,  +inf)    s 级，1s 分辨率，末尾截断
const (
	subMsBuckets   = 1000
	subSecBuckets  = 1000
	overSecBuckets = 64
	totalBuckets   = subMsBuckets + subSecBuckets + overSecBuckets
)

// Histogram 以无锁原子桶记录操作耗时分布，微秒级精度。
type Histogram struct {
	buckets [totalBuckets]uint64
	count   uint64
	sumUs   uint64
	maxUs   int64
	minUs   int64
}

// NewHistogram 创建空直方图。
func NewHistogram() *Histogram {
	return &Histogram{minUs: math.MaxInt64}
}

func bucketIndex(us int64) int {
	if us < 0 {
		us = 0
	}
	if us < subMsBuckets {
		return int(us)
	}
	ms := us / 1000
	if ms < subSecBuckets {
		return subMsBuckets + int(ms)
	}
	sec := ms / 1000
	idx := subMsBuckets + subSecBuckets + int(sec)
	if idx >= totalBuckets {
		idx = totalBuckets - 1
	}
	return idx
}

func bucketLowerBoundUs(idx int) int64 {
	switch {
	case idx < subMsBuckets:
		return int64(idx)
	case idx < subMsBuckets+subSecBuckets:
		return int64(idx-subMsBuckets) * 1000
	default:
		return int64(idx-subMsBuckets-subSecBuckets) * 1000 * 1000
	}
}

// Record 记录一次耗时，全程仅使用原子操作，无锁。
func (h *Histogram) Record(d time.Duration) {
	us := d.Nanoseconds() / 1000
	atomic.AddUint64(&h.buckets[bucketIndex(us)], 1)
	atomic.AddUint64(&h.count, 1)
	atomic.AddUint64(&h.sumUs, uint64(us))
	for {
		old := atomic.LoadInt64(&h.maxUs)
		if us <= old || atomic.CompareAndSwapInt64(&h.maxUs, old, us) {
			break
		}
	}
	for {
		old := atomic.LoadInt64(&h.minUs)
		if us >= old || atomic.CompareAndSwapInt64(&h.minUs, old, us) {
			break
		}
	}
}

// Count 返回已记录的样本数。
func (h *Histogram) Count() uint64 { return atomic.LoadUint64(&h.count) }

// MeanUs 返回平均耗时（微秒）。
func (h *Histogram) MeanUs() float64 {
	c := h.Count()
	if c == 0 {
		return 0
	}
	return float64(atomic.LoadUint64(&h.sumUs)) / float64(c)
}

// Percentile 返回 p 百分位耗时（微秒），p ∈ [0,100]。
// 亚毫秒区间具有 1us 分辨率。
func (h *Histogram) Percentile(p float64) int64 {
	c := h.Count()
	if c == 0 {
		return 0
	}
	target := uint64(math.Ceil(p / 100 * float64(c)))
	if target == 0 {
		target = 1
	}
	var cum uint64
	for i := 0; i < totalBuckets; i++ {
		cum += atomic.LoadUint64(&h.buckets[i])
		if cum >= target {
			return bucketLowerBoundUs(i)
		}
	}
	return bucketLowerBoundUs(totalBuckets - 1)
}

// MinUs 返回最小耗时（微秒）。
func (h *Histogram) MinUs() int64 {
	if h.Count() == 0 {
		return 0
	}
	return atomic.LoadInt64(&h.minUs)
}

// MaxUs 返回最大耗时（微秒）。
func (h *Histogram) MaxUs() int64 { return atomic.LoadInt64(&h.maxUs) }

// Window 是一个时间窗口内的原子计数器组。
type Window struct {
	Hits      uint64
	Misses    uint64
	Sets      uint64
	Evictions uint64
}

// WindowSeries 将指标按固定时长窗口分片，用于事后定位
// 命中率拐点与驱逐突变区间。
type WindowSeries struct {
	start    time.Time
	windowNs int64
	windows  []Window
}

// NewWindowSeries 创建窗口序列：每个窗口 window 长，共 count 个。
func NewWindowSeries(window time.Duration, count int) *WindowSeries {
	return &WindowSeries{
		start:    time.Now(),
		windowNs: int64(window),
		windows:  make([]Window, count),
	}
}

func (ws *WindowSeries) index() int {
	idx := int(time.Since(ws.start).Nanoseconds() / ws.windowNs)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(ws.windows) {
		idx = len(ws.windows) - 1
	}
	return idx
}

// RecordGet 在当前窗口记录一次读。
func (ws *WindowSeries) RecordGet(hit bool) {
	w := &ws.windows[ws.index()]
	if hit {
		atomic.AddUint64(&w.Hits, 1)
	} else {
		atomic.AddUint64(&w.Misses, 1)
	}
}

// RecordSet 在当前窗口记录一次写及伴随的驱逐数。
func (ws *WindowSeries) RecordSet(evictions uint64) {
	w := &ws.windows[ws.index()]
	atomic.AddUint64(&w.Sets, 1)
	if evictions > 0 {
		atomic.AddUint64(&w.Evictions, evictions)
	}
}

// Snapshot 原子地拷贝全部窗口。
func (ws *WindowSeries) Snapshot() []Window {
	out := make([]Window, len(ws.windows))
	for i := range ws.windows {
		out[i] = Window{
			Hits:      atomic.LoadUint64(&ws.windows[i].Hits),
			Misses:    atomic.LoadUint64(&ws.windows[i].Misses),
			Sets:      atomic.LoadUint64(&ws.windows[i].Sets),
			Evictions: atomic.LoadUint64(&ws.windows[i].Evictions),
		}
	}
	return out
}

// WindowDuration 返回单个窗口时长。
func (ws *WindowSeries) WindowDuration() time.Duration {
	return time.Duration(ws.windowNs)
}

// Metrics 是缓存审计组件的核心：无锁原子收集核心指标。
type Metrics struct {
	hits      uint64
	misses    uint64
	sets      uint64
	evictions uint64

	// GetLatency / SetLatency 为微秒级精度延迟直方图。
	GetLatency *Histogram
	SetLatency *Histogram

	// Windows 为时间窗口序列，用于拐点与突变区间分析。
	Windows *WindowSeries
}

// NewMetrics 创建度量组件；window/windowCount 控制时间窗口粒度。
func NewMetrics(window time.Duration, windowCount int) *Metrics {
	return &Metrics{
		GetLatency: NewHistogram(),
		SetLatency: NewHistogram(),
		Windows:    NewWindowSeries(window, windowCount),
	}
}

// RecordGet 记录一次缓存读（含耗时与命中结果）。
func (m *Metrics) RecordGet(hit bool, d time.Duration) {
	if hit {
		atomic.AddUint64(&m.hits, 1)
	} else {
		atomic.AddUint64(&m.misses, 1)
	}
	m.GetLatency.Record(d)
	m.Windows.RecordGet(hit)
}

// RecordSet 记录一次缓存写（含耗时与本次引发的驱逐数）。
func (m *Metrics) RecordSet(d time.Duration, evictions uint64) {
	atomic.AddUint64(&m.sets, 1)
	if evictions > 0 {
		atomic.AddUint64(&m.evictions, evictions)
	}
	m.SetLatency.Record(d)
	m.Windows.RecordSet(evictions)
}

// Hits 返回累计命中数。
func (m *Metrics) Hits() uint64 { return atomic.LoadUint64(&m.hits) }

// Misses 返回累计未命中数。
func (m *Metrics) Misses() uint64 { return atomic.LoadUint64(&m.misses) }

// Sets 返回累计写入数。
func (m *Metrics) Sets() uint64 { return atomic.LoadUint64(&m.sets) }

// Evictions 返回累计驱逐数。
func (m *Metrics) Evictions() uint64 { return atomic.LoadUint64(&m.evictions) }

// Gets 返回累计读取数（命中 + 未命中）。
func (m *Metrics) Gets() uint64 { return m.Hits() + m.Misses() }

// HitRate 返回累计命中率。
func (m *Metrics) HitRate() float64 {
	gets := m.Gets()
	if gets == 0 {
		return 0
	}
	return float64(m.Hits()) / float64(gets)
}
