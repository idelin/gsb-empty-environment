package main

import "fmt"

func main() {
	fmt.Println("=== 场景 1：验收边界 —— 4 个变量共享值域 {1, 2, 3} ===")
	vars := []*Variable{
		NewVariable("X1", 1, 2, 3),
		NewVariable("X2", 1, 2, 3),
		NewVariable("X3", 1, 2, 3),
		NewVariable("X4", 1, 2, 3),
	}
	for _, v := range vars {
		fmt.Println(" ", v)
	}
	c := NewAllDifferent(vars...)
	if !c.Propagate() {
		fmt.Println("=> 匹配阶段判定：最大匹配无法覆盖全部 4 个变量（值域只有 3 个值）")
		fmt.Println("=> 搜索开启前瞬时报告：无解 (UNSAT)，无需任何回溯搜索")
	} else {
		fmt.Println("=> 可满足")
	}

	fmt.Println()
	fmt.Println("=== 场景 2：可行但可过滤 —— Régin 剔除不可能的值 ===")
	vars2 := []*Variable{
		NewVariable("X1", 1, 2),
		NewVariable("X2", 1, 2),
		NewVariable("X3", 1, 2, 3),
	}
	for _, v := range vars2 {
		fmt.Println("  过滤前:", v)
	}
	c2 := NewAllDifferent(vars2...)
	if c2.Propagate() {
		for _, v := range vars2 {
			fmt.Println("  过滤后:", v)
		}
		fmt.Println("=> X3 的值 1、2 不可能出现在任何可行解中，已被精准剔除")
	}
}
