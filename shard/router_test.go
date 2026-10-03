package shard

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
)

func buildRouter(shards uint32, replicas, zones, nodesPerZone int) *Router {
	r := NewRouter(shards, replicas)
	for z := 0; z < zones; z++ {
		for n := 0; n < nodesPerZone; n++ {
			r.AddNode(&Node{
				ID:   NodeID(fmt.Sprintf("node-z%d-%d", z, n)),
				Zone: ZoneID(fmt.Sprintf("zone-%d", z)),
			})
		}
	}
	return r
}

func TestNewRouterPanicsOnInvalidConfig(t *testing.T) {
	for _, cfg := range []struct {
		shards   uint32
		replicas int
	}{{0, 1}, {16, 0}, {16, -1}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("NewRouter(%d, %d) should panic", cfg.shards, cfg.replicas)
				}
			}()
			NewRouter(cfg.shards, cfg.replicas)
		}()
	}
}

func TestRouteEmptyTopology(t *testing.T) {
	r := NewRouter(16, 2)
	if _, _, err := r.Route("key", "zone-0"); err != ErrNoNodes {
		t.Fatalf("expected ErrNoNodes, got %v", err)
	}
}

func TestRouteDeterministic(t *testing.T) {
	r := buildRouter(256, 3, 3, 4)
	for i := 0; i < 200; i++ {
		key := fmt.Sprintf("key-%d", i)
		n1, s1, err := r.Route(key, "zone-1")
		if err != nil {
			t.Fatal(err)
		}
		n2, s2, err := r.Route(key, "zone-1")
		if err != nil {
			t.Fatal(err)
		}
		if n1 != n2 || s1 != s2 {
			t.Fatalf("routing not deterministic for %q: (%s,%d) vs (%s,%d)", key, n1, s1, n2, s2)
		}
	}
}

func TestLocalityPreference(t *testing.T) {
	// Replica factor equals node count, so every shard's replica set
	// contains a node in every zone: routing must always stay local.
	r := buildRouter(128, 6, 3, 2)
	for z := 0; z < 3; z++ {
		zone := ZoneID(fmt.Sprintf("zone-%d", z))
		for i := 0; i < 500; i++ {
			node, _, err := r.Route(fmt.Sprintf("user-%d", i), zone)
			if err != nil {
				t.Fatal(err)
			}
			r.mu.RLock()
			got := r.nodes[node].Zone
			r.mu.RUnlock()
			if got != zone {
				t.Fatalf("client in %s routed to %s in %s", zone, node, got)
			}
		}
	}
}

func TestLocalityFallbackForUnknownZone(t *testing.T) {
	r := buildRouter(128, 2, 2, 2)
	for i := 0; i < 200; i++ {
		node, shard, err := r.Route(fmt.Sprintf("k%d", i), "zone-nonexistent")
		if err != nil {
			t.Fatal(err)
		}
		reps := r.Replicas(shard)
		found := false
		for _, id := range reps {
			if id == node {
				found = true
			}
		}
		if !found {
			t.Fatalf("fallback node %s not in replica set %v", node, reps)
		}
	}
}

func TestReplicaSetSize(t *testing.T) {
	r := buildRouter(64, 3, 2, 2) // 4 nodes, 3 replicas
	for s := uint32(0); s < 64; s++ {
		reps := r.Replicas(ShardID(s))
		if len(reps) != 3 {
			t.Fatalf("shard %d: expected 3 replicas, got %d", s, len(reps))
		}
		seen := make(map[NodeID]bool)
		for _, id := range reps {
			if seen[id] {
				t.Fatalf("shard %d: duplicate replica %s", s, id)
			}
			seen[id] = true
		}
	}
	if got := r.Replicas(ShardID(64)); got != nil {
		t.Fatalf("unknown shard should have nil replicas, got %v", got)
	}
}

func TestReplicasCappedByNodeCount(t *testing.T) {
	r := NewRouter(32, 10)
	r.AddNode(&Node{ID: "a", Zone: "z"})
	r.AddNode(&Node{ID: "b", Zone: "z"})
	for s := uint32(0); s < 32; s++ {
		if reps := r.Replicas(ShardID(s)); len(reps) != 2 {
			t.Fatalf("shard %d: replicas should cap at 2 nodes, got %d", s, len(reps))
		}
	}
}

