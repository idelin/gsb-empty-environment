package topo

import (
	"math/rand"
	"reflect"
	"testing"
)

// bruteForceLexMin enumerates every permutation of 0..n-1 and returns
// the lexicographically smallest valid topological order, or nil when
// the constraints are cyclic.
func bruteForceLexMin(n int, edges []Edge) []int {
	perm := make([]int, n)
	for i := range perm {
		perm[i] = i
	}
	var best []int
	var walk func(k int)
	walk = func(k int) {
		if k == n {
			pos := make([]int, n)
			for i, v := range perm {
				pos[v] = i
			}
			for _, e := range edges {
				if pos[e.From] >= pos[e.To] {
					return
				}
			}
			cand := append([]int(nil), perm...)
			if best == nil || lessInts(cand, best) {
				best = cand
			}
			return
		}
		for i := k; i < n; i++ {
			perm[k], perm[i] = perm[i], perm[k]
			walk(k + 1)
			perm[k], perm[i] = perm[i], perm[k]
		}
	}
	walk(0)
	return best
}

func lessInts(a, b []int) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

// TestSortMatchesExhaustive cross-checks Sort against exhaustive
// enumeration of all topological orders on random small graphs,
// including duplicate constraints.
func TestSortMatchesExhaustive(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 400; trial++ {
		n := 1 + rng.Intn(7)
		var edges []Edge
		for _, e := range randomEdges(rng, n, rng.Intn(n*n+1)) {
			edges = append(edges, e)
			if rng.Intn(3) == 0 { // inject duplicates
				edges = append(edges, e)
			}
		}
		res := Sort(n, edges)
		want := bruteForceLexMin(n, edges)
		if want == nil {
			if res.Cycle == nil {
				t.Fatalf("trial %d: cyclic graph n=%d edges=%v but Sort returned order %v", trial, n, edges, res.Order)
			}
			assertRealCycle(t, n, edges, res)
			assertMaximalPrefix(t, n, edges, res.Order)
			continue
		}
		if res.Cycle != nil {
			t.Fatalf("trial %d: acyclic graph n=%d edges=%v but Sort reported cycle %v", trial, n, edges, res.Cycle)
		}
		if !reflect.DeepEqual(res.Order, want) {
			t.Fatalf("trial %d: n=%d edges=%v\n got %v\nwant %v", trial, n, edges, res.Order, want)
		}
	}
}

func randomEdges(rng *rand.Rand, n, count int) []Edge {
	var edges []Edge
	for i := 0; i < count; i++ {
		edges = append(edges, Edge{rng.Intn(n), rng.Intn(n)})
	}
	return edges
}

// assertRealCycle verifies the reported cycle is a genuine closed loop
// of distinct tasks in the deduplicated constraint graph.
func assertRealCycle(t *testing.T, n int, edges []Edge, res Result) {
	t.Helper()
	c := res.Cycle
	if len(c) < 2 || c[0] != c[len(c)-1] {
		t.Fatalf("cycle %v is not a closed loop", c)
	}
	edgeSet := make(map[Edge]bool)
	for _, e := range edges {
		edgeSet[e] = true
	}
	seen := make(map[int]bool)
	inOrder := make(map[int]bool)
	for _, u := range res.Order {
		inOrder[u] = true
	}
	for i := 0; i+1 < len(c); i++ {
		if !edgeSet[Edge{c[i], c[i+1]}] {
			t.Fatalf("cycle %v uses non-edge %d->%d", c, c[i], c[i+1])
		}
		if seen[c[i]] {
			t.Fatalf("cycle %v repeats task %d", c, c[i])
		}
		seen[c[i]] = true
		if inOrder[c[i]] {
			t.Fatalf("cycle task %d also appears in order %v", c[i], res.Order)
		}
	}
}

// assertMaximalPrefix verifies the executable prefix is valid and
// maximal: every unordered task still has an unordered predecessor.
func assertMaximalPrefix(t *testing.T, n int, edges []Edge, order []int) {
	t.Helper()
	done := make(map[int]bool, len(order))
	pos := make(map[int]int, len(order))
	for i, u := range order {
		done[u] = true
		pos[u] = i
	}
	for _, e := range edges {
		a, okA := pos[e.From]
		b, okB := pos[e.To]
		if okA && okB && a >= b {
			t.Fatalf("prefix %v violates constraint %v", order, e)
		}
	}
	for u := 0; u < n; u++ {
		if done[u] {
			continue
		}
		blocked := false
		for _, e := range edges {
			if e.To == u && !done[e.From] {
				blocked = true
				break
			}
		}
		if !blocked {
			t.Fatalf("prefix %v not maximal: task %d is executable", order, u)
		}
	}
}

func TestSortDuplicateConstraints(t *testing.T) {
	edges := []Edge{{2, 0}, {2, 0}, {2, 0}, {1, 0}}
	res := Sort(3, edges)
	want := []int{1, 2, 0}
	if !reflect.DeepEqual(res.Order, want) || res.Cycle != nil {
		t.Fatalf("got %+v, want order %v", res, want)
	}
}

func TestSortDeterministicPick(t *testing.T) {
	// 3 and 0 are both initially executable; 0 must win; then 1 unblocks
	// nothing new, so 3 goes next only after smaller runnable tasks.
	edges := []Edge{{3, 2}, {0, 1}}
	res := Sort(4, edges)
	want := []int{0, 1, 3, 2}
	if !reflect.DeepEqual(res.Order, want) {
		t.Fatalf("got %v, want %v", res.Order, want)
	}
}

func TestSortCycleReportsLoopNotAllRemaining(t *testing.T) {
	// Cycle 1<->2; task 3 is merely blocked by it; task 0 is free.
	edges := []Edge{{1, 2}, {2, 1}, {1, 3}}
	res := Sort(4, edges)
	if !reflect.DeepEqual(res.Order, []int{0}) {
		t.Fatalf("executable prefix = %v, want [0]", res.Order)
	}
	assertRealCycle(t, 4, edges, res)
	for _, u := range res.Cycle {
		if u == 3 {
			t.Fatalf("blocked-but-acyclic task 3 reported in cycle %v", res.Cycle)
		}
	}
}

func TestSortSelfLoop(t *testing.T) {
	res := Sort(2, []Edge{{0, 0}, {0, 1}})
	if !reflect.DeepEqual(res.Cycle, []int{0, 0}) {
		t.Fatalf("self loop cycle = %v, want [0 0]", res.Cycle)
	}
	if len(res.Order) != 0 {
		t.Fatalf("order = %v, want empty", res.Order)
	}
}
