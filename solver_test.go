package main

import (
	"math/rand"
	"testing"
)

// bruteForce 枚举全部子集，返回满足前置/互斥/预算约束的最大收益。
func bruteForce(t *testing.T, inst Instance) int64 {
	t.Helper()
	s, err := buildSolver(inst)
	if err != nil {
		t.Fatalf("buildSolver: %v", err)
	}
	n := s.n
	best := int64(0) // 空集恒可行
	for mask := uint32(0); mask < uint32(1)<<uint(n); mask++ {
		var cost, val int64
		feasible := true
		for i := 0; i < n && feasible; i++ {
			if mask&(uint32(1)<<uint(i)) == 0 {
				continue
			}
			cost += s.cost[i]
			val += s.value[i]
			if s.reqMask[i]&^mask != 0 { // 前置闭包未全部选中
				feasible = false
			}
			if s.exclMask[i]&mask != 0 { // 与已选项互斥
				feasible = false
			}
		}
		if !feasible || cost > s.budget {
			continue
		}
		for i := 0; i < n; i++ {
			if mask&(uint32(1)<<uint(i)) == 0 {
				continue
			}
			for j := i + 1; j < n; j++ {
				if mask&(uint32(1)<<uint(j)) != 0 {
					val += s.syn[i][j]
				}
			}
		}
		if val > best {
			best = val
		}
	}
	return best
}

// recompute 独立核算 Result 的分解是否自洽。
func recompute(t *testing.T, inst Instance, r Result) {
	t.Helper()
	if r.TotalValue != r.BaseValue+r.SynergyValue {
		t.Fatalf("total value mismatch: %d != %d + %d", r.TotalValue, r.BaseValue, r.SynergyValue)
	}
	var costSum, baseSum int64
	for _, p := range r.Selected {
		costSum += p.Cost
		baseSum += p.Value
	}
	if costSum != r.TotalCost || baseSum != r.BaseValue {
		t.Fatalf("decomposition mismatch: cost %d/%d base %d/%d", costSum, r.TotalCost, baseSum, r.BaseValue)
	}
	if r.TotalCost > r.Budget {
		t.Fatalf("over budget: %d > %d", r.TotalCost, r.Budget)
	}
	var synSum int64
	for _, sy := range r.Synergies {
		synSum += sy.Value
	}
	if synSum != r.SynergyValue {
		t.Fatalf("synergy sum mismatch: %d != %d", synSum, r.SynergyValue)
	}
	if r.RootUpperBound < r.TotalValue {
		t.Fatalf("root upper bound %d below optimum %d: bound invalid", r.RootUpperBound, r.TotalValue)
	}
}

func TestAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(20261005))
	for trial := 0; trial < 300; trial++ {
		n := 1 + rng.Intn(15) // 1..15 个项目
		inst := Instance{}
		ids := make([]string, n)
		for i := 0; i < n; i++ {
			ids[i] = string(rune('A' + i))
			inst.Projects = append(inst.Projects, Project{
				ID:    ids[i],
				Cost:  int64(rng.Intn(12)),
				Value: int64(rng.Intn(26) - 5), // 含负固定收益
			})
		}
		total := int64(0)
		for _, p := range inst.Projects {
			total += p.Cost
		}
		inst.Budget = total * int64(20+rng.Intn(60)) / 100
		// 随机前置边（允许成环，闭包语义下依然有意义）
		for i := 0; i < n; i++ {
			if rng.Intn(3) == 0 {
				j := rng.Intn(n)
				if j != i {
					inst.Projects[i].Requires = append(inst.Projects[i].Requires, ids[j])
				}
			}
		}
		// 随机互斥
		for i := 0; i < n; i++ {
			if rng.Intn(4) == 0 {
				j := rng.Intn(n)
				if j != i {
					inst.Projects[i].Excludes = append(inst.Projects[i].Excludes, ids[j])
				}
			}
		}
		// 随机协同（可为负）
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				if rng.Intn(3) == 0 {
					inst.Synergies = append(inst.Synergies, Synergy{
						A: ids[i], B: ids[j], Value: int64(rng.Intn(21) - 8),
					})
				}
			}
		}
		res, err := Solve(inst)
		if err != nil {
			t.Fatalf("trial %d: Solve: %v", trial, err)
		}
		recompute(t, inst, res)
		want := bruteForce(t, inst)
		if res.TotalValue != want {
			t.Fatalf("trial %d: got %d, brute force says %d (instance: %+v)", trial, res.TotalValue, want, inst)
		}
	}
}

