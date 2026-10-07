package facility

import (
	"math/rand"
	"strings"
	"testing"
)

// 手工构造的两阶段问题,验证最坏收益最大、期望费用最小与策略树输出。
func TestRobustBasic(t *testing.T) {
	// 设施: 0 根,1 是 0 的子节点,2 独立根。
	in := &RobustInstance{
		Costs:      []int64{2, 1, 4},
		EmergCosts: []int64{3, 1, 5},
		Parents:    []int{-1, 0, -1},
		Profits:    []int64{6, 6},
		Budget:     4,
		EmergLimit: 1,
		Scenarios: []Scenario{
			{Name: "s0", Prob: 0.5, Group: 0, Covers: [][]int{{0}, {1}}},
			{Name: "s1", Prob: 0.5, Group: 0, Covers: [][]int{{1}, {0}}},
		},
	}
	pol, err := in.SolveRobust()
	if err != nil {
		t.Fatal(err)
	}
	if err := in.VerifyPolicy(pol); err != nil {
		t.Fatalf("VerifyPolicy: %v", err)
	}
	if err := in.CrossCheckRobust(pol); err != nil {
		t.Fatalf("CrossCheckRobust: %v", err)
	}
	// 一阶段开 {0,1} 费用 3,两情景收益均为 12,最坏 12。
	if pol.WorstProfit != 12 {
		t.Fatalf("最坏收益 = %d, 期望 12", pol.WorstProfit)
	}
	if pol.ExpectedCost != 3 {
		t.Fatalf("期望费用 = %v, 期望 3", pol.ExpectedCost)
	}
	tree := in.RenderPolicyTree(pol)
	if !strings.Contains(tree, "观测组 0") || !strings.Contains(tree, "最坏收益 12") {
		t.Fatalf("策略树输出异常:\n%s", tree)
	}
	for _, c := range in.CheckScenarios(pol) {
		if !c.BudgetOK {
			t.Fatalf("情景 %s 超预算", c.Name)
		}
	}
}

// 应急动作受观测组约束:同组情景必须采取相同应急集。
func TestRobustGroupConstraint(t *testing.T) {
	// 组 0 含两个情景:s0 中只有设施 1 覆盖需求点,s1 中只有设施 2 覆盖。
	// 应急上限 1,同组只能选一种应急,故最坏收益至多为覆盖一个需求点。
	// 一阶段费用高昂,设施 1、2 只能作为应急启用。
	in := &RobustInstance{
		Costs:      []int64{0, 5, 5},
		EmergCosts: []int64{9, 1, 1},
		Parents:    []int{-1, -1, -1},
		Profits:    []int64{5},
		Budget:     1,
		EmergLimit: 1,
		Scenarios: []Scenario{
			{Name: "s0", Prob: 0.5, Group: 0, Covers: [][]int{{1}}},
			{Name: "s1", Prob: 0.5, Group: 0, Covers: [][]int{{2}}},
		},
	}
	pol, err := in.SolveRobust()
	if err != nil {
		t.Fatal(err)
	}
	if pol.WorstProfit != 0 {
		t.Fatalf("同组同动作约束失效: 最坏收益 = %d, 期望 0", pol.WorstProfit)
	}
	// 若两情景分组,则各自动作可分别覆盖,最坏收益为 5。
	in.Scenarios[1].Group = 1
	pol2, err := in.SolveRobust()
	if err != nil {
		t.Fatal(err)
	}
	if pol2.WorstProfit != 5 {
		t.Fatalf("分组后最坏收益 = %d, 期望 5", pol2.WorstProfit)
	}
	if err := in.VerifyPolicy(pol2); err != nil {
		t.Fatal(err)
	}
	if err := in.CrossCheckRobust(pol2); err != nil {
		t.Fatal(err)
	}
}

