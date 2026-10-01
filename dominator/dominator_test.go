package dominator

import (
	"errors"
	"reflect"
	"testing"
)

// newDiamond builds the acceptance diamond: S -> A, S -> B, A -> T, B -> T.
func newDiamond() *Graph {
	g := New("S")
	g.AddEdge("S", "A")
	g.AddEdge("S", "B")
	g.AddEdge("A", "T")
	g.AddEdge("B", "T")
	return g
}

func mustIdom(t *testing.T, g *Graph, n string) string {
	t.Helper()
	d, ok := g.Idom(n)
	if !ok {
		t.Fatalf("node %s unexpectedly unreachable", n)
	}
	return d
}

// Acceptance: in the diamond, T's immediate dominator is S.
func TestDiamondIdom(t *testing.T) {
	g := newDiamond()
	want := map[string]string{"S": "S", "A": "S", "B": "S", "T": "S"}
	for n, w := range want {
		if got := mustIdom(t, g, n); got != w {
			t.Errorf("idom(%s) = %s, want %s", n, got, w)
		}
	}
}

// Acceptance: adding the bypass edge S->T keeps idom(T) == S.
func TestAddBypassEdgeKeepsIdom(t *testing.T) {
	g := newDiamond()
	g.AddEdge("S", "T")
	if got := mustIdom(t, g, "T"); got != "S" {
		t.Fatalf("after adding S->T: idom(T) = %s, want S", got)
	}
}

// Acceptance: removing B->T makes idom(T) jump to A.
func TestRemoveEdgeIdomTransition(t *testing.T) {
	g := newDiamond()
	g.RemoveEdge("B", "T")
	if got := mustIdom(t, g, "T"); got != "A" {
		t.Fatalf("after removing B->T: idom(T) = %s, want A", got)
	}
	if !g.Dominates("A", "T") {
		t.Fatal("after removing B->T: A should dominate T")
	}
}

// Acceptance: a valid counterexample path reaches T without going through A.
func TestCounterexamplePath(t *testing.T) {
	g := newDiamond()
	path, err := g.Counterexample("A", "T")
	if err != nil {
		t.Fatalf("Counterexample: %v", err)
	}
	if path[0] != "S" || path[len(path)-1] != "T" {
		t.Fatalf("path %v does not run from S to T", path)
	}
	for _, n := range path {
		if n == "A" {
			t.Fatalf("path %v passes through A", path)
		}
	}
	// Every step must be a real edge of the graph.
	for i := 0; i+1 < len(path); i++ {
		if _, ok := g.succ[path[i]][path[i+1]]; !ok {
			t.Fatalf("path %v uses non-edge %s->%s", path, path[i], path[i+1])
		}
	}
	if g.Dominates("A", "T") {
		t.Fatal("A should not dominate T in the diamond")
	}
}

// No counterexample exists when the domination actually holds.
func TestCounterexampleNoneWhenDominates(t *testing.T) {
	g := newDiamond()
	if _, err := g.Counterexample("S", "T"); !errors.Is(err, ErrNoPath) {
		t.Fatalf("want ErrNoPath for true dominator S, got %v", err)
	}
	g.RemoveEdge("B", "T")
	if _, err := g.Counterexample("A", "T"); !errors.Is(err, ErrNoPath) {
		t.Fatalf("after removing B->T, want ErrNoPath for A, got %v", err)
	}
}

// The full acceptance scenario as one dynamic sequence.
func TestAcceptanceSequence(t *testing.T) {
	g := newDiamond()
	if got := mustIdom(t, g, "T"); got != "S" {
		t.Fatalf("step 1: idom(T) = %s, want S", got)
	}
	g.AddEdge("S", "T")
	if got := mustIdom(t, g, "T"); got != "S" {
		t.Fatalf("step 2: idom(T) = %s, want S", got)
	}
	g.RemoveEdge("S", "T") // restore the plain diamond
	g.RemoveEdge("B", "T")
	if got := mustIdom(t, g, "T"); got != "A" {
		t.Fatalf("step 3: idom(T) = %s, want A", got)
	}
}

// Incremental updates must match a cold recomputation from scratch.
func TestIncrementalMatchesColdRecompute(t *testing.T) {
	ops := []struct {
		add      bool
		from, to string
	}{
		{true, "S", "A"}, {true, "S", "B"}, {true, "A", "T"}, {true, "B", "T"},
		{true, "S", "T"}, {false, "B", "T"}, {true, "A", "B"}, {false, "S", "T"},
		{true, "B", "A"}, {false, "A", "B"},
	}
	inc := New("S")
	cold := New("S")
	for _, op := range ops {
		if op.add {
			inc.AddEdge(op.from, op.to)
			cold.AddEdge(op.from, op.to)
		} else {
			inc.RemoveEdge(op.from, op.to)
			cold.RemoveEdge(op.from, op.to)
		}
		// Force cold recomputation by rebuilding the graph from its edges.
		fresh := New("S")
		for from, tos := range cold.succ {
			for to := range tos {
				fresh.AddEdge(from, to)
			}
		}
		if !reflect.DeepEqual(inc.idom, fresh.idom) {
			t.Fatalf("after op %+v: incremental %v != cold %v", op, inc.idom, fresh.idom)
		}
	}
}

// Unreachable nodes have no idom; deletion that disconnects a node drops it.
func TestUnreachableAfterRemoval(t *testing.T) {
	g := New("S")
	g.AddEdge("S", "X")
	g.AddEdge("X", "Y")
	g.RemoveEdge("S", "X")
	if _, ok := g.Idom("Y"); ok {
		t.Fatal("Y should be unreachable after removing S->X")
	}
	if g.Dominates("S", "Y") {
		t.Fatal("unreachable Y must not report dominators")
	}
}

// Dominators set walks the full idom chain.
func TestDominatorsChain(t *testing.T) {
	g := New("S")
	g.AddEdge("S", "A")
	g.AddEdge("A", "B")
	g.AddEdge("B", "T")
	set, ok := g.Dominators("T")
	if !ok {
		t.Fatal("T should be reachable")
	}
	want := map[string]bool{"S": true, "A": true, "B": true, "T": true}
	if !reflect.DeepEqual(set, want) {
		t.Fatalf("Dominators(T) = %v, want %v", set, want)
	}
}

// Idempotent mutations are no-ops.
func TestDuplicateAndMissingEdges(t *testing.T) {
	g := newDiamond()
	before := g.idom
	g.AddEdge("S", "A")    // duplicate
	g.RemoveEdge("A", "S") // missing
	if !reflect.DeepEqual(g.idom, before) {
		t.Fatal("no-op mutations changed the dominator tree")
	}
}
