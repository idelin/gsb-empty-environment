package cachebench

import (
	"strconv"
	"sync"
	"time"
)

// StressConfig 配置高并发读写压力场景。
type StressConfig struct {
	Capacity    int           // 缓存容量（条目数）
	Workers     int           // 并发 goroutine 数
	HotKeys     int           // 热点键数量（可全部装入缓存）
	Phase       time.Duration // 每个阶段时长
	Window      time.Duration // 指标窗口粒度
	WindowCount int           // 窗口数量上限
}

// DefaultStressConfig 返回默认压力场景配置。
func DefaultStressConfig() StressConfig {
	return StressConfig{
		Capacity:    50000,
		Workers:     32,
		HotKeys:     40000,
		Phase:       300 * time.Millisecond,
		Window:      10 * time.Millisecond,
		WindowCount: 4096,
	}
}

// runPhase 在 dur 时长内以 workers 个 goroutine 执行读写混合负载。
// readRatio 为读操作占比（按操作序号确定性交错）。
func runPhase(c *Cache, workers int, dur time.Duration, readRatio uint64, keyFn func(worker int, seq uint64) string) {
	deadline := time.Now().Add(dur)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			var seq uint64
			value := make([]byte, 64+(w*37)%256)
			for time.Now().Before(deadline) {
				seq++
				key := keyFn(w, seq)
				if seq%100 < readRatio {
					c.Get(key)
				} else {
					c.Set(key, value)
				}
			}
		}(w)
	}
	wg.Wait()
}

// RunStressScenario 执行三阶段高并发压力场景并返回评估报告：
//
//	阶段1：热点键读为主（命中率高位平稳）
//	阶段2：冷键洪峰读写（制造命中率拐点与驱逐突变区间）
//	阶段3：回到热点键（观察驱逐后的命中率恢复）
func RunStressScenario(cfg StressConfig) *Report {
	metrics := NewMetrics(cfg.Window, cfg.WindowCount)
	cache := NewCache(cfg.Capacity, metrics)

	// 预热：写入热点数据集。
	warm := make([]byte, 128)
	for i := 0; i < cfg.HotKeys; i++ {
		cache.Set("hot-"+strconv.Itoa(i), warm)
	}

	hotKey := func(w int, seq uint64) string {
		return "hot-" + strconv.Itoa(int((uint64(w)*2654435761+seq)%uint64(cfg.HotKeys)))
	}
	coldKey := func(w int, seq uint64) string {
		return "cold-" + strconv.Itoa(w) + "-" + strconv.FormatUint(seq, 10)
	}

	runPhase(cache, cfg.Workers, cfg.Phase, 90, hotKey)  // 阶段1
	runPhase(cache, cfg.Workers, cfg.Phase, 50, coldKey) // 阶段2
	runPhase(cache, cfg.Workers, cfg.Phase, 90, hotKey)  // 阶段3

	return BuildReport(metrics, cache.MemoryStats())
}
