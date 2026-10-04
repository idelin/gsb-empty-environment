package dom

import (
	"reflect"
	"testing"
)

// diamond builds: 0 -> {1,2}, {1,2} -> 3, 3 -> 4.
func diamond() *Graph {
	g := NewGraph(0)
	g.AddEdge(0, 1)
	g.AddEdge(0, 2)
	g.AddEdge(1, 3)
	g.AddEdge(2, 3)
	g.AddEdge(3, 4)
	return g
}

func TestBasicDominance(t *testing.T) {
	a := NewAnalysis(diamond())

	cases := []struct {
		x, y int
		want bool
	}{
		{0, 0, true}, {0, 4, true}, {3, 4, true},
		{1, 1, true}, {1, 3, false}, {2, 3, false},
		{4, 3, false}, {1, 2, false}, {3, 1, false},
	}
	for _, c := range cases {
		if got := a.Dominates(c.x, c.y); got != c.want {
			t.Errorf("Dominates(%d, %d) = %v, want %v", c.x, c.y, got, c.want)
		}
	}

	for n, want := range map[int]int{1: 0, 2: 0, 3: 0, 4: 3} {
		if got, ok := a.ImmediateDominator(n); !ok || got != want {
			t.Errorf("ImmediateDominator(%d) = %d, %v; want %d, true", n, got, ok, want)
		}
	}
	if _, ok := a.ImmediateDominator(0); ok {
		t.Error("entry must not have an immediate dominator")
	}
}

func TestDominatorTree(t *testing.T) {
	a := NewAnalysis(diamond())
	tree := a.DominatorTree()
	want := map[int][]int{0: {1, 2, 3}, 3: {4}}
	if !reflect.DeepEqual(tree, want) {
		t.Errorf("DominatorTree() = %v, want %v", tree, want)
	}
}

func TestDuplicateEdgesIgnored(t *testing.T) {
	g := diamond()
	if g.AddEdge(0, 1) {
		t.Error("duplicate edge reported as inserted")
	}
	if g.AddEdge(3, 4) {
		t.Error("duplicate edge reported as inserted")
	}
	if got := g.Successors(0); !reflect.DeepEqual(got, []int{1, 2}) {
		t.Errorf("Successors(0) = %v, want [1 2]", got)
	}
	a := NewAnalysis(g)
	if !a.Dominates(3, 4) || a.Dominates(1, 3) {
		t.Error("duplicate edges changed dominance results")
	}
}

func TestUnreachableNodes(t *testing.T) {
	g := diamond()
	g.AddNode(9)     // isolated
	g.AddEdge(8, 8)  // self-loop, still unreachable
	g.AddEdge(9, 10) // edge between unreachable nodes
	a := NewAnalysis(g)

	if got := a.Unreachable(); !reflect.DeepEqual(got, []int{8, 9, 10}) {
		t.Errorf("Unreachable() = %v, want [8 9 10]", got)
	}
	for _, q := range [][2]int{{0, 9}, {9, 9}, {9, 0}, {8, 10}} {
		if a.Dominates(q[0], q[1]) {
			t.Errorf("Dominates(%d, %d) = true for unreachable node", q[0], q[1])
		}
	}
	if a.Reachable(9) || !a.Reachable(4) {
		t.Error("Reachable misclassifies nodes")
	}
}

// cycleGraph: 0 -> 1, 1 -> 2, 2 -> 1 (cycle), 2 -> 2 (self-loop), 1 -> 3.
func cycleGraph() *Graph {
	g := NewGraph(0)
	g.AddEdge(0, 1)
	g.AddEdge(1, 2)
	g.AddEdge(2, 1)
	g.AddEdge(2, 2)
	g.AddEdge(1, 3)
	return g
}

func TestCycleAndSelfLoop(t *testing.T) {
	a := NewAnalysis(cycleGraph())
	for n, want := range map[int]int{1: 0, 2: 1, 3: 1} {
		if got, _ := a.ImmediateDominator(n); got != want {
			t.Errorf("idom(%d) = %d, want %d", n, got, want)
		}
	}
	if !a.Dominates(1, 2) || !a.Dominates(2, 2) {
		t.Error("dominance inside cycle/self-loop wrong")
	}
}

// multiEntryGraph simulates multiple entries: a synthetic super-entry
// 100 fans out to real entry blocks 1 and 2, which join at 3; 3 loops
// back to 1.
func multiEntryGraph() *Graph {
	g := NewGraph(100)
	g.AddEdge(100, 1)
	g.AddEdge(100, 2)
	g.AddEdge(1, 3)
	g.AddEdge(2, 3)
	g.AddEdge(3, 1)
	return g
}

