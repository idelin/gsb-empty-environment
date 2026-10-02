package main

import "testing"

// TestSchedulingOptimum 验收：最优排期违反恰好 2 个次要软约束，
// 总惩罚 3，且全部硬约束成立。
func TestSchedulingOptimum(t *testing.T) {
	p := NewSchedulingProblem()
	sol := Solve(p)

	if !sol.Feasible {
		t.Fatal("expected a feasible schedule")
	}

	// 硬约束绝对成立
	a := Assignment(sol.Values)
	for _, h := range p.Hard {
		if st := h.Eval(a); st == Violated {
			t.Errorf("hard constraint violated in solution: %s", h.Name)
		}
	}

	// 恰好违反 2 个软约束
	if len(sol.Violated) != 2 {
		t.Errorf("violated %d soft constraints (%v), want 2", len(sol.Violated), sol.Violated)
	}

	// 总惩罚全局最小 = 3（T5 必挤占 {0,1,2} 之一：
	// 挤 T1 罚 3+1，挤 T3/T4 罚 2 且邻接不可得再罚 1）
	if sol.Penalty != 3 {
		t.Errorf("penalty = %d, want 3", sol.Penalty)
	}

	// 被违反的必须是次要约束（权重 3 的 S1 不应被违反）
	for _, name := range sol.Violated {
		if name == "S1 T1 偏好 slot 0" {
			t.Errorf("important soft constraint S1 should not be violated: %v", sol.Violated)
		}
	}
}

// TestSchedulingMatchesBruteForce 分支定界结果与全空间暴力枚举一致。
func TestSchedulingMatchesBruteForce(t *testing.T) {
	p := NewSchedulingProblem()
	sol := Solve(p)
	want, ok := bruteForce(p)
	if !ok {
		t.Fatal("brute force found no feasible schedule")
	}
	if sol.Penalty != want {
		t.Fatalf("B&B penalty = %d, brute-force optimum = %d", sol.Penalty, want)
	}
}

// TestSchedulingPruning 剪枝必须显著压缩搜索空间（全空间 6^6 = 46656 个叶节点）。
func TestSchedulingPruning(t *testing.T) {
	p := NewSchedulingProblem()
	sol := Solve(p)
	full := 1
	for i := 0; i < numTasks; i++ {
		full *= numSlots
	}
	if sol.Nodes >= full {
		t.Errorf("visited %d nodes, want pruning well below full space %d", sol.Nodes, full)
	}
	if sol.Pruned == 0 {
		t.Error("expected some nodes to be pruned")
	}
	t.Logf("visited %d nodes, pruned %d (full space %d leaves)", sol.Nodes, sol.Pruned, full)
}
