package main

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
	"time"
)

func buildTestGraph(t *testing.T, verts []VertexInput, edges [][2]string) *Graph {
	t.Helper()
	g, err := BuildGraph(&Input{Vertices: verts, Edges: edges})
	if err != nil {
		t.Fatalf("BuildGraph: %v", err)
	}
	return g
}

// verts builds vertices v0..v{n-1} with the given costs.
func verts(costs ...int64) []VertexInput {
	vs := make([]VertexInput, len(costs))
	for i, c := range costs {
		c := c
		vs[i] = VertexInput{ID: fmt.Sprintf("v%d", i), Cost: &c}
	}
	return vs
}

func namedVerts(ids ...string) []VertexInput {
	vs := make([]VertexInput, len(ids))
	one := int64(1)
	for i, id := range ids {
		vs[i] = VertexInput{ID: id, Cost: &one}
	}
	return vs
}

func checkOptimal(t *testing.T, g *Graph, wantIDs []string, wantCost int64) {
	t.Helper()
	out := Solve(g, 0)
	if out.Status != "optimal" {
		t.Fatalf("status = %q, want optimal", out.Status)
	}
	if !reflect.DeepEqual(out.Deleted, wantIDs) {
		t.Fatalf("deleted = %v, want %v", out.Deleted, wantIDs)
	}
	if out.Cost != wantCost || out.Count != len(wantIDs) {
		t.Fatalf("cost,count = %d,%d, want %d,%d", out.Cost, out.Count, wantCost, len(wantIDs))
	}
	if out.LowerBound != out.Cost || out.UpperBound != out.Cost {
		t.Fatalf("bounds [%d,%d] do not match cost %d", out.LowerBound, out.UpperBound, out.Cost)
	}
	if err := VerifyCertificate(g, out.Deleted, out.TopoOrder); err != nil {
		t.Fatalf("certificate invalid: %v", err)
	}
}

func TestSelfLoopIsForcedDeletion(t *testing.T) {
	g := buildTestGraph(t,
		verts(7, 1),
		[][2]string{{"v0", "v0"}, {"v0", "v1"}},
	)
	checkOptimal(t, g, []string{"v0"}, 7)
}

func TestZeroCostSelfLoopStillForced(t *testing.T) {
	// A zero-cost self-loop must be deleted even though deleting it is free;
	// the isolated zero-cost vertex must be kept.
	g := buildTestGraph(t,
		verts(0, 0),
		[][2]string{{"v0", "v0"}},
	)
	checkOptimal(t, g, []string{"v0"}, 0)
}

func TestZeroCostVertexPreferredOnCycle(t *testing.T) {
	g := buildTestGraph(t,
		verts(0, 5, 9),
		[][2]string{{"v0", "v1"}, {"v1", "v2"}, {"v2", "v0"}},
	)
	checkOptimal(t, g, []string{"v0"}, 0)
}

func TestOneDeletionPerSCCIsNotEnough(t *testing.T) {
	// Two 2-cycles linked into a single SCC. Deleting any single vertex
	// leaves the other 2-cycle intact, so the optimum needs two deletions.
	g := buildTestGraph(t,
		namedVerts("a", "b", "c", "d"),
		[][2]string{
			{"a", "b"}, {"b", "a"},
			{"c", "d"}, {"d", "c"},
			{"b", "c"}, {"d", "a"},
		},
	)
	comps := g.SCCs([]bool{true, true, true, true})
	if len(comps) != 1 {
		t.Fatalf("expected one SCC, got %d", len(comps))
	}
	// Must hit {a,b} and {c,d}; lexicographically smallest pair is {a,c}.
	checkOptimal(t, g, []string{"a", "c"}, 2)
}

func TestSharedVertexBreaksBothCycles(t *testing.T) {
	// Figure-eight: a->b->a and a->c->a share vertex a.
	g := buildTestGraph(t,
		namedVerts("a", "b", "c"),
		[][2]string{{"a", "b"}, {"b", "a"}, {"a", "c"}, {"c", "a"}},
	)
	checkOptimal(t, g, []string{"a"}, 1)
}