func TestSingleNode(t *testing.T) {
	r := NewRouter(16, 3)
	r.AddNode(&Node{ID: "only", Zone: "z0"})
	for i := 0; i < 100; i++ {
		node, _, err := r.Route(fmt.Sprintf("k%d", i), "z0")
		if err != nil {
			t.Fatal(err)
		}
		if node != "only" {
			t.Fatalf("single-node topology must always route to it, got %s", node)
		}
	}
}

func TestRemoveNode(t *testing.T) {
	r := buildRouter(128, 2, 2, 3)
	r.RemoveNode("node-z0-0")
	r.RemoveNode("node-z0-0") // idempotent no-op
	if r.NumNodes() != 5 {
		t.Fatalf("expected 5 nodes, got %d", r.NumNodes())
	}
	for s := uint32(0); s < 128; s++ {
		for _, id := range r.Replicas(ShardID(s)) {
			if id == "node-z0-0" {
				t.Fatalf("removed node still owns shard %d", s)
			}
		}
	}
	for i := 0; i < 200; i++ {
		node, _, err := r.Route(fmt.Sprintf("k%d", i), "zone-0")
		if err != nil {
			t.Fatal(err)
		}
		if node == "node-z0-0" {
			t.Fatal("removed node still receives traffic")
		}
	}
}

func TestMinimalMovementOnNodeAdd(t *testing.T) {
	const shards = 1024
	before := buildRouter(shards, 1, 4, 5) // 20 nodes
	primaryBefore := make(map[ShardID]NodeID)
	for s := uint32(0); s < shards; s++ {
		primaryBefore[ShardID(s)] = before.Replicas(ShardID(s))[0]
	}
	after := buildRouter(shards, 1, 4, 5)
	after.AddNode(&Node{ID: "node-new", Zone: "zone-0"})
	moved := 0
	for s := uint32(0); s < shards; s++ {
		if after.Replicas(ShardID(s))[0] != primaryBefore[ShardID(s)] {
			moved++
		}
	}
	// Ideal rendezvous movement is shards/21 ~ 4.8%; allow generous slack.
	if moved == 0 || moved > shards/5 {
		t.Fatalf("expected roughly %d shards to move, got %d", shards/21, moved)
	}
}

func TestShardDistributionBalance(t *testing.T) {
	const shards = 1024
	r := buildRouter(shards, 1, 4, 5)
	dist := r.Distribution()
	if len(dist) != 20 {
		t.Fatalf("all 20 nodes should hold primaries, got %d", len(dist))
	}
	avg := shards / 20
	for id, n := range dist {
		if n < avg/4 || n > avg*4 {
			t.Fatalf("node %s holds %d primaries, far from average %d", id, n, avg)
		}
	}
}

func TestWeightedNodes(t *testing.T) {
	r := NewRouter(512, 1)
	r.AddNode(&Node{ID: "heavy", Zone: "z", Weight: 4})
	r.AddNode(&Node{ID: "light", Zone: "z", Weight: 1})
	r.AddNode(&Node{ID: "zero", Zone: "z", Weight: 0}) // normalized to 1
	dist := r.Distribution()
	if dist["heavy"] <= dist["light"] {
		t.Fatalf("weighted node should hold more shards: %v", dist)
	}
}

func TestAddNodePanicsOnEmptyID(t *testing.T) {
	r := NewRouter(8, 1)
	defer func() {
		if recover() == nil {
			t.Fatal("AddNode with empty ID should panic")
		}
	}()
	r.AddNode(&Node{Zone: "z"})
}

func TestConcurrentRoutingAndRebalance(t *testing.T) {
	before := runtime.NumGoroutine()
	r := buildRouter(256, 3, 3, 3)

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			zone := ZoneID(fmt.Sprintf("zone-%d", g%3))
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				node, shard, err := r.Route(fmt.Sprintf("g%d-key-%d", g, i), zone)
				if err != nil {
					t.Errorf("route failed: %v", err)
					return
				}
				if node == "" {
					t.Errorf("empty node for shard %d", shard)
					return
				}
			}
		}(g)
	}
	// Reconfigure the topology while readers are hammering it.
	for i := 0; i < 30; i++ {
		r.AddNode(&Node{ID: NodeID(fmt.Sprintf("churn-%d", i)), Zone: "zone-0"})
		r.RemoveNode(NodeID(fmt.Sprintf("churn-%d", i)))
	}
	close(stop)
	wg.Wait()

	if got := runtime.NumGoroutine(); got > before+2 {
		t.Fatalf("goroutine leak suspected: before=%d after=%d", before, got)
	}
	if r.NumNodes() != 9 {
		t.Fatalf("churned nodes should all be removed, got %d nodes", r.NumNodes())
	}
}
