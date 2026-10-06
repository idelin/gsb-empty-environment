package cfgdom_test

import (
	"math/rand"
	"reflect"
	"sort"
	"testing"

	"cfgdom"
)

// --- Independent model: node-deletion reachability enumeration ---
//
// d dominates n iff every path from the entry to n passes through d,
// i.e. n becomes unreachable when d is removed from the graph. The
// immediate dominator of n is its unique strict dominator that is
// dominated by every other strict dominator of n.

const noRemoval = -1

// reachableSet returns the nodes reachable from the entry when the node
// `removed` (or no node, if removed == noRemoval) is deleted.
func reachableSet(g *cfgdom.Graph, removed int) map[int]bool {
	reached := map[int]bool{}
	entry := g.Entry()
	if entry == removed {
		return reached
	}
	queue := []int{entry}
	reached[entry] = true
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		for _, s := range g.Successors(n) {
			if s == removed || reached[s] {
				continue
			}
			reached[s] = true
			queue = append(queue, s)
		}
	}
	return reached
}

// bruteForceIdom derives entry, idom map and reachable set purely from
// node-deletion reachability, without any dominator algorithm.
func bruteForceIdom(g *cfgdom.Graph) (entry int, idom map[int]int, reachable map[int]bool) {
	entry = g.Entry()
	reachable = reachableSet(g, noRemoval)
	idom = map[int]int{}
	nodes := g.Nodes()
	for _, n := range nodes {
		if n == entry || !reachable[n] {
			continue
		}
		// Strict dominators of n: deleting them makes n unreachable.
		var strict []int
		for _, d := range nodes {
			if d == n || !reachable[d] {
				continue
			}
			if !reachableSet(g, d)[n] {
				strict = append(strict, d)
			}
		}
		// The immediate dominator is the strict dominator dominated by
		// all other strict dominators: removing any other strict
		// dominator e must also remove d.
		var found []int
		for _, d := range strict {
			innermost := true
			for _, e := range strict {
				if e != d && reachableSet(g, e)[d] {
					innermost = false
					break
				}
			}
			if innermost {
				found = append(found, d)
			}
		}
		if len(found) != 1 {
			panic("brute force model: expected exactly one idom")
		}
		idom[n] = found[0]
	}
	return entry, idom, reachable
}

// checkAgainstModel cross-validates the library result with the
// independent node-deletion model.
func checkAgainstModel(t *testing.T, g *cfgdom.Graph) {
	t.Helper()
	wantEntry, wantIdom, wantReachable := bruteForceIdom(g)
	got := g.Analyze()
	if got.Entry != wantEntry {
		t.Errorf("entry: got %d, want %d", got.Entry, wantEntry)
	}
	if !reflect.DeepEqual(got.Idom, wantIdom) {
		t.Errorf("idom mismatch:\n got %v\nwant %v", got.Idom, wantIdom)
	}
	for _, n := range g.Nodes() {
		if got.Reachable(n) != wantReachable[n] {
			t.Errorf("Reachable(%d) = %v, want %v", n, got.Reachable(n), wantReachable[n])
		}
		if _, ok := got.Idom[n]; ok != (wantReachable[n] && n != wantEntry) {
			t.Errorf("Idom presence for %d = %v, want %v", n, ok, wantReachable[n] && n != wantEntry)
		}
	}
}