func TestTieBreakPrefersSmallestIDs(t *testing.T) {
	// All costs zero: any single vertex breaks the triangle; pick smallest ID.
	g := buildTestGraph(t,
		verts(0, 0, 0),
		[][2]string{{"v0", "v1"}, {"v1", "v2"}, {"v2", "v0"}},
	)
	checkOptimal(t, g, []string{"v0"}, 0)
}

func TestTieBreakAcrossDisjointCycles(t *testing.T) {
	// Two disjoint zero-cost triangles: delete the smallest-ID vertex of each.
	g := buildTestGraph(t,
		verts(0, 0, 0, 0, 0, 0),
		[][2]string{
			{"v0", "v1"}, {"v1", "v2"}, {"v2", "v0"},
			{"v3", "v4"}, {"v4", "v5"}, {"v5", "v3"},
		},
	)
	checkOptimal(t, g, []string{"v0", "v3"}, 0)
}

func TestCostBeatsCount(t *testing.T) {
	// Two 2-cycles share v0. Deleting v0 alone costs 2; deleting {v1,v2}
	// costs 1+1=2 as well, so the cost ties and the count decides: {v0}.
	g := buildTestGraph(t,
		verts(2, 1, 1),
		[][2]string{
			{"v0", "v1"}, {"v1", "v0"},
			{"v0", "v2"}, {"v2", "v0"},
		},
	)
	checkOptimal(t, g, []string{"v0"}, 2)
}

func TestEmptyAndAcyclicGraphs(t *testing.T) {
	g := buildTestGraph(t, verts(3, 4, 5), [][2]string{{"v0", "v1"}, {"v1", "v2"}})
	checkOptimal(t, g, []string{}, 0)
	out := Solve(g, 0)
	if !reflect.DeepEqual(out.TopoOrder, []string{"v0", "v1", "v2"}) {
		t.Fatalf("topological order = %v", out.TopoOrder)
	}
}

func TestInvalidInputRejected(t *testing.T) {
	neg := int64(-1)
	if _, err := BuildGraph(&Input{Vertices: []VertexInput{{ID: "a", Cost: &neg}}}); err == nil {
		t.Fatal("negative cost accepted")
	}
	if _, err := BuildGraph(&Input{Vertices: []VertexInput{{ID: "a"}, {ID: "a"}}}); err == nil {
		t.Fatal("duplicate id accepted")
	}
	if _, err := BuildGraph(&Input{Vertices: []VertexInput{{ID: "a"}}, Edges: [][2]string{{"a", "b"}}}); err == nil {
		t.Fatal("unknown edge endpoint accepted")
	}
}

func TestRandomGraphsAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(20261005))
	for trial := 0; trial < 400; trial++ {
		n := 1 + rng.Intn(9) // 1..9 vertices
		in := &Input{}
		for i := 0; i < n; i++ {
			cost := int64(rng.Intn(5)) // 0..4, includes zero-cost vertices
			in.Vertices = append(in.Vertices, VertexInput{
				ID:   fmt.Sprintf("v%d", i),
				Cost: &cost,
			})
		}
		p := 0.15 + 0.35*rng.Float64()
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				self := i == j
				prob := p
				if self {
					prob = 0.1 // sprinkle self-loops
				}
				if rng.Float64() < prob {
					in.Edges = append(in.Edges, [2]string{
						fmt.Sprintf("v%d", i), fmt.Sprintf("v%d", j),
					})
				}
			}
		}
		g, err := BuildGraph(in)
		if err != nil {
			t.Fatalf("trial %d: BuildGraph: %v", trial, err)
		}
		out := Solve(g, 0)
		if out.Status != "optimal" {
			t.Fatalf("trial %d: status = %q", trial, out.Status)
		}
		want, err := BruteForce(g)
		if err != nil {
			t.Fatalf("trial %d: BruteForce: %v", trial, err)
		}
		gotIDs := out.Deleted
		wantIDs := sortedIDs(g, want.Deleted)
		if out.Cost != want.Cost || out.Count != want.Count || !reflect.DeepEqual(gotIDs, wantIDs) {
			t.Fatalf("trial %d: solve = (%v cost %d), brute = (%v cost %d)",
				trial, gotIDs, out.Cost, wantIDs, want.Cost)
		}
		if out.LowerBound != out.Cost || out.UpperBound != out.Cost {
			t.Fatalf("trial %d: bounds [%d,%d] != cost %d",
				trial, out.LowerBound, out.UpperBound, out.Cost)
		}
		if err := VerifyCertificate(g, out.Deleted, out.TopoOrder); err != nil {
			t.Fatalf("trial %d: certificate invalid: %v", trial, err)
		}
	}
}

