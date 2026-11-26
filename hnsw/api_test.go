package hnsw

import (
	"strings"
	"testing"
)

func TestConfigValidation(t *testing.T) {
	if _, err := NewGraph[vec2](Config[vec2]{M: 0, Distance: euclid}); err == nil {
		t.Fatal("M=0 must be rejected")
	}
	if _, err := NewGraph[vec2](Config[vec2]{M: 4}); err == nil {
		t.Fatal("nil Distance must be rejected")
	}
	g := newTestGraph(t, 3, true)
	if g.MaxDegree(0) != 6 || g.MaxDegree(2) != 3 {
		t.Fatalf("degree bounds = (%d,%d), want (6,3)", g.MaxDegree(0), g.MaxDegree(2))
	}
	if !g.KeepPrunedConnections() {
		t.Fatal("KeepPrunedConnections flag not retained")
	}
}

func TestMutationValidation(t *testing.T) {
	g := newTestGraph(t, 2, false)
	addNode(t, g, 0, vec2{}, 1)
	addNode(t, g, 1, vec2{1, 0}, 1)
	addNode(t, g, 2, vec2{2, 0}, 0)

	if err := g.AddNode(0, vec2{}, 0); err == nil {
		t.Fatal("duplicate AddNode must fail")
	}
	if err := g.AddNode(-1, vec2{}, 0); err == nil {
		t.Fatal("negative id must fail")
	}
	if err := g.AddNode(3, vec2{}, -1); err == nil {
		t.Fatal("negative level must fail")
	}
	if err := g.AddEdge(0, 0, 0); err == nil {
		t.Fatal("self edge must fail")
	}
	if err := g.AddEdge(0, 0, 9); err == nil {
		t.Fatal("edge to unknown node must fail")
	}
	if err := g.AddEdge(1, 0, 2); err == nil {
		t.Fatal("edge beyond node level must fail")
	}

	if err := g.SetEntry(2); err == nil {
		t.Fatal("entry below highest layer must fail")
	}
	if err := g.SetEntry(1); err != nil {
		t.Fatalf("SetEntry on a top-layer node: %v", err)
	}

	if _, err := g.Neighbors(9, 0); err == nil {
		t.Fatal("Neighbors of unknown node must fail")
	}
	if _, err := g.Neighbors(0, 5); err == nil {
		t.Fatal("Neighbors beyond layer must fail")
	}
	if _, err := g.Level(9); err == nil {
		t.Fatal("Level of unknown node must fail")
	}
	if _, err := g.Node(9); err == nil {
		t.Fatal("Node lookup of unknown id must fail")
	}
	if _, err := g.KNN(vec2{}, 0, 4); err == nil {
		t.Fatal("k=0 must fail")
	}
	if _, err := g.KNN(vec2{}, 2, 0); err == nil {
		t.Fatal("ef=0 must fail")
	}

	if _, err := g.Delete([]int{0, 9}); err == nil {
		t.Fatal("batch containing an unknown id must fail without mutating")
	}
	exists, deleted := g.IsDeleted(0)
	if !exists || deleted {
		t.Fatal("failed Delete batch must leave node 0 alive")
	}

	// Deleted nodes cannot serve as edge or query endpoints.
	if _, err := g.Delete([]int{2}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := g.AddEdge(0, 0, 2); err == nil {
		t.Fatal("edge to deleted node must fail")
	}
	if _, err := g.KNNFrom(2, 1, 4); err == nil {
		t.Fatal("KNNFrom deleted node must fail")
	}
	if err := g.SetEntry(2); err == nil {
		t.Fatal("SetEntry to deleted node must fail")
	}
}

func TestDeterministicEqualDistanceRanking(t *testing.T) {
	g := newTestGraph(t, 2, false)
	addNode(t, g, 0, vec2{0, 0}, 0)
	addNode(t, g, 1, vec2{1, 0}, 0)
	addNode(t, g, 2, vec2{-1, 0}, 0)
	addNode(t, g, 3, vec2{0, 1}, 0)
	addNode(t, g, 4, vec2{0, -1}, 0)
	for id := 1; id <= 4; id++ {
		addEdge(t, g, 0, 0, id)
	}

	hits, err := g.KNN(vec2{0, 0}, 4, 8)
	if err != nil {
		t.Fatalf("KNN: %v", err)
	}
	got := resultIDs(hits)
	want := []int{0, 1, 2, 3}
	if len(got) != len(want) {
		t.Fatalf("hits = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("equal-distance tie break = %v, want %v", got, want)
		}
	}

	// Invariant error aggregates and renders all violations.
	g.nodes[1].conns[0] = append(g.nodes[1].conns[0], 99)
	err = g.CheckInvariants()
	if err == nil {
		t.Fatal("corrupted graph must fail invariant check")
	}
	if !strings.Contains(err.Error(), "missing node") {
		t.Fatalf("invariant error missing detail: %v", err)
	}
}
