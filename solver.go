package main

import "math"

// Status 是约束在（部分）赋值下的三值求值结果。
type Status int

const (
	Undecided Status = iota // 尚未被当前部分赋值完全决定
	Satisfied               // 已满足
	Violated                // 已被违反（无论剩余变量如何取值都无法挽回）
)

// Assignment 按变量下标存放所选域值，-1 表示尚未赋值。
type Assignment []int

// SoftConstraint 软约束：违反时产生 Weight 的惩罚。
// Eval 必须对部分赋值安全：只有当已赋值变量足以判定违反时才返回 Violated。
type SoftConstraint struct {
	Name   string
	Weight int
	Eval   func(a Assignment) Status
}

// HardConstraint 硬约束：任何可行解都必须满足，违反即剪枝。
type HardConstraint struct {
	Name string
	Eval func(a Assignment) Status
}

// Problem 带权 Max-CSP 问题定义。
type Problem struct {
	VarNames []string
	Domains  [][]int
	Hard     []HardConstraint
	Soft     []SoftConstraint
}

// Solution 求解结果。
type Solution struct {
	Feasible bool     // 是否存在满足全部硬约束的赋值
	Values   []int    // 每个变量选中的域值
	Penalty  int      // 被违反软约束的权重之和（全局最小）
	Violated []string // 被违反的软约束名称
	Nodes    int      // 分支定界访问的节点数
	Pruned   int      // 被剪枝的节点数
}

// Solve 使用分支定界求解加权违反代价最小的赋值。
//
// 下界：当前部分赋值下已被确定违反的软约束权重之和。
// 由于“违反”对赋值扩展是单调的，该下界是可采纳的，
// 因此剪枝（下界 >= 当前最优）不会丢失全局最优解。
func Solve(p *Problem) Solution {
	n := len(p.VarNames)
	a := make(Assignment, n)
	for i := range a {
		a[i] = -1
	}

	best := Solution{Penalty: math.MaxInt}
	var bestValues []int

	var dfs func(depth, lowerBound int)
	dfs = func(depth, lowerBound int) {
		best.Nodes++
		if lowerBound >= best.Penalty {
			best.Pruned++
			return
		}
		if depth == n {
			best.Penalty = lowerBound
			bestValues = append([]int(nil), a...)
			return
		}
		for _, v := range p.Domains[depth] {
			a[depth] = v

			hardOK := true
			for _, h := range p.Hard {
				if h.Eval(a) == Violated {
					hardOK = false
					break
				}
			}
			if hardOK {
				penalty := 0
				for _, s := range p.Soft {
					if s.Eval(a) == Violated {
						penalty += s.Weight
					}
				}
				dfs(depth+1, penalty)
			} else {
				best.Pruned++
			}
			a[depth] = -1
		}
	}
	dfs(0, 0)

	if bestValues == nil {
		return best // Feasible = false
	}
	best.Feasible = true
	best.Values = bestValues
	for _, s := range p.Soft {
		if s.Eval(bestValues) == Violated {
			best.Violated = append(best.Violated, s.Name)
		}
	}
	return best
}

// TotalSoftWeight 返回所有软约束权重之和，用于展示。
func TotalSoftWeight(p *Problem) int {
	total := 0
	for _, s := range p.Soft {
		total += s.Weight
	}
	return total
}
