package flowgraph

import (
	"math/rand"
	"testing"
)

// --- Independent model: dominance by node deletion ---
//
// The model knows nothing about the iterative algorithm in idom.go.
// It derives dominators purely from reachability: d dominates n iff
// deleting d from the graph makes n unreachable from the entry.
// The immediate dominator of n is then its unique strict dominator
// that is dominated by every other strict dominator of n.

// reachableAfterDelete returns the set of nodes reachable from the
// entry when node `del` is removed (del < 0 removes nothing).
func reachableAfterDelete(g *Graph, del int) map[int]bool {
	reach := map[int]bool{}
	if g.Entry() == del {
		return reach
	}
	stack := []int{g.Entry()}
	reach[g.Entry()] = true
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, s := range g.Successors(v) {
			if s != del && !reach[s] {
				reach[s] = true
				stack = append(stack, s)
			}
		}
	}
	return reach
}

// modelDominates reports whether d dominates n, by deletion.
func modelDominates(g *Graph, d, n int, full map[int]bool) bool {
	if !full[n] {
		return false // unreachable nodes have no dominators
	}
	if d == n {
		return true
	}
	return !reachableAfterDelete(g, d)[n]
}

// modelIdom computes the immediate dominator of every reachable node
// using only deletion-based dominance. Returns nil for unreachable
// nodes. The entry maps to itself.
func modelIdom(g *Graph) map[int]int {
	full := reachableAfterDelete(g, -1)
	result := map[int]int{}
	for n := range full {
		if n == g.Entry() {
			result[n] = n
			continue
		}
		// Strict dominators of n.
		var strict []int
		for d := 0; d < g.NumNodes(); d++ {
			if d != n && modelDominates(g, d, n, full) {
				strict = append(strict, d)
			}
		}
		// The immediate dominator is the strict dominator dominated
		// by all other strict dominators.
		found := -1
		for _, cand := range strict {
			ok := true
			for _, other := range strict {
				if other != cand && !modelDominates(g, other, cand, full) {
					ok = false
					break
				}
			}
			if ok {
				if found != -1 {
					panic("model: multiple immediate dominators")
				}
				found = cand
			}
		}
		if found == -1 {
			panic("model: no immediate dominator found")
		}
		result[n] = found
	}
	return result
}

// checkAgainstModel verifies the library result against the model.
func checkAgainstModel(t *testing.T, g *Graph) {
	t.Helper()
	dom := g.Analyze()
	model := modelIdom(g)
	for n := 0; n < g.NumNodes(); n++ {
		want, reachable := model[n]
		got, ok := dom.Idom(n)
		if ok != reachable {
			t.Errorf("node %d: Reachable=%v, model says reachable=%v", n, ok, reachable)
			continue
		}
		if reachable && got != want {
			t.Errorf("node %d: Idom=%d, model says %d", n, got, want)
		}
	}
	if got, ok := dom.Idom(g.Entry()); !ok || got != g.Entry() {
		t.Errorf("entry: Idom=(%d,%v), want (%d,true)", got, ok, g.Entry())
	}
}

// --- Targeted cases ---

// Cycle: entry -> a -> b -> a, b -> c.
func TestCycle(t *testing.T) {
	g := New(4, 0)
	g.AddEdge(0, 1)
	g.AddEdge(1, 2)
	g.AddEdge(2, 1) // back edge forming the cycle
	g.AddEdge(2, 3)
	checkAgainstModel(t, g)

	dom := g.Analyze()
	want := map[int]int{0: 0, 1: 0, 2: 1, 3: 2}
	for n, w := range want {
		if got, _ := dom.Idom(n); got != w {
			t.Errorf("node %d: Idom=%d, want %d", n, got, w)
		}
	}
}

