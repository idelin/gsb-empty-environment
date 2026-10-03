package main

import (
	"fmt"
	"time"
)

func main() {
	const (
		capacityEntries = 4096
		entrySize       = int64(256)
		workers         = 16
	)

	fmt.Println("=== 阶段一：命中率拐点扫描（工作集/容量 0.25x ~ 8x）===")
	ratios := []float64{0.25, 0.5, 0.75, 1.0, 1.5, 2.0, 4.0, 8.0}
	phases := SweepHitRate(capacityEntries, entrySize, ratios, 100000, workers)
	for _, p := range phases {
		fmt.Printf("  工作集 %5.2fx (%6d keys): 命中率 %6.2f%%  驱逐 %8d  吞吐 %12.0f ops/s\n",
			p.WorkingSetRatio, p.WorkingSetKeys, p.HitRate*100, p.Evictions, p.ThroughputOps)
	}
	if idx := FindHitRateInflection(phases); idx >= 0 {
		fmt.Printf(">> 命中率拐点位于工作集 %.2fx（命中率 %.2f%% -> %.2f%%）\n",
			phases[idx].WorkingSetRatio, phases[idx-1].HitRate*100, phases[idx].HitRate*100)
	}

	fmt.Println("\n=== 阶段二：高并发压测 + 驱逐突变区间检测 ===")
	m := NewMetrics()
	c := NewCache(capacityEntries*entrySize, 512, m)
	stop := make(chan struct{})
	windowsCh := make(chan []EvictionWindow)
	go func() { windowsCh <- SampleEvictions(m, 20*time.Millisecond, stop) }()

	start := time.Now()
	// 交替运行平稳期（0.5x 工作集、低写比例）与突发期（8x 工作集、高写比例），
	// 制造周期性驱逐风暴以验证突变区间检测。
	for i := 0; i < 5; i++ {
		runWorkload(c, m, capacityEntries/2, 60000, workers, entrySize, 0.2)
		runWorkload(c, m, capacityEntries*8, 40000, workers, entrySize, 0.8)
	}
	elapsed := time.Since(start)
	close(stop)
	windows := <-windowsCh

	fmt.Printf("  压测完成：%d ops，耗时 %v，吞吐 %.0f ops/s\n",
		m.Hits.Load()+m.Misses.Load()+m.Sets.Load(), elapsed.Round(time.Millisecond),
		float64(500000)/elapsed.Seconds())

	surges := DetectEvictionSurges(windows, 2.0)
	if len(surges) == 0 {
		fmt.Println("  未检测到驱逐突变区间")
	} else {
		fmt.Printf("  检测到 %d 个驱逐突变区间（median+2σ 阈值）：\n", len(surges))
		for _, w := range surges {
			fmt.Printf("    窗口 #%d [%v, %v): 驱逐 %d 次\n", w.Index,
				w.Start.Round(time.Millisecond), w.End.Round(time.Millisecond), w.Evictions)
		}
	}

	fmt.Println("\n=== 阶段三：缓存健康度评估报告（JSON）===")
	report := BuildReport(c, m)
	js, err := report.JSON()
	if err != nil {
		panic(err)
	}
	fmt.Println(js)
}
