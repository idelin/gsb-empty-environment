package netsec

import "testing"

func TestTopologicalFallbackPath(t *testing.T) {
	p, err := NewProblem(
		[]string{"a", "b", "c"},
		[]int64{0, 0, 0}, []int64{4, 4, 4},
		[]Order{
			{Before: "a", After: "b", Kind: StrictOrder},
			{Before: "b", After: "c", Kind: WeakOrder},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	sol, cyc := p.solve(nil)
	if cyc != nil {
		t.Fatal(cyc)
	}

	// Drive the topological construction directly so the fallback helpers and
	// min/max window math are exercised regardless of rejection luck.
	times := topologicalSample(p, sol, 12345)
	if !p.Legal(times) {
		t.Fatalf("topological sample illegal: %v", times)
	}

	// Strict ordering must hold with at least one integer tick.
	if !(times[0] < times[1]) {
		t.Fatalf("strict order violated: %v", times)
	}
	if !(times[1] <= times[2]) {
		t.Fatalf("weak order violated: %v", times)
	}

	// Determinism of the construction.
	again := topologicalSample(p, sol, 12345)
	for i := range times {
		if times[i] != again[i] {
			t.Fatalf("topological sample not reproducible: %v != %v", times, again)
		}
	}

	// Helper branches.
	if maxInt64(1, 2) != 2 || maxInt64(3, 2) != 3 {
		t.Fatal("maxInt64 wrong")
	}
	if minInt64(1, 2) != 1 || minInt64(3, 2) != 2 {
		t.Fatal("minInt64 wrong")
	}
	if w := p.edgeWeight(0, 1); w != 1 {
		t.Fatalf("strict edge weight = %d, want 1", w)
	}
	if w := p.edgeWeight(1, 2); w != 0 {
		t.Fatalf("weak edge weight = %d, want 0", w)
	}
	if got := p.predecessors(); len(got[1]) != 1 || len(got[2]) != 1 {
		t.Fatalf("predecessors wrong: %v", got)
	}
}

func TestSimultaneousViaExplicitPin(t *testing.T) {
	// Non-zero lower bounds make the earliest vector distinct even though the
	// weak order permits equality, forcing the explicit-pin branch.
	p, err := NewProblem(
		[]string{"a", "b", "c"},
		[]int64{5, 7, 0}, []int64{10, 10, 10},
		[]Order{
			{Before: "a", After: "b", Kind: WeakOrder},
			{Before: "b", After: "c", Kind: StrictOrder},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	sol, cyc := p.solve(nil)
	if cyc != nil {
		t.Fatal(cyc)
	}
	if countDistinct(sol.earliest) == len(sol.earliest) {
		// Sanity: earliest vector really has no coincidence, so the explicit
		// equality-pin loop is the path under test.
	} else {
		t.Fatalf("test setup expected distinct earliest vector: %v", sol.earliest)
	}

	mut, err := p.simultaneous()
	if err != nil {
		t.Fatalf("simultaneous: %v", err)
	}
	if !p.Legal(mut.Times) {
		t.Fatalf("simultaneous assignment illegal: %v", mut.Times)
	}
	if countDistinct(mut.Times) == len(mut.Times) {
		t.Fatalf("simultaneous assignment has no equal timestamps: %v", mut.Times)
	}
	if mut.Tag != "simultaneous" || mut.Seed != seedSimult {
		t.Fatalf("wrong mutation metadata: %+v", mut)
	}
}
