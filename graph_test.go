package dynclique

import (
	"math/rand"
	"reflect"
	"testing"
)

// cliqueSet converts a clique list into a comparable set of keys.
func cliqueSet(cliques [][]int) map[string]struct{} {
	out := make(map[string]struct{}, len(cliques))
	for _, c := range cliques {
		out[cliqueKey(c)] = struct{}{}
	}
	return out
}

func assertCliques(t *testing.T, got [][]int, want ...[]int) {
	t.Helper()
	gotSet := cliqueSet(got)
	wantSet := cliqueSet(want)
	if !reflect.DeepEqual(gotSet, wantSet) {
		t.Fatalf("cliques mismatch\n got: %v\nwant: %v", got, want)
	}
}

// bruteForceMaximalCliques enumerates maximal cliques by exhaustive subset
// search; used as ground truth for small random graphs.
func bruteForceMaximalCliques(nodes []int, edges map[[2]int]bool) [][]int {
	adjacent := func(a, b int) bool {
		if a > b {
			a, b = b, a
		}
		return edges[[2]int{a, b}]
	}
	isClique := func(set []int) bool {
		for i := 0; i < len(set); i++ {
			for j := i + 1; j < len(set); j++ {
				if !adjacent(set[i], set[j]) {
					return false
				}
			}
		}
		return true
	}
	var out [][]int
	n := len(nodes)
	for mask := 1; mask < 1<<n; mask++ {
		var set []int
		for i := 0; i < n; i++ {
			if mask&(1<<i) != 0 {
				set = append(set, nodes[i])
			}
		}
		if !isClique(set) {
			continue
		}
		maximal := true
		for _, v := range nodes {
			if mask&(1<<indexOf(nodes, v)) != 0 {
				continue
			}
			allAdj := true
			for _, u := range set {
				if !adjacent(u, v) {
					allAdj = false
					break
				}
			}
			if allAdj {
				maximal = false
				break
			}
		}
		if maximal {
			out = append(out, set)
		}
	}
	return out
}

func indexOf(xs []int, v int) int {
	for i, x := range xs {
		if x == v {
			return i
		}
	}
	return -1
}

func TestBronKerboschKnownGraph(t *testing.T) {
	// Classic example: two triangles sharing the edge (2,3), plus a
	// pendant edge (3,4).
	//   1-2-5, 2-3, 3-5  -> cliques {1,2}, wait: build explicitly.
	adj := map[int]map[int]struct{}{}
	add := func(u, v int) {
		if adj[u] == nil {
			adj[u] = map[int]struct{}{}
		}
		if adj[v] == nil {
			adj[v] = map[int]struct{}{}
		}
		adj[u][v] = struct{}{}
		adj[v][u] = struct{}{}
	}
	add(1, 2)
	add(2, 3)
	add(1, 3) // triangle 1-2-3
	add(3, 4)
	add(4, 5)
	add(3, 5) // triangle 3-4-5
	add(5, 6)
	add(6, 7) // path 5-6-7
	add(8, 8) // ignored: self loop not added via map trick; add isolated node instead
	adj[8] = map[int]struct{}{}

	got := MaximalCliques(adj)
	assertCliques(t, got,
		[]int{1, 2, 3},
		[]int{3, 4, 5},
		[]int{5, 6},
		[]int{6, 7},
		[]int{8},
	)
}

func TestBronKerboschAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 200; trial++ {
		n := 2 + rng.Intn(9) // 2..10 vertices
		nodes := make([]int, n)
		adj := make(map[int]map[int]struct{}, n)
		for i := range nodes {
			nodes[i] = i
			adj[i] = map[int]struct{}{}
		}
		edges := map[[2]int]bool{}
		p := rng.Float64()
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				if rng.Float64() < p {
					adj[i][j] = struct{}{}
					adj[j][i] = struct{}{}
					edges[[2]int{i, j}] = true
				}
			}
		}
		got := MaximalCliques(adj)
		want := bruteForceMaximalCliques(nodes, edges)
		if !reflect.DeepEqual(cliqueSet(got), cliqueSet(want)) {
			t.Fatalf("trial %d (n=%d, p=%.2f): mismatch\n got: %v\nwant: %v", trial, n, p, got, want)
		}
	}
}

// Acceptance scenario: a social circle where one new friendship completes
// a 5-node fully connected group.
func TestAddEdgeBirthsFiveClique(t *testing.T) {
	g := NewGraph()
	// K5 on {1..5} missing only the edge (1,2).
	for _, pair := range [][2]int{
		{1, 3}, {1, 4}, {1, 5},
		{2, 3}, {2, 4}, {2, 5},
		{3, 4}, {3, 5}, {4, 5},
	} {
		g.AddEdge(pair[0], pair[1])
	}
	// Before the final edge: two overlapping 4-cliques.
	assertCliques(t, g.MaximalCliques(),
		[]int{1, 3, 4, 5},
		[]int{2, 3, 4, 5},
	)

	// Adding edge (1,2) completes K5: the new maximal clique is born and
	// both 4-cliques are absorbed and removed.
	g.AddEdge(1, 2)
	assertCliques(t, g.MaximalCliques(), []int{1, 2, 3, 4, 5})

	// Query: maximal cliques containing a node set.
	assertCliques(t, g.CliquesContaining(1, 2), []int{1, 2, 3, 4, 5})
	assertCliques(t, g.CliquesContaining(3), []int{1, 2, 3, 4, 5})
	if got := g.CliquesContaining(9); len(got) != 0 {
		t.Fatalf("expected no clique containing node 9, got %v", got)
	}
}

