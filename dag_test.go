package main

import (
	"math/rand"
	"testing"
)

func TestParallelBranchesNotDoubleCounted(t *testing.T) {
	d := NewDAG()
	src := d.AddEvent(0, "created", 1, -1, "")
	a := d.AddEvent(5, "slow_copy_done", 1, 0, "")
	b := d.AddEvent(3, "fast_copy_done", 1, 1, "")
	join := d.AddEvent(5, "delivered", 1, 0, "merged")
	d.AddEdge(src, a, PhasePropagate)
	d.AddEdge(src, b, PhasePropagate)
	d.AddEdge(a, join, PhaseOther)
	d.AddEdge(b, join, PhaseOther)
	an := d.Analyze([]int{src}, join)
	if an.Upper != 5 || an.Lower != 5 {
		t.Fatalf("critical path = [%d,%d], want [5,5]", an.Lower, an.Upper)
	}
	if an.WorkUpper != 10 {
		t.Fatalf("cumulative work = %d, want 10 (both parallel copies plus merge wait)", an.WorkUpper)
	}
	if an.WorkUpper <= an.Upper {
		t.Fatalf("work %d should exceed wall clock %d when branches overlap", an.WorkUpper, an.Upper)
	}
	paths, err := d.EnumeratePaths([]int{src}, join, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 {
		t.Fatalf("enumerated %d paths, want 2", len(paths))
	}
	maxW := 0
	for _, p := range paths {
		if w := d.PathWeight(p, true); w > maxW {
			maxW = w
		}
	}
	if maxW != an.Upper {
		t.Fatalf("exhaustive max path %d != DP critical path %d", maxW, an.Upper)
	}
}

func TestMissingEventGivesBoundsNotZero(t *testing.T) {
	d := NewDAG()
	created := d.AddEvent(0, "created", 1, -1, "")
	enq := d.AddEvent(0, "enqueued", 1, 0, "A")
	txEnd := d.AddEvent(10, "tx_end", 1, 0, "A")
	recv := d.AddEvent(15, "received", 1, 0, "A")
	del := d.AddEvent(15, "delivered", 1, 0, "")
	d.AddEdge(created, enq, PhaseOther)
	d.AddInferredEdge(enq, txEnd, PhaseQueueSerialize, 0, 10)
	d.AddEdge(txEnd, recv, PhasePropagate)
	d.AddEdge(recv, del, PhaseOther)
	an := d.Analyze([]int{created}, del)
	if an.Lower != 5 || an.Upper != 15 {
		t.Fatalf("critical path = [%d,%d], want [5,15]", an.Lower, an.Upper)
	}
	b := an.Breakdown[PhaseQueueSerialize]
	if b != [2]int{0, 10} {
		t.Fatalf("queue+serialize = %v, want [0,10] (missing tx_start must not become 0)", b)
	}
}
func TestIncrementalEventRevision(t *testing.T) {
	d := NewDAG()
	created := d.AddEvent(0, "created", 1, -1, "")
	enq := d.AddEvent(0, "enqueued", 1, 0, "A")
	txEnd := d.AddEvent(10, "tx_end", 1, 0, "A")
	recv := d.AddEvent(15, "received", 1, 0, "A")
	del := d.AddEvent(15, "delivered", 1, 0, "")
	d.AddEdge(created, enq, PhaseOther)
	d.AddInferredEdge(enq, txEnd, PhaseQueueSerialize, 0, 10)
	d.AddEdge(txEnd, recv, PhasePropagate)
	d.AddEdge(recv, del, PhaseOther)
	before := d.Analyze([]int{created}, del)
	if before.Lower != 5 || before.Upper != 15 {
		t.Fatalf("before revision: [%d,%d], want [5,15]", before.Lower, before.Upper)
	}
	txStart := d.AddEvent(4, "tx_start", 1, 0, "A")
	d.RemoveEdgesBetween(enq, txEnd)
	d.AddEdge(enq, txStart, PhaseQueue)
	d.AddEdge(txStart, txEnd, PhaseSerialize)
	after := d.Analyze([]int{created}, del)
	if after.Lower != 15 || after.Upper != 15 {
		t.Fatalf("after revision: [%d,%d], want [15,15]", after.Lower, after.Upper)
	}
	if got := after.Breakdown[PhaseQueue]; got != [2]int{4, 4} {
		t.Fatalf("queue = %v, want [4,4]", got)
	}
	if got := after.Breakdown[PhaseSerialize]; got != [2]int{6, 6} {
		t.Fatalf("serialize = %v, want [6,6]", got)
	}
	if _, ok := after.Breakdown[PhaseQueueSerialize]; ok {
		t.Fatal("inferred phase must disappear after revision")
	}
	lo, hi := after.BreakdownSum()
	if lo != 15 || hi != 15 {
		t.Fatalf("breakdown sum = [%d,%d], want [15,15]", lo, hi)
	}
}

func TestContradictionKeepsEdgeEvidence(t *testing.T) {
	d := NewDAG()
	tx := d.AddEvent(8, "tx_end", 9, 0, "linkX")
	rv := d.AddEvent(3, "received", 9, 0, "linkX")
	d.AddEdge(tx, rv, PhasePropagate)
	cs := d.Contradictions()
	if len(cs) != 1 {
		t.Fatalf("got %d contradictions, want 1", len(cs))
	}
	c := cs[0]
	if c.Edge.RawDelta != -5 {
		t.Fatalf("raw delta = %d, want -5 (original evidence preserved)", c.Edge.RawDelta)
	}
	if c.FromEvent.ID != tx || c.ToEvent.ID != rv {
		t.Fatalf("evidence events = #%d->#%d, want #%d->#%d", c.FromEvent.ID, c.ToEvent.ID, tx, rv)
	}
	if c.Edge.Lower != 0 || c.Edge.Upper != 0 {
		t.Fatalf("contradictory edge weight = [%d,%d], want clamped [0,0]", c.Edge.Lower, c.Edge.Upper)
	}
}

func TestExhaustiveReferenceOnRandomDAGs(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 30; trial++ {
		d := NewDAG()
		n := 4 + rng.Intn(8)
		ids := make([]int, n)
		tick := 0
		for i := 0; i < n; i++ {
			tick += rng.Intn(4)
			ids[i] = d.AddEvent(tick, "e", 1, i, "")
		}
		for j := 1; j < n; j++ {
			parents := 1 + rng.Intn(2)
			for k := 0; k < parents; k++ {
				i := rng.Intn(j)
				d.AddEdge(ids[i], ids[j], PhaseOther)
			}
		}
		src := []int{ids[0]}
		sink := ids[n-1]
		an := d.Analyze(src, sink)
		paths, err := d.EnumeratePaths(src, sink, 100000)
		if err != nil {
			t.Fatal(err)
		}
		maxLo, maxHi := 0, 0
		for _, p := range paths {
			if w := d.PathWeight(p, false); w > maxLo {
				maxLo = w
			}
			if w := d.PathWeight(p, true); w > maxHi {
				maxHi = w
			}
		}
		if maxLo != an.Lower || maxHi != an.Upper {
			t.Fatalf("trial %d: DP [%d,%d] != exhaustive [%d,%d]",
				trial, an.Lower, an.Upper, maxLo, maxHi)
		}
		if an.WorkUpper < an.Upper {
			t.Fatalf("trial %d: work %d < critical path %d", trial, an.WorkUpper, an.Upper)
		}
	}
}

func TestCeilDiv(t *testing.T) {
	cases := []struct{ a, b, want int }{
		{10, 2, 5}, {10, 3, 4}, {1, 100, 1}, {10, 1, 10}, {7, 7, 1},
	}
	for _, c := range cases {
		if got := ceilDiv(c.a, c.b); got != c.want {
			t.Fatalf("ceilDiv(%d,%d) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
