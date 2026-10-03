package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestConcurrentStress 高并发读写压力测试：
// 32 个 goroutine 混合读写，验证无锁计数器一致性、
// 微秒级延迟打点与 JSON 健康报告输出。
func TestConcurrentStress(t *testing.T) {
	const (
		workers    = 32
		ops        = 400000
		keys       = 8192
		entrySize  = int64(256)
		capEntries = 4096
	)
	m := NewMetrics()
	c := NewCache(capEntries*entrySize, 512, m)

	stop := make(chan struct{})
	windowsCh := make(chan []EvictionWindow)
	go func() { windowsCh <- SampleEvictions(m, 20*time.Millisecond, stop) }()

	var counter atomic.Uint64
	var wg sync.WaitGroup
	start := time.Now()
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
				if rng.Float64() < 0.25 {
					c.Set(key, entrySize)
				} else {
					c.Get(key)
				}
			}
		}(int64(w)*104729 + 7)
	}
	wg.Wait()
	elapsed := time.Since(start)
	close(stop)
	windows := <-windowsCh

	hits, misses, sets := m.Hits.Load(), m.Misses.Load(), m.Sets.Load()
	total := hits + misses + sets
	if total != ops {
		t.Fatalf("计数不一致: hits(%d)+misses(%d)+sets(%d)=%d, 期望 %d",
			hits, misses, sets, total, ops)
	}
	if m.Latency.Count() != ops {
		t.Fatalf("延迟样本数 %d != 操作数 %d", m.Latency.Count(), ops)
	}
	if m.Evictions.Load() == 0 {
		t.Fatalf("工作集 2x 容量，应发生驱逐")
	}

	report := BuildReport(c, m)
	if _, err := json.Marshal(report); err != nil {
		t.Fatalf("报告 JSON 序列化失败: %v", err)
	}
	if report.LatencyP99Micros < report.LatencyP50Micros {
		t.Fatalf("P99 (%dus) 不应小于 P50 (%dus)", report.LatencyP99Micros, report.LatencyP50Micros)
	}
	if report.MemoryFragmentation < 0 || report.MemoryFragmentation >= 1 {
		t.Fatalf("碎片率 %.4f 超出 [0,1)", report.MemoryFragmentation)
	}

	surges := DetectEvictionSurges(windows, 2.0)
	t.Logf("压测: %d ops / %v = %.0f ops/s, 命中率 %.2f%%, 驱逐 %d, P99 %dus, 碎片率 %.2f%%, 驱逐突变区间 %d 个",
		ops, elapsed.Round(time.Millisecond), float64(ops)/elapsed.Seconds(),
		report.HitRate*100, m.Evictions.Load(), report.LatencyP99Micros,
		report.MemoryFragmentation*100, len(surges))
	js, _ := report.JSON()
	t.Logf("健康报告:\n%s", js)
}

// TestLatencyMicrosecondPrecision 验证延迟记录为微秒级精度且分位数正确。
func TestLatencyMicrosecondPrecision(t *testing.T) {
	r := NewLatencyRecorder()
	// 写入 0..999 微秒共 1000 个已知样本
	for i := 0; i < 1000; i++ {
		r.Record(time.Duration(i) * time.Microsecond)
	}
	snap := r.Snapshot()
	if len(snap) != 1000 {
		t.Fatalf("样本数 %d != 1000", len(snap))
	}
	if p50 := Percentile(snap, 50); p50 < 499 || p50 > 500 {
		t.Fatalf("P50 = %dus, 期望约 500us", p50)
	}
	if p99 := Percentile(snap, 99); p99 < 989 || p99 > 991 {
		t.Fatalf("P99 = %dus, 期望约 990us", p99)
	}
	if pmax := Percentile(snap, 100); pmax != 999 {
		t.Fatalf("Max = %dus, 期望 999us", pmax)
	}
}

