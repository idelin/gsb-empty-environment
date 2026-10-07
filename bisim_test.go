package bisim

import (
	"math/rand"
	"testing"
)

func blockSets(q *Quotient) map[int][]int {
	sets := make(map[int][]int)
	for s, b := range q.BlockOf {
		sets[b] = append(sets[b], s)
	}
	return sets
}

func sameBlock(q *Quotient, s, t int) bool { return q.BlockOf[s] == q.BlockOf[t] }

// A missing transition must differ from a transition into a rejecting
// state: 0 (accept, no moves) vs 1 (accept, -a-> reject sink).
func TestMissingTransitionDiffersFromRejectTarget(t *testing.T) {
	a := New(3, []string{"accept", "accept", "reject"})
	a.AddEdge(1, "a", 2)
	q := a.BisimulationQuotient()
	if q.NumBlocks != 3 {
		t.Fatalf("got %d blocks, want 3\n%s", q.NumBlocks, q)
	}
	if sameBlock(q, 0, 1) {
		t.Errorf("state with missing transition merged with state transitioning to reject")
	}
	if err := a.CheckQuotient(q); err != nil {
		t.Error(err)
	}
}

// Reachable accepting states must not be merged on acceptance alone:
// both chains below stay in "accept" forever, but with different move
// structure at every depth.
func TestNoMergeByAcceptanceAlone(t *testing.T) {
	a := New(4, []string{"accept", "accept", "accept", "reject"})
	a.AddEdge(0, "a", 1) // 0 -> accepting sink
	a.AddEdge(2, "a", 2) // 2 loops on itself
	a.AddEdge(2, "b", 3) // 2 can also fall into reject
	q := a.BisimulationQuotient()
	if sameBlock(q, 0, 2) {
		t.Errorf("accepting states with different behavior were merged")
	}
	if sameBlock(q, 0, 1) || sameBlock(q, 1, 2) {
		t.Errorf("accepting states merged without matching moves")
	}
	if err := a.CheckQuotient(q); err != nil {
		t.Error(err)
	}
}

// Structurally equivalent states do merge, and the quotient graph plus
// old->new mapping reflect the original edges.
func TestQuotientGraphAndMapping(t *testing.T) {
	a := New(4, []string{"x", "y", "x", "y"})
	a.AddEdge(0, "a", 1)
	a.AddEdge(2, "a", 3)
	a.AddEdge(2, "a", 1) // extra edge into the same target block
	q := a.BisimulationQuotient()
	if q.NumBlocks != 2 {
		t.Fatalf("got %d blocks, want 2\n%s", q.NumBlocks, q)
	}
	if !sameBlock(q, 0, 2) || !sameBlock(q, 1, 3) {
		t.Errorf("bisimilar states not merged: %v", q.BlockOf)
	}
	bx := q.BlockOf[0]
	by := q.BlockOf[1]
	if q.Output[bx] != "x" || q.Output[by] != "y" {
		t.Errorf("wrong block outputs: %v", q.Output)
	}
	targets := q.Edges[bx]["a"]
	if len(targets) != 1 || targets[0] != by {
		t.Errorf("quotient edge block(x) -a-> %v, want [%d]", targets, by)
	}
	if len(q.Edges[by]) != 0 {
		t.Errorf("block(y) should have no outgoing edges, got %v", q.Edges[by])
	}
	if err := a.CheckQuotient(q); err != nil {
		t.Error(err)
	}
}

// Multi-edges are accepted and do not change the partition.
func TestMultiEdgesAreTransparent(t *testing.T) {
	build := func(multi bool) *Automaton {
		a := New(3, []string{"p", "q", "q"})
		a.AddEdge(0, "a", 1)
		if multi {
			a.AddEdge(0, "a", 1) // exact duplicate
			a.AddEdge(0, "a", 2) // parallel edge into same block
			a.AddEdge(0, "a", 2)
		}
		return a
	}
	q1 := build(false).BisimulationQuotient()
	q2 := build(true).BisimulationQuotient()
	if q1.NumBlocks != q2.NumBlocks {
		t.Fatalf("multi-edges changed block count: %d vs %d", q1.NumBlocks, q2.NumBlocks)
	}
	for s := range q1.BlockOf {
		if sameBlock(q1, s, (s+1)%3) != sameBlock(q2, s, (s+1)%3) {
			t.Errorf("multi-edges changed the partition")
		}
	}
	if err := build(true).CheckQuotient(build(true).BisimulationQuotient()); err != nil {
		t.Error(err)
	}
}

// Cross-check the refinement algorithm against the direct fixed-point
// relation model on many random automata (including multi-edges and
// unreachable states).
func TestRandomAgainstFixedPoint(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	outputs := []string{"accept", "reject", "trap"}
	labels := []string{"a", "b", "c"}
	for trial := 0; trial < 500; trial++ {
		n := 1 + rng.Intn(8)
		out := make([]string, n)
		for i := range out {
			out[i] = outputs[rng.Intn(len(outputs))]
		}
		a := New(n, out)
		for e := 0; e < rng.Intn(3*n+1); e++ {
			a.AddEdge(rng.Intn(n), labels[rng.Intn(len(labels))], rng.Intn(n))
		}
		q := a.BisimulationQuotient()
		if err := a.CheckQuotient(q); err != nil {
			t.Fatalf("trial %d: %v\noutputs=%v\n%s", trial, err, out, q)
		}
	}
}
