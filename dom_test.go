package dom

import (
	"reflect"
	"testing"
)

func mustIDom(t *testing.T, a *Analysis, n, want int) {
	t.Helper()
	got, ok := a.IDom(n)
	if !ok || got != want {
		t.Fatalf("idom(%d) = %d, %v; want %d, true", n, got, ok, want)
	}
}

// assertSameAnalysis checks that two analyses agree on every query.
func assertSameAnalysis(t *testing.T, g *Graph, got, want *Analysis) {
	t.Helper()
	if !reflect.DeepEqual(got.Unreachable(), want.Unreachable()) {
		t.Fatalf("unreachable = %v; want %v", got.Unreachable(), want.Unreachable())
	}
	for _, n := range g.Nodes() {
		if got.Reachable(n) != want.Reachable(n) {
			t.Fatalf("Reachable(%d) mismatch", n)
		}
		ig, og := got.IDom(n)
		iw, ow := want.IDom(n)
		if ig != iw || og != ow {
			t.Fatalf("IDom(%d) = %d,%v; want %d,%v", n, ig, og, iw, ow)
		}
		for _, d := range g.Nodes() {
			if got.Dominates(d, n) != want.Dominates(d, n) {
				t.Fatalf("Dominates(%d, %d) mismatch", d, n)
			}
		}
	}
}

func TestDiamond(t *testing.T) {
	g := NewGraph()
	g.AddEntry(0)
	for _, e := range [][2]int{{0, 1}, {0, 2}, {1, 3}, {2, 3}, {3, 4}} {
		g.AddEdge(e[0], e[1])
	}
	a := g.Analyze()
	mustIDom(t, a, 1, 0)
	mustIDom(t, a, 2, 0)
	mustIDom(t, a, 3, 0)
	mustIDom(t, a, 4, 3)
	if !a.Dominates(0, 3) || a.Dominates(1, 3) || a.Dominates(2, 3) {
		t.Fatal("wrong domination in diamond")
	}
	if !a.Dominates(3, 3) {
		t.Fatal("domination must be reflexive for reachable nodes")
	}
	if err := g.Verify(a); err != nil {
		t.Fatal(err)
	}
}

func TestLoop(t *testing.T) {
	g := NewGraph()
	g.AddEntry(0)
	// 0 -> 1 -> 2 -> 3, back edge 3 -> 1, exit 2 -> 4
	for _, e := range [][2]int{{0, 1}, {1, 2}, {2, 3}, {3, 1}, {2, 4}} {
		g.AddEdge(e[0], e[1])
	}
	a := g.Analyze()
	mustIDom(t, a, 1, 0)
	mustIDom(t, a, 2, 1)
	mustIDom(t, a, 3, 2)
	mustIDom(t, a, 4, 2)
	if !a.Dominates(1, 3) {
		t.Fatal("loop header must dominate loop body")
	}
	if a.Dominates(3, 1) {
		t.Fatal("back edge target is not dominated by the back edge source")
	}
	if err := g.Verify(a); err != nil {
		t.Fatal(err)
	}
}

func TestSelfLoop(t *testing.T) {
	g := NewGraph()
	g.AddEntry(0)
	for _, e := range [][2]int{{0, 1}, {1, 1}, {1, 2}, {2, 2}} {
		g.AddEdge(e[0], e[1])
	}
	a := g.Analyze()
	mustIDom(t, a, 1, 0)
	mustIDom(t, a, 2, 1)
	if got := a.Dominators(1); !reflect.DeepEqual(got, []int{0, 1}) {
		t.Fatalf("Dominators(1) = %v", got)
	}
	if err := g.Verify(a); err != nil {
		t.Fatal(err)
	}
}

func TestDuplicateEdgesIgnored(t *testing.T) {
	g := NewGraph()
	g.AddEntry(0)
	if !g.AddEdge(0, 1) {
		t.Fatal("first insert must succeed")
	}
	if g.AddEdge(0, 1) {
		t.Fatal("duplicate insert must be ignored")
	}
	g.AddEdge(1, 2)
	if got := g.Predecessors(1); len(got) != 1 {
		t.Fatalf("Predecessors(1) = %v", got)
	}
	a := g.Analyze()
	mustIDom(t, a, 1, 0)
	mustIDom(t, a, 2, 1)
}

