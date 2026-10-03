package router

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

func mustRouter(t *testing.T, shards, repl int) *Router {
	t.Helper()
	r, err := NewRouter(shards, repl)
	if err != nil {
		t.Fatalf("NewRouter(%d, %d): %v", shards, repl, err)
	}
	return r
}

func addNodes(t *testing.T, r *Router, n int, loc func(i int) Locality) []NodeID {
	t.Helper()
	ids := make([]NodeID, n)
	for i := 0; i < n; i++ {
		ids[i] = NodeID(fmt.Sprintf("node-%d", i))
		if err := r.AddNode(ids[i], loc(i)); err != nil {
			t.Fatalf("AddNode %s: %v", ids[i], err)
		}
	}
	return ids
}

func assertNoGoroutineLeak(t *testing.T, fn func()) {
	t.Helper()
	runtime.GC()
	before := runtime.NumGoroutine()
	fn()
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, got)
	}
}

func TestNewRouterValidation(t *testing.T) {
	if _, err := NewRouter(0, 1); err != ErrBadShardCount {
		t.Errorf("NewRouter(0,1) err = %v, want ErrBadShardCount", err)
	}
	if _, err := NewRouter(-3, 1); err != ErrBadShardCount {
		t.Errorf("NewRouter(-3,1) err = %v, want ErrBadShardCount", err)
	}
	if _, err := NewRouter(16, 0); err != ErrBadReplication {
		t.Errorf("NewRouter(16,0) err = %v, want ErrBadReplication", err)
	}
}

func TestEmptyCluster(t *testing.T) {
	r := mustRouter(t, 64, 2)
	if _, _, err := r.Route("key", Locality{}); err != ErrNoNodes {
		t.Errorf("Route on empty cluster err = %v, want ErrNoNodes", err)
	}
	if _, err := r.Replicas(0); err != ErrNoNodes {
		t.Errorf("Replicas on empty cluster err = %v, want ErrNoNodes", err)
	}
	if _, err := r.Replicas(-1); err != ErrShardRange {
		t.Errorf("Replicas(-1) err = %v, want ErrShardRange", err)
	}
	if _, err := r.Replicas(64); err != ErrShardRange {
		t.Errorf("Replicas(64) err = %v, want ErrShardRange", err)
	}
}

func TestNodeMembershipErrors(t *testing.T) {
	r := mustRouter(t, 16, 1)
	if err := r.AddNode("n1", Locality{Region: "r1", Zone: "z1"}); err != nil {
		t.Fatalf("AddNode: %v", err)
	}
	if err := r.AddNode("n1", Locality{}); err != ErrNodeExists {
		t.Errorf("duplicate AddNode err = %v, want ErrNodeExists", err)
	}
	if err := r.RemoveNode("ghost"); err != ErrNodeNotFound {
		t.Errorf("RemoveNode(ghost) err = %v, want ErrNodeNotFound", err)
	}
	if _, err := r.Load("ghost"); err != ErrNodeNotFound {
		t.Errorf("Load(ghost) err = %v, want ErrNodeNotFound", err)
	}
}

func TestRouteDeterministicAndInReplicaSet(t *testing.T) {
	r := mustRouter(t, 128, 3)
	addNodes(t, r, 8, func(i int) Locality {
		return Locality{Region: fmt.Sprintf("region-%d", i%2), Zone: fmt.Sprintf("zone-%d", i)}
	})

	for _, key := range []string{"alpha", "beta", "gamma", "delta"} {
		shard := r.ShardOf(key)
		reps, err := r.Replicas(shard)
		if err != nil {
			t.Fatalf("Replicas: %v", err)
		}
		if len(reps) != 3 {
			t.Fatalf("shard %d has %d replicas, want 3", shard, len(reps))
		}
		seen := map[NodeID]bool{}
		for _, id := range reps {
			if seen[id] {
				t.Fatalf("shard %d replica set has duplicate %s", shard, id)
			}
			seen[id] = true
		}
		for i := 0; i < 20; i++ {
			id, gotShard, err := r.Route(key, Locality{})
			if err != nil {
				t.Fatalf("Route: %v", err)
			}
			if gotShard != shard {
				t.Fatalf("Route shard = %d, want %d", gotShard, shard)
			}
			if !seen[id] {
				t.Fatalf("Route returned %s, not in replica set %v", id, reps)
			}
			r.Done(id)
		}
	}
}

