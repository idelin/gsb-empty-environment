package clique

import (
	"math/rand"
	"reflect"
	"testing"
)

// --- 测试辅助：指数级暴力枚举，作为正确性参照 ---

func bruteForceMaximalCliques(n int, edges [][2]uint64) [][]uint64 {
	adj := make([][]bool, n)
	for i := range adj {
		adj[i] = make([]bool, n)
	}
	for _, e := range edges {
		adj[e[0]][e[1]] = true
		adj[e[1]][e[0]] = true
	}
	isClique := func(sub []uint64) bool {
		for i := range sub {
			for j := i + 1; j < len(sub); j++ {
				if !adj[sub[i]][sub[j]] {
					return false
				}
			}
		}
		return true
	}
	var out [][]uint64
	for mask := 1; mask < 1<<uint(n); mask++ {
		var sub []uint64
		for i := 0; i < n; i++ {
			if mask&(1<<uint(i)) != 0 {
				sub = append(sub, uint64(i))
			}
		}
		if !isClique(sub) {
			continue
		}
		maximal := true
		for v := 0; v < n && maximal; v++ {
			if mask&(1<<uint(v)) != 0 {
				continue
			}
			all := true
			for _, u := range sub {
				if !adj[v][u] {
					all = false
					break
				}
			}
			if all {
				maximal = false
			}
		}
		if maximal {
			out = append(out, sub)
		}
	}
	sortCliques(out)
	return out
}

func cliquesEqual(a, b [][]uint64) bool {
	return reflect.DeepEqual(a, b)
}

// --- 经典样例：Wikipedia 的 Bron–Kerbosch 示例图 ---

func TestKnownGraph(t *testing.T) {
	g := New()
	for _, e := range [][2]uint64{{1, 2}, {1, 5}, {2, 3}, {2, 5}, {3, 4}, {4, 5}, {4, 6}} {
		g.AddEdge(e[0], e[1])
	}
	want := [][]uint64{{1, 2, 5}, {2, 3}, {3, 4}, {4, 5}, {4, 6}}
	if got := g.MaximalCliques(); !cliquesEqual(got, want) {
		t.Fatalf("MaximalCliques = %v, want %v", got, want)
	}
	if got := g.Recompute(); !cliquesEqual(got, want) {
		t.Fatalf("Recompute = %v, want %v", got, want)
	}
}

// --- 验收场景：加边促成 5 节点完全子图，新团诞生、子团消解 ---

func buildK5MinusEdge(missing [2]uint64) *Graph {
	g := New()
	for u := uint64(1); u <= 5; u++ {
		for v := u + 1; v <= 5; v++ {
			if u == missing[0] && v == missing[1] {
				continue
			}
			g.AddEdge(u, v)
		}
	}
	return g
}

func TestAddEdgeBirthsFiveClique(t *testing.T) {
	g := buildK5MinusEdge([2]uint64{1, 5})

	before := g.MaximalCliques()
	wantBefore := [][]uint64{{1, 2, 3, 4}, {2, 3, 4, 5}}
	if !cliquesEqual(before, wantBefore) {
		t.Fatalf("before = %v, want %v", before, wantBefore)
	}

	born, died := g.AddEdge(1, 5)
	if want := [][]uint64{{1, 2, 3, 4, 5}}; !cliquesEqual(born, want) {
		t.Fatalf("born = %v, want %v", born, want)
	}
	if !cliquesEqual(died, wantBefore) {
		t.Fatalf("died = %v, want %v (两个 4 团应被 5 团吞并)", died, wantBefore)
	}
	if got := g.MaximalCliques(); !cliquesEqual(got, [][]uint64{{1, 2, 3, 4, 5}}) {
		t.Fatalf("after = %v, want single 5-clique", got)
	}
}

// --- 验收场景：删边后 5 团正确退化 ---

func TestRemoveEdgeDegenerates(t *testing.T) {
	g := buildK5MinusEdge([2]uint64{1, 5})
	g.AddEdge(1, 5) // 现在是 K5

	born, died := g.RemoveEdge(2, 4)
	wantBorn := [][]uint64{{1, 2, 3, 5}, {1, 3, 4, 5}}
	wantDied := [][]uint64{{1, 2, 3, 4, 5}}
	if !cliquesEqual(born, wantBorn) {
		t.Fatalf("born = %v, want %v", born, wantBorn)
	}
	if !cliquesEqual(died, wantDied) {
		t.Fatalf("died = %v, want %v", died, wantDied)
	}
	if got := g.MaximalCliques(); !cliquesEqual(got, wantBorn) {
		t.Fatalf("after = %v, want %v", got, wantBorn)
	}
}

