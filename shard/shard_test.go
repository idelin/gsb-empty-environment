package shard

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
)

// buildCluster creates zones*zones racks*nodesPerRack nodes:
// zone z0.., rack r0.., node n0.. with IDs "z0-r1-n2".
func buildCluster(r *Router, zones, racks, nodesPerRack int) []Node {
	var out []Node
	for z := 0; z < zones; z++ {
		for rk := 0; rk < racks; rk++ {
			for n := 0; n < nodesPerRack; n++ {
				node := Node{
					ID: fmt.Sprintf("z%d-r%d-n%d", z, rk, n),
					Locality: Locality{
						Zone: fmt.Sprintf("z%d", z),
						Rack: fmt.Sprintf("r%d", rk),
						Node: fmt.Sprintf("z%d-r%d-n%d", z, rk, n),
					},
				}
				if err := r.AddNode(node); err != nil {
					panic(err)
				}
				out = append(out, node)
			}
		}
	}
	return out
}

func TestRouteEmptyCluster(t *testing.T) {
	r := NewRouter(256, 3)
	if _, err := r.Route("key", Locality{}); err != ErrNoNodes {
		t.Fatalf("want ErrNoNodes, got %v", err)
	}
}

func TestAddNodeValidation(t *testing.T) {
	r := NewRouter(64, 1)
	if err := r.AddNode(Node{}); err == nil {
		t.Fatalf("empty node ID must be rejected")
	}
	if err := r.AddNode(Node{ID: "a"}); err != nil {
		t.Fatalf("valid node: %v", err)
	}
	if !r.RemoveNode("a") {
		t.Fatalf("RemoveNode(a) should report true")
	}
	if r.RemoveNode("a") {
		t.Fatalf("RemoveNode(a) twice should report false")
	}
}

func TestDeterministicRouting(t *testing.T) {
	r := NewRouter(512, 3)
	buildCluster(r, 2, 2, 3)
	for _, key := range []string{"alpha", "beta", "gamma", "key-12345"} {
		d1, err := r.Route(key, Locality{})
		if err != nil {
			t.Fatalf("route: %v", err)
		}
		d2, err := r.Route(key, Locality{})
		if err != nil {
			t.Fatalf("route: %v", err)
		}
		if d1.Shard != d2.Shard || d1.Node.ID != d2.Node.ID {
			t.Fatalf("non-deterministic route for %q: %+v vs %+v", key, d1, d2)
		}
		if d1.Shard != r.ShardOf(key) {
			t.Fatalf("ShardOf mismatch for %q", key)
		}
	}
}

func TestLocalityPreference(t *testing.T) {
	r := NewRouter(1024, 3)
	nodes := buildCluster(r, 2, 2, 2)

	// For every replica set, a client co-located with a replica member must
	// be routed to that exact node (HopSameNode).
	for _, n := range nodes {
		// Find a key whose replica set contains n.
		found := false
		for i := 0; i < 4096 && !found; i++ {
			key := fmt.Sprintf("probe-%d", i)
			shardID := r.ShardOf(key)
			for _, id := range r.Replicas(shardID) {
				if id == n.ID {
					found = true
					break
				}
			}
			if !found {
				continue
			}
			d, err := r.Route(key, Locality{Zone: n.Locality.Zone, Rack: n.Locality.Rack, Node: n.ID})
			if err != nil {
				t.Fatalf("route: %v", err)
			}
			if d.Hops != HopSameNode || d.Node.ID != n.ID {
				t.Fatalf("co-located client: want same-node %s, got %s (%s)", n.ID, d.Node.ID, d.Hops)
			}
		}
		if !found {
			t.Fatalf("no key found with %s in replica set", n.ID)
		}
	}
}