func TestReplicationCappedByClusterSize(t *testing.T) {
	r := mustRouter(t, 32, 5)
	addNodes(t, r, 2, func(i int) Locality { return Locality{Region: "r", Zone: fmt.Sprintf("z%d", i)} })
	reps, err := r.Replicas(0)
	if err != nil {
		t.Fatalf("Replicas: %v", err)
	}
	if len(reps) != 2 {
		t.Fatalf("replica count = %d, want 2 (capped by cluster size)", len(reps))
	}
}

func TestLocalityAwareRouting(t *testing.T) {
	r := mustRouter(t, 256, 3)
	// Every node lives in its own zone so locality preference is exact.
	addNodes(t, r, 9, func(i int) Locality {
		return Locality{Region: fmt.Sprintf("region-%d", i/3), Zone: fmt.Sprintf("zone-%d", i)}
	})

	zoneHits, regionHits, remote := 0, 0, 0
	const keys = 2000
	for i := 0; i < keys; i++ {
		key := fmt.Sprintf("key-%d", i)
		// First discover the replica set, then route as a client sitting in
		// the first replica's zone.
		reps, err := r.Replicas(r.ShardOf(key))
		if err != nil {
			t.Fatalf("Replicas: %v", err)
		}
		r.mu.RLock()
		loc := r.nodes[reps[0]].Locality
		r.mu.RUnlock()

		id, _, err := r.Route(key, loc)
		if err != nil {
			t.Fatalf("Route: %v", err)
		}
		r.mu.RLock()
		gotLoc := r.nodes[id].Locality
		r.mu.RUnlock()
		switch {
		case gotLoc.Zone == loc.Zone && gotLoc.Region == loc.Region:
			zoneHits++
		case gotLoc.Region == loc.Region:
			regionHits++
		default:
			remote++
		}
		r.Done(id)
	}
	if remote != 0 {
		t.Fatalf("%d/%d routes left the client's region, want 0", remote, keys)
	}
	if zoneHits != keys {
		t.Fatalf("zone-local routes = %d/%d, want all zone-local", zoneHits, keys)
	}
	t.Logf("locality: zone=%d region=%d remote=%d", zoneHits, regionHits, remote)
}

func TestLocalityFallsBackToRegion(t *testing.T) {
	r := mustRouter(t, 64, 2)
	// Two nodes in region A (different zones), one in region B.
	if err := r.AddNode("a1", Locality{Region: "A", Zone: "z1"}); err != nil {
		t.Fatal(err)
	}
	if err := r.AddNode("a2", Locality{Region: "A", Zone: "z2"}); err != nil {
		t.Fatal(err)
	}
	if err := r.AddNode("b1", Locality{Region: "B", Zone: "z9"}); err != nil {
		t.Fatal(err)
	}
	// A client in region A zone z3 (no node there) must never land in B
	// when a region-A replica exists.
	for i := 0; i < 500; i++ {
		key := fmt.Sprintf("k%d", i)
		reps, _ := r.Replicas(r.ShardOf(key))
		inA := false
		for _, id := range reps {
			if id == "a1" || id == "a2" {
				inA = true
			}
		}
		if !inA {
			continue
		}
		id, _, err := r.Route(key, Locality{Region: "A", Zone: "z3"})
		if err != nil {
			t.Fatalf("Route: %v", err)
		}
		if id == "b1" {
			t.Fatalf("key %q routed cross-region to b1 despite region-A replica", key)
		}
		r.Done(id)
	}
}

func TestMinimalShardMovement(t *testing.T) {
	const shards, nodes = 1024, 10
	r := mustRouter(t, shards, 1)
	ids := addNodes(t, r, nodes, func(i int) Locality {
		return Locality{Region: "r", Zone: fmt.Sprintf("z%d", i)}
	})

	before := make(map[int]NodeID, shards)
	for s := 0; s < shards; s++ {
		reps, err := r.Replicas(s)
		if err != nil {
			t.Fatalf("Replicas: %v", err)
		}
		before[s] = reps[0]
	}

	victim := ids[3]
	if err := r.RemoveNode(victim); err != nil {
		t.Fatalf("RemoveNode: %v", err)
	}

	moved, movedFromOther := 0, 0
	for s := 0; s < shards; s++ {
		reps, err := r.Replicas(s)
		if err != nil {
			t.Fatalf("Replicas: %v", err)
		}
		if reps[0] == victim {
			t.Fatalf("shard %d still assigned to removed node %s", s, victim)
		}
		if reps[0] != before[s] {
			moved++
			if before[s] != victim {
				movedFromOther++
			}
		}
	}
	// Rendezvous hashing: only shards owned by the removed node may move.
	if movedFromOther != 0 {
		t.Fatalf("%d shards moved off healthy nodes, want 0", movedFromOther)
	}
	// Expect roughly shards/nodes moves; allow generous slack for hashing.
	lo, hi := shards/nodes/2, shards/nodes*2
	if moved < lo || moved > hi {
		t.Fatalf("moved = %d, want within [%d, %d]", moved, lo, hi)
	}
	t.Logf("shard movement after removing 1/%d nodes: %d/%d shards", nodes, moved, shards)
}

