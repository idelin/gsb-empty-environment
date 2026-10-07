// facilitydemo 演示设施覆盖求解库的两个问题:
//  1. 确定性问题:整数预算内最大化覆盖收益,再最小化费用,输出开启闭包;
//  2. 两阶段问题:一阶段开启固定,情景下按观测组启用应急设施,
//     最大化最坏收益、再最小化期望费用,输出策略树并逐情景检查预算。
package main

import (
	"fmt"

	"facility/facility"
)

func main() {
	demoCover()
	demoRobust()
}

func demoCover() {
	fmt.Println("=== 问题一:确定性覆盖(<=20 设施,整数预算) ===")
	// 设施树: 0 为根;1、2 为 0 的子节点;3 为 1 的子节点;4 为 2 的子节点。
	in := &facility.Instance{
		Costs:   []int64{4, 3, 3, 1, 2},
		Parents: []int{-1, 0, 0, 1, 2},
		Profits: []int64{8, 6, 6, 5, 4, 3},
		Covers: [][]int{
			{1, 2}, // 需求点 0 被设施 1、2 重叠覆盖,只计一次
			{1},    // 需求点 1
			{2},    // 需求点 2
			{3},    // 需求点 3
			{4},    // 需求点 4
			{3, 4}, // 需求点 5 被设施 3、4 重叠覆盖
		},
		Budget: 9,
	}
	sol, err := in.Solve()
	must(err)
	must(in.Verify(sol))
	must(in.CrossCheck(sol))
	var opened []int
	for i, o := range sol.Opened {
		if o {
			opened = append(opened, i)
		}
	}
	fmt.Printf("开启闭包: %v\n", opened)
	fmt.Printf("覆盖收益: %d, 开启费用: %d (预算 %d)\n", sol.Profit, sol.Cost, in.Budget)
	fmt.Println("子集穷举复核: 通过(重叠覆盖未重复计分)")
	fmt.Println()
}

func demoRobust() {
	fmt.Println("=== 问题二:两阶段鲁棒覆盖(<=5 情景,<=12 设施) ===")
	// 设施树: 0 为根;1、2 为 0 的子节点;3 为 2 的子节点;4 独立根。
	in := &facility.RobustInstance{
		Costs:      []int64{3, 2, 2, 1, 5},
		EmergCosts: []int64{4, 2, 2, 1, 6},
		Parents:    []int{-1, 0, 0, 2, -1},
		Profits:    []int64{7, 6, 5, 4},
		Budget:     7,
		EmergLimit: 2,
		Scenarios: []facility.Scenario{
			{Name: "s0", Prob: 0.4, Group: 0, Covers: [][]int{{1}, {2}, {3}, {4}}},
			{Name: "s1", Prob: 0.3, Group: 0, Covers: [][]int{{2}, {1}, {4}, {3}}},
			{Name: "s2", Prob: 0.2, Group: 1, Covers: [][]int{{1, 4}, {3}, {2}, {1}}},
			{Name: "s3", Prob: 0.1, Group: 1, Covers: [][]int{{4}, {2, 4}, {1}, {3}}},
		},
	}
	pol, err := in.SolveRobust()
	must(err)
	must(in.VerifyPolicy(pol))
	must(in.CrossCheckRobust(pol))
	fmt.Print(in.RenderPolicyTree(pol))
	fmt.Println("逐情景预算检查:")
	for _, c := range in.CheckScenarios(pol) {
		status := "OK"
		if !c.BudgetOK {
			status = "超预算!"
		}
		fmt.Printf("  情景 %-3s 组 %d: 收益 %2d, 费用 %d/%d, 应急设施 %d 个 [%s]\n",
			c.Name, c.Group, c.Profit, c.TotalCost, c.Budget, c.EmergCount, status)
	}
	fmt.Println("暴力枚举复核: 通过")
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
