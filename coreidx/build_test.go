package coreidx

import (
	"errors"
	"reflect"
	"testing"
)

func mustAddEdge(t *testing.T, g *Graph, u, v int64) {
	t.Helper()
	if err := g.AddEdge(u, v); err != nil {
		t.Fatalf("AddEdge(%d, %d): %v", u, v, err)
	}
}

func clique(t *testing.T, g *Graph, ids ...int64) {
	t.Helper()
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			mustAddEdge(t, g, ids[i], ids[j])
		}
	}
}

func coresOf(t *testing.T, g *Graph, ids ...int64) map[int64]int {
	t.Helper()
	out := make(map[int64]int)
	for _, id := range ids {
		c, ok := g.Core(id)
		if !ok {
			t.Fatalf("node %d has no core value", id)
		}
		out[id] = c
	}
	return out
}

func TestBuildKnownGraphs(t *testing.T) {
	t.Run("triangle", func(t *testing.T) {
		g := New()
		clique(t, g, 1, 2, 3)
		g.Build()
		got := coresOf(t, g, 1, 2, 3)
		want := map[int64]int{1: 2, 2: 2, 3: 2}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v want %v", got, want)
		}
	})
	t.Run("star", func(t *testing.T) {
		g := New()
		for i := int64(2); i <= 5; i++ {
			mustAddEdge(t, g, 1, i)
		}
		g.Build()
		got := coresOf(t, g, 1, 2, 3, 4, 5)
		want := map[int64]int{1: 1, 2: 1, 3: 1, 4: 1, 5: 1}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v want %v", got, want)
		}
	})
	t.Run("clique4", func(t *testing.T) {
		g := New()
		clique(t, g, 10, 20, 30, 40)
		g.Build()
		got := coresOf(t, g, 10, 20, 30, 40)
		want := map[int64]int{10: 3, 20: 3, 30: 3, 40: 3}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v want %v", got, want)
		}
	})
	t.Run("isolated node kept with core 0", func(t *testing.T) {
		g := New()
		mustAddEdge(t, g, 1, 2)
		g.AddNode(99)
		g.Build()
		if !g.HasNode(99) {
			t.Fatal("isolated node was dropped")
		}
		got := coresOf(t, g, 1, 2, 99)
		want := map[int64]int{1: 1, 2: 1, 99: 0}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v want %v", got, want)
		}
	})
	t.Run("empty graph", func(t *testing.T) {
		g := New()
		res := g.Build()
		if len(res.Steps) != 0 {
			t.Fatalf("expected no steps, got %v", res.Steps)
		}
	})
}

// The peeling order must be deterministic and the recorded remaining degrees
// must reproduce the core numbers via
// core(vi) = max(d(vi), max{core(vj) : j<i, vj adjacent vi}).
func TestPeelOrderDeterministicAndVerifiable(t *testing.T) {
	build := func() *Graph {
		g := New()
		clique(t, g, 1, 2, 3, 4) // 3-core
		clique(t, g, 5, 6, 7)    // triangle
		mustAddEdge(t, g, 4, 5)  // bridge
		mustAddEdge(t, g, 7, 8)  // tail
		mustAddEdge(t, g, 8, 9)
		g.AddNode(100) // isolated
		g.AddNode(50)  // isolated
		return g
	}
	g1, g2 := build(), build()
	r1, r2 := g1.Build(), g2.Build()
	if !reflect.DeepEqual(r1.Steps, r2.Steps) {
		t.Fatalf("non-deterministic peel order:\n%v\n%v", r1.Steps, r2.Steps)
	}

	// verify the recorded remaining degrees reproduce the core numbers
	coreOf := map[int64]int{}
	peeled := map[int64]bool{}
	for _, s := range r1.Steps {
		maxPrev := 0
		for nb := range g1.adj[s.Node] {
			if peeled[nb] && coreOf[nb] > maxPrev {
				maxPrev = coreOf[nb]
			}
		}
		want := s.Degree
		if maxPrev > want {
			want = maxPrev
		}
		if s.Core != want {
			t.Fatalf("node %d: core %d does not match max(deg %d, prev %d)",
				s.Node, s.Core, s.Degree, maxPrev)
		}
		coreOf[s.Node] = s.Core
		peeled[s.Node] = true
	}
	if len(peeled) != len(g1.adj) {
		t.Fatalf("peeled %d nodes, graph has %d", len(peeled), len(g1.adj))
	}
}

func TestConstructionRules(t *testing.T) {
	g := New()
	mustAddEdge(t, g, 1, 2)
	mustAddEdge(t, g, 1, 2) // duplicate add is idempotent
	mustAddEdge(t, g, 2, 1) // also in the other direction
	if n := len(g.Edges()); n != 1 {
		t.Fatalf("duplicate adds: got %d edges, want 1", n)
	}

	if err := g.AddEdge(7, 7); !errors.Is(err, ErrSelfLoop) {
		t.Fatalf("self-loop: got %v, want ErrSelfLoop", err)
	}
	if g.HasNode(7) {
		t.Fatal("rejected self-loop created a node")
	}

	g.DeleteEdge(3, 4) // deleting a missing edge is a no-op
	g.DeleteEdge(2, 1) // delete the real edge (reversed direction)
	if n := len(g.Edges()); n != 0 {
		t.Fatalf("got %d edges, want 0", n)
	}
	if !g.HasNode(1) || !g.HasNode(2) {
		t.Fatal("endpoints must be kept after their last edge is deleted")
	}

	g.Build()
	got := coresOf(t, g, 1, 2)
	if got[1] != 0 || got[2] != 0 {
		t.Fatalf("isolated endpoints should have core 0, got %v", got)
	}
}

func TestApplyBatchRequiresBuild(t *testing.T) {
	g := New()
	mustAddEdge(t, g, 1, 2)
	if _, err := g.ApplyBatch([]Op{AddEdgeOp(2, 3)}); !errors.Is(err, ErrNotBuilt) {
		t.Fatalf("got %v, want ErrNotBuilt", err)
	}
}