func TestNegativeSynergyAvoidsPairing(t *testing.T) {
	inst := Instance{
		Budget: 100,
		Projects: []Project{
			{ID: "A", Cost: 10, Value: 20},
			{ID: "B", Cost: 10, Value: 15},
		},
		Synergies: []Synergy{{A: "A", B: "B", Value: -30}},
	}
	res, err := Solve(inst)
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalValue != 20 || len(res.Selected) != 1 || res.Selected[0].ID != "A" {
		t.Fatalf("expected only A with value 20, got %+v", res)
	}
	if res.SynergyValue != 0 {
		t.Fatalf("negative synergy should not be realized, got %+v", res.Synergies)
	}
}

func TestNegativeSynergyAcceptedWhenWorthIt(t *testing.T) {
	inst := Instance{
		Budget: 100,
		Projects: []Project{
			{ID: "A", Cost: 10, Value: 30},
			{ID: "B", Cost: 10, Value: 25},
		},
		Synergies: []Synergy{{A: "A", B: "B", Value: -10}},
	}
	res, err := Solve(inst)
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalValue != 45 || len(res.Selected) != 2 {
		t.Fatalf("expected both selected with net 45, got %+v", res)
	}
	if res.SynergyValue != -10 {
		t.Fatalf("expected synergy -10 in decomposition, got %d", res.SynergyValue)
	}
}

func TestPrerequisiteChainForced(t *testing.T) {
	inst := Instance{
		Budget: 100,
		Projects: []Project{
			{ID: "BASE", Cost: 5, Value: -8},
			{ID: "MID", Cost: 5, Value: 1, Requires: []string{"BASE"}},
			{ID: "TOP", Cost: 5, Value: 30, Requires: []string{"MID"}},
		},
	}
	res, err := Solve(inst)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Selected) != 3 || res.TotalValue != 23 {
		t.Fatalf("expected whole chain with net 23, got %+v", res)
	}
}

func TestPrerequisiteTooExpensiveBanned(t *testing.T) {
	inst := Instance{
		Budget: 8,
		Projects: []Project{
			{ID: "BASE", Cost: 6, Value: 1},
			{ID: "TOP", Cost: 6, Value: 100, Requires: []string{"BASE"}},
		},
	}
	res, err := Solve(inst)
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalValue != 1 || len(res.Selected) != 1 || res.Selected[0].ID != "BASE" {
		t.Fatalf("expected only BASE, got %+v", res)
	}
}

func TestMutualExclusion(t *testing.T) {
	inst := Instance{
		Budget: 100,
		Projects: []Project{
			{ID: "A", Cost: 5, Value: 10},
			{ID: "B", Cost: 5, Value: 12, Excludes: []string{"A"}},
			{ID: "C", Cost: 5, Value: 1},
		},
	}
	res, err := Solve(inst)
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalValue != 13 {
		t.Fatalf("expected B+C = 13, got %+v", res)
	}
	for _, p := range res.Selected {
		if p.ID == "A" {
			t.Fatalf("A should be excluded in favor of B: %+v", res)
		}
	}
}

func TestTooManyProjects(t *testing.T) {
	inst := Instance{Budget: 1}
	for i := 0; i < MaxProjects+1; i++ {
		inst.Projects = append(inst.Projects, Project{ID: string(rune('A' + i))})
	}
	if _, err := Solve(inst); err == nil {
		t.Fatal("expected error for more than 26 projects")
	}
}

func TestEmptySelectionWhenAllNegative(t *testing.T) {
	inst := Instance{
		Budget: 10,
		Projects: []Project{
			{ID: "A", Cost: 3, Value: -5},
			{ID: "B", Cost: 3, Value: -1},
		},
		Synergies: []Synergy{{A: "A", B: "B", Value: -2}},
	}
	res, err := Solve(inst)
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalValue != 0 || len(res.Selected) != 0 {
		t.Fatalf("expected empty selection with value 0, got %+v", res)
	}
}

func TestDemoInstance(t *testing.T) {
	inst, err := ParseInstance(demoJSON)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Solve(inst)
	if err != nil {
		t.Fatal(err)
	}
	recompute(t, inst, res)
	if res.TotalValue <= 0 {
		t.Fatalf("demo instance should have a profitable solution, got %+v", res)
	}
}
