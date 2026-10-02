package main

import "fmt"

// 任务排期场景：
//   6 个任务 T1..T6 争夺同一间会议室的 6 个时段（slot 0..5），
//   每个任务恰好占一个时段。
//
// 硬约束（绝对成立，违反即剪枝）：
//   H1 资源互斥：任意两个任务不能占用同一时段
//   H2 先后依赖：T1 必须排在 T2 之前
//   H3 可用窗口：T5 只能排在上午时段（slot <= 2）
//
// 软约束（违反时计入惩罚权重）：
//   S1 T1 希望在 slot 0 开工            权重 3（重要）
//   S2 T3 希望在 slot 1                 权重 2（次要）
//   S3 T4 希望在 slot 2                 权重 2（次要）
//   S4 T6 希望在 slot 3                 权重 1（次要）
//   S5 T2 与 T5 希望相邻（|差值| == 1） 权重 1（次要）
//
// 由于 H1 要求 6 个任务占满 6 个不同时段，而 H3 把 T5 限制在
// {0,1,2}，T5 必然挤占 T1/T3/T4 中某一个的偏好时段，全部软约束
// 无法同时满足——这正是 Max-CSP 的典型情形。

const (
	numTasks = 6
	numSlots = 6
)

// 任务下标
const (
	T1 = iota
	T2
	T3
	T4
	T5
	T6
)

// NewSchedulingProblem 构造上述排期 Max-CSP 问题。
func NewSchedulingProblem() *Problem {
	varNames := []string{"T1", "T2", "T3", "T4", "T5", "T6"}
	domains := make([][]int, numTasks)
	for i := range domains {
		domains[i] = make([]int, numSlots)
		for s := 0; s < numSlots; s++ {
			domains[i][s] = s
		}
	}

	preferSlot := func(task int) func(Assignment) Status {
		return func(a Assignment) Status {
			if a[task] == -1 {
				return Undecided
			}
			if a[task] == preferredSlot[task] {
				return Satisfied
			}
			return Violated
		}
	}

	hard := []HardConstraint{
		{
			Name: "H1 资源互斥：任意两个任务不得同时段",
			Eval: func(a Assignment) Status {
				seen := make(map[int]bool, len(a))
				for _, v := range a {
					if v == -1 {
						continue
					}
					if seen[v] {
						return Violated
					}
					seen[v] = true
				}
				return Satisfied
			},
		},
		{
			Name: "H2 先后依赖：T1 必须早于 T2",
			Eval: func(a Assignment) Status {
				if a[T1] == -1 || a[T2] == -1 {
					return Undecided
				}
				if a[T1] < a[T2] {
					return Satisfied
				}
				return Violated
			},
		},
		{
			Name: "H3 可用窗口：T5 必须在上午（slot <= 2）",
			Eval: func(a Assignment) Status {
				if a[T5] == -1 {
					return Undecided
				}
				if a[T5] <= 2 {
					return Satisfied
				}
				return Violated
			},
		},
	}

	soft := []SoftConstraint{
		{Name: "S1 T1 偏好 slot 0", Weight: 3, Eval: preferSlot(T1)},
		{Name: "S2 T3 偏好 slot 1", Weight: 2, Eval: preferSlot(T3)},
		{Name: "S3 T4 偏好 slot 2", Weight: 2, Eval: preferSlot(T4)},
		{Name: "S4 T6 偏好 slot 3", Weight: 1, Eval: preferSlot(T6)},
		{
			Name:   "S5 T2 与 T5 相邻",
			Weight: 1,
			Eval: func(a Assignment) Status {
				if a[T2] == -1 || a[T5] == -1 {
					return Undecided
				}
				d := a[T2] - a[T5]
				if d < 0 {
					d = -d
				}
				if d == 1 {
					return Satisfied
				}
				return Violated
			},
		},
	}

	return &Problem{VarNames: varNames, Domains: domains, Hard: hard, Soft: soft}
}

// preferredSlot 各任务的偏好时段（S1..S4 使用）。
var preferredSlot = map[int]int{T1: 0, T3: 1, T4: 2, T6: 3}

// DescribeSolution 把解格式化为按时段排序的排期表。
func DescribeSolution(p *Problem, sol Solution) string {
	out := ""
	bySlot := make([]string, numSlots)
	for i, name := range p.VarNames {
		bySlot[sol.Values[i]] = name
	}
	for s, name := range bySlot {
		out += fmt.Sprintf("  slot %d -> %s\n", s, name)
	}
	return out
}
