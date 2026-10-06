package certcache

import (
	"math/rand"
	"testing"
)

func mustAdd(t *testing.T, g *Graph, from, to int, w float64) {
	t.Helper()
	if err := g.AddEdge(from, to, w); err != nil {
		t.Fatalf("AddEdge(%d,%d): %v", from, to, err)
	}
}

func mustRemove(t *testing.T, g *Graph, from, to int) {
	t.Helper()
	if err := g.RemoveEdge(from, to); err != nil {
		t.Fatalf("RemoveEdge(%d,%d): %v", from, to, err)
	}
}

func mustSet(t *testing.T, g *Graph, from, to int, w float64) {
	t.Helper()
	if err := g.SetWeight(from, to, w); err != nil {
		t.Fatalf("SetWeight(%d,%d): %v", from, to, err)
	}
}

func TestReachCertHitAndTargetedWeightInvalidation(t *testing.T) {
	g := NewGraph()
	mustAdd(t, g, 1, 2, 1)
	mustAdd(t, g, 2, 4, 1)
	mustAdd(t, g, 1, 3, 1)
	mustAdd(t, g, 3, 4, 1)
	e := NewEngine(g)

	if !e.Reachable(1, 4) {
		t.Fatal("expected 1->4 reachable")
	}
	if e.ReachMisses != 1 {
		t.Fatalf("expected 1 miss, got %d", e.ReachMisses)
	}
	if !e.Reachable(1, 4) || e.ReachHits != 1 {
		t.Fatal("expected second query to hit the cache")
	}

	// BFS from 1 visits 2 before 3, so the certified path is 1->2->4 and the
	// certificate does not depend on 3->4.
	mustSet(t, g, 3, 4, 9)
	if e.Cache.ReachSize() != 1 {
		t.Fatal("weight change on an unrelated edge must not invalidate the certificate")
	}
	if !e.Reachable(1, 4) || e.ReachHits != 2 {
		t.Fatal("expected cache hit after unrelated weight change")
	}

	mustSet(t, g, 2, 4, 5)
	if e.Cache.ReachSize() != 0 {
		t.Fatal("weight change on a depended-on edge must invalidate the certificate")
	}
	if !e.Reachable(1, 4) || e.ReachMisses != 2 {
		t.Fatal("expected recompute after dependent weight change")
	}
	if err := e.Cache.VerifyAll(g); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveEdgeInvalidatesOnlyDependentCerts(t *testing.T) {
	g := NewGraph()
	mustAdd(t, g, 1, 2, 1)
	mustAdd(t, g, 2, 5, 1)
	mustAdd(t, g, 1, 3, 1)
	mustAdd(t, g, 3, 4, 1)
	mustAdd(t, g, 4, 5, 1)
	e := NewEngine(g)

	e.Reachable(1, 5) // path 1-2-5
	e.Reachable(1, 4) // path 1-3-4
	e.Reachable(3, 4) // path 3-4
	if e.Cache.ReachSize() != 3 {
		t.Fatalf("expected 3 certificates, got %d", e.Cache.ReachSize())
	}

	mustRemove(t, g, 4, 5) // no cached certificate depends on 4->5
	if e.Cache.ReachSize() != 3 {
		t.Fatal("removing an unrelated edge must not invalidate certificates")
	}

	mustRemove(t, g, 1, 2) // kills only the (1,5) certificate
	if e.Cache.ReachSize() != 2 {
		t.Fatalf("expected 2 certificates after dependent removal, got %d", e.Cache.ReachSize())
	}
	if _, ok := e.Cache.getReach(g, Pair{1, 4}); !ok {
		t.Fatal("certificate for (1,4) must survive")
	}
	if _, ok := e.Cache.getReach(g, Pair{3, 4}); !ok {
		t.Fatal("certificate for (3,4) must survive")
	}
	if _, ok := e.Cache.getReach(g, Pair{1, 5}); ok {
		t.Fatal("certificate for (1,5) must be invalidated")
	}

	// With 1->2 and 4->5 gone, 5 is no longer reachable from 1.
	if e.Reachable(1, 5) {
		t.Fatal("expected 1->5 unreachable after removals")
	}
	if err := e.Cache.VerifyAll(g); err != nil {
		t.Fatal(err)
	}
}

func TestEdgeAdditionInvalidatesSPButNotReach(t *testing.T) {
	g := NewGraph()
	mustAdd(t, g, 1, 2, 1)
	mustAdd(t, g, 2, 4, 5)
	mustAdd(t, g, 1, 3, 2)
	mustAdd(t, g, 3, 4, 2)
	e := NewEngine(g)

	e.Reachable(1, 4)
	dist, _, ok := e.ShortestPath(1, 4) // 4 via 1-3-4
	if !ok || dist != 4 {
		t.Fatalf("expected dist 4, got %v", dist)
	}
	e.ShortestPath(1, 4)
	if e.SPHits != 1 {
		t.Fatal("expected shortest-path cache hit")
	}

	mustAdd(t, g, 1, 4, 3) // opens a shorter route
	if e.Cache.SPSize() != 0 {
		t.Fatal("edge addition must invalidate shortest-path certificates")
	}
	if e.Cache.ReachSize() != 1 {
		t.Fatal("edge addition must not invalidate reachability certificates")
	}
	if !e.Reachable(1, 4) || e.ReachHits != 1 {
		t.Fatal("reachability certificate must still serve after addition")
	}

	dist, _, _ = e.ShortestPath(1, 4)
	if dist != 3 {
		t.Fatalf("expected recomputed dist 3, got %v", dist)
	}
	if e.SPMisses != 2 {
		t.Fatal("expected shortest-path recompute after addition")
	}
	if err := e.Cache.VerifyAll(g); err != nil {
		t.Fatal(err)
	}
}

func TestSPWeightChangeTargetsDependencies(t *testing.T) {
	g := NewGraph()
	mustAdd(t, g, 1, 2, 1)
	mustAdd(t, g, 2, 4, 5)
	mustAdd(t, g, 1, 3, 2)
	mustAdd(t, g, 3, 4, 2)
	mustAdd(t, g, 4, 5, 1) // out-edge of the target, not a dependency
	e := NewEngine(g)

	dist, _, _ := e.ShortestPath(1, 4) // 4 via 1-3-4
	if dist != 4 {
		t.Fatalf("expected dist 4, got %v", dist)
	}

	mustSet(t, g, 4, 5, 9) // not in the dependency set
	if e.Cache.SPSize() != 1 {
		t.Fatal("weight change outside the dependency set must keep the certificate")
	}
	if _, _, ok := e.ShortestPath(1, 4); !ok || e.SPHits != 1 {
		t.Fatal("expected cache hit after unrelated weight change")
	}

	mustSet(t, g, 2, 4, 0.5) // in the dependency set
	if e.Cache.SPSize() != 0 {
		t.Fatal("dependent weight change must invalidate the certificate")
	}
	dist, _, _ = e.ShortestPath(1, 4) // 1.5 via 1-2-4
	if dist != 1.5 {
		t.Fatalf("expected recomputed dist 1.5, got %v", dist)
	}
	if err := e.Cache.VerifyAll(g); err != nil {
		t.Fatal(err)
	}
}

func TestVersionWraparoundRejectsStaleCerts(t *testing.T) {
	g := NewGraph()
	mustAdd(t, g, 1, 2, 1)
	mustAdd(t, g, 2, 3, 1)
	e := NewEngine(g)

	e.Reachable(1, 3)
	cert, ok := e.Cache.getReach(g, Pair{1, 3})
	if !ok {
		t.Fatal("expected cached certificate")
	}
	staleDeps := make(map[EdgeKey]Version, len(cert.Deps))
	for k, v := range cert.Deps {
		staleDeps[k] = v
	}

	// Drive edge 1->2 through a full version cycle: 255 mutations bring Value
	// from 0 to 255, the 256th wraps and bumps the graph generation.
	for i := 0; i < 256; i++ {
		mustSet(t, g, 1, 2, float64(i%7+1))
	}
	if g.gen != 1 {
		t.Fatalf("expected generation 1 after wraparound, got %d", g.gen)
	}
	if e.Cache.ReachSize() != 0 || e.Cache.SPSize() != 0 {
		t.Fatal("generation bump must flush the whole cache")
	}

	// The wrapped edge is back at Value 0, exactly what the stale certificate
	// recorded, but the generation differs: it must be rejected.
	edge, _ := g.Edge(EdgeKey{1, 2})
	if edge.Version.Value != staleDeps[EdgeKey{1, 2}].Value {
		t.Fatalf("test setup: expected wrapped value to collide, got %+v", edge.Version)
	}
	if validDeps(g, staleDeps) {
		t.Fatal("stale certificate with wrapped version must be rejected")
	}

	// Fresh queries still work and record versions in the new generation.
	if !e.Reachable(1, 3) {
		t.Fatal("expected 1->3 reachable")
	}
	fresh, ok := e.Cache.getReach(g, Pair{1, 3})
	if !ok {
		t.Fatal("expected fresh certificate to validate")
	}
	if fresh.Deps[EdgeKey{1, 2}].Gen != 1 {
		t.Fatalf("expected fresh certificate in generation 1, got %+v", fresh.Deps[EdgeKey{1, 2}])
	}
	if err := e.Cache.VerifyAll(g); err != nil {
		t.Fatal(err)
	}
}

func TestReAddedEdgeGetsFreshVersion(t *testing.T) {
	g := NewGraph()
	mustAdd(t, g, 1, 2, 1)
	mustRemove(t, g, 1, 2)
	mustAdd(t, g, 1, 2, 1)
	edge, _ := g.Edge(EdgeKey{1, 2})
	if edge.Version != (Version{Value: 1, Gen: 0}) {
		t.Fatalf("re-added edge must continue the version sequence, got %+v", edge.Version)
	}
}

func TestNegativeWeightRejected(t *testing.T) {
	g := NewGraph()
	if err := g.AddEdge(1, 2, -1); err != ErrNegativeWeight {
		t.Fatalf("expected ErrNegativeWeight, got %v", err)
	}
	mustAdd(t, g, 1, 2, 1)
	if err := g.SetWeight(1, 2, -0.5); err != ErrNegativeWeight {
		t.Fatalf("expected ErrNegativeWeight, got %v", err)
	}
}

// TestFullReverificationCrossCheck hammers the graph with random mutations
// and queries; after every step each surviving certificate is fully
// re-verified from scratch, and cached answers are compared against
// cache-free recomputation.
func TestFullReverificationCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	g := NewGraph()
	e := NewEngine(g)
	const nodes = 8

	edgeKeys := func() []EdgeKey {
		keys := make([]EdgeKey, 0, len(g.edges))
		for k := range g.edges {
			keys = append(keys, k)
		}
		return keys
	}

	for step := 0; step < 500; step++ {
		switch rng.Intn(4) {
		case 0:
			g.AddEdge(rng.Intn(nodes), rng.Intn(nodes), float64(rng.Intn(9)+1))
		case 1:
			if keys := edgeKeys(); len(keys) > 0 {
				k := keys[rng.Intn(len(keys))]
				g.RemoveEdge(k.From, k.To)
			}
		case 2:
			if keys := edgeKeys(); len(keys) > 0 {
				k := keys[rng.Intn(len(keys))]
				g.SetWeight(k.From, k.To, float64(rng.Intn(9)+1))
			}
		case 3:
			a, b := rng.Intn(nodes), rng.Intn(nodes)
			_, wantReach := bfsPath(g, a, b)
			if got := e.Reachable(a, b); got != wantReach {
				t.Fatalf("step %d: Reachable(%d,%d)=%v, want %v", step, a, b, got, wantReach)
			}
			wantDist, _, _, wantOK := dijkstra(g, a, b)
			gotDist, _, gotOK := e.ShortestPath(a, b)
			if gotOK != wantOK || (wantOK && gotDist != wantDist) {
				t.Fatalf("step %d: ShortestPath(%d,%d)=(%v,%v), want (%v,%v)",
					step, a, b, gotDist, gotOK, wantDist, wantOK)
			}
		}
		if err := e.Cache.VerifyAll(g); err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
	}
}
