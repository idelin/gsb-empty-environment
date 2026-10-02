// Demo of the acceptance scenario:
//   - task A must end before task B        (A before B)
//   - B and C partially overlap            (B overlaps C)
//   - C must start before project day 5    (start(C) < 5)
// The solver propagates the temporal relations, derives A's latest end
// time, and computes the global critical-path window of the project.
package main

import (
	"fmt"
	"math"
	"strings"

	"temporal/allen"
	"temporal/sched"
)

func fmtTime(v float64) string {
	switch {
	case math.IsInf(v, 1):
		return "+inf"
	case math.IsInf(v, -1):
		return "-inf"
	case v == 0:
		return "0"
	default:
		return fmt.Sprintf("%g", v)
	}
}

func fmtWindow(w sched.Window) string {
	lo, hi := "[", "]"
	if w.EarliestExclusive || math.IsInf(w.Earliest, -1) {
		lo = "("
	}
	if w.LatestExclusive || math.IsInf(w.Latest, 1) {
		hi = ")"
	}
	return fmt.Sprintf("%s%4s, %4s%s", lo, fmtTime(w.Earliest), fmtTime(w.Latest), hi)
}

func main() {
	s := sched.NewSolver()
	a := s.AddTask("A", 2, 2)
	b := s.AddTask("B", 3, 3)
	c := s.AddTask("C", 4, 4)
	s.AddRelation(a, b, allen.Before)   // A 必须在 B 之前结束
	s.AddRelation(b, c, allen.Overlaps) // B 与 C 部分重叠
	s.MustStartBefore(c, 5)             // C 必须在工期第 5 天前开始

	fmt.Println("=== Allen 时间区间代数约束求解器：排程演示 ===")
	fmt.Println()
	fmt.Println("任务（工期，单位：天）:")
	for _, name := range []string{"A: 2 天", "B: 3 天", "C: 4 天"} {
		fmt.Println("  " + name)
	}
	fmt.Println()
	fmt.Println("时态约束:")
	fmt.Println("  A before B        (A 必须在 B 之前结束)")
	fmt.Println("  B overlaps C      (B 与 C 部分重叠)")
	fmt.Println("  start(C) < 5      (C 必须在工期第 5 天前开始)")

	res, err := s.Solve()
	if err != nil {
		fmt.Println("约束不一致:", err)
		return
	}

	fmt.Println()
	fmt.Println("传播后的导出关系（传递闭包）:")
	names := []string{"A", "B", "C"}
	for i := 0; i < 3; i++ {
		for j := i + 1; j < 3; j++ {
			r := s.Network().Relation(i, j)
			fmt.Printf("  %s -> %s : %-3s (%s)\n", names[i], names[j], r, r.Name())
		}
	}

	fmt.Println()
	fmt.Println("各任务可行时间窗（项目第 0 天开始）:")
	for _, tw := range res.Tasks {
		fmt.Printf("  %s  开始 %s   结束 %s\n",
			tw.Name, fmtWindow(tw.Start), fmtWindow(tw.End))
	}

	fmt.Println()
	end := res.Tasks[a].End
	fmt.Printf("任务 A 最迟结束时间: 第 %s 天%s\n", fmtTime(end.Latest),
		map[bool]string{true: " 之前（开区间）", false: "（含）"}[end.LatestExclusive])
	fmt.Printf("项目全局关键路径窗口: 项目结束时间 ∈ %s\n", fmtWindow(res.Project))
	fmt.Printf("关键路径: %s\n", strings.Join(res.CriticalPath, " -> "))
}
