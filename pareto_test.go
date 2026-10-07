package paretopath

import (
	"math/rand"
	"reflect"
	"testing"
)

func mustGraph(t *testing.T, n int, edges []Edge) *Graph {
	t.Helper()
	g, err := NewGraph(n)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	for _, e := range edges {
		if err := g.AddEdge(e.From, e.To, e.Time, e.Cost); err != nil {
			t.Fatalf("AddEdge: %v", err)
		}
	}
	return g
}

func TestParetoPathsTradeoff(t *testing.T) {
	g := mustGraph(t, 4, []Edge{
		{From: 0, To: 1, Time: 1, Cost: 9},
		{From: 1, To: 3, Time: 1, Cost: 9},
		{From: 0, To: 2, Time: 9, Cost: 1},
		{From: 2, To: 3, Time: 9, Cost: 1},
		{From: 0, To: 3, Time: 8, Cost: 8},
	})
	got, err := ParetoPaths(g, 0, 3)
	if err != nil {
		t.Fatalf("ParetoPaths: %v", err)
	}
	want := []Path{
		{Time: 2, Cost: 18, Edges: []Edge{{0, 1, 1, 9}, {1, 3, 1, 9}}},
		{Time: 8, Cost: 8, Edges: []Edge{{0, 3, 8, 8}}},
		{Time: 18, Cost: 2, Edges: []Edge{{0, 2, 9, 1}, {2, 3, 9, 1}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for _, p := range got[1:] {
		if p.Time+p.Cost == got[0].Time+got[0].Cost {
			continue
		}
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 Pareto paths, got %d", len(got))
	}
}

func TestLexicographicTieBreak(t *testing.T) {
	g := mustGraph(t, 4, []Edge{
		{From: 0, To: 2, Time: 5, Cost: 5},
		{From: 2, To: 3, Time: 5, Cost: 5},
		{From: 0, To: 1, Time: 5, Cost: 5},
		{From: 1, To: 3, Time: 5, Cost: 5},
	})
	got, err := ParetoPaths(g, 0, 3)
	if err != nil {
		t.Fatalf("ParetoPaths: %v", err)
	}
	want := []Path{
		{Time: 10, Cost: 10, Edges: []Edge{{0, 1, 5, 5}, {1, 3, 5, 5}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestDominatedPathExcluded(t *testing.T) {
	g := mustGraph(t, 3, []Edge{
		{From: 0, To: 1, Time: 3, Cost: 3},
		{From: 1, To: 2, Time: 3, Cost: 3},
		{From: 0, To: 2, Time: 7, Cost: 7},
	})
	got, err := ParetoPaths(g, 0, 2)
	if err != nil {
		t.Fatalf("ParetoPaths: %v", err)
	}
	if len(got) != 1 || got[0].Time != 6 || got[0].Cost != 6 {
		t.Fatalf("got %+v", got)
	}
}

func TestSimplePathConstraint(t *testing.T) {
	g := mustGraph(t, 4, []Edge{
		{From: 0, To: 1, Time: 1, Cost: 1},
		{From: 1, To: 2, Time: 1, Cost: 1},
		{From: 2, To: 1, Time: 1, Cost: 1},
		{From: 2, To: 3, Time: 1, Cost: 1},
	})
	got, err := ParetoPaths(g, 0, 3)
	if err != nil {
		t.Fatalf("ParetoPaths: %v", err)
	}
	if len(got) != 1 || got[0].Time != 3 || got[0].Cost != 3 {
		t.Fatalf("got %+v, want single simple path of time 3 cost 3", got)
	}
}

func TestSrcEqualsDst(t *testing.T) {
	g := mustGraph(t, 2, []Edge{{From: 0, To: 1, Time: 1, Cost: 1}})
	got, err := ParetoPaths(g, 1, 1)
	if err != nil {
		t.Fatalf("ParetoPaths: %v", err)
	}
	if len(got) != 1 || got[0].Time != 0 || got[0].Cost != 0 || len(got[0].Edges) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestNoPath(t *testing.T) {
	g := mustGraph(t, 3, []Edge{{From: 0, To: 1, Time: 1, Cost: 1}})
	got, err := ParetoPaths(g, 0, 2)
	if err != nil {
		t.Fatalf("ParetoPaths: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want empty", got)
	}
}

func TestValidation(t *testing.T) {
	if _, err := NewGraph(0); err == nil {
		t.Fatal("expected error for 0 vertices")
	}
	if _, err := NewGraph(MaxVertices + 1); err == nil {
		t.Fatal("expected error for > MaxVertices vertices")
	}
	g := mustGraph(t, 2, nil)
	if err := g.AddEdge(0, 1, -1, 0); err == nil {
		t.Fatal("expected error for negative time")
	}
	if err := g.AddEdge(0, 1, 0, -1); err == nil {
		t.Fatal("expected error for negative cost")
	}
	if err := g.AddEdge(0, 5, 1, 1); err == nil {
		t.Fatal("expected error for out-of-range vertex")
	}
	if _, err := ParetoPaths(g, 0, 3); err == nil {
		t.Fatal("expected error for out-of-range endpoint")
	}
}

func TestMatchesBruteForce(t *testing.T) {
	for seed := int64(0); seed < 400; seed++ {
		rng := rand.New(rand.NewSource(seed))
		n := 2 + rng.Intn(MaxVertices-1)
		g, err := NewGraph(n)
		if err != nil {
			t.Fatalf("NewGraph: %v", err)
		}
		for from := 0; from < n; from++ {
			for to := 0; to < n; to++ {
				if from == to || rng.Float64() > 0.4 {
					continue
				}
				if err := g.AddEdge(from, to, int64(rng.Intn(16)), int64(rng.Intn(16))); err != nil {
					t.Fatalf("AddEdge: %v", err)
				}
			}
		}
		src, dst := rng.Intn(n), rng.Intn(n)

		fast, err := ParetoPaths(g, src, dst)
		if err != nil {
			t.Fatalf("seed %d: ParetoPaths: %v", seed, err)
		}
		slow, err := BruteForceParetoPaths(g, src, dst)
		if err != nil {
			t.Fatalf("seed %d: BruteForceParetoPaths: %v", seed, err)
		}
		if !reflect.DeepEqual(fast, slow) {
			t.Fatalf("seed %d (n=%d, src=%d, dst=%d):\nfast=%+v\nslow=%+v", seed, n, src, dst, fast, slow)
		}
	}
}
