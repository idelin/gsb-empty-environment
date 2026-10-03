package inspector

import (
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDanglingReference(t *testing.T) {
	rep := Inspect([]Record{
		{ID: 1, Refs: []uint64{2, 77}, Root: true},
		{ID: 2},
	}, Options{})
	issues := rep.IssuesOf(KindDanglingRef)
	if len(issues) != 1 || !strings.Contains(issues[0].Detail, "77") {
		t.Fatalf("want 1 dangling ref to 77, got %v", rep.Issues)
	}
	if rep.OK() {
		t.Fatal("report should not be OK with a fatal dangling ref")
	}
}

func TestOrphanCycleVsReachableCycle(t *testing.T) {
	rep := Inspect([]Record{
		{ID: 1, Refs: []uint64{2}, Root: true},
		{ID: 2, Refs: []uint64{3}},
		{ID: 3, Refs: []uint64{2}}, // reachable cycle 2<->3
		{ID: 4, Refs: []uint64{5}}, // orphan cycle 4<->5
		{ID: 5, Refs: []uint64{4}},
	}, Options{})
	if n := len(rep.IssuesOf(KindOrphanCycle)); n != 1 {
		t.Fatalf("want 1 orphan cycle, got %d: %v", n, rep.Issues)
	}
	if n := len(rep.IssuesOf(KindReachableCycle)); n != 1 {
		t.Fatalf("want 1 reachable cycle, got %d: %v", n, rep.Issues)
	}
	if rep.CyclicGroups != 2 {
		t.Fatalf("want 2 cyclic groups, got %d", rep.CyclicGroups)
	}
	if rep.Reachable != 3 {
		t.Fatalf("want 3 reachable nodes, got %d", rep.Reachable)
	}
}

func TestSelfLoop(t *testing.T) {
	rep := Inspect([]Record{{ID: 1, Refs: []uint64{1}, Root: true}}, Options{})
	if n := len(rep.IssuesOf(KindSelfLoop)); n != 1 {
		t.Fatalf("want 1 self loop, got %v", rep.Issues)
	}
	// A self loop reachable from a root is a reachable cycle, not orphan.
	if n := len(rep.IssuesOf(KindOrphanCycle)); n != 0 {
		t.Fatalf("self loop at root must not be orphan: %v", rep.Issues)
	}
}

func TestDuplicateIDAndNegativeWeight(t *testing.T) {
	rep := Inspect([]Record{
		{ID: 1, Root: true},
		{ID: 1},
		{ID: 2, Weight: -5},
	}, Options{})
	if n := len(rep.IssuesOf(KindDuplicateID)); n != 1 {
		t.Fatalf("want 1 duplicate id, got %v", rep.Issues)
	}
	if n := len(rep.IssuesOf(KindNegativeWeight)); n != 1 {
		t.Fatalf("want 1 negative weight, got %v", rep.Issues)
	}
}

func TestUnreachableNode(t *testing.T) {
	rep := Inspect([]Record{
		{ID: 1, Root: true},
		{ID: 2},
	}, Options{})
	if n := len(rep.IssuesOf(KindUnreachable)); n != 1 {
		t.Fatalf("want 1 unreachable node, got %v", rep.Issues)
	}
}

func TestDataSkew(t *testing.T) {
	// mean = (10+10+10+1000)/4 = 257.5, ratio = 1000/257.5 ≈ 3.88 > 3.
	skewed := Inspect([]Record{
		{ID: 1, Weight: 10, Root: true},
		{ID: 2, Weight: 10},
		{ID: 3, Weight: 10},
		{ID: 4, Weight: 1000},
	}, Options{})
	if n := len(skewed.IssuesOf(KindDataSkew)); n != 1 {
		t.Fatalf("want skew flagged, got %v", skewed.Issues)
	}
	if skewed.SkewRatio <= 3.0 {
		t.Fatalf("skew ratio %.2f should exceed 3.0", skewed.SkewRatio)
	}
	// Uniform weights: no skew.
	flat := Inspect([]Record{
		{ID: 1, Weight: 10, Root: true},
		{ID: 2, Weight: 10},
	}, Options{})
	if len(flat.IssuesOf(KindDataSkew)) != 0 || flat.SkewRatio != 1.0 {
		t.Fatalf("uniform weights must not skew: ratio=%.2f issues=%v", flat.SkewRatio, flat.Issues)
	}
	// All-zero weights: no skew computation, no panic.
	zero := Inspect([]Record{{ID: 1, Root: true}}, Options{})
	if zero.SkewRatio != 0 {
		t.Fatalf("zero weights: ratio=%.2f", zero.SkewRatio)
	}
}

func TestInvariantAssertions(t *testing.T) {
	clean := Inspect([]Record{
		{ID: 1, Refs: []uint64{2}, Weight: 5, Root: true},
		{ID: 2, Weight: 5},
	}, Options{})
	if err := Assert(clean, StandardInvariants...); err != nil {
		t.Fatalf("clean graph must pass standard invariants: %v", err)
	}
	if err := Assert(clean, AllReachable, SkewWithin(2.0)); err != nil {
		t.Fatalf("clean graph must pass extended invariants: %v", err)
	}

	dirty := Inspect([]Record{
		{ID: 1, Refs: []uint64{9}, Root: true},
		{ID: 2, Refs: []uint64{3}},
		{ID: 3, Refs: []uint64{2}},
	}, Options{})
	err := Assert(dirty, StandardInvariants...)
	if err == nil {
		t.Fatal("dirty graph must fail standard invariants")
	}
	verr, ok := err.(*ViolationError)
	if !ok {
		t.Fatalf("want *ViolationError, got %T", err)
	}
	joined := strings.Join(verr.Violations, ",")
	if !strings.Contains(joined, "NoDanglingRefs") || !strings.Contains(joined, "NoOrphanCycles") {
		t.Fatalf("expected dangling+orphan violations, got %v", verr)
	}
	if err := Assert(dirty, AllReachable); err == nil {
		t.Fatal("orphan nodes must fail AllReachable")
	}
}

func TestDeepGraphNoStackOverflow(t *testing.T) {
	// 200k-node chain: iterative BFS/Tarjan must not blow the stack.
	const n = 200000
	records := make([]Record, n)
	for i := 0; i < n; i++ {
		records[i] = Record{ID: uint64(i + 1)}
		if i+1 < n {
			records[i].Refs = []uint64{uint64(i + 2)}
		}
	}
	records[0].Root = true
	rep := Inspect(records, Options{})
	if rep.Reachable != n {
		t.Fatalf("reachable=%d want %d", rep.Reachable, n)
	}
	if rep.CyclicGroups != 0 {
		t.Fatalf("chain must be acyclic, got %d groups", rep.CyclicGroups)
	}
}

func TestConcurrentInspectDeterministic(t *testing.T) {
	snapshot := []Record{
		{ID: 1, Refs: []uint64{2, 3}, Weight: 10, Root: true},
		{ID: 2, Refs: []uint64{3}, Weight: 12},
		{ID: 3, Refs: []uint64{99}, Weight: 400},
		{ID: 4, Refs: []uint64{5}, Weight: 8},
		{ID: 5, Refs: []uint64{4}, Weight: 9},
	}
	want := Inspect(snapshot, Options{})
	var wg sync.WaitGroup
	mismatch := make(chan *Report, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				got := Inspect(snapshot, Options{})
				if got.Nodes != want.Nodes || got.Edges != want.Edges ||
					got.Reachable != want.Reachable || got.CyclicGroups != want.CyclicGroups ||
					got.SkewRatio != want.SkewRatio || len(got.Issues) != len(want.Issues) {
					mismatch <- got
					return
				}
			}
		}()
	}
	wg.Wait()
	close(mismatch)
	for got := range mismatch {
		t.Fatalf("non-deterministic report: %+v", got)
	}
}

func TestNoGoroutineLeak(t *testing.T) {
	before := settledGoroutines()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			Inspect([]Record{
				{ID: 1, Refs: []uint64{2}, Root: true},
				{ID: 2, Refs: []uint64{1}},
			}, Options{})
		}()
	}
	wg.Wait()
	after := settledGoroutines()
	if after > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}

func settledGoroutines() int {
	// Let any transient goroutines exit before sampling.
	for i := 0; i < 20; i++ {
		runtime.GC()
		time.Sleep(5 * time.Millisecond)
	}
	return runtime.NumGoroutine()
}