func TestUnreachableNodes(t *testing.T) {
	g := NewGraph()
	g.AddEntry(0)
	g.AddEdge(0, 1)
	g.AddEdge(5, 6) // disconnected component
	g.AddNode(7)    // isolated node
	a := g.Analyze()
	if got := a.Unreachable(); !reflect.DeepEqual(got, []int{5, 6, 7}) {
		t.Fatalf("Unreachable() = %v", got)
	}
	if a.Dominates(5, 6) || a.Dominates(0, 6) || a.Dominates(6, 6) {
		t.Fatal("unreachable nodes carry no domination")
	}
	if _, ok := a.IDom(6); ok {
		t.Fatal("unreachable node must have no idom")
	}
	if err := g.Verify(a); err != nil {
		t.Fatal(err)
	}
}

func TestMultiEntry(t *testing.T) {
	g := NewGraph()
	g.AddEntry(0)
	g.AddEntry(5)
	// Component of entry 0, component of entry 5, and node 3 fed by both.
	for _, e := range [][2]int{{0, 1}, {1, 3}, {5, 6}, {6, 3}, {3, 4}} {
		g.AddEdge(e[0], e[1])
	}
	a := g.Analyze()
	mustIDom(t, a, 1, 0)
	mustIDom(t, a, 6, 5)
	// 3 is reachable from both entries: only itself and the virtual
	// super-root dominate it, so it has no idom and is a forest root.
	if _, ok := a.IDom(3); ok {
		t.Fatal("node 3 must not have an idom")
	}
	mustIDom(t, a, 4, 3)
	tree := a.Tree()
	if !reflect.DeepEqual(tree.Roots, []int{0, 3, 5}) {
		t.Fatalf("tree roots = %v", tree.Roots)
	}
	if !reflect.DeepEqual(tree.Children[3], []int{4}) {
		t.Fatalf("children of 3 = %v", tree.Children[3])
	}
	if err := g.Verify(a); err != nil {
		t.Fatal(err)
	}
}

func TestIncrementalAddEdge(t *testing.T) {
	g := NewGraph()
	g.AddEntry(0)
	for _, e := range [][2]int{{0, 1}, {0, 2}, {1, 3}, {2, 3}, {3, 4}} {
		g.AddEdge(e[0], e[1])
	}
	a := g.Analyze()
	// Insert 1 -> 4: node 4 gains a path that avoids 3.
	res := a.AddEdge(1, 4)
	if !reflect.DeepEqual(res.Affected, []int{4}) {
		t.Fatalf("Affected = %v", res.Affected)
	}
	assertSameAnalysis(t, g, res.Analysis, g.Analyze())
	mustIDom(t, res.Analysis, 4, 0)
	if res.Analysis.Dominates(3, 4) {
		t.Fatal("3 must no longer dominate 4")
	}
	if err := g.Verify(res.Analysis); err != nil {
		t.Fatal(err)
	}
}

func TestIncrementalRemoveEdge(t *testing.T) {
	g := NewGraph()
	g.AddEntry(0)
	for _, e := range [][2]int{{0, 1}, {0, 2}, {1, 3}, {2, 3}, {3, 4}} {
		g.AddEdge(e[0], e[1])
	}
	a := g.Analyze()
	res := a.RemoveEdge(1, 3)
	assertSameAnalysis(t, g, res.Analysis, g.Analyze())
	mustIDom(t, res.Analysis, 3, 2)
	if err := g.Verify(res.Analysis); err != nil {
		t.Fatal(err)
	}
}

func TestIncrementalReachabilityChange(t *testing.T) {
	g := NewGraph()
	g.AddEntry(0)
	for _, e := range [][2]int{{0, 1}, {1, 2}, {2, 3}, {3, 1}, {1, 4}} {
		g.AddEdge(e[0], e[1])
	}
	a := g.Analyze()

	// Cutting 1 -> 2 strands the loop {2, 3}.
	rm := a.RemoveEdge(1, 2)
	if !reflect.DeepEqual(rm.BecameUnreachable, []int{2, 3}) {
		t.Fatalf("BecameUnreachable = %v", rm.BecameUnreachable)
	}
	assertSameAnalysis(t, g, rm.Analysis, g.Analyze())
	if err := g.Verify(rm.Analysis); err != nil {
		t.Fatal(err)
	}

	// Restoring the edge brings the loop back.
	add := rm.Analysis.AddEdge(1, 2)
	if !reflect.DeepEqual(add.BecameReachable, []int{2, 3}) {
		t.Fatalf("BecameReachable = %v", add.BecameReachable)
	}
	assertSameAnalysis(t, g, add.Analysis, g.Analyze())
	mustIDom(t, add.Analysis, 2, 1)
	mustIDom(t, add.Analysis, 3, 2)
	if err := g.Verify(add.Analysis); err != nil {
		t.Fatal(err)
	}
}