// Multi-predecessor join: three branches merge at node 4, then split
// and merge again at node 6.
func TestMultiPredJoin(t *testing.T) {
	g := New(7, 0)
	g.AddEdge(0, 1)
	g.AddEdge(0, 2)
	g.AddEdge(0, 3)
	g.AddEdge(1, 4)
	g.AddEdge(2, 4)
	g.AddEdge(3, 4) // 4 has three predecessors
	g.AddEdge(4, 5)
	g.AddEdge(4, 6)
	g.AddEdge(5, 6)
	checkAgainstModel(t, g)

	dom := g.Analyze()
	if got, _ := dom.Idom(4); got != 0 {
		t.Errorf("join node 4: Idom=%d, want 0", got)
	}
	if got, _ := dom.Idom(6); got != 4 {
		t.Errorf("join node 6: Idom=%d, want 4", got)
	}
}

// Entry back edge: a successor points back at the entry. The entry
// must remain its own immediate dominator and nothing else may change.
func TestEntryBackEdge(t *testing.T) {
	g := New(4, 0)
	g.AddEdge(0, 1)
	g.AddEdge(1, 2)
	g.AddEdge(2, 0) // back edge to the entry
	g.AddEdge(2, 3)
	checkAgainstModel(t, g)

	dom := g.Analyze()
	if got, ok := dom.Idom(0); !ok || got != 0 {
		t.Errorf("entry: Idom=(%d,%v), want (0,true)", got, ok)
	}
	if got, _ := dom.Idom(1); got != 0 {
		t.Errorf("node 1: Idom=%d, want 0", got)
	}
}

// Unreachable nodes have no dominator; reachable nodes are unaffected.
func TestUnreachableNodes(t *testing.T) {
	g := New(5, 0)
	g.AddEdge(0, 1)
	g.AddEdge(1, 2)
	// Nodes 3 and 4 form an island, even with internal edges.
	g.AddEdge(3, 4)
	g.AddEdge(4, 3)
	checkAgainstModel(t, g)

	dom := g.Analyze()
	for _, n := range []int{3, 4} {
		if _, ok := dom.Idom(n); ok {
			t.Errorf("unreachable node %d: expected no dominator", n)
		}
		if dom.Reachable(n) {
			t.Errorf("unreachable node %d: Reachable=true", n)
		}
	}
}

// Re-analysis: after Analyze, add an edge that (a) reaches a previously
// unreachable node and (b) changes an existing idom, then re-analyze.
func TestReanalyzeAfterAddEdge(t *testing.T) {
	g := New(5, 0)
	g.AddEdge(0, 1)
	g.AddEdge(1, 2)
	g.AddEdge(2, 3)
	// Node 4 unreachable so far.

	dom1 := g.Analyze()
	if got, _ := dom1.Idom(3); got != 2 {
		t.Fatalf("before: node 3 Idom=%d, want 2", got)
	}
	if dom1.Reachable(4) {
		t.Fatalf("before: node 4 should be unreachable")
	}

	// Shortcut past 1 and 2, and an edge into the island.
	g.AddEdge(0, 3)
	g.AddEdge(3, 4)

	dom2 := g.Analyze()
	if got, _ := dom2.Idom(3); got != 0 {
		t.Errorf("after: node 3 Idom=%d, want 0", got)
	}
	if got, ok := dom2.Idom(4); !ok || got != 3 {
		t.Errorf("after: node 4 Idom=(%d,%v), want (3,true)", got, ok)
	}
	// Old result must be untouched (fresh analysis per call).
	if got, _ := dom1.Idom(3); got != 2 {
		t.Errorf("stale result mutated: node 3 Idom=%d, want 2", got)
	}
	checkAgainstModel(t, g)
}

// Randomized cross-check against the deletion model, covering cycles,
// joins, entry back edges and unreachable nodes all at once.
func TestRandomAgainstModel(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 300; trial++ {
		n := 1 + rng.Intn(9)
		g := New(n, rng.Intn(n))
		edges := rng.Intn(2 * n * n)
		for i := 0; i < edges; i++ {
			g.AddEdge(rng.Intn(n), rng.Intn(n))
		}
		checkAgainstModel(t, g)
	}
}
