// Command example runs the acceptance scenario of the temporal constraint
// solver: task A must finish before B starts, B partially overlaps C, and C
// must start before day 5 of the project.
package main

import (
	"fmt"

	"tempsched"
)

func main() {
	s := tempsched.NewSolver()
	s.AddTask("A", 2, 2) // task A: 2 days
	s.AddTask("B", 4, 4) // task B: 4 days
	s.AddTask("C", 3, 3) // task C: 3 days

	s.Constrain("A", "B", tempsched.Before)   // A must end before B starts
	s.Constrain("B", "C", tempsched.Overlaps) // B partially overlaps C
	s.ConstrainStartBefore("C", 5)            // C must start before day 5

	res := s.Solve()
	if !res.Consistent {
		fmt.Println("约束网络不相容：无可行排程")
		return
	}

	fmt.Println("=== 时态约束求解（Allen 区间代数 + STN）===")
	fmt.Println("约束: A Before B;  B Overlaps C;  C 开始 ≤ 第 5 天")
	fmt.Println("工期: A=2 天, B=4 天, C=3 天")
	fmt.Println()
	fmt.Println("传递闭包推断:")
	fmt.Printf("  A 与 C 的时态关系: %s\n", res.Network.Relation("A", "C"))
	fmt.Println()
	fmt.Println("各任务可行时间窗 (单位: 天):")
	for _, name := range []string{"A", "B", "C"} {
		w := res.Windows[name]
		fmt.Printf("  任务 %s: 开始 [%g, %g]  结束 [%g, %g]\n",
			name, w.EarliestStart, w.LatestStart, w.EarliestEnd, w.LatestEnd)
	}
	fmt.Println()
	fmt.Printf("任务 A 最迟结束时间: 第 %g 天\n", res.Windows["A"].LatestEnd)
	fmt.Printf("项目全局关键路径窗口: [第 %g 天, 第 %g 天]\n",
		res.EarliestFinish, res.LatestFinish)
}
