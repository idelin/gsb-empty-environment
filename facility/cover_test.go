package facility

import (
	"math/rand"
	"testing"
)

// 手工构造:重叠覆盖不得重复计分;子设施必须带动祖先;同收益取低费用。
func TestSolveBasic(t *testing.T) {
	// 设施树: 0 为根,1、2 是 0 的子节点,3 是 1 的子节点。
	in := &Instance{
		Costs:   []int64{3, 2, 2, 1},
		Parents: []int{-1, 0, 0, 1},
		Profits: []int64{5, 5, 4},
		// 需求点 0 同时被设施 1、2 覆盖(重叠,只计一次)。
		Covers: [][]int{
			{1, 2},
			{2},
			{3},
		},
		Budget: 6,
	}
	sol, err := in.Solve()
	if err != nil {
		t.Fatal(err)
	}
	if err := in.Verify(sol); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if err := in.CrossCheck(sol); err != nil {
		t.Fatalf("CrossCheck: %v", err)
	}
	// 开 {0,1,2}: 费用 7 超预算;开 {0,2}: 费用 5,收益 10(点 0、1)。
	// 开 {0,1,3}: 费用 6,收益 9(点 0、2)。最优为 {0,2},收益 10。
	if sol.Profit != 10 {
		t.Fatalf("收益 = %d, 期望 10", sol.Profit)
	}
	if sol.Cost != 5 {
		t.Fatalf("费用 = %d, 期望 5", sol.Cost)
	}
	if !sol.Opened[0] || !sol.Opened[2] || sol.Opened[1] || sol.Opened[3] {
		t.Fatalf("开启集合不符: %v", sol.Opened)
	}
}

// 重叠覆盖:一个需求点被多个已开设施覆盖,收益只计一次。
func TestNoDoubleCount(t *testing.T) {
	in := &Instance{
		Costs:   []int64{1, 1},
		Parents: []int{-1, -1},
		Profits: []int64{7},
		Covers:  [][]int{{0, 1}}, // 两个设施都覆盖需求点 0
		Budget:  2,
	}
	sol, err := in.Solve()
	if err != nil {
		t.Fatal(err)
	}
	if sol.Profit != 7 {
		t.Fatalf("重叠覆盖被重复计分: 收益 = %d, 期望 7", sol.Profit)
	}
	if err := in.Verify(sol); err != nil {
		t.Fatal(err)
	}
}

// 同收益取更低费用。
func TestTieBreakCost(t *testing.T) {
	in := &Instance{
		Costs:   []int64{5, 2},
		Parents: []int{-1, -1},
		Profits: []int64{3},
		Covers:  [][]int{{0, 1}},
		Budget:  10,
	}
	sol, err := in.Solve()
	if err != nil {
		t.Fatal(err)
	}
	if sol.Profit != 3 || sol.Cost != 2 {
		t.Fatalf("收益/费用 = %d/%d, 期望 3/2", sol.Profit, sol.Cost)
	}
	if !sol.Opened[1] || sol.Opened[0] {
		t.Fatalf("应开启设施 1: %v", sol.Opened)
	}
}

// 随机实例:Solve 与独立子集穷举复核一致,且 Verify 通过。
func TestSolveRandomCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 30; trial++ {
		n := 1 + rng.Intn(12) // 1..12 个设施
		m := 1 + rng.Intn(20) // 1..20 个需求点
		in := &Instance{
			Costs:   make([]int64, n),
			Parents: make([]int, n),
			Profits: make([]int64, m),
			Covers:  make([][]int, m),
		}
		for i := 0; i < n; i++ {
			in.Costs[i] = int64(rng.Intn(8))
			if i > 0 && rng.Intn(4) > 0 {
				in.Parents[i] = rng.Intn(i) // 随机森林
			} else {
				in.Parents[i] = -1
			}
		}
		for j := 0; j < m; j++ {
			in.Profits[j] = int64(rng.Intn(10))
			k := rng.Intn(4) // 0..3 个覆盖设施,制造重叠
			for c := 0; c < k; c++ {
				in.Covers[j] = append(in.Covers[j], rng.Intn(n))
			}
		}
		in.Budget = int64(rng.Intn(20))
		sol, err := in.Solve()
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		if err := in.Verify(sol); err != nil {
			t.Fatalf("trial %d Verify: %v", trial, err)
		}
		if err := in.CrossCheck(sol); err != nil {
			t.Fatalf("trial %d CrossCheck: %v", trial, err)
		}
	}
}

// 上限规模冒烟:n = 20 时求解应快速完成。
func TestSolveMaxScale(t *testing.T) {
	n := 20
	in := &Instance{
		Costs:   make([]int64, n),
		Parents: make([]int, n),
		Profits: make([]int64, 30),
		Covers:  make([][]int, 30),
	}
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < n; i++ {
		in.Costs[i] = int64(1 + rng.Intn(5))
		if i > 0 {
			in.Parents[i] = rng.Intn(i)
		} else {
			in.Parents[i] = -1
		}
	}
	for j := range in.Profits {
		in.Profits[j] = int64(1 + rng.Intn(9))
		for c := 0; c < 3; c++ {
			in.Covers[j] = append(in.Covers[j], rng.Intn(n))
		}
	}
	in.Budget = 25
	sol, err := in.Solve()
	if err != nil {
		t.Fatal(err)
	}
	if err := in.Verify(sol); err != nil {
		t.Fatal(err)
	}
}
