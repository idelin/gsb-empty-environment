package main

import "fmt"

func main() {
	p := NewSchedulingProblem()

	fmt.Println("=== 带权 Max-CSP 分支定界求解器：任务排期 ===")
	fmt.Println()
	fmt.Println("硬约束（必须全部满足）:")
	for _, h := range p.Hard {
		fmt.Println("  " + h.Name)
	}
	fmt.Println("软约束（违反计入惩罚）:")
	for _, s := range p.Soft {
		fmt.Printf("  %-22s 权重 %d\n", s.Name, s.Weight)
	}
	fmt.Println()

	sol := Solve(p)
	if !sol.Feasible {
		fmt.Println("无可行解：硬约束无法同时满足")
		return
	}

	fmt.Println("最优排期表:")
	fmt.Print(DescribeSolution(p, sol))
	fmt.Println()
	fmt.Printf("被违反的软约束（%d 个）:\n", len(sol.Violated))
	for _, name := range sol.Violated {
		fmt.Println("  " + name)
	}
	fmt.Println()
	fmt.Printf("总惩罚（全局最小）: %d / 软约束权重总和 %d\n", sol.Penalty, TotalSoftWeight(p))
	fmt.Printf("分支定界统计: 访问节点 %d，剪枝节点 %d（全空间 %d 叶节点）\n",
		sol.Nodes, sol.Pruned, pow(numSlots, numTasks))
}

func pow(base, exp int) int {
	r := 1
	for i := 0; i < exp; i++ {
		r *= base
	}
	return r
}