func TestIncrementalNoops(t *testing.T) {
	g := NewGraph()
	g.AddEntry(0)
	g.AddEdge(0, 1)
	g.AddEdge(8, 9) // unreachable component
	a := g.Analyze()

	if res := a.AddEdge(0, 1); res.Analysis != a || len(res.Affected) != 0 {
		t.Fatal("duplicate edge must be a no-op")
	}
	if res := a.AddEdge(8, 1); res.Analysis != a || len(res.Affected) != 0 {
		t.Fatal("edge from an unreachable node must be a no-op")
	}
	if res := a.RemoveEdge(1, 2); res.Analysis != a || len(res.Affected) != 0 {
		t.Fatal("removing a missing edge must be a no-op")
	}
}

func TestIncrementalSequence(t *testing.T) {
	g := NewGraph()
	g.AddEntry(0)
	g.AddEntry(9)
	for _, e := range [][2]int{{0, 1}, {1, 2}, {2, 1}, {9, 2}, {2, 3}, {3, 3}} {
		g.AddEdge(e[0], e[1])
	}
	a := g.Analyze()
	updates := []struct {
		add      bool
		from, to int
	}{
		{false, 9, 2},
		{true, 3, 4},
		{true, 0, 4},
		{false, 1, 2},
		{true, 4, 2},
		{false, 2, 3},
	}
	for i, up := range updates {
		var res *UpdateResult
		if up.add {
			res = a.AddEdge(up.from, up.to)
		} else {
			res = a.RemoveEdge(up.from, up.to)
		}
		a = res.Analysis
		assertSameAnalysis(t, g, a, g.Analyze())
		if err := g.Verify(a); err != nil {
			t.Fatalf("update %d: %v", i, err)
		}
	}
}

func TestEvidence(t *testing.T) {
	g := NewGraph()
	g.AddEntry(0)
	for _, e := range [][2]int{{0, 1}, {1, 2}, {2, 3}, {3, 1}, {1, 4}} {
		g.AddEdge(e[0], e[1])
	}
	a := g.Analyze()

	chain, err := a.Evidence(1, 4)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(chain, []int{4, 1}) {
		t.Fatalf("chain = %v", chain)
	}
	if err := g.VerifyEvidence(chain); err != nil {
		t.Fatal(err)
	}

	chain, err = a.Evidence(0, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(chain, []int{3, 2, 1, 0}) {
		t.Fatalf("chain = %v", chain)
	}
	if err := g.VerifyEvidence(chain); err != nil {
		t.Fatal(err)
	}

	if _, err := a.Evidence(2, 1); err == nil {
		t.Fatal("2 does not dominate 1, evidence must fail")
	}
	// A tampered chain that skips the true idom must be rejected.
	if err := g.VerifyEvidence([]int{4, 0}); err == nil {
		t.Fatal("tampered evidence accepted")
	}
	// A chain that does not even connect must be rejected.
	if err := g.VerifyEvidence([]int{3, 4}); err == nil {
		t.Fatal("bogus evidence accepted")
	}
}

func TestEvidenceAfterIncrementalUpdate(t *testing.T) {
	g := NewGraph()
	g.AddEntry(0)
	for _, e := range [][2]int{{0, 1}, {0, 2}, {1, 3}, {2, 3}} {
		g.AddEdge(e[0], e[1])
	}
	a := g.Analyze()
	// Reroute node 2 through node 1: remove 0->2, then add 1->2.
	a = a.RemoveEdge(0, 2).Analysis
	a = a.AddEdge(1, 2).Analysis
	chain, err := a.Evidence(1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.VerifyEvidence(chain); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(chain, []int{2, 1}) {
		t.Fatalf("chain = %v", chain)
	}
}
