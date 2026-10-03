package main

import (
	"fmt"

	"kmatcher"
)

func printResult(title string, res matcher.Result) {
	fmt.Println(title)
	for _, mt := range res.Matches {
		fmt.Printf("  工人%d -> 任务%d  产值 %.0f\n", mt.Worker, mt.Task, mt.Weight)
	}
	fmt.Printf("  总产值: %.0f\n", res.Total)
	for _, u := range res.Unmatched {
		name := "工人"
		if u.Side == "task" {
			name = "任务"
		}
		fmt.Printf("  未匹配 %s%d  机会成本 %.0f\n", name, u.Index, u.OpportunityCost)
	}
	if len(res.Unmatched) > 0 {
		fmt.Printf("  未匹配最小机会成本: %.0f\n", res.MinOpportunityCost)
	}
	fmt.Println()
}

func main() {
	weight := [][]float64{
		{100, 60, 10, 40},
		{90, 55, 20, 35},
		{80, 50, 30, 25},
		{70, 45, 15, 20},
	}
	m := matcher.New(4, 4)
	for i := range weight {
		for j, w := range weight[i] {
			m.SetWeight(i, j, w)
		}
	}

	printResult("== 初始最大权匹配 ==", m.Solve())

	fmt.Println(">> 工人0 (最高产值) 临时请假下线，局部调整顶标并重新增广")
	m.MaskWorker(0, true)
	printResult("== 切换后的次优匹配 ==", m.Solve())

	fmt.Println(">> 任务2 的产值临时上调: 工人3-任务2 15 -> 95")
	m.SetWeight(3, 2, 95)
	printResult("== 权重调整后的匹配 ==", m.Solve())

	fmt.Println(">> 工人0 返岗")
	m.MaskWorker(0, false)
	printResult("== 恢复后的匹配 ==", m.Solve())
}
