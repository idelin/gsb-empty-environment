package main

import (
	"sort"
	"sync/atomic"
	"time"
)

// 延迟记录器分片参数：16 个分片，每片 8192 个采样槽。
// 每个分片是一个固定大小的环形缓冲，写入仅依赖 atomic.AddUint64
// 获取槽位，完全无锁；槽满后覆盖最旧样本（蓄水池式采样）。
const (
	latShardCount = 16
	latShardSize  = 8192
)

type latShard struct {
	idx atomic.Uint64
	buf [latShardSize]int64 // 微秒
	_   [56]byte            // 填充缓存行，避免分片间伪共享
}

// LatencyRecorder 以微秒级精度无锁记录每次操作耗时分布。
type LatencyRecorder struct {
	shards [latShardCount]latShard
	next   atomic.Uint64
}

func NewLatencyRecorder() *LatencyRecorder {
	return &LatencyRecorder{}
}

// Record 记录一次耗时，精度为微秒。
func (r *LatencyRecorder) Record(d time.Duration) {
	shard := &r.shards[r.next.Add(1)&(latShardCount-1)]
	slot := shard.idx.Add(1) - 1
	shard.buf[slot%latShardSize] = d.Microseconds()
}

// Snapshot 返回当前所有有效样本（微秒）的副本，已排序。
func (r *LatencyRecorder) Snapshot() []int64 {
	var all []int64
	for i := 0; i < latShardCount; i++ {
		shard := &r.shards[i]
		n := shard.idx.Load()
		if n == 0 {
			continue
		}
		if n > latShardSize {
			n = latShardSize
		}
		for j := uint64(0); j < n; j++ {
			all = append(all, shard.buf[j])
		}
	}
	sort.Slice(all, func(a, b int) bool { return all[a] < all[b] })
	return all
}

// Count 返回已记录样本总数（含被覆盖的）。
func (r *LatencyRecorder) Count() uint64 {
	var total uint64
	for i := 0; i < latShardCount; i++ {
		total += r.shards[i].idx.Load()
	}
	return total
}

// Percentile 计算 p 分位延迟（微秒），p ∈ [0,100]。无样本时返回 0。
func Percentile(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 100 {
		return sorted[len(sorted)-1]
	}
	rank := p / 100 * float64(len(sorted)-1)
	lo := int(rank)
	hi := lo + 1
	if hi >= len(sorted) {
		return sorted[lo]
	}
	frac := rank - float64(lo)
	return sorted[lo] + int64(frac*float64(sorted[hi]-sorted[lo]))
}

// Metrics 缓存审计核心：全部计数器通过 sync/atomic 无锁收集。
type Metrics struct {
	Hits      atomic.Uint64 // 命中次数
	Misses    atomic.Uint64 // 未命中次数
	Evictions atomic.Uint64 // 驱逐次数
	Sets      atomic.Uint64 // 写入次数
	Latency   *LatencyRecorder
}

func NewMetrics() *Metrics {
	return &Metrics{Latency: NewLatencyRecorder()}
}

func (m *Metrics) RecordHit(start time.Time) {
	m.Hits.Add(1)
	m.Latency.Record(time.Since(start))
}

func (m *Metrics) RecordMiss(start time.Time) {
	m.Misses.Add(1)
	m.Latency.Record(time.Since(start))
}

func (m *Metrics) RecordSet(start time.Time) {
	m.Sets.Add(1)
	m.Latency.Record(time.Since(start))
}

func (m *Metrics) RecordEviction() {
	m.Evictions.Add(1)
}

// HitRate 返回命中率，无读取时返回 0。
func (m *Metrics) HitRate() float64 {
	hits := m.Hits.Load()
	total := hits + m.Misses.Load()
	if total == 0 {
		return 0
	}
	return float64(hits) / float64(total)
}
