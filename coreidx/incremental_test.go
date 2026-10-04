package coreidx

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// referenceCores recomputes core numbers from scratch with an independent
// method: for each k, the k-core is found by repeatedly stripping nodes with
// degree < k; core(v) is the largest k whose k-core contains v.
func referenceCores(g *Graph) map[int64]int {
	adj := map[int64]map[int64]bool{}
	for _, id := range g.Nodes() {
		adj[id] = map[int64]bool{}
	}
	maxDeg := 0
	for _, e := range g.Edges() {
		adj[e.U][e.V] = true
		adj[e.V][e.U] = true
		if len(adj[e.U]) > maxDeg {
			maxDeg = len(adj[e.U])
		}
		if len(adj[e.V]) > maxDeg {
			maxDeg = len(adj[e.V])
		}
	}
	cores := map[int64]int{}
	for id := range adj {
		cores[id] = 0
	}
	for k := 1; k <= maxDeg; k++ {
		deg := map[int64]int{}
		removed := map[int64]bool{}
		var queue []int64
		for id := range adj {
			deg[id] = len(adj[id])
			if deg[id] < k {
				queue = append(queue, id)
			}
		}
		for len(queue) > 0 {
			w := queue[0]
			queue = queue[1:]
			if removed[w] || deg[w] >= k {
				continue
			}
			removed[w] = true
			for x := range adj[w] {
				if !removed[x] {
					deg[x]--
					if deg[x] < k {
						queue = append(queue, x)
					}
				}
			}
		}
		for id := range adj {
			if !removed[id] {
				cores[id] = k
			}
		}
	}
	return cores
}

func assertCoresMatchReference(t *testing.T, g *Graph, context string) {
	t.Helper()
	want := referenceCores(g)
	got := g.Cores()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: incremental cores diverge from full decomposition:\n got %v\nwant %v", context, got, want)
	}
}

// kCoreByEnumeration returns the set of nodes belonging to some induced
// subgraph with minimum degree >= k, found by enumerating all node subsets.
// The k-core is exactly the union of all such subsets (the union of two
// induced subgraphs with min degree >= k still has min degree >= k).
func kCoreByEnumeration(g *Graph, k int) map[int64]bool {
	nodes := g.Nodes()
	n := len(nodes)
	if n > 20 {
		panic("subset enumeration is only feasible for tiny graphs")
	}
	idx := map[int64]int{}
	for i, id := range nodes {
		idx[id] = i
	}
	var adj [20][20]bool
	for _, e := range g.Edges() {
		adj[idx[e.U]][idx[e.V]] = true
		adj[idx[e.V]][idx[e.U]] = true
	}
	inUnion := map[int64]bool{}
	for mask := 1; mask < (1 << uint(n)); mask++ {
		var members []int
		for i := 0; i < n; i++ {
			if mask&(1<<uint(i)) != 0 {
				members = append(members, i)
			}
		}
		ok := true
		for _, i := range members {
			d := 0
			for _, j := range members {
				if adj[i][j] {
					d++
				}
			}
			if d < k {
				ok = false
				break
			}
		}
		if ok {
			for _, i := range members {
				inUnion[nodes[i]] = true
			}
		}
	}
	return inUnion
}

// assertKCoreDefinition checks, for every k, that {v : core(v) >= k} equals
// the union of all induced subgraphs with minimum degree >= k.
func assertKCoreDefinition(t *testing.T, g *Graph, context string) {
	t.Helper()
	maxCore := 0
	for _, c := range g.Cores() {
		if c > maxCore {
			maxCore = c
		}
	}
	for k := 1; k <= maxCore+1; k++ {
		want := kCoreByEnumeration(g, k)
		var gotIDs, wantIDs []int64
		for id, c := range g.Cores() {
			if c >= k {
				gotIDs = append(gotIDs, id)
			}
		}
		for id := range want {
			wantIDs = append(wantIDs, id)
		}
		sort.Slice(gotIDs, func(i, j int) bool { return gotIDs[i] < gotIDs[j] })
		sort.Slice(wantIDs, func(i, j int) bool { return wantIDs[i] < wantIDs[j] })
		if !reflect.DeepEqual(gotIDs, wantIDs) {
			t.Fatalf("%s: %d-core mismatch:\n got %v\nwant %v (subset enumeration)", context, k, gotIDs, wantIDs)
		}
	}
}

// Two 5-cliques (4-cores) joined by a single low-core bridge edge, plus an
// isolated node. Deleting one internal clique edge cascades: the whole side
// drops from core 4 to core 3 while the other side is unaffected.
func buildBridgeGraph(t *testing.T) *Graph {
	t.Helper()
	g := New()
	clique(t, g, 1, 2, 3, 4, 5)
	clique(t, g, 6, 7, 8, 9, 10)
	mustAddEdge(t, g, 1, 6) // bridge
	g.AddNode(100)          // isolated
	g.Build()
	return g
}

