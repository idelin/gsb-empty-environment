package topo

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func mustAdd(t *testing.T, g *Graph, from, to int) {
	t.Helper()
	if err := g.AddEdge(from, to); err != nil {
		t.Fatalf("AddEdge(%d,%d) unexpected error: %v", from, to, err)
	}
}

func chainGraph(t *testing.T) *Graph {
	t.Helper()
	g := New()
	mustAdd(t, g, 1, 2)
	mustAdd(t, g, 2, 3)
	mustAdd(t, g, 3, 4)
	return g
}

// Acceptance: with 1->2->3->4, inserting 4->2 must be rejected with the
// exact causal cycle [2 3 4 2], and the original order must be intact.
func TestCycleRejectedWithExactPath(t *testing.T) {
	g := chainGraph(t)
	before := g.TopologicalOrder()

	err := g.AddEdge(4, 2)
	if err == nil {
		t.Fatal("expected cycle error, got nil")
	}
	ce, ok := err.(*CycleError)
	if !ok {
		t.Fatalf("expected *CycleError, got %T", err)
	}
	want := []int{2, 3, 4, 2}
	if !reflect.DeepEqual(ce.Path, want) {
		t.Fatalf("cycle path = %v, want %v", ce.Path, want)
	}
	if got := g.TopologicalOrder(); !reflect.DeepEqual(got, before) {
		t.Fatalf("order mutated after rejection: got %v, want %v", got, before)
	}
	if !g.Validate() {
		t.Fatal("order invalid after rejection")
	}
}

// Acceptance: inserting 1->4 into 1->2->3->4 takes the fast path and
// keeps the topological order monotonic.
func TestFastPathKeepsMonotonicOrder(t *testing.T) {
	g := chainGraph(t)
	before := g.TopologicalOrder()

	mustAdd(t, g, 1, 4)

	if got := g.TopologicalOrder(); !reflect.DeepEqual(got, before) {
		t.Fatalf("order changed on fast path: got %v, want %v", got, before)
	}
	for i := 1; i < len(before); i++ {
		if g.Rank(before[i-1]) >= g.Rank(before[i]) {
			t.Fatalf("ranks not monotonic: %v", before)
		}
	}
	if !g.Validate() {
		t.Fatal("order invalid after fast-path insert")
	}
}

// Inserting 5->2 (with 5 initially unrelated) forces a backward edge;
// only the affected region {2,3,4,5} may be recolored, node 1 untouched.
func TestBidirectionalLocalRecolor(t *testing.T) {
	g := chainGraph(t)
	g.AddNode(5)
	rank1 := g.Rank(1)

	mustAdd(t, g, 5, 2)

	want := []int{1, 5, 2, 3, 4}
	if got := g.TopologicalOrder(); !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if g.Rank(1) != rank1 {
		t.Fatalf("unaffected node 1 recolored: %d -> %d", rank1, g.Rank(1))
	}
	if !g.Validate() {
		t.Fatal("order invalid after recolor")
	}
}

func TestSelfLoopRejected(t *testing.T) {
	g := New()
	err := g.AddEdge(7, 7)
	ce, ok := err.(*CycleError)
	if !ok {
		t.Fatalf("expected *CycleError, got %v", err)
	}
	if !reflect.DeepEqual(ce.Path, []int{7, 7}) {
		t.Fatalf("self-loop path = %v", ce.Path)
	}
}

func TestDuplicateEdgeIsNoOp(t *testing.T) {
	g := chainGraph(t)
	before := g.TopologicalOrder()
	mustAdd(t, g, 2, 3)
	if got := g.TopologicalOrder(); !reflect.DeepEqual(got, before) {
		t.Fatalf("duplicate edge changed order: %v", got)
	}
}

// Concurrent edge insertions into a large DAG must all succeed (no
// false cycles) and leave a valid ordering. Run with -race.
func TestConcurrentAdds(t *testing.T) {
	const n = 2000
	g := New()
	for i := 0; i < n; i++ {
		g.AddNode(i)
	}
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := w; i+8 < n; i += 8 {
				if err := g.AddEdge(i, i+8); err != nil {
					errs <- err
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent insert failed: %v", err)
	}
	if !g.Validate() {
		t.Fatal("order invalid after concurrent inserts")
	}
}

// A cycle created through concurrent inserts must still be detected by
// exactly one of the racing writers.
func TestConcurrentCycleDetection(t *testing.T) {
	g := chainGraph(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var cycles []string
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := g.AddEdge(4, 2); err != nil {
				mu.Lock()
				cycles = append(cycles, fmt.Sprintf("%v", err.(*CycleError).Path))
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(cycles) != 16 {
		t.Fatalf("expected 16 rejections, got %d", len(cycles))
	}
	for _, c := range cycles {
		if c != "[2 3 4 2]" {
			t.Fatalf("unexpected cycle path %s", c)
		}
	}
	if got, want := g.TopologicalOrder(), []int{1, 2, 3, 4}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}
