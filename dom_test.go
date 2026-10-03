package dom

import (
	"reflect"
	"testing"
)

// diamond builds S -> {A, B} -> T.
func diamond() (*Graph, *Tree) {
	g := NewGraph()
	g.AddEdge("S", "A")
	g.AddEdge("S", "B")
	g.AddEdge("A", "T")
	g.AddEdge("B", "T")
	return g, Compute(g, "S")
}

func mustIdom(t *testing.T, tr *Tree, node, want string) {
	t.Helper()
	got, ok := tr.Idom(node)
	if !ok {
		t.Fatalf("idom(%s): node has no immediate dominator, want %s", node, want)
	}
	if got != want {
		t.Fatalf("idom(%s) = %s, want %s", node, got, want)
	}
}

// assertConsistentWithRecompute verifies the incrementally maintained tree
// matches a from-scratch computation on the same graph.
func assertConsistentWithRecompute(t *testing.T, g *Graph, tr *Tree, entry string) {
	t.Helper()
	fresh := Compute(g, entry)
	for _, n := range g.Nodes() {
		if fresh.Reachable(n) != tr.Reachable(n) {
			t.Fatalf("reachability mismatch for %s after incremental update", n)
		}
		if !fresh.Reachable(n) {
			continue
		}
		if !reflect.DeepEqual(fresh.Dominators(n), tr.Dominators(n)) {
			t.Fatalf("dom(%s) mismatch: incremental %v vs recompute %v",
				n, tr.Dominators(n), fresh.Dominators(n))
		}
		fi, fok := fresh.Idom(n)
		ti, tok := tr.Idom(n)
		if fok != tok || fi != ti {
			t.Fatalf("idom(%s) mismatch: incremental (%s,%v) vs recompute (%s,%v)",
				n, ti, tok, fi, fok)
		}
	}
}

func TestDiamondIdom(t *testing.T) {
	_, tr := diamond()
	mustIdom(t, tr, "A", "S")
	mustIdom(t, tr, "B", "S")
	mustIdom(t, tr, "T", "S") // join point: only S and T dominate T
	if _, ok := tr.Idom("S"); ok {
		t.Fatal("entry node must not have an immediate dominator")
	}
}

func TestAddBypassEdgeKeepsIdom(t *testing.T) {
	g, tr := diamond()
	tr.AddEdge("S", "T") // bypass edge
	mustIdom(t, tr, "T", "S")
	assertConsistentWithRecompute(t, g, tr, "S")
}

func TestRemoveEdgeIdomTransition(t *testing.T) {
	g, tr := diamond()
	tr.RemoveEdge("B", "T") // now every path to T goes through A
	mustIdom(t, tr, "T", "A")
	mustIdom(t, tr, "A", "S")
	assertConsistentWithRecompute(t, g, tr, "S")
}

func TestCounterexampleAvoidsNode(t *testing.T) {
	g, tr := diamond()
	if tr.Dominates("A", "T") {
		t.Fatal("A must not dominate T in the diamond")
	}
	path, ok := tr.Counterexample("A", "T")
	if !ok {
		t.Fatal("expected a counterexample path for A dominating T")
	}
	want := []string{"S", "B", "T"}
	if !reflect.DeepEqual(path, want) {
		t.Fatalf("counterexample = %v, want %v", path, want)
	}
	// Validate the path: starts at entry, ends at T, avoids A, edges exist.
	if path[0] != "S" || path[len(path)-1] != "T" {
		t.Fatalf("invalid counterexample endpoints: %v", path)
	}
	for i := 0; i+1 < len(path); i++ {
		if path[i] == "A" || !g.HasEdge(path[i], path[i+1]) {
			t.Fatalf("invalid counterexample path: %v", path)
		}
	}
}

func TestNoCounterexampleWhenDominanceHolds(t *testing.T) {
	_, tr := diamond()
	if _, ok := tr.Counterexample("S", "T"); ok {
		t.Fatal("S dominates T: no counterexample should exist")
	}
	tr.RemoveEdge("B", "T")
	if !tr.Dominates("A", "T") {
		t.Fatal("after removing B->T, A must dominate T")
	}
	if _, ok := tr.Counterexample("A", "T"); ok {
		t.Fatal("A dominates T after edge removal: no counterexample should exist")
	}
}

func TestUnreachableNode(t *testing.T) {
	g, tr := diamond()
	g.AddNode("Z")
	if tr.Reachable("Z") {
		t.Fatal("Z must be unreachable")
	}
	if _, ok := tr.Idom("Z"); ok {
		t.Fatal("unreachable node must not have an idom")
	}
	if _, ok := tr.Counterexample("A", "Z"); ok {
		t.Fatal("no counterexample for unreachable target")
	}
	// Adding an edge to Z makes it reachable with idom S.
	tr.AddEdge("S", "Z")
	mustIdom(t, tr, "Z", "S")
	assertConsistentWithRecompute(t, g, tr, "S")
	// Removing it again makes Z unreachable once more.
	tr.RemoveEdge("S", "Z")
	if tr.Reachable("Z") {
		t.Fatal("Z must be unreachable after removing its only incoming edge")
	}
	assertConsistentWithRecompute(t, g, tr, "S")
}

func TestChainAfterBypassRemoval(t *testing.T) {
	// S -> A -> T plus bypass S -> T; removing the bypass restores idom(T)=A.
	g := NewGraph()
	g.AddEdge("S", "A")
	g.AddEdge("A", "T")
	g.AddEdge("S", "T")
	tr := Compute(g, "S")
	mustIdom(t, tr, "T", "S")
	tr.RemoveEdge("S", "T")
	mustIdom(t, tr, "T", "A")
	assertConsistentWithRecompute(t, g, tr, "S")
}