// --- 包含指定节点集合的极大团清单 ---

func TestCliquesContaining(t *testing.T) {
	g := New()
	edges := [][2]uint64{
		{1, 2}, {1, 3}, {1, 4}, {2, 3}, {2, 4}, {2, 5}, {3, 4}, {3, 5}, {4, 5},
		{1, 6}, {2, 6}, {6, 7},
	}
	for _, e := range edges {
		g.AddEdge(e[0], e[1])
	}
	// 极大团: {1,2,3,4} {2,3,4,5} {1,2,6} {6,7}
	if got := g.CliquesContaining(1, 2); !cliquesEqual(got, [][]uint64{{1, 2, 3, 4}, {1, 2, 6}}) {
		t.Fatalf("containing {1,2} = %v", got)
	}
	if got := g.CliquesContaining(5); !cliquesEqual(got, [][]uint64{{2, 3, 4, 5}}) {
		t.Fatalf("containing {5} = %v", got)
	}
	if got := g.CliquesContaining(3, 6); len(got) != 0 {
		t.Fatalf("containing {3,6} = %v, want empty", got)
	}
	if got := g.CliquesContaining(); len(got) != 4 {
		t.Fatalf("containing {} = %v, want all 4 cliques", got)
	}
}

// --- 孤立节点与空操作 ---

func TestIsolatedNodesAndNoops(t *testing.T) {
	g := New()
	g.AddNode(1)
	g.AddNode(2)
	if got := g.MaximalCliques(); !cliquesEqual(got, [][]uint64{{1}, {2}}) {
		t.Fatalf("isolated = %v", got)
	}
	born, died := g.AddEdge(1, 2)
	if !cliquesEqual(born, [][]uint64{{1, 2}}) || !cliquesEqual(died, [][]uint64{{1}, {2}}) {
		t.Fatalf("add: born=%v died=%v", born, died)
	}
	if b, d := g.AddEdge(1, 2); b != nil || d != nil {
		t.Fatalf("duplicate add should be noop, got %v %v", b, d)
	}
	if b, d := g.AddEdge(3, 3); b != nil || d != nil {
		t.Fatalf("self-loop should be noop, got %v %v", b, d)
	}
	if b, d := g.RemoveEdge(1, 5); b != nil || d != nil {
		t.Fatalf("removing missing edge should be noop, got %v %v", b, d)
	}
	born, died = g.RemoveEdge(1, 2)
	if !cliquesEqual(born, [][]uint64{{1}, {2}}) || !cliquesEqual(died, [][]uint64{{1, 2}}) {
		t.Fatalf("remove: born=%v died=%v", born, died)
	}
}

// --- 随机增删压力下，增量维护结果始终等于暴力枚举与全量重算 ---

func TestIncrementalMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	const n = 8
	g := New()
	for i := 0; i < n; i++ {
		g.AddNode(uint64(i))
	}
	var edges [][2]uint64
	present := make(map[[2]uint64]bool)

	check := func(step int) {
		want := bruteForceMaximalCliques(n, edges)
		if got := g.MaximalCliques(); !cliquesEqual(got, want) {
			t.Fatalf("step %d: incremental = %v, brute force = %v", step, got, want)
		}
		if got := g.Recompute(); !cliquesEqual(got, want) {
			t.Fatalf("step %d: recompute = %v, brute force = %v", step, got, want)
		}
	}

	for step := 0; step < 200; step++ {
		u, v := uint64(rng.Intn(n)), uint64(rng.Intn(n))
		if u == v {
			continue
		}
		if u > v {
			u, v = v, u
		}
		e := [2]uint64{u, v}
		if present[e] {
			g.RemoveEdge(u, v)
			delete(present, e)
			for i := range edges {
				if edges[i] == e {
					edges = append(edges[:i], edges[i+1:]...)
					break
				}
			}
		} else {
			g.AddEdge(u, v)
			present[e] = true
			edges = append(edges, e)
		}
		check(step)
	}
}
