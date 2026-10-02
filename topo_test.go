package dyntopo

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

func buildChain(t *testing.T) *Graph[int] {
	t.Helper()
	g := New[int]()
	for _, e := range [][2]int{{1, 2}, {2, 3}, {3, 4}} {
		if cyc, rejected := g.AddEdge(e[0], e[1]); rejected {
			t.Fatalf("AddEdge(%d,%d) unexpectedly rejected: %v", e[0], e[1], cyc)
		}
	}
	return g
}

func assertValid(t *testing.T, g *Graph[int]) {
	t.Helper()
	seen := make(map[int]int) // order value -> node
	for _, v := range g.TopoOrder() {
		o, ok := g.Order(v)
		if !ok {
			t.Fatalf("node %d missing order", v)
		}
		if prev, dup := seen[o]; dup {
			t.Fatalf("duplicate order %d for nodes %d and %d", o, prev, v)
		}
		seen[o] = v
	}
	for _, e := range g.Edges() {
		ou, _ := g.Order(e[0])
		ov, _ := g.Order(e[1])
		if ou >= ov {
			t.Fatalf("topological order violated by edge %d->%d: %d !< %d", e[0], e[1], ou, ov)
		}
	}
}

// Acceptance: chain 1->2->3->4, adding 4->2 must be rejected with the
// exact cycle [2 3 4 2] and must not modify the existing ordering.
func TestCycleRejectedWithExactPath(t *testing.T) {
	g := buildChain(t)
	before := g.TopoOrder()

	cyc, rejected := g.AddEdge(4, 2)
	if !rejected {
		t.Fatal("AddEdge(4,2) should be rejected")
	}
	if want := []int{2, 3, 4, 2}; !reflect.DeepEqual(cyc, want) {
		t.Fatalf("cycle = %v, want %v", cyc, want)
	}
	if after := g.TopoOrder(); !reflect.DeepEqual(before, after) {
		t.Fatalf("ordering modified by rejected edge: before %v after %v", before, after)
	}
	if g.HasEdge(4, 2) {
		t.Fatal("rejected edge must not be recorded")
	}
	assertValid(t, g)
}

// Acceptance: adding 1->4 to the chain is consistent with the current
// order, so it takes the fast path with zero relabeling.
func TestForwardEdgeFastPath(t *testing.T) {
	g := buildChain(t)
	before := g.TopoOrder()

	if cyc, rejected := g.AddEdge(1, 4); rejected {
		t.Fatalf("AddEdge(1,4) unexpectedly rejected: %v", cyc)
	}
	if after := g.TopoOrder(); !reflect.DeepEqual(before, after) {
		t.Fatalf("fast path should not relabel: before %v after %v", before, after)
	}
	if !g.HasEdge(1, 4) {
		t.Fatal("edge 1->4 not recorded")
	}
	assertValid(t, g)
}

// A backward edge that does not close a cycle must trigger
// bidirectional local recoloring: only nodes inside the affected
// interval change rank, and monotonicity is preserved.
func TestBidirectionalLocalRebalance(t *testing.T) {
	g := New[int]()
	for _, e := range [][2]int{{1, 2}, {3, 4}, {5, 6}} {
		g.AddEdge(e[0], e[1])
	}
	// Current ranks: 1:0 2:1 3:2 4:3 5:4 6:5
	snapshot := map[int]int{}
	for _, v := range g.TopoOrder() {
		o, _ := g.Order(v)
		snapshot[v] = o
	}

	if cyc, rejected := g.AddEdge(4, 2); rejected {
		t.Fatalf("AddEdge(4,2) unexpectedly rejected: %v", cyc)
	}

	// Nodes outside the affected interval [order(2), order(4)] = [1,3]
	// keep their exact ranks.
	for _, v := range []int{1, 5, 6} {
		if o, _ := g.Order(v); o != snapshot[v] {
			t.Fatalf("node %d rank changed: %d -> %d, want untouched", v, snapshot[v], o)
		}
	}
	// Recolored nodes must satisfy 3 < 4 < 2 now.
	o3, _ := g.Order(3)
	o4, _ := g.Order(4)
	o2, _ := g.Order(2)
	if !(o3 < o4 && o4 < o2) {
		t.Fatalf("local recoloring broken: order(3)=%d order(4)=%d order(2)=%d", o3, o4, o2)
	}
	assertValid(t, g)
}

// Concurrent edge insertion must be safe and keep the order valid.
func TestConcurrentAddEdge(t *testing.T) {
	g := New[int]()
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				g.AddEdge(base+i, base+i+1) // forward chains, never cycle
			}
		}(w * 10000)
	}
	wg.Wait()
	if g.Size() != 8*501 {
		t.Fatalf("Size = %d, want %d", g.Size(), 8*501)
	}
	assertValid(t, g)
}

// Randomized stress: every accepted edge keeps the order valid; every
// rejection reports a genuine cycle whose edges (except the closing,
// rejected one) all exist in the graph.
func TestRandomizedAgainstOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 20; trial++ {
		g := New[int]()
		n := 30
		for i := 0; i < n; i++ {
			g.AddNode(i)
		}
		for step := 0; step < 300; step++ {
			a, b := rng.Intn(n), rng.Intn(n)
			cyc, rejected := g.AddEdge(a, b)
			if !rejected {
				assertValid(t, g)
				continue
			}
			// Verify the reported cycle: consecutive hops exist, and
			// the closing hop is exactly the rejected edge a->b.
			if len(cyc) < 2 || cyc[0] != cyc[len(cyc)-1] {
				t.Fatalf("malformed cycle %v", cyc)
			}
			for i := 0; i+2 < len(cyc); i++ {
				if !g.HasEdge(cyc[i], cyc[i+1]) {
					t.Fatalf("cycle hop %d->%d not an edge (cycle %v)", cyc[i], cyc[i+1], cyc)
				}
			}
			if cyc[len(cyc)-2] != a || cyc[len(cyc)-1] != b {
				t.Fatalf("cycle %v does not close with rejected edge %d->%d", cyc, a, b)
			}
			assertValid(t, g)
		}
	}
}

func ExampleGraph_AddEdge() {
	g := New[int]()
	for _, e := range [][2]int{{1, 2}, {2, 3}, {3, 4}} {
		g.AddEdge(e[0], e[1])
	}
	fmt.Println("topo:", g.TopoOrder())

	if cyc, rejected := g.AddEdge(4, 2); rejected {
		fmt.Println("rejected 4->2, cycle:", cyc)
	}
	fmt.Println("topo after rejection:", g.TopoOrder())

	g.AddEdge(1, 4)
	fmt.Println("topo after 1->4:", g.TopoOrder())
	// Output:
	// topo: [1 2 3 4]
	// rejected 4->2, cycle: [2 3 4 2]
	// topo after rejection: [1 2 3 4]
	// topo after 1->4: [1 2 3 4]
}
