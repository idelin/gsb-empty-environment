package scheduler

import (
	"math/rand"
	"reflect"
	"testing"
)

func TestSolveNegativeProfits(t *testing.T) {
	p := Problem{Experiments: []Experiment{
		{ID: 1, Device: 0, Slot: 0, Profit: -3},
		{ID: 2, Device: 0, Slot: 5, Profit: -1},
	}}
	if got := Solve(p); len(got) != 0 {
		t.Fatalf("全负收益应选空集, 得到 %v", got)
	}
}

func TestSolveCooldown(t *testing.T) {
	// 同设备时隙 0,1,2，冷却 1：0 与 2 兼容，0 与 1、1 与 2 不兼容。
	p := Problem{Cooldown: 1, Experiments: []Experiment{
		{ID: 10, Device: 0, Slot: 0, Profit: 5},
		{ID: 11, Device: 0, Slot: 1, Profit: 6},
		{ID: 12, Device: 0, Slot: 2, Profit: 5},
	}}
	if got := Solve(p); !reflect.DeepEqual(got, []int{10, 12}) {
		t.Fatalf("冷却约束应选 {10,12}, 得到 %v", got)
	}
}

func TestSolveMutexGroup(t *testing.T) {
	p := Problem{Experiments: []Experiment{
		{ID: 1, Device: 0, Slot: 0, Profit: 4, Group: 7},
		{ID: 2, Device: 1, Slot: 0, Profit: 5, Group: 7},
		{ID: 3, Device: 2, Slot: 0, Profit: 1},
	}}
	if got := Solve(p); !reflect.DeepEqual(got, []int{2, 3}) {
		t.Fatalf("互斥组应选收益高的 {2,3}, 得到 %v", got)
	}
}

func TestSolveTieBreak(t *testing.T) {
	// 收益并列 (2) 时选更少实验：{9} 一个实验优于 {1,2} 两个。
	p := Problem{Experiments: []Experiment{
		{ID: 9, Device: 0, Slot: 0, Profit: 2, Group: 1},
		{ID: 1, Device: 0, Slot: 0, Profit: 1},
		{ID: 2, Device: 1, Slot: 0, Profit: 1, Group: 1},
	}}
	if got := Solve(p); !reflect.DeepEqual(got, []int{9}) {
		t.Fatalf("收益并列应选更少实验 {9}, 得到 %v", got)
	}
	// 收益与数量都并列时按 id 列表字典序：[2] 优于 [5]。
	p2 := Problem{Experiments: []Experiment{
		{ID: 5, Device: 0, Slot: 0, Profit: 2, Group: 1},
		{ID: 2, Device: 1, Slot: 0, Profit: 2, Group: 1},
	}}
	if got := Solve(p2); !reflect.DeepEqual(got, []int{2}) {
		t.Fatalf("数量并列应选 id 更小者 {2}, 得到 %v", got)
	}
}

// oracleSolve 独立预言机：递归穷举 + 逐对检查（与 Solve 不同代码路径）。
func oracleSolve(p Problem) []int {
	n := len(p.Experiments)
	bestProfit, bestCount := 0, 0
	var bestIDs []int
	var rec func(i int, sel []int, profit int)
	rec = func(i int, sel []int, profit int) {
		if i == n {
			if !LegalSubset(p.Experiments, p.Cooldown, sel) {
				return
			}
			ids := sortedIDs(p.Experiments, sel)
			if better(profit, len(sel), ids, bestProfit, bestCount, bestIDs) {
				bestProfit, bestCount, bestIDs = profit, len(sel), ids
			}
			return
		}
		rec(i+1, sel, profit)
		rec(i+1, append(append([]int(nil), sel...), i), profit+p.Experiments[i].Profit)
	}
	rec(0, nil, 0)
	return bestIDs
}

func TestSolveCrossCheckRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 200; trial++ {
		n := 1 + rng.Intn(12)
		p := Problem{Cooldown: rng.Intn(3)}
		for i := 0; i < n; i++ {
			g := 0
			if rng.Intn(3) == 0 {
				g = 1 + rng.Intn(3)
			}
			p.Experiments = append(p.Experiments, Experiment{
				ID:     100 + i,
				Device: rng.Intn(3),
				Slot:   rng.Intn(8),
				Profit: rng.Intn(21) - 10,
				Group:  g,
			})
		}
		got := Solve(p)
		want := oracleSolve(p)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("trial %d: Solve=%v oracle=%v\n问题: %+v", trial, got, want, p)
		}
	}
}

// TestReplayAgreesWithPairwise 交叉核对两种合法性检查。
func TestReplayAgreesWithPairwise(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 500; trial++ {
		n := 1 + rng.Intn(10)
		exps := make([]Experiment, n)
		for i := range exps {
			g := 0
			if rng.Intn(3) == 0 {
				g = 1 + rng.Intn(3)
			}
			exps[i] = Experiment{ID: i, Device: rng.Intn(3), Slot: rng.Intn(6),
				Profit: rng.Intn(11) - 5, Group: g}
		}
		cd := rng.Intn(3)
		var sel []int
		for i := 0; i < n; i++ {
			if rng.Intn(2) == 0 {
				sel = append(sel, i)
			}
		}
		if LegalSubset(exps, cd, sel) != ReplayLegal(exps, cd, sel) {
			t.Fatalf("trial %d: 两种合法性检查不一致, sel=%v exps=%+v c=%d",
				trial, sel, exps, cd)
		}
	}
}
