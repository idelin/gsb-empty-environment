// 运行样例：工人×任务完全二分图的最大产值分配，
// 以及高产值工人临时下线时的增量切换。
package main

import (
	"fmt"

	"kmmatcher"
)

func printResult(title string, res matcher.Result) {
	fmt.Println("=== " + title + " ===")
	for _, p := range res.Pairs {
		fmt.Printf("  工人 W%d -> 任务 T%d  (产值 %d)\n", p.Worker, p.Task, p.Weight)
	}
	fmt.Printf("  全局总产值: %d\n", res.Total)
	if len(res.UnmatchedWorkers) > 0 {
		fmt.Printf("  未匹配工人: %v\n", res.UnmatchedWorkers)
	}
	if len(res.UnmatchedTasks) > 0 {
		fmt.Printf("  未匹配任务: %v\n", res.UnmatchedTasks)
	}
	if len(res.OfflineWorkers) > 0 {
		fmt.Printf("  已下线工人: %v\n", res.OfflineWorkers)
	}
	fmt.Printf("  未匹配最小机会成本: %d\n", res.MinOpportunityCost)
	fmt.Println()
}

func main() {
	// 4 名工人 × 4 项任务的完全二分图，边权为产值。
	// W0 是高产值工人。
	weights := [][]int64{
		{100, 90, 95, 85}, // W0
		{10, 12, 11, 14},  // W1
		{9, 8, 13, 7},     // W2
		{6, 15, 5, 10},    // W3
	}
	m := matcher.New(4, 4)
	for u := range weights {
		for v, w := range weights[u] {
			m.SetEdge(u, v, w)
		}
	}

	printResult("初始最大权匹配（KM 全量求解）", m.Match())

	lx, ly := m.Labels()
	fmt.Printf("  当前顶标 lx=%v ly=%v\n\n", lx, ly)

	// 高产值工人 W0 临时请假下线：局部调整顶标 + 交错增广轨，快速切换次优匹配。
	printResult("W0 请假下线（MaskWorker + 增量 Rematch）", m.MaskWorker(0))

	// 任务 T1 的产值临时上调：单条边权动态调整。
	printResult("W1->T1 产值上调为 30（UpdateEdge + 增量 Rematch）", m.UpdateEdge(1, 1, 30))

	// W0 复工。
	printResult("W0 复工（UnmaskWorker + 增量 Rematch）", m.UnmaskWorker(0))
}