func TestLocalityHopClasses(t *testing.T) {
	r := NewRouter(4096, 2)
	buildCluster(r, 4, 2, 2)

	// Scan keys until we have observed every hop class reachable from a
	// fixed client position.
	client := Locality{Zone: "z0", Rack: "r0", Node: "z0-r0-n0"}
	seen := map[HopClass]bool{}
	for i := 0; i < 20000 && len(seen) < 4; i++ {
		d, err := r.Route(fmt.Sprintf("k-%d", i), client)
		if err != nil {
			t.Fatalf("route: %v", err)
		}
		seen[d.Hops] = true
		// Verify the hop class matches the actual topology relationship.
		want := localityDistance(client, d.Node)
		if want != d.Hops {
			t.Fatalf("hop class %s but topology says %s", d.Hops, want)
		}
	}
	if !seen[HopSameNode] || !seen[HopSameRack] || !seen[HopSameZone] || !seen[HopRemote] {
		t.Fatalf("expected to observe all hop classes, got %v", seen)
	}
}

func TestReplicasSpreadAcrossZones(t *testing.T) {
	r := NewRouter(512, 2)
	buildCluster(r, 3, 2, 2)
	for s := uint32(0); s < r.NumShards(); s++ {
		reps := r.Replicas(s)
		if len(reps) != 2 {
			t.Fatalf("shard %d: want 2 replicas, got %v", s, reps)
		}
		zones := map[string]bool{}
		for _, id := range reps {
			for _, n := range r.Nodes() {
				if n.ID == id {
					zones[n.Locality.Zone] = true
				}
			}
		}
		if len(zones) != 2 {
			t.Fatalf("shard %d replicas %v not spread across zones", s, reps)
		}
	}
}

func TestDistributionBalance(t *testing.T) {
	r := NewRouter(4096, 1)
	nodes := buildCluster(r, 2, 2, 3) // 12 equal-weight nodes
	dist := r.Distribution()
	if len(dist) != len(nodes) {
		t.Fatalf("every node should own shards: %v", dist)
	}
	ideal := float64(r.NumShards()) / float64(len(nodes))
	for id, cnt := range dist {
		// HRW hashing is random per shard; allow generous 60% deviation.
		if float64(cnt) < ideal*0.4 || float64(cnt) > ideal*1.6 {
			t.Fatalf("node %s owns %d shards, ideal %.0f", id, cnt, ideal)
		}
	}
}

func TestMinimalMovementOnMembershipChange(t *testing.T) {
	before := NewRouter(4096, 1)
	nodes := buildCluster(before, 2, 2, 4) // 16 nodes

	after := NewRouter(4096, 1)
	buildCluster(after, 2, 2, 4)
	if err := after.AddNode(Node{ID: "z0-r0-nX", Locality: Locality{Zone: "z0", Rack: "r0", Node: "z0-r0-nX"}}); err != nil {
		t.Fatalf("add: %v", err)
	}

	moved, total, err := before.Movement(after)
	if err != nil {
		t.Fatalf("movement: %v", err)
	}
	// Ideal consistent-hashing movement is 1/17 of shards; allow 2x slack.
	limit := total/len(nodes)*2 + 8
	if moved > limit {
		t.Fatalf("too much movement on add: %d/%d (limit %d)", moved, total, limit)
	}

	// Removing the node must restore the original placement.
	after.RemoveNode("z0-r0-nX")
	moved, _, err = before.Movement(after)
	if err != nil {
		t.Fatalf("movement: %v", err)
	}
	if moved != 0 {
		t.Fatalf("removing added node should restore placement, %d moved", moved)
	}
}

func TestStatsAccounting(t *testing.T) {
	r := NewRouter(256, 3)
	buildCluster(r, 2, 2, 2)
	const n = 500
	for i := 0; i < n; i++ {
		d, err := r.Route(fmt.Sprintf("stat-%d", i), Locality{Zone: "z0", Rack: "r0", Node: "z0-r0-n0"})
		if err != nil {
			t.Fatalf("route: %v", err)
		}
		r.Acquire(d.Node.ID)
		r.Done(d.Node.ID)
	}
	st := r.Stats()
	if st.Total != n {
		t.Fatalf("Total=%d, want %d", st.Total, n)
	}
	if st.SameNode+st.SameRack+st.SameZone+st.Remote != st.Total {
		t.Fatalf("hop counters do not add up: %+v", st)
	}
	if st.CrossNode() != st.SameRack+st.SameZone+st.Remote {
		t.Fatalf("CrossNode mismatch: %+v", st)
	}
	// After Done, all load counters must drain to zero.
	for _, node := range r.Nodes() {
		if l := r.Load(node.ID); l != 0 {
			t.Fatalf("node %s load=%d after Done", node.ID, l)
		}
	}
}