func TestLoadAwareTieBreak(t *testing.T) {
	r := mustRouter(t, 8, 2)
	// Both replicas share a zone, so locality ties and load decides.
	if err := r.AddNode("n1", Locality{Region: "r", Zone: "z"}); err != nil {
		t.Fatal(err)
	}
	if err := r.AddNode("n2", Locality{Region: "r", Zone: "z"}); err != nil {
		t.Fatal(err)
	}

	counts := map[NodeID]int{}
	for i := 0; i < 200; i++ {
		id, _, err := r.Route(fmt.Sprintf("key-%d", i), Locality{Region: "r", Zone: "z"})
		if err != nil {
			t.Fatalf("Route: %v", err)
		}
		counts[id]++
		// Never call Done: load accumulates and must steer later routes.
	}
	if counts["n1"] == 0 || counts["n2"] == 0 {
		t.Fatalf("load-aware tie-break never used a node: %v", counts)
	}
	if diff := counts["n1"] - counts["n2"]; diff > 1 || diff < -1 {
		t.Fatalf("load imbalance with in-flight requests: %v", counts)
	}
}

func TestConcurrentRoutingAndMembership(t *testing.T) {
	assertNoGoroutineLeak(t, func() {
		r := mustRouter(t, 512, 3)
		addNodes(t, r, 12, func(i int) Locality {
			return Locality{Region: fmt.Sprintf("region-%d", i%3), Zone: fmt.Sprintf("zone-%d", i)}
		})

		var wg sync.WaitGroup
		// Readers: hammer Route/Done/Replicas concurrently.
		for g := 0; g < 8; g++ {
			wg.Add(1)
			go func(g int) {
				defer wg.Done()
				loc := Locality{Region: fmt.Sprintf("region-%d", g%3), Zone: fmt.Sprintf("zone-%d", g)}
				for i := 0; i < 5000; i++ {
					id, shard, err := r.Route(fmt.Sprintf("key-%d-%d", g, i), loc)
					if err != nil {
						t.Errorf("Route: %v", err)
						return
					}
					if shard < 0 || shard >= r.ShardCount() {
						t.Errorf("shard %d out of range", shard)
						return
					}
					if _, err := r.Replicas(shard); err != nil {
						t.Errorf("Replicas: %v", err)
						return
					}
					r.Done(id)
				}
			}(g)
		}
		// Writers: churn membership with spare nodes.
		for g := 0; g < 2; g++ {
			wg.Add(1)
			go func(g int) {
				defer wg.Done()
				for i := 0; i < 50; i++ {
					id := NodeID(fmt.Sprintf("spare-%d-%d", g, i))
					if err := r.AddNode(id, Locality{Region: "region-0", Zone: "zone-spare"}); err != nil {
						t.Errorf("AddNode: %v", err)
						return
					}
					if err := r.RemoveNode(id); err != nil {
						t.Errorf("RemoveNode: %v", err)
						return
					}
				}
			}(g)
		}
		wg.Wait()

		// All loads must drain back to zero after Done.
		for _, id := range r.Nodes() {
			load, err := r.Load(id)
			if err != nil {
				t.Fatalf("Load(%s): %v", id, err)
			}
			if load != 0 {
				t.Fatalf("node %s load = %d after all Done, want 0", id, load)
			}
		}
	})
}

func TestNoGoroutineLeakOnIdle(t *testing.T) {
	assertNoGoroutineLeak(t, func() {
		r := mustRouter(t, 64, 2)
		ids := addNodes(t, r, 4, func(i int) Locality {
			return Locality{Region: "r", Zone: fmt.Sprintf("z%d", i)}
		})
		for i := 0; i < 1000; i++ {
			id, _, err := r.Route(fmt.Sprintf("k%d", i), Locality{})
			if err != nil {
				t.Fatalf("Route: %v", err)
			}
			r.Done(id)
		}
		for _, id := range ids {
			if err := r.RemoveNode(id); err != nil {
				t.Fatalf("RemoveNode: %v", err)
			}
		}
	})
}
