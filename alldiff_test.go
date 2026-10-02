package main

import (
	"reflect"
	"testing"
)

// 验收边界：4 个变量共享值域 {1,2,3}，匹配阶段直接判定无解。
func TestPropagate_InfeasiblePigeonhole(t *testing.T) {
	vars := []*Variable{
		NewVariable("X1", 1, 2, 3),
		NewVariable("X2", 1, 2, 3),
		NewVariable("X3", 1, 2, 3),
		NewVariable("X4", 1, 2, 3),
	}
	c := NewAllDifferent(vars...)
	if c.Propagate() {
		t.Fatal("expected infeasible: 4 variables cannot take 3 distinct values")
	}
}

// 匹配大小必须等于变量数才可行。
func TestMaximumMatching_Size(t *testing.T) {
	c := NewAllDifferent(
		NewVariable("X1", 1, 2, 3),
		NewVariable("X2", 1, 2, 3),
		NewVariable("X3", 1, 2, 3),
		NewVariable("X4", 1, 2, 3),
	)
	if got := c.maximumMatching().size; got != 3 {
		t.Fatalf("matching size = %d, want 3", got)
	}

	c2 := NewAllDifferent(
		NewVariable("X1", 1, 2),
		NewVariable("X2", 2, 3),
		NewVariable("X3", 1, 3),
	)
	if got := c2.maximumMatching().size; got != 3 {
		t.Fatalf("matching size = %d, want 3", got)
	}
}

// Régin 过滤：X3 的值 1、2 不在任何最大匹配中，必须被剔除。
func TestPropagate_PrunesImpossibleValues(t *testing.T) {
	x1 := NewVariable("X1", 1, 2)
	x2 := NewVariable("X2", 1, 2)
	x3 := NewVariable("X3", 1, 2, 3)
	c := NewAllDifferent(x1, x2, x3)
	if !c.Propagate() {
		t.Fatal("expected feasible")
	}
	if got := x3.SortedDomain(); !reflect.DeepEqual(got, []int{3}) {
		t.Fatalf("X3 domain = %v, want [3]", got)
	}
	if got := x1.SortedDomain(); !reflect.DeepEqual(got, []int{1, 2}) {
		t.Fatalf("X1 domain = %v, want [1 2]", got)
	}
	if got := x2.SortedDomain(); !reflect.DeepEqual(got, []int{1, 2}) {
		t.Fatalf("X2 domain = %v, want [1 2]", got)
	}
}

// 每个值都能出现在某个最大匹配中：不得误删（达到广义弧一致）。
func TestPropagate_KeepsViableValues(t *testing.T) {
	x1 := NewVariable("X1", 1, 2)
	x2 := NewVariable("X2", 2, 3)
	x3 := NewVariable("X3", 1, 3)
	c := NewAllDifferent(x1, x2, x3)
	if !c.Propagate() {
		t.Fatal("expected feasible")
	}
	domains := map[string][]int{
		"X1": {1, 2},
		"X2": {2, 3},
		"X3": {1, 3},
	}
	for _, v := range c.Vars {
		if got := v.SortedDomain(); !reflect.DeepEqual(got, domains[v.Name]) {
			t.Fatalf("%s domain = %v, want %v", v.Name, got, domains[v.Name])
		}
	}
}

// 经典 Régin 例子：自由值节点的交替可达性参与判定。
func TestPropagate_AlternatingReachability(t *testing.T) {
	// X1∈{1,2}, X2∈{1,2}, X3∈{2,3,4}, X4∈{3,4}
	// 值 4 是自由值；X3 的 2 与 X1/X2 同 SCC 外且不可达 → 剔除。
	x1 := NewVariable("X1", 1, 2)
	x2 := NewVariable("X2", 1, 2)
	x3 := NewVariable("X3", 2, 3, 4)
	x4 := NewVariable("X4", 3, 4)
	c := NewAllDifferent(x1, x2, x3, x4)
	if !c.Propagate() {
		t.Fatal("expected feasible")
	}
	if got := x3.SortedDomain(); !reflect.DeepEqual(got, []int{3, 4}) {
		t.Fatalf("X3 domain = %v, want [3 4]", got)
	}
}

// 过滤导致某个值域为空 → 无解。
func TestPropagate_DomainWipeout(t *testing.T) {
	c := NewAllDifferent(
		NewVariable("X1", 1),
		NewVariable("X2", 1),
	)
	if c.Propagate() {
		t.Fatal("expected infeasible: two variables forced to the same value")
	}
}

// 暴力校验：过滤后保留的每个值，都必须出现在某个可行全不同指派中。
func TestPropagate_MatchesBruteForce(t *testing.T) {
	vars := []*Variable{
		NewVariable("X1", 1, 2, 5),
		NewVariable("X2", 1, 2, 3),
		NewVariable("X3", 2, 3, 4),
		NewVariable("X4", 3, 4, 5),
	}
	c := NewAllDifferent(vars...)
	if !c.Propagate() {
		t.Fatal("expected feasible")
	}

	viable := make([]map[int]bool, len(vars))
	for i := range viable {
		viable[i] = make(map[int]bool)
	}
	orig := []map[int]bool{
		{1: true, 2: true, 5: true},
		{1: true, 2: true, 3: true},
		{2: true, 3: true, 4: true},
		{3: true, 4: true, 5: true},
	}
	assign := make([]int, len(vars))
	var enum func(i int, used map[int]bool)
	enum = func(i int, used map[int]bool) {
		if i == len(vars) {
			for j, val := range assign {
				viable[j][val] = true
			}
			return
		}
		for val := range orig[i] {
			if !used[val] {
				used[val] = true
				assign[i] = val
				enum(i+1, used)
				delete(used, val)
			}
		}
	}
	enum(0, make(map[int]bool))

	for i, v := range c.Vars {
		for val := range v.Domain {
			if !viable[i][val] {
				t.Fatalf("%s kept value %d which appears in no solution", v.Name, val)
			}
		}
		for val := range viable[i] {
			if !v.Domain[val] {
				t.Fatalf("%s pruned value %d which appears in a solution", v.Name, val)
			}
		}
	}
}