func TestBridgeScenario(t *testing.T) {
	g := buildBridgeGraph(t)

	want := map[int64]int{1: 4, 2: 4, 3: 4, 4: 4, 5: 4, 6: 4, 7: 4, 8: 4, 9: 4, 10: 4, 100: 0}
	if got := g.Cores(); !reflect.DeepEqual(got, want) {
		t.Fatalf("initial cores: got %v want %v", got, want)
	}
	assertKCoreDefinition(t, g, "initial")

	// Delete a key internal edge of clique A: the whole A side cascades
	// down one core level; clique B keeps core 4.
	res, err := g.ApplyBatch([]Op{DeleteEdgeOp(2, 3)})
	if err != nil {
		t.Fatal(err)
	}
	wantChanges := []CoreChange{
		{1, 4, 3}, {2, 4, 3}, {3, 4, 3}, {4, 4, 3}, {5, 4, 3},
	}
	if !reflect.DeepEqual(res.Changes, wantChanges) {
		t.Fatalf("changes: got %v want %v", res.Changes, wantChanges)
	}
	for id := int64(6); id <= 10; id++ {
		if c, _ := g.Core(id); c != 4 {
			t.Fatalf("node %d: core %d, want 4 (other side must be unaffected)", id, c)
		}
	}
	assertCoresMatchReference(t, g, "after deleting (2,3)")
	assertKCoreDefinition(t, g, "after deleting (2,3)")

	// Deleting the bridge now changes nothing: both sides keep their cores.
	res, err = g.ApplyBatch([]Op{DeleteEdgeOp(1, 6)})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changes) != 0 {
		t.Fatalf("bridge deletion should affect nobody, got %v", res.Changes)
	}
	assertCoresMatchReference(t, g, "after deleting bridge (1,6)")

	// Repeated / no-op operations plus a new isolated node: nothing moves.
	res, err = g.ApplyBatch([]Op{
		AddEdgeOp(6, 7),    // duplicate add: idempotent
		DeleteEdgeOp(3, 7), // deleting a missing edge: no-op
		DeleteEdgeOp(1, 6), // already deleted above: no-op
		AddNodeOp(200),     // new isolated node
		AddNodeOp(200),     // duplicate node add: idempotent
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changes) != 0 {
		t.Fatalf("no-op batch should affect nobody, got %v", res.Changes)
	}
	if c, ok := g.Core(200); !ok || c != 0 {
		t.Fatalf("new isolated node: core %d ok=%v, want 0", c, ok)
	}
	assertCoresMatchReference(t, g, "after no-op batch")
	assertKCoreDefinition(t, g, "after no-op batch")

	// Re-inserting the key edge lifts the whole A side back to core 4.
	res, err = g.ApplyBatch([]Op{AddEdgeOp(2, 3)})
	if err != nil {
		t.Fatal(err)
	}
	wantChanges = []CoreChange{
		{1, 3, 4}, {2, 3, 4}, {3, 3, 4}, {4, 3, 4}, {5, 3, 4},
	}
	if !reflect.DeepEqual(res.Changes, wantChanges) {
		t.Fatalf("changes: got %v want %v", res.Changes, wantChanges)
	}
	assertCoresMatchReference(t, g, "after re-adding (2,3)")
	assertKCoreDefinition(t, g, "after re-adding (2,3)")

	// Isolated nodes survive everything.
	for _, id := range []int64{100, 200} {
		if !g.HasNode(id) {
			t.Fatalf("isolated node %d was dropped", id)
		}
		if c, _ := g.Core(id); c != 0 {
			t.Fatalf("isolated node %d: core %d, want 0", id, c)
		}
	}
}

