package netsec

import (
	"math/rand"
	"reflect"
	"testing"
)

// bruteForce 是刻意独立的参考实现：枚举全部子集，用普通 map/slice
// 计算前置闭包与可达性，返回可行复位集合的最低成本。
func bruteForce(t *testing.T, spec Spec) int64 {
	t.Helper()
	n := len(spec.Blocks)
	best := int64(-1)
	for mask := 0; mask < 1<<uint(n); mask++ {
		// 前置条件闭包
		closure := map[int]bool{}
		var add func(i int) bool
		add = func(i int) bool {
			if closure[i] {
				return true
			}
			closure[i] = true
			for _, p := range spec.Pre[i] {
				if !add(p) {
					return false
				}
			}
			return true
		}
		ok := true
		for i := 0; i < n && ok; i++ {
			if mask>>uint(i)&1 == 1 {
				// 检测前置环：从 i 沿前置边走 DFS，若能回到 i 则不可行
				if reachesSelf(spec.Pre, i) {
					ok = false
					break
				}
				add(i)
			}
		}
		if !ok {
			continue
		}
		// 闭包内不得有前置环
		for i := range closure {
			if reachesSelf(spec.Pre, i) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		// 割检查：从任一 tainted 块出发，不经过 closure，能否到达 observable 块
		adj := map[int][]int{}
		for _, e := range spec.Edges {
			adj[e.From] = append(adj[e.From], e.To)
		}
		seen := map[int]bool{}
		var stack []int
		for i, b := range spec.Blocks {
			if b.Tainted && !closure[i] {
				stack = append(stack, i)
			}
		}
		leak := false
		for len(stack) > 0 && !leak {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if seen[cur] {
				continue
			}
			seen[cur] = true
			if spec.Blocks[cur].Observable {
				leak = true
				break
			}
			for _, nxt := range adj[cur] {
				if !closure[nxt] && !seen[nxt] {
					stack = append(stack, nxt)
				}
			}
		}
		if leak {
			continue
		}
		var cost int64
		for i := range closure {
			cost += spec.Blocks[i].Cost
		}
		if best < 0 || cost < best {
			best = cost
		}
	}
	return best
}

// reachesSelf 报告从 i 的前置条件出发（不含 i 自身）能否再回到 i。
func reachesSelf(pre [][]int, i int) bool {
	seen := map[int]bool{}
	stack := append([]int(nil), pre[i]...)
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur == i {
			return true
		}
		if seen[cur] {
			continue
		}
		seen[cur] = true
		stack = append(stack, pre[cur]...)
	}
	return false
}

// enumeratePaths 枚举所有从 tainted 到 observable 的简单路径。
func enumeratePaths(spec Spec) [][]int {
	adj := map[int][]int{}
	for _, e := range spec.Edges {
		adj[e.From] = append(adj[e.From], e.To)
	}
	var paths [][]int
	var visit func(cur int, path []int, onPath map[int]bool)
	visit = func(cur int, path []int, onPath map[int]bool) {
		path = append(path, cur)
		onPath[cur] = true
		defer delete(onPath, cur)
		if spec.Blocks[cur].Observable {
			cp := append([]int(nil), path...)
			paths = append(paths, cp)
		}
		for _, nxt := range adj[cur] {
			if !onPath[nxt] {
				visit(nxt, path, onPath)
			}
		}
	}
	for i, b := range spec.Blocks {
		if b.Tainted {
			visit(i, nil, map[int]bool{})
		}
	}
	return paths
}

