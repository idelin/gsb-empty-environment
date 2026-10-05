package main

import (
	"math/rand"
	"reflect"
	"testing"
)

func makeGraph(t *testing.T, edges []RawEdge) (*Graph, map[string]int) {
	t.Helper()
	g, index, err := NewGraph(edges)
	if err != nil {
		t.Fatalf("建图失败: %v", err)
	}
	return g, index
}

func checkSolution(t *testing.T, sol Solution, cost int64, risk int, path []int) {
	t.Helper()
	if !sol.Found {
		t.Fatalf("期望有解，实际无解")
	}
	if sol.Cost != cost || sol.Risk != risk || !reflect.DeepEqual(sol.Path, path) {
		t.Fatalf("解不符: 得到 cost=%d risk=%d path=%v, 期望 cost=%d risk=%d path=%v",
			sol.Cost, sol.Risk, sol.Path, cost, risk, path)
	}
}

// 先贵后省：到 a 的便宜路径(费1,险6)吃光风险预算，必须先用贵的(费5,险1)，
// 才能接上便宜的后段。每点只留一个距离的算法会在这里失败。
func TestExpensiveIntermediateNecessary(t *testing.T) {
	g, idx := makeGraph(t, []RawEdge{
		{ID: 1, From: "s", To: "a", Cost: 1, Risk: 6},
		{ID: 2, From: "s", To: "a", Cost: 5, Risk: 1},
		{ID: 3, From: "a", To: "t", Cost: 1, Risk: 5},
	})
	sol, labels := solve(g, idx["s"], idx["t"], 6)
	checkSolution(t, sol, 6, 6, []int{2, 3})

	// a 点必须同时保留两个非支配标签：(费1,险6) 与 (费5,险1)
	if got := len(labels[idx["a"]]); got != 2 {
		t.Fatalf("a 点非支配标签数 = %d, 期望 2", got)
	}

	ref := referenceSolve(g, idx["s"], idx["t"], 6)
	if !reflect.DeepEqual(sol, ref) {
		t.Fatalf("与参照实现不一致: solve=%+v reference=%+v", sol, ref)
	}
}

// 零费用零风险环：必须终止，且环上的兜圈被“同价同险边更多”支配掉。
func TestZeroCostCycle(t *testing.T) {
	g, idx := makeGraph(t, []RawEdge{
		{ID: 1, From: "s", To: "x", Cost: 2, Risk: 1},
		{ID: 2, From: "x", To: "y", Cost: 0, Risk: 0},
		{ID: 3, From: "y", To: "x", Cost: 0, Risk: 0},
		{ID: 4, From: "y", To: "t", Cost: 1, Risk: 1},
	})
	sol, _ := solve(g, idx["s"], idx["t"], 2)
	checkSolution(t, sol, 3, 2, []int{1, 2, 4})

	ref := referenceSolve(g, idx["s"], idx["t"], 2)
	if !reflect.DeepEqual(sol, ref) {
		t.Fatalf("与参照实现不一致: solve=%+v reference=%+v", sol, ref)
	}
}

// 零费用但带风险的环：风险顶到预算后必须停止绕圈。
func TestZeroCostRiskyCycle(t *testing.T) {
	g, idx := makeGraph(t, []RawEdge{
		{ID: 1, From: "s", To: "x", Cost: 0, Risk: 1},
		{ID: 2, From: "x", To: "x", Cost: 0, Risk: 1},
		{ID: 3, From: "x", To: "t", Cost: 1, Risk: 0},
	})
	sol, _ := solve(g, idx["s"], idx["t"], 3)
	checkSolution(t, sol, 1, 1, []int{1, 3})

	ref := referenceSolve(g, idx["s"], idx["t"], 3)
	if !reflect.DeepEqual(sol, ref) {
		t.Fatalf("与参照实现不一致: solve=%+v reference=%+v", sol, ref)
	}
}

// 同价先少边：直达边 ID 更大，但边数少，应选直达。
func TestTieBreakFewerHops(t *testing.T) {
	g, idx := makeGraph(t, []RawEdge{
		{ID: 9, From: "s", To: "t", Cost: 5, Risk: 1},
		{ID: 1, From: "s", To: "u", Cost: 2, Risk: 0},
		{ID: 2, From: "u", To: "t", Cost: 3, Risk: 0},
	})
	sol, _ := solve(g, idx["s"], idx["t"], 5)
	checkSolution(t, sol, 5, 1, []int{9})
}

// 同价同边数按边 ID 字典序：[1,3] 优于 [2,4]。
func TestTieBreakEdgeIDOrder(t *testing.T) {
	g, idx := makeGraph(t, []RawEdge{
		{ID: 1, From: "s", To: "a", Cost: 1, Risk: 0},
		{ID: 2, From: "s", To: "b", Cost: 1, Risk: 0},
		{ID: 3, From: "a", To: "t", Cost: 1, Risk: 0},
		{ID: 4, From: "b", To: "t", Cost: 1, Risk: 0},
	})
	sol, _ := solve(g, idx["s"], idx["t"], 0)
	checkSolution(t, sol, 2, 0, []int{1, 3})
}

// 预算不足时报告无解。
func TestNoPathWithinBudget(t *testing.T) {
	g, idx := makeGraph(t, []RawEdge{
		{ID: 1, From: "s", To: "t", Cost: 1, Risk: 2},
	})
	sol, _ := solve(g, idx["s"], idx["t"], 1)
	if sol.Found {
		t.Fatalf("期望无解，实际得到 %+v", sol)
	}
}

// 源点即终点：空路径，费用 0。
func TestSourceEqualsTarget(t *testing.T) {
	g, idx := makeGraph(t, []RawEdge{
		{ID: 1, From: "s", To: "s", Cost: 0, Risk: 0},
	})
	sol, _ := solve(g, idx["s"], idx["s"], 0)
	checkSolution(t, sol, 0, 0, nil)
}

// 随机小图上与状态展开参照实现交叉验证。
func TestRandomizedCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for iter := 0; iter < 500; iter++ {
		n := 2 + rng.Intn(7)
		m := 1 + rng.Intn(3*n)
		ids := rng.Perm(m)
		raw := make([]RawEdge, m)
		for i := 0; i < m; i++ {
			from := rng.Intn(n)
			to := rng.Intn(n)
			raw[i] = RawEdge{
				ID:   ids[i] + 1,
				From: nodeName(from),
				To:   nodeName(to),
				Cost: int64(rng.Intn(10)),
				Risk: rng.Intn(5),
			}
		}
		g, index, err := NewGraph(raw)
		if err != nil {
			t.Fatalf("iter %d: 建图失败: %v", iter, err)
		}
		src, ok1 := index[nodeName(0)]
		dst, ok2 := index[nodeName(n-1)]
		if !ok1 || !ok2 {
			continue // 端点未出现在任何边上，跳过
		}
		budget := rng.Intn(12)
		got, _ := solve(g, src, dst, budget)
		want := referenceSolve(g, src, dst, budget)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("iter %d 不一致 (budget=%d):\n solve=%+v\n reference=%+v\n edges=%+v",
				iter, budget, got, want, raw)
		}
	}
}

func nodeName(i int) string {
	return string(rune('a' + i))
}
