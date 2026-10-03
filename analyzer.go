package main

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// PhaseResult 单个基准阶段的统计结果。
type PhaseResult struct {
	WorkingSetRatio float64 `json:"working_set_ratio"` // 工作集 / 缓存容量（条目数）
	WorkingSetKeys  int     `json:"working_set_keys"`
	Ops             uint64  `json:"ops"`
	HitRate         float64 `json:"hit_rate"`
	Evictions       uint64  `json:"evictions"`
	ThroughputOps   float64 `json:"throughput_ops_per_sec"`
}

// runWorkload 在 c 上以 workers 个 goroutine 执行 ops 次混合读写，
// key 均匀取自 [0, keys) 区间。返回实际执行的读操作数。
func runWorkload(c *Cache, m *Metrics, keys int, ops uint64, workers int, entrySize int64, writeRatio float64) {
	var counter atomic.Uint64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for {
				n := counter.Add(1)
				if n > ops {
					return
				}
				key := fmt.Sprintf("key-%d", rng.Intn(keys))
				if rng.Float64() < writeRatio {
					c.Set(key, entrySize)
				} else {
					c.Get(key)
				}
			}
		}(int64(w)*7919 + 1)
	}
	wg.Wait()
}

// SweepHitRate 逐阶段扩大工作集，测量各阶段命中率，用于定位命中率拐点。
// 每个阶段使用全新缓存，容量固定为 capacityEntries 个条目。
func SweepHitRate(capacityEntries int, entrySize int64, ratios []float64, opsPerPhase uint64, workers int) []PhaseResult {
	results := make([]PhaseResult, 0, len(ratios))
	for _, ratio := range ratios {
		keys := int(float64(capacityEntries) * ratio)
		if keys < 1 {
			keys = 1
		}
		m := NewMetrics()
		c := NewCache(int64(capacityEntries)*entrySize, 4096, m)
		start := time.Now()
		runWorkload(c, m, keys, opsPerPhase, workers, entrySize, 0.2)
		elapsed := time.Since(start)
		results = append(results, PhaseResult{
			WorkingSetRatio: ratio,
			WorkingSetKeys:  keys,
			Ops:             m.Hits.Load() + m.Misses.Load() + m.Sets.Load(),
			HitRate:         m.HitRate(),
			Evictions:       m.Evictions.Load(),
			ThroughputOps:   float64(opsPerPhase) / elapsed.Seconds(),
		})
	}
	return results
}

// FindHitRateInflection 返回命中率下降幅度最大的阶段下标（拐点）。
// 即 hitRate[i-1] - hitRate[i] 最大的 i；无下降时返回 -1。
func FindHitRateInflection(phases []PhaseResult) int {
	best := -1
	var maxDrop float64
	for i := 1; i < len(phases); i++ {
		drop := phases[i-1].HitRate - phases[i].HitRate
		if drop > maxDrop {
			maxDrop = drop
			best = i
		}
	}
	if maxDrop <= 0 {
		return -1
	}
	return best
}

// EvictionWindow 一个时间窗内的驱逐统计。
type EvictionWindow struct {
	Index     int           `json:"index"`
	Start     time.Duration `json:"start"`
	End       time.Duration `json:"end"`
	Evictions uint64        `json:"evictions"`
}

// SampleEvictions 在压测运行期间按 interval 周期采样驱逐计数，直到 stop 关闭。
func SampleEvictions(m *Metrics, interval time.Duration, stop <-chan struct{}) []EvictionWindow {
	var windows []EvictionWindow
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	start := time.Now()
	last := uint64(0)
	lastTick := time.Duration(0)
	for {
		select {
		case <-stop:
			return windows
		case <-ticker.C:
			now := time.Since(start)
			cur := m.Evictions.Load()
			windows = append(windows, EvictionWindow{
				Index:     len(windows),
				Start:     lastTick,
				End:       now,
				Evictions: cur - last,
			})
			last = cur
			lastTick = now
		}
	}
}

// DetectEvictionSurges 检测驱逐突变区间：窗口驱逐数超过 median + k*stddev
// 且大于 0 的窗口。以中位数为基线可避免突发窗口抬高均值导致漏检，
// k 通常取 2 或 3。
func DetectEvictionSurges(windows []EvictionWindow, k float64) []EvictionWindow {
	if len(windows) == 0 {
		return nil
	}
	vals := make([]float64, len(windows))
	var sum float64
	for i, w := range windows {
		vals[i] = float64(w.Evictions)
		sum += float64(w.Evictions)
	}
	mean := sum / float64(len(windows))
	var sq float64
	for _, w := range windows {
		d := float64(w.Evictions) - mean
		sq += d * d
	}
	stddev := math.Sqrt(sq / float64(len(windows)))
	sort.Float64s(vals)
	median := vals[len(vals)/2]
	if len(vals)%2 == 0 {
		median = (vals[len(vals)/2-1] + vals[len(vals)/2]) / 2
	}
	threshold := median + k*stddev
	var surges []EvictionWindow
	for _, w := range windows {
		if w.Evictions > 0 && float64(w.Evictions) > threshold {
			surges = append(surges, w)
		}
	}
	return surges
}
