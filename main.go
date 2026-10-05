package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "用法: %s <input.json>   （\"-\" 表示标准输入）\n", os.Args[0])
		os.Exit(1)
	}
	os.Exit(run(os.Args[1]))
}

func run(path string) int {
	in, err := LoadInput(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "输入错误:", err)
		return 1
	}
	sys := buildSystem(in)

	// 1. 可行性：负环 = 矛盾约束，定位后报告
	if _, cycle := bellmanFord(sys.n(), sys.edges); cycle != nil {
		printCycle(os.Stdout, sys, cycle)
		return 2
	}
	fmt.Println("=== 可行性 ===")
	fmt.Println("约束系统可行：在已知时钟误差区间内存在一致的时间线。")
	fmt.Println()

	// 2. 总耗时范围（差分约束下的精确界）
	minSpan, maxSpan := spanRange(sys, in.SpanBegin, in.SpanEnd)
	if maxSpan < -eps {
		// 系统可行但终点被强制早于起点：定位约束链，而不是输出负耗时
		printForcedNegativeSpan(os.Stdout, sys, in.SpanBegin, in.SpanEnd, maxSpan)
		return 2
	}
	fmt.Println("=== 总耗时关键路径范围 ===")
	fmt.Printf("关键路径: %s -> %s\n", in.SpanBegin, in.SpanEnd)
	fmt.Printf("最短可能总耗时: %.3f\n", minSpan)
	fmt.Printf("最长可能总耗时: %.3f\n", maxSpan)
	if minSpan < 0 {
		fmt.Printf("注意: 约束未强制终点晚于起点，最短 %.3f 为负，说明缺少起止顺序约束。\n", minSpan)
	}
	fmt.Println()

	// 3. 各构造一条可实现解释
	a := sys.idx[in.SpanBegin]
	b := sys.idx[in.SpanEnd]

	minEdges := append(append([]cedge{}, sys.edges...),
		cedge{from: a, to: b, w: minSpan, desc: fmt.Sprintf("总耗时不超过最短界 %.3f", minSpan)})
	distMin, cycle := bellmanFord(sys.n(), minEdges)
	if cycle != nil {
		fmt.Fprintln(os.Stderr, "内部错误: 最短界不可达")
		return 1
	}
	timesMin := shiftedTimes(sys, distMin)
	printExplanation(os.Stdout, in, "最短解释", timesMin, minSpan)
	fmt.Println()

	maxEdges := append(append([]cedge{}, sys.edges...),
		cedge{from: b, to: a, w: -maxSpan, desc: fmt.Sprintf("总耗时不小于最长界 %.3f", maxSpan)})
	distMax, cycle := bellmanFord(sys.n(), maxEdges)
	if cycle != nil {
		fmt.Fprintln(os.Stderr, "内部错误: 最长界不可达")
		return 1
	}
	timesMax := shiftedTimes(sys, distMax)
	printExplanation(os.Stdout, in, "最长解释", timesMax, maxSpan)
	return 0
}

// shiftedTimes 把差分约束的解平移到超级源点为 0，使事件时刻落回其绝对时钟区间。
func shiftedTimes(sys *csystem, dist []float64) map[string]float64 {
	base := dist[sys.idx[srcName]]
	times := make(map[string]float64, len(sys.names))
	for i, name := range sys.names {
		times[name] = dist[i] - base
	}
	return times
}