func randomGraph(rng *rand.Rand, n int, edgeProb, selfProb float64, maxCost int64) *Graph {
	in := &Input{}
	for i := 0; i < n; i++ {
		cost := int64(rng.Int63n(maxCost + 1))
		in.Vertices = append(in.Vertices, VertexInput{
			ID:   fmt.Sprintf("v%02d", i),
			Cost: &cost,
		})
	}
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			prob := edgeProb
			if i == j {
				prob = selfProb
			}
			if rng.Float64() < prob {
				in.Edges = append(in.Edges, [2]string{
					fmt.Sprintf("v%02d", i), fmt.Sprintf("v%02d", j),
				})
			}
		}
	}
	g, err := BuildGraph(in)
	if err != nil {
		panic(err)
	}
	return g
}

func checkInterruptedOutput(t *testing.T, g *Graph, out *Output) {
	t.Helper()
	if out.Status != "interrupted" {
		t.Fatalf("status = %q, want interrupted (nodes=%d)", out.Status, out.Nodes)
	}
	if out.LowerBound > out.UpperBound {
		t.Fatalf("lower bound %d exceeds upper bound %d", out.LowerBound, out.UpperBound)
	}
	if out.UpperBound != out.Cost {
		t.Fatalf("upper bound %d != incumbent cost %d", out.UpperBound, out.Cost)
	}
	// The incumbent must still be a genuine feedback vertex set.
	if err := VerifyCertificate(g, out.Deleted, out.TopoOrder); err != nil {
		t.Fatalf("interrupted incumbent certificate invalid: %v", err)
	}
	// Recompute the incumbent cost from the deleted set.
	var cost int64
	for _, id := range out.Deleted {
		cost += g.Cost[g.Index[id]]
	}
	if cost != out.Cost {
		t.Fatalf("reported cost %d != recomputed %d", out.Cost, cost)
	}
}

func TestNodeBudgetInterruptReturnsBounds(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	g := randomGraph(rng, 80, 0.2, 0, 3)
	out := solve(g, 0, 150)
	checkInterruptedOutput(t, g, out)
}

func TestInterruptedBoundsBracketOptimum(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for trial := 0; trial < 60; trial++ {
		g := randomGraph(rng, 14, 0.3, 0.05, 3)
		alive := make([]bool, len(g.IDs))
		for i := range alive {
			alive[i] = !g.Self[i]
		}
		if g.anyCycle(alive) == nil {
			continue // need a cyclic graph so the search cannot finish instantly
		}
		want, err := BruteForce(g)
		if err != nil {
			t.Fatalf("trial %d: BruteForce: %v", trial, err)
		}
		out := solve(g, 0, 2) // stop after the root and one child
		checkInterruptedOutput(t, g, out)
		if out.LowerBound > want.Cost {
			t.Fatalf("trial %d: lower bound %d exceeds optimum %d", trial, out.LowerBound, want.Cost)
		}
		if want.Cost > out.UpperBound {
			t.Fatalf("trial %d: optimum %d exceeds upper bound %d", trial, want.Cost, out.UpperBound)
		}
	}
}

func TestTimeBudgetInterruptSmoke(t *testing.T) {
	rng := rand.New(rand.NewSource(13))
	g := randomGraph(rng, 60, 0.2, 0, 3)
	out := Solve(g, time.Nanosecond)
	checkInterruptedOutput(t, g, out)
}