func TestLoadAwareTieBreak(t *testing.T) {
	r := NewRouter(256, 3)
	nodes := buildCluster(r, 1, 1, 3) // single rack: all candidates equally local
	client := Locality{Zone: "z0", Rack: "r9"} // no node in rack r9 -> all HopSameZone

	// Saturate one node with in-flight load.
	overloaded := nodes[0].ID
	for i := 0; i < 50; i++ {
		r.Acquire(overloaded)
	}
	// The router must now steer equally-local traffic away from it.
	counts := map[string]int{}
	for i := 0; i < 300; i++ {
		d, err := r.Route(fmt.Sprintf("load-%d", i), client)
		if err != nil {
			t.Fatalf("route: %v", err)
		}
		counts[d.Node.ID]++
	}
	if counts[overloaded] != 0 {
		t.Fatalf("overloaded node %s received %d routes", overloaded, counts[overloaded])
	}
	for i := 0; i < 50; i++ {
		r.Done(overloaded)
	}
	if l := r.Load(overloaded); l != 0 {
		t.Fatalf("load did not drain: %d", l)
	}
	// With balanced load the HRW order breaks ties deterministically.
	d1, _ := r.Route("stable-key", client)
	d2, _ := r.Route("stable-key", client)
	if d1.Node.ID != d2.Node.ID {
		t.Fatalf("balanced-load routing not deterministic: %s vs %s", d1.Node.ID, d2.Node.ID)
	}
}

func TestConcurrentRoutingRaceFree(t *testing.T) {
	before := runtime.NumGoroutine()

	r := NewRouter(1024, 3)
	buildCluster(r, 3, 2, 3)

	var wg sync.WaitGroup
	const workers = 16
	const routesEach = 2000
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			client := Locality{Zone: fmt.Sprintf("z%d", w%3), Rack: fmt.Sprintf("r%d", w%2)}
			for i := 0; i < routesEach; i++ {
				d, err := r.Route(fmt.Sprintf("w%d-key-%d", w, i), client)
				if err != nil {
					t.Errorf("route: %v", err)
					return
				}
				r.Acquire(d.Node.ID)
				r.Done(d.Node.ID)
			}
		}(w)
	}
	// Concurrent membership churn on separate nodes.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			id := fmt.Sprintf("churn-%d", i)
			_ = r.AddNode(Node{ID: id, Locality: Locality{Zone: "z9", Rack: "r9", Node: id}})
			r.RemoveNode(id)
		}
	}()
	wg.Wait()

	st := r.Stats()
	if st.Total != workers*routesEach {
		t.Fatalf("Total=%d, want %d", st.Total, workers*routesEach)
	}
	for _, n := range r.Nodes() {
		if l := r.Load(n.ID); l != 0 {
			t.Fatalf("node %s leaked load %d", n.ID, l)
		}
	}

	// Goroutine leak assertion: the router itself must not spawn or retain
	// goroutines.
	after := runtime.NumGoroutine()
	if after > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}

func TestSnapshotIsolation(t *testing.T) {
	r := NewRouter(64, 2)
	buildCluster(r, 1, 1, 2)
	reps := r.Replicas(0)
	if len(reps) == 0 {
		t.Fatalf("no replicas")
	}
	reps[0] = "corrupted"
	if r.Replicas(0)[0] == "corrupted" {
		t.Fatalf("Replicas must return a copy")
	}
	d, err := r.Route("iso", Locality{})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	d.Replicas[0] = "corrupted"
	if r.Replicas(d.Shard)[0] == "corrupted" {
		t.Fatalf("RouteDecision.Replicas must be a copy")
	}
}