func TestMultiEntrySimulation(t *testing.T) {
	a := NewAnalysis(multiEntryGraph())
	if !a.Dominates(100, 3) {
		t.Error("super-entry must dominate the join block")
	}
	if a.Dominates(1, 3) || a.Dominates(2, 3) {
		t.Error("no single real entry dominates the join block")
	}
	if got, _ := a.ImmediateDominator(3); got != 100 {
		t.Errorf("idom(3) = %d, want 100", got)
	}
	if got, _ := a.ImmediateDominator(1); got != 100 {
		t.Errorf("idom(1) = %d, want 100 (loop back via 3 does not change it)", got)
	}
}

// crossCheck compares Analysis.Dominates against the independent
// removal-based checker for every ordered pair of nodes.
func crossCheck(t *testing.T, g *Graph) {
	t.Helper()
	a := NewAnalysis(g)
	for _, x := range g.Nodes() {
		for _, y := range g.Nodes() {
			got := a.Dominates(x, y)
			want := DominatesByRemoval(g, x, y)
			if got != want {
				t.Errorf("Dominates(%d, %d) = %v, removal check says %v", x, y, got, want)
			}
		}
	}
}

func TestCrossCheckWithRemovalMethod(t *testing.T) {
	t.Run("diamond", func(t *testing.T) { crossCheck(t, diamond()) })
	t.Run("cycle+self-loop", func(t *testing.T) { crossCheck(t, cycleGraph()) })
	t.Run("multi-entry", func(t *testing.T) { crossCheck(t, multiEntryGraph()) })
	t.Run("with-unreachable", func(t *testing.T) {
		g := diamond()
		g.AddEdge(9, 9)
		g.AddEdge(9, 4) // unreachable source pointing into the CFG
		crossCheck(t, g)
	})
}

func TestIncrementalRecompute(t *testing.T) {
	// Chain 0 -> 1 -> 2 -> 3 -> 4: idom(3) is 2.
	g := NewGraph(0)
	g.AddEdge(0, 1)
	g.AddEdge(1, 2)
	g.AddEdge(2, 3)
	g.AddEdge(3, 4)
	a := NewAnalysis(g)

	// Add a shortcut edge 0 -> 3: node 3's idom becomes the entry,
	// while node 4 still hangs off 3 and is unaffected.
	g.AddEdge(0, 3)
	affected := a.Recompute()
	if !reflect.DeepEqual(affected, []int{3}) {
		t.Errorf("affected after AddEdge(0,3) = %v, want [3]", affected)
	}
	if got, _ := a.ImmediateDominator(3); got != 0 {
		t.Errorf("idom(3) = %d, want 0 after shortcut", got)
	}
	if got, _ := a.ImmediateDominator(4); got != 3 {
		t.Errorf("idom(4) = %d, want 3 (unchanged)", got)
	}

	// Remove 3 -> 4: node 4 becomes unreachable and is reported.
	g.RemoveEdge(3, 4)
	affected = a.Recompute()
	if !reflect.DeepEqual(affected, []int{4}) {
		t.Errorf("affected after RemoveEdge(3,4) = %v, want [4]", affected)
	}
	if !reflect.DeepEqual(a.Unreachable(), []int{4}) {
		t.Errorf("Unreachable() = %v, want [4]", a.Unreachable())
	}

	// Restore the chain: everything reachable again, 3 and 4 change.
	g.AddEdge(3, 4)
	g.RemoveEdge(0, 3)
	affected = a.Recompute()
	if !reflect.DeepEqual(affected, []int{3, 4}) {
		t.Errorf("affected after restore = %v, want [3 4]", affected)
	}
	if got, _ := a.ImmediateDominator(3); got != 2 {
		t.Errorf("idom(3) = %d, want 2 after restore", got)
	}
	crossCheck(t, g)
}

func TestDominancePathEvidence(t *testing.T) {
	a := NewAnalysis(diamond())

	path := a.DominancePath(0, 4)
	if !reflect.DeepEqual(path, []int{0, 3, 4}) {
		t.Fatalf("DominancePath(0, 4) = %v, want [0 3 4]", path)
	}
	if err := a.VerifyDominancePath(path); err != nil {
		t.Errorf("valid evidence rejected: %v", err)
	}
	if err := a.VerifyDominancePath([]int{4}); err != nil {
		t.Errorf("single-node self evidence rejected: %v", err)
	}

	bad := [][]int{
		nil,           // empty
		{0, 4},        // 0 is not the idom of 4
		{0, 1, 4},     // 1 is not the idom of 4
		{3, 4, 3},     // 4 does not dominate 3
		{0, 3, 4, 99}, // 99 does not exist
	}
	for _, p := range bad {
		if err := a.VerifyDominancePath(p); err == nil {
			t.Errorf("invalid evidence %v accepted", p)
		}
	}
	if got := a.DominancePath(1, 4); got != nil {
		t.Errorf("DominancePath(1, 4) = %v, want nil (1 does not dominate 4)", got)
	}
}