func checkPlanInvariants(t *testing.T, spec Spec, plan Plan) {
	t.Helper()
	inSet := map[int]bool{}
	for _, i := range plan.Set {
		inSet[i] = true
	}
	// 每条 污染源→观察汇 简单路径都必须被 Set 切断
	for _, path := range enumeratePaths(spec) {
		cut := false
		for _, i := range path {
			if inSet[i] {
				cut = true
				break
			}
		}
		if !cut {
			t.Fatalf("path %v not cut by set %v", path, plan.Set)
		}
	}
	// 前置条件封闭
	for _, i := range plan.Set {
		for _, p := range spec.Pre[i] {
			if !inSet[p] {
				t.Fatalf("set %v missing precondition %d of block %d", plan.Set, p, i)
			}
		}
	}
	// 顺序覆盖 Set 且满足前置条件
	if len(plan.Order) != len(plan.Set) {
		t.Fatalf("order %v does not cover set %v", plan.Order, plan.Set)
	}
	pos := map[int]int{}
	for k, i := range plan.Order {
		if !inSet[i] {
			t.Fatalf("order contains %d not in set %v", i, plan.Set)
		}
		pos[i] = k
	}
	for _, i := range plan.Set {
		for _, p := range spec.Pre[i] {
			if pos[p] >= pos[i] {
				t.Fatalf("order %v violates precondition %d<-%d", plan.Order, i, p)
			}
		}
	}
	// 成本一致
	var cost int64
	for _, i := range plan.Set {
		cost += spec.Blocks[i].Cost
	}
	if cost != plan.Cost {
		t.Fatalf("plan cost %d != set cost %d", plan.Cost, cost)
	}
}

func randomSpec(r *rand.Rand, n int) Spec {
	spec := Spec{
		Blocks: make([]Block, n),
		Pre:    make([][]int, n),
	}
	for i := range spec.Blocks {
		spec.Blocks[i] = Block{
			Cost:       int64(r.Intn(10)),
			Tainted:    r.Float64() < 0.3,
			Observable: r.Float64() < 0.3,
		}
	}
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i != j && r.Float64() < 0.25 {
				spec.Edges = append(spec.Edges, Edge{From: i, To: j})
			}
		}
	}
	// 前置条件生成为 DAG（只允许更低下标），保证存在可行计划
	for i := 1; i < n; i++ {
		for j := 0; j < i; j++ {
			if r.Float64() < 0.2 {
				spec.Pre[i] = append(spec.Pre[i], j)
			}
		}
	}
	return spec
}

func TestPlanResetAgainstBruteForce(t *testing.T) {
	r := rand.New(rand.NewSource(20261111))
	for trial := 0; trial < 400; trial++ {
		n := 1 + r.Intn(10)
		spec := randomSpec(r, n)
		plan, err := PlanReset(spec)
		if err != nil {
			t.Fatalf("trial %d: unexpected error: %v", trial, err)
		}
		want := bruteForce(t, spec)
		if want < 0 {
			t.Fatalf("trial %d: brute force found no feasible plan, got %+v", trial, plan)
		}
		if plan.Cost != want {
			t.Fatalf("trial %d: plan cost %d, brute force optimum %d\nspec=%+v",
				trial, plan.Cost, want, spec)
		}
		checkPlanInvariants(t, spec, plan)
	}
}

func TestPlanResetChain(t *testing.T) {
	spec := Spec{
		Blocks: []Block{
			{Name: "src", Cost: 5, Tainted: true},
			{Name: "mid", Cost: 1},
			{Name: "snk", Cost: 5, Observable: true},
		},
		Edges: []Edge{{0, 1}, {1, 2}},
		Pre:   make([][]int, 3),
	}
	plan, err := PlanReset(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.Set, []int{1}) || plan.Cost != 1 {
		t.Fatalf("got %+v, want set [1] cost 1", plan)
	}
	if !reflect.DeepEqual(plan.Order, []int{1}) {
		t.Fatalf("got order %v", plan.Order)
	}
}