// TestEvictionAndFragmentation 验证驱逐计数与内存碎片率计算。
func TestEvictionAndFragmentation(t *testing.T) {
	m := NewMetrics()
	// 容量 10 页 = 40960 字节，每条约 3000 字节（占 1 页），最多容纳 13 条
	c := NewCache(10*4096, 4096, m)
	for i := 0; i < 20; i++ {
		c.Set(fmt.Sprintf("k%d", i), 3000)
	}
	if m.Evictions.Load() != 7 {
		t.Fatalf("驱逐数 %d != 7", m.Evictions.Load())
	}
	if c.Len() != 13 {
		t.Fatalf("缓存条目 %d != 13", c.Len())
	}
	// 13 条 3000 字节各占 4096 一页：碎片率 = (13*4096-13*3000)/(13*4096)
	frag := c.Fragmentation()
	want := float64(13*4096-13*3000) / float64(13*4096)
	if frag != want {
		t.Fatalf("碎片率 %.6f != %.6f", frag, want)
	}
	// 驱逐最旧的 k0..k6，k13 应命中，k0 应未命中
	if !c.Get("k13") {
		t.Fatalf("k13 应命中")
	}
	if c.Get("k0") {
		t.Fatalf("k0 应已被驱逐")
	}
	if m.Hits.Load() != 1 || m.Misses.Load() != 1 {
		t.Fatalf("命中/未命中计数错误: %d/%d", m.Hits.Load(), m.Misses.Load())
	}
}

// TestHitRateInflection 验证命中率拐点定位：工作集超过容量后命中率骤降。
func TestHitRateInflection(t *testing.T) {
	ratios := []float64{0.25, 0.5, 1.0, 2.0, 4.0}
	phases := SweepHitRate(2048, 256, ratios, 60000, 8)
	for _, p := range phases {
		t.Logf("工作集 %.2fx: 命中率 %.2f%%, 驱逐 %d", p.WorkingSetRatio, p.HitRate*100, p.Evictions)
	}
	idx := FindHitRateInflection(phases)
	if idx < 0 {
		t.Fatalf("未检测到命中率拐点")
	}
	// 拐点应出现在工作集 >= 1x 容量处
	if phases[idx].WorkingSetRatio < 1.0 {
		t.Fatalf("拐点出现在 %.2fx，应 >= 1.0x", phases[idx].WorkingSetRatio)
	}
	if phases[idx].HitRate >= phases[idx-1].HitRate {
		t.Fatalf("拐点处命中率未下降: %.4f -> %.4f",
			phases[idx-1].HitRate, phases[idx].HitRate)
	}
}

// TestEvictionSurgeDetection 用合成数据验证驱逐突变区间检测。
func TestEvictionSurgeDetection(t *testing.T) {
	var windows []EvictionWindow
	for i := 0; i < 50; i++ {
		windows = append(windows, EvictionWindow{Index: i, Evictions: 10})
	}
	// 注入两个突变窗口
	windows[10].Evictions = 500
	windows[30].Evictions = 800
	surges := DetectEvictionSurges(windows, 2.0)
	if len(surges) != 2 {
		t.Fatalf("检测到 %d 个突变区间，期望 2", len(surges))
	}
	if surges[0].Index != 10 || surges[1].Index != 30 {
		t.Fatalf("突变区间定位错误: #%d, #%d", surges[0].Index, surges[1].Index)
	}
}

// TestHealthAssessment 验证健康度评级逻辑。
func TestHealthAssessment(t *testing.T) {
	good := &HealthReport{HitRate: 0.95, LatencyP99Micros: 50, MemoryFragmentation: 0.1}
	bad := &HealthReport{HitRate: 0.3, LatencyP99Micros: 5000, MemoryFragmentation: 0.8}
	if h, _ := assess(good); h != "HEALTHY" {
		t.Fatalf("健康报告评级 = %s, 期望 HEALTHY", h)
	}
	if h, _ := assess(bad); h != "CRITICAL" {
		t.Fatalf("异常报告评级 = %s, 期望 CRITICAL", h)
	}
}