func TestBatchRollback(t *testing.T) {
	g := buildBridgeGraph(t)
	wantCores := g.Cores()
	wantNodes := g.Nodes()
	wantEdges := g.Edges()

	// The batch starts with effective operations, then hits a self-loop:
	// everything must be rolled back.
	_, err := g.ApplyBatch([]Op{
		DeleteEdgeOp(2, 3), // would cascade
		AddEdgeOp(50, 51),  // would create a new component
		AddNodeOp(300),     // would add an isolated node
		AddEdgeOp(9, 9),    // invalid: self-loop
	})
	if !errors.Is(err, ErrSelfLoop) {
		t.Fatalf("got %v, want ErrSelfLoop", err)
	}
	if got := g.Cores(); !reflect.DeepEqual(got, wantCores) {
		t.Fatalf("cores changed despite rollback:\n got %v\nwant %v", got, wantCores)
	}
	if got := g.Nodes(); !reflect.DeepEqual(got, wantNodes) {
		t.Fatalf("nodes changed despite rollback:\n got %v\nwant %v", got, wantNodes)
	}
	if got := g.Edges(); !reflect.DeepEqual(got, wantEdges) {
		t.Fatalf("edges changed despite rollback:\n got %v\nwant %v", got, wantEdges)
	}

	// A self-loop delete is rejected the same way.
	if _, err := g.ApplyBatch([]Op{DeleteEdgeOp(4, 4)}); !errors.Is(err, ErrSelfLoop) {
		t.Fatalf("got %v, want ErrSelfLoop", err)
	}
	if got := g.Cores(); !reflect.DeepEqual(got, wantCores) {
		t.Fatal("cores changed despite rollback")
	}

	// The graph keeps working after a rejected batch.
	res, err := g.ApplyBatch([]Op{DeleteEdgeOp(2, 3)})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changes) != 5 {
		t.Fatalf("expected the cascade after the valid batch, got %v", res.Changes)
	}
	assertCoresMatchReference(t, g, "after rollback + valid batch")
}

// Randomized differential test: random batches against the independent
// full-decomposition reference, checking both the core values and the exact
// affected set reported by the incremental algorithm.
func TestRandomizedDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	const n = 40
	g := New()
	for i := 0; i < n; i++ {
		g.AddNode(int64(i))
	}
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if rng.Float64() < 0.08 {
				mustAddEdge(t, g, int64(i), int64(j))
			}
		}
	}
	g.Build()
	assertCoresMatchReference(t, g, "initial build")

	for batch := 0; batch < 150; batch++ {
		before := g.Cores()
		var ops []Op
		numOps := 1 + rng.Intn(8)
		for k := 0; k < numOps; k++ {
			u := int64(rng.Intn(n + 5)) // some IDs may not exist yet
			v := int64(rng.Intn(n + 5))
			switch rng.Intn(4) {
			case 0, 1:
				ops = append(ops, AddEdgeOp(u, v))
			case 2:
				ops = append(ops, DeleteEdgeOp(u, v))
			default:
				ops = append(ops, AddNodeOp(u))
			}
		}
		res, err := g.ApplyBatch(ops)
		if err != nil {
			if !errors.Is(err, ErrSelfLoop) {
				t.Fatalf("batch %d: unexpected error %v", batch, err)
			}
			// rolled back: cores must equal the pre-batch state
			if got := g.Cores(); !reflect.DeepEqual(got, before) {
				t.Fatalf("batch %d: rollback restored wrong cores", batch)
			}
			continue
		}
		context := fmt.Sprintf("batch %d ops %v", batch, ops)
		assertCoresMatchReference(t, g, context)

		// The reported affected set must equal the true diff.
		var wantChanged []int64
		after := g.Cores()
		for id, c := range after {
			if before[id] != c {
				wantChanged = append(wantChanged, id)
			}
		}
		sort.Slice(wantChanged, func(i, j int) bool { return wantChanged[i] < wantChanged[j] })
		if got := res.ChangedNodes(); !reflect.DeepEqual(got, wantChanged) {
			t.Fatalf("%s: changed set got %v want %v", context, got, wantChanged)
		}
	}
}

// Small random graphs: after every incremental batch, verify the k-core
// definition by brute-force subset enumeration.
func TestSubsetEnumerationAfterUpdates(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	const n = 11
	g := New()
	for i := 0; i < n; i++ {
		g.AddNode(int64(i))
	}
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if rng.Float64() < 0.25 {
				mustAddEdge(t, g, int64(i), int64(j))
			}
		}
	}
	g.Build()
	assertKCoreDefinition(t, g, "initial")

	for batch := 0; batch < 40; batch++ {
		var ops []Op
		for k := 0; k < 1+rng.Intn(4); k++ {
			u := int64(rng.Intn(n))
			v := int64(rng.Intn(n))
			if u == v {
				continue
			}
			if rng.Intn(2) == 0 {
				ops = append(ops, AddEdgeOp(u, v))
			} else {
				ops = append(ops, DeleteEdgeOp(u, v))
			}
		}
		if _, err := g.ApplyBatch(ops); err != nil {
			t.Fatalf("batch %d: %v", batch, err)
		}
		assertKCoreDefinition(t, g, fmt.Sprintf("batch %d", batch))
		assertCoresMatchReference(t, g, fmt.Sprintf("batch %d", batch))
	}
}