// 应急设施必须在已开祖先下启用。
func TestRobustEmergencyAncestor(t *testing.T) {
	// 设施 1 是设施 0 的子节点;只有设施 1 覆盖需求点。
	// 预算 2:一阶段开 0(费用 2)后应急开 1(费用 0)可行;
	// 直接应急开 1 而祖先 0 未开不可行。
	in := &RobustInstance{
		Costs:      []int64{2, 0},
		EmergCosts: []int64{9, 0},
		Parents:    []int{-1, 0},
		Profits:    []int64{8},
		Budget:     2,
		EmergLimit: 1,
		Scenarios: []Scenario{
			{Name: "s0", Prob: 1, Group: 0, Covers: [][]int{{1}}},
		},
	}
	pol, err := in.SolveRobust()
	if err != nil {
		t.Fatal(err)
	}
	if pol.WorstProfit != 8 {
		t.Fatalf("最坏收益 = %d, 期望 8", pol.WorstProfit)
	}
	if !pol.Opened[0] || pol.Opened[1] {
		t.Fatalf("一阶段应只开启设施 0: %v", pol.Opened)
	}
	if !pol.Emergency[0][1] {
		t.Fatalf("应应急启用设施 1: %v", pol.Emergency[0])
	}
	if err := in.VerifyPolicy(pol); err != nil {
		t.Fatal(err)
	}
	if err := in.CrossCheckRobust(pol); err != nil {
		t.Fatal(err)
	}
}

// 随机小规模实例:SolveRobust 与独立暴力复核一致。
func TestRobustRandomCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for trial := 0; trial < 40; trial++ {
		n := 1 + rng.Intn(7) // 1..7 设施
		m := 1 + rng.Intn(6) // 1..6 需求点
		S := 1 + rng.Intn(4) // 1..4 情景
		G := 1 + rng.Intn(2) // 1..2 观测组
		in := &RobustInstance{
			Costs:      make([]int64, n),
			EmergCosts: make([]int64, n),
			Parents:    make([]int, n),
			Profits:    make([]int64, m),
			EmergLimit: rng.Intn(3),
		}
		for i := 0; i < n; i++ {
			in.Costs[i] = int64(rng.Intn(6))
			in.EmergCosts[i] = int64(rng.Intn(6))
			if i > 0 && rng.Intn(3) > 0 {
				in.Parents[i] = rng.Intn(i)
			} else {
				in.Parents[i] = -1
			}
		}
		for j := 0; j < m; j++ {
			in.Profits[j] = int64(rng.Intn(8))
		}
		in.Budget = int64(rng.Intn(12))
		for s := 0; s < S; s++ {
			sc := Scenario{
				Prob:   1.0 / float64(S),
				Group:  rng.Intn(G),
				Covers: make([][]int, m),
			}
			for j := 0; j < m; j++ {
				for c := 0; c < rng.Intn(3); c++ {
					sc.Covers[j] = append(sc.Covers[j], rng.Intn(n))
				}
			}
			in.Scenarios = append(in.Scenarios, sc)
		}
		pol, err := in.SolveRobust()
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		if err := in.VerifyPolicy(pol); err != nil {
			t.Fatalf("trial %d VerifyPolicy: %v", trial, err)
		}
		if err := in.CrossCheckRobust(pol); err != nil {
			t.Fatalf("trial %d CrossCheckRobust: %v", trial, err)
		}
	}
}

// 上限规模冒烟:12 设施、5 情景。
func TestRobustMaxScale(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	n, m, S := 12, 10, 5
	in := &RobustInstance{
		Costs:      make([]int64, n),
		EmergCosts: make([]int64, n),
		Parents:    make([]int, n),
		Profits:    make([]int64, m),
		Budget:     15,
		EmergLimit: 3,
	}
	for i := 0; i < n; i++ {
		in.Costs[i] = int64(1 + rng.Intn(4))
		in.EmergCosts[i] = int64(1 + rng.Intn(4))
		if i > 0 {
			in.Parents[i] = rng.Intn(i)
		} else {
			in.Parents[i] = -1
		}
	}
	for j := 0; j < m; j++ {
		in.Profits[j] = int64(1 + rng.Intn(9))
	}
	for s := 0; s < S; s++ {
		sc := Scenario{Prob: 0.2, Group: s % 2, Covers: make([][]int, m)}
		for j := 0; j < m; j++ {
			for c := 0; c < 3; c++ {
				sc.Covers[j] = append(sc.Covers[j], rng.Intn(n))
			}
		}
		in.Scenarios = append(in.Scenarios, sc)
	}
	pol, err := in.SolveRobust()
	if err != nil {
		t.Fatal(err)
	}
	if err := in.VerifyPolicy(pol); err != nil {
		t.Fatal(err)
	}
}