// Acceptance scenario: removing the edge degrades K5 back into the two
// 4-cliques.
func TestRemoveEdgeDegrades(t *testing.T) {
	g := NewGraph()
	for i := 1; i <= 5; i++ {
		for j := i + 1; j <= 5; j++ {
			g.AddEdge(i, j)
		}
	}
	assertCliques(t, g.MaximalCliques(), []int{1, 2, 3, 4, 5})

	g.RemoveEdge(1, 2)
	assertCliques(t, g.MaximalCliques(),
		[]int{1, 3, 4, 5},
		[]int{2, 3, 4, 5},
	)

	// Degrade further: removing (1,3) splits {1,3,4,5} into {1,4,5},
	// while {2,3,4,5} survives untouched.
	g.RemoveEdge(1, 3)
	assertCliques(t, g.MaximalCliques(),
		[]int{1, 4, 5},
		[]int{2, 3, 4, 5},
	)
}

// Removal must not resurrect a candidate that is still contained in a
// surviving maximal clique.
func TestRemoveEdgeCandidateAbsorbed(t *testing.T) {
	g := NewGraph()
	// K4 on {1,2,3,4} plus vertex 5 connected to 1,3,4 (not 2).
	for _, pair := range [][2]int{
		{1, 2}, {1, 3}, {1, 4}, {2, 3}, {2, 4}, {3, 4},
		{1, 5}, {3, 5}, {4, 5},
	} {
		g.AddEdge(pair[0], pair[1])
	}
	assertCliques(t, g.MaximalCliques(),
		[]int{1, 2, 3, 4},
		[]int{1, 3, 4, 5},
	)
	// Remove (1,2): {1,2,3,4} dissolves. Candidate {2,3,4} is maximal,
	// but candidate {1,3,4} is contained in {1,3,4,5} and must be dropped.
	g.RemoveEdge(1, 2)
	assertCliques(t, g.MaximalCliques(),
		[]int{2, 3, 4},
		[]int{1, 3, 4, 5},
	)
}

func TestCliquesContaining(t *testing.T) {
	g := NewGraph()
	for _, pair := range [][2]int{
		{1, 2}, {2, 3}, {1, 3}, // triangle A
		{3, 4}, {4, 5}, {3, 5}, // triangle B sharing node 3
		{7, 8}, // separate edge
	} {
		g.AddEdge(pair[0], pair[1])
	}
	g.AddNode(9)

	assertCliques(t, g.CliquesContaining(3),
		[]int{1, 2, 3},
		[]int{3, 4, 5},
	)
	assertCliques(t, g.CliquesContaining(1, 2), []int{1, 2, 3})
	assertCliques(t, g.CliquesContaining(9), []int{9})
	assertCliques(t, g.CliquesContaining(1, 4)) // none
}

// Random dynamic operations must always agree with a from-scratch
// Bron-Kerbosch recomputation.
func TestDynamicRandomConsistency(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 100; trial++ {
		g := NewGraph()
		n := 3 + rng.Intn(8) // 3..10 vertices
		for v := 0; v < n; v++ {
			g.AddNode(v)
		}
		for step := 0; step < 60; step++ {
			u, v := rng.Intn(n), rng.Intn(n)
			if u == v {
				continue
			}
			if g.HasEdge(u, v) && rng.Float64() < 0.5 {
				g.RemoveEdge(u, v)
			} else {
				g.AddEdge(u, v)
			}
		}
		// Ground truth: static enumeration on the final adjacency.
		adj := make(map[int]map[int]struct{}, n)
		for _, v := range g.Nodes() {
			adj[v] = map[int]struct{}{}
			for w := range g.adj[v] {
				adj[v][w] = struct{}{}
			}
		}
		want := MaximalCliques(adj)
		got := g.MaximalCliques()
		if !reflect.DeepEqual(cliqueSet(got), cliqueSet(want)) {
			t.Fatalf("trial %d: dynamic set diverged\n got: %v\nwant: %v", trial, got, want)
		}
	}
}

func TestDuplicateAndMissingEdgeOps(t *testing.T) {
	g := NewGraph()
	g.AddEdge(1, 2)
	g.AddEdge(1, 2) // idempotent
	assertCliques(t, g.MaximalCliques(), []int{1, 2})
	g.RemoveEdge(1, 2)
	g.RemoveEdge(1, 2) // no-op
	assertCliques(t, g.MaximalCliques(), []int{1}, []int{2})
}
