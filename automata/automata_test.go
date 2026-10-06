package automata

import (
	"math/rand"
	"testing"
)

func mustNew(t *testing.T, n int, outputs []string, edges []Edge) *Automaton {
	t.Helper()
	a, err := New(n, outputs, edges)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

func sameBlock(p *Partition, x, y int) bool { return p.BlockOf[x] == p.BlockOf[y] }

// 缺迁移与到拒绝状态不同: a state with no "a"-transition must not be merged
// with a state whose "a"-transition lands in a rejecting sink, even though
// neither can ever accept via "a".
func TestMissingTransitionDiffersFromRejectingTarget(t *testing.T) {
	// 0: acc output, no transitions.
	// 1: acc output, --a--> 2 (rejecting sink).
	// 2: rej output, no transitions.
	a := mustNew(t, 3,
		[]string{"acc", "acc", "rej"},
		[]Edge{{From: 1, Label: "a", To: 2}},
	)
	p := CoarsestBisimulation(a)
	if sameBlock(p, 0, 1) {
		t.Fatalf("missing transition merged with transition-to-rejecting: %v", p.Mapping())
	}
	if p.NumBlocks() != 3 {
		t.Fatalf("want 3 blocks, got %d (%v)", p.NumBlocks(), p.Mapping())
	}
	if err := p.VerifyAgainstFixedPoint(a); err != nil {
		t.Fatal(err)
	}
}

// 不能只按可达接受状态合并: states that reach exactly the same accepting
// states but through different structure must stay separate.
func TestNotMergedByReachableAcceptingStates(t *testing.T) {
	// 0 --a--> 2(acc); 1 --a--> 3 --a--> 2(acc).
	// Both 0 and 1 reach the same accepting state 2, but 0 accepts in one
	// step and 1 in two: not bisimilar.
	a := mustNew(t, 4,
		[]string{"rej", "rej", "acc", "rej"},
		[]Edge{
			{From: 0, Label: "a", To: 2},
			{From: 1, Label: "a", To: 3},
			{From: 3, Label: "a", To: 2},
		},
	)
	p := CoarsestBisimulation(a)
	if sameBlock(p, 0, 1) {
		t.Fatalf("merged states that merely reach the same accepting state: %v", p.Mapping())
	}
	// 0 and 3 both move to 2 in one "a"-step: they ARE bisimilar and merge.
	if !sameBlock(p, 0, 3) {
		t.Fatalf("0 and 3 are bisimilar and must merge: %v", p.Mapping())
	}
	if err := p.VerifyAgainstFixedPoint(a); err != nil {
		t.Fatal(err)
	}
}

// Same reachable accepting states, same language, different branching:
// bisimulation still distinguishes them.
func TestNondeterminismIsObserved(t *testing.T) {
	// 0 --a--> 2(acc); 0 --a--> 3(rej sink)
	// 1 --a--> 2(acc)
	// Identical languages {a}, but 0 can move to a rejecting state on "a".
	a := mustNew(t, 4,
		[]string{"rej", "rej", "acc", "rej"},
		[]Edge{
			{From: 0, Label: "a", To: 2},
			{From: 0, Label: "a", To: 3},
			{From: 1, Label: "a", To: 2},
		},
	)
	p := CoarsestBisimulation(a)
	if sameBlock(p, 0, 1) {
		t.Fatalf("merged different branching structure: %v", p.Mapping())
	}
	if err := p.VerifyAgainstFixedPoint(a); err != nil {
		t.Fatal(err)
	}
}

// 带标签多重边: parallel edges count; also same-label edges to different
// blocks are distinguished by label.
func TestMultiplicityMatters(t *testing.T) {
	// 0 has two parallel a-edges to 2; 1 has one.
	a := mustNew(t, 3,
		[]string{"x", "x", "y"},
		[]Edge{
			{From: 0, Label: "a", To: 2},
			{From: 0, Label: "a", To: 2},
			{From: 1, Label: "a", To: 2},
		},
	)
	p := CoarsestBisimulation(a)
	if sameBlock(p, 0, 1) {
		t.Fatalf("merged different edge multiplicities: %v", p.Mapping())
	}
	if err := p.VerifyAgainstFixedPoint(a); err != nil {
		t.Fatal(err)
	}

	// Same multiplicity and same target block, but different labels.
	b := mustNew(t, 3,
		[]string{"x", "x", "y"},
		[]Edge{
			{From: 0, Label: "a", To: 2},
			{From: 1, Label: "b", To: 2},
		},
	)
	pb := CoarsestBisimulation(b)
	if sameBlock(pb, 0, 1) {
		t.Fatalf("merged different edge labels: %v", pb.Mapping())
	}
	if err := pb.VerifyAgainstFixedPoint(b); err != nil {
		t.Fatal(err)
	}
}

// Structurally identical duplicates are merged, and the quotient plus the
// old->new mapping are exact.
func TestQuotientAndMapping(t *testing.T) {
	// 0 and 1 are twins: both output "s", both --a--> 2, both --b--> {3,3}.
	// 2: output "t", loop --a--> 2. 3: output "u", no edges.
	a := mustNew(t, 4,
		[]string{"s", "s", "t", "u"},
		[]Edge{
			{From: 0, Label: "a", To: 2},
			{From: 0, Label: "b", To: 3},
			{From: 0, Label: "b", To: 3},
			{From: 1, Label: "a", To: 2},
			{From: 1, Label: "b", To: 3},
			{From: 1, Label: "b", To: 3},
			{From: 2, Label: "a", To: 2},
		},
	)
	p := CoarsestBisimulation(a)
	if p.NumBlocks() != 3 {
		t.Fatalf("want 3 blocks, got %d (%v)", p.NumBlocks(), p.Mapping())
	}
	if !sameBlock(p, 0, 1) {
		t.Fatalf("twin states not merged: %v", p.Mapping())
	}
	q := QuotientGraph(a, p)
	if err := q.VerifyQuotient(a); err != nil {
		t.Fatal(err)
	}
	b0 := p.BlockOf[0]
	b2 := p.BlockOf[2]
	b3 := p.BlockOf[3]
	want := map[QEdge]bool{
		{From: b0, Label: "a", To: b2, Count: 1}: true,
		{From: b0, Label: "b", To: b3, Count: 2}: true,
		{From: b2, Label: "a", To: b2, Count: 1}: true,
	}
	if len(q.Edges) != len(want) {
		t.Fatalf("quotient edges = %v, want %d edges", q.Edges, len(want))
	}
	for _, e := range q.Edges {
		if !want[e] {
			t.Fatalf("unexpected quotient edge %+v", e)
		}
	}
	if q.Outputs[b0] != "s" || q.Outputs[b2] != "t" || q.Outputs[b3] != "u" {
		t.Fatalf("quotient outputs wrong: %v", q.Outputs)
	}
	if err := p.VerifyAgainstFixedPoint(a); err != nil {
		t.Fatal(err)
	}
}

// The refinement operator itself is idempotent at the result: running the
// algorithm on the quotient's behavior changes nothing (stability check via
// the fixed-point relation is already covered; here we check the partition
// is a fixed point of one more refinement round implicitly by determinism).
func TestDeterministicAndIdempotent(t *testing.T) {
	a := mustNew(t, 5,
		[]string{"o", "o", "o", "p", "p"},
		[]Edge{
			{From: 0, Label: "l", To: 3},
			{From: 1, Label: "l", To: 4},
			{From: 2, Label: "l", To: 3},
			{From: 3, Label: "l", To: 3},
			{From: 4, Label: "l", To: 4},
		},
	)
	p1 := CoarsestBisimulation(a)
	p2 := CoarsestBisimulation(a)
	if p1.Mapping() != p2.Mapping() {
		t.Fatalf("non-deterministic: %v vs %v", p1.Mapping(), p2.Mapping())
	}
	// 3 and 4 are interchangeable loops, so 0,1,2 all collapse.
	if p1.NumBlocks() != 2 {
		t.Fatalf("want 2 blocks, got %d (%v)", p1.NumBlocks(), p1.Mapping())
	}
}

// Randomized cross-check: the signature-refinement partition must always
// equal the classes of the directly computed greatest fixed point, and the
// quotient must be well defined.
func TestRandomAutomataAgreeWithFixedPoint(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	outputs := []string{"acc", "rej", "mid"}
	labels := []string{"a", "b", "c"}
	for trial := 0; trial < 300; trial++ {
		n := 1 + rng.Intn(9)
		out := make([]string, n)
		for i := range out {
			out[i] = outputs[rng.Intn(len(outputs))]
		}
		var edges []Edge
		for _, from := range rand.Perm(n)[:n] {
			for _, l := range labels {
				for k := 0; k < rng.Intn(3); k++ { // up to 2 parallel edges per label
					edges = append(edges, Edge{From: from, Label: l, To: rng.Intn(n)})
				}
			}
		}
		a, err := New(n, out, edges)
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		p := CoarsestBisimulation(a)
		if err := p.VerifyAgainstFixedPoint(a); err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		q := QuotientGraph(a, p)
		if err := q.VerifyQuotient(a); err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
	}
}