func sortedKeys(m map[int]int) []int {
	var out []int
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

// --- Focused cases ---

// Cycle: 0 -> 1 -> 2 -> 1 (loop), 2 -> 3.
func TestCycle(t *testing.T) {
	g := cfgdom.NewGraph(0)
	g.AddEdge(0, 1)
	g.AddEdge(1, 2)
	g.AddEdge(2, 1) // back edge forming a cycle
	g.AddEdge(2, 3)
	checkAgainstModel(t, g)

	d := g.Analyze()
	want := map[int]int{1: 0, 2: 1, 3: 2}
	if !reflect.DeepEqual(d.Idom, want) {
		t.Errorf("got %v, want %v", d.Idom, want)
	}
}

// Multi-entry merge: two branches from the entry converge at 3, and a
// second convergence at 5 with a predecessor from outside the diamond.
func TestMultiEntryMerge(t *testing.T) {
	g := cfgdom.NewGraph(0)
	g.AddEdge(0, 1)
	g.AddEdge(0, 2)
	g.AddEdge(1, 3)
	g.AddEdge(2, 3) // 3 has two entry-side predecessors
	g.AddEdge(3, 4)
	g.AddEdge(1, 5)
	g.AddEdge(4, 5) // 5 merges paths that split before 3
	checkAgainstModel(t, g)

	d := g.Analyze()
	want := map[int]int{1: 0, 2: 0, 3: 0, 4: 3, 5: 0}
	if !reflect.DeepEqual(d.Idom, want) {
		t.Errorf("got %v, want %v", d.Idom, want)
	}
}

// Entry back-edge: 2 points back at the entry; the entry must still be
// listed separately and must not acquire a dominator.
func TestEntryBackEdge(t *testing.T) {
	g := cfgdom.NewGraph(0)
	g.AddEdge(0, 1)
	g.AddEdge(1, 2)
	g.AddEdge(2, 0) // back edge to the entry
	g.AddEdge(2, 3)
	checkAgainstModel(t, g)

	d := g.Analyze()
	if _, ok := d.ImmediateDominator(0); ok {
		t.Errorf("entry must not have an immediate dominator")
	}
	if d.Entry != 0 {
		t.Errorf("entry listed as %d, want 0", d.Entry)
	}
	want := map[int]int{1: 0, 2: 1, 3: 2}
	if !reflect.DeepEqual(d.Idom, want) {
		t.Errorf("got %v, want %v", d.Idom, want)
	}
}

// Unreachable nodes: present in the graph but with no dominator.
func TestUnreachableNodes(t *testing.T) {
	g := cfgdom.NewGraph(0)
	g.AddEdge(0, 1)
	g.AddEdge(2, 3) // disconnected component
	g.AddNode(4)    // isolated node
	checkAgainstModel(t, g)

	d := g.Analyze()
	for _, n := range []int{2, 3, 4} {
		if d.Reachable(n) {
			t.Errorf("node %d should be unreachable", n)
		}
		if _, ok := d.ImmediateDominator(n); ok {
			t.Errorf("unreachable node %d must have no dominator", n)
		}
	}
	if keys := sortedKeys(d.Idom); !reflect.DeepEqual(keys, []int{1}) {
		t.Errorf("Idom keys = %v, want [1]", keys)
	}
}

// Re-analysis: adding an edge invalidates the old result; Analyze must
// reflect the new graph when called again.
func TestReanalyzeAfterAddEdge(t *testing.T) {
	g := cfgdom.NewGraph(0)
	g.AddEdge(0, 1)
	g.AddEdge(1, 2)
	g.AddEdge(2, 3)

	before := g.Analyze()
	if idom, _ := before.ImmediateDominator(3); idom != 2 {
		t.Fatalf("before: idom(3) = %d, want 2", idom)
	}

	g.AddEdge(0, 3) // shortcut: 3 is now directly reachable from the entry
	checkAgainstModel(t, g)

	after := g.Analyze()
	if idom, _ := after.ImmediateDominator(3); idom != 0 {
		t.Errorf("after AddEdge(0,3): idom(3) = %d, want 0", idom)
	}

	g.AddEdge(0, 4) // brand-new node via edge
	checkAgainstModel(t, g)
	if idom, ok := g.Analyze().ImmediateDominator(4); !ok || idom != 0 {
		t.Errorf("after AddEdge(0,4): idom(4) = %d, %v; want 0, true", idom, ok)
	}
}

// Random cross-check: enumerate random graphs (cycles, merges, entry
// back-edges and unreachable nodes arise naturally) and compare the
// library against the node-deletion model.
func TestRandomCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 300; trial++ {
		n := 2 + rng.Intn(8)
		g := cfgdom.NewGraph(rng.Intn(n)) // arbitrary node as entry
		for i := 0; i < n; i++ {
			g.AddNode(i)
		}
		edges := rng.Intn(2 * n)
		for i := 0; i < edges; i++ {
			g.AddEdge(rng.Intn(n), rng.Intn(n)) // may include self-loops and entry back-edges
		}
		checkAgainstModel(t, g)
	}
}