func TestPlanResetPreconditionClosure(t *testing.T) {
	// 直接复位 mid 最便宜，但前置条件强制先复位昂贵的 guard，
	// 因此最优解改为复位源块 src。
	spec := Spec{
		Blocks: []Block{
			{Name: "src", Cost: 3, Tainted: true},
			{Name: "mid", Cost: 1},
			{Name: "snk", Cost: 9, Observable: true},
			{Name: "guard", Cost: 10},
		},
		Edges: []Edge{{0, 1}, {1, 2}},
		Pre:   [][]int{nil, {3}, nil, nil},
	}
	plan, err := PlanReset(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.Set, []int{0}) || plan.Cost != 3 {
		t.Fatalf("got %+v, want set [0] cost 3", plan)
	}

	// 调低 guard 成本后，经闭包 {mid, guard} 复位成为最优。
	spec.Blocks[3].Cost = 1
	plan, err = PlanReset(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.Set, []int{1, 3}) || plan.Cost != 2 {
		t.Fatalf("got %+v, want set [1 3] cost 2", plan)
	}
	if !reflect.DeepEqual(plan.Order, []int{3, 1}) {
		t.Fatalf("got order %v, want [3 1]", plan.Order)
	}
}

func TestPlanResetDiamond(t *testing.T) {
	spec := Spec{
		Blocks: []Block{
			{Name: "src", Cost: 10, Tainted: true},
			{Name: "x", Cost: 2},
			{Name: "y", Cost: 2},
			{Name: "snk", Cost: 10, Observable: true},
		},
		Edges: []Edge{{0, 1}, {0, 2}, {1, 3}, {2, 3}},
		Pre:   make([][]int, 4),
	}
	plan, err := PlanReset(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.Set, []int{1, 2}) || plan.Cost != 4 {
		t.Fatalf("got %+v, want set [1 2] cost 4", plan)
	}
}

func TestPlanResetTaintedObservableSameBlock(t *testing.T) {
	spec := Spec{
		Blocks: []Block{{Name: "both", Cost: 7, Tainted: true, Observable: true}},
		Pre:    make([][]int, 1),
	}
	plan, err := PlanReset(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.Set, []int{0}) || plan.Cost != 7 {
		t.Fatalf("got %+v, want set [0] cost 7", plan)
	}
}

func TestPlanResetPreconditionCycleInfeasible(t *testing.T) {
	spec := Spec{
		Blocks: []Block{
			{Name: "both", Cost: 1, Tainted: true, Observable: true},
			{Name: "dep", Cost: 1},
		},
		Pre: [][]int{{1}, {0}},
	}
	if _, err := PlanReset(spec); err == nil {
		t.Fatal("expected infeasible error for precondition cycle")
	}
}

func TestPlanResetNoLeakNeedsNoReset(t *testing.T) {
	spec := Spec{
		Blocks: []Block{
			{Name: "src", Cost: 1, Tainted: true},
			{Name: "snk", Cost: 1, Observable: true},
		},
		Pre: make([][]int, 2),
	}
	plan, err := PlanReset(spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Set) != 0 || plan.Cost != 0 || len(plan.Order) != 0 {
		t.Fatalf("got %+v, want empty plan", plan)
	}
}

func TestPlanResetTooManyBlocks(t *testing.T) {
	spec := Spec{Blocks: make([]Block, MaxBlocks+1)}
	if _, err := PlanReset(spec); err == nil {
		t.Fatal("expected error for too many blocks")
	}
}

func TestPlanResetMaxBlocksFeasible(t *testing.T) {
	// 20 块链：0(tainted) -> 1 -> ... -> 19(observable)，中间块成本最低。
	spec := Spec{Pre: make([][]int, MaxBlocks)}
	for i := 0; i < MaxBlocks; i++ {
		spec.Blocks = append(spec.Blocks, Block{Cost: 5})
		if i+1 < MaxBlocks {
			spec.Edges = append(spec.Edges, Edge{From: i, To: i + 1})
		}
	}
	spec.Blocks[0].Tainted = true
	spec.Blocks[MaxBlocks-1].Observable = true
	spec.Blocks[7].Cost = 1
	plan, err := PlanReset(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.Set, []int{7}) || plan.Cost != 1 {
		t.Fatalf("got %+v, want set [7] cost 1", plan)
	}
}
