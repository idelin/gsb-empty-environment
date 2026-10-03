package shard

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
)

func makeNodes(spec ...Location) []*Node {
	var out []*Node
	for i, loc := range spec {
		out = append(out, &Node{ID: fmt.Sprintf("n%d", i), Loc: loc, Capacity: 1})
	}
	return out
}

// 3 regions x 2 zones x 2 nodes = 12 nodes.
func bigTopology() []*Node {
	var nodes []*Node
	id := 0
	for _, region := range []string{"r1", "r2", "r3"} {
		for _, zone := range []string{"z1", "z2"} {
			for i := 0; i < 2; i++ {
				nodes = append(nodes, &Node{
					ID:       fmt.Sprintf("n%d", id),
					Loc:      Location{Region: region, Zone: zone},
					Capacity: 1,
				})
				id++
			}
		}
	}
	return nodes
}

func TestEmptyRouter(t *testing.T) {
	r, err := NewRouter(128, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Route("k"); err != ErrNoNodes {
		t.Fatalf("err=%v, want ErrNoNodes", err)
	}
	if _, err := r.RouteLocal("k", Location{"r1", "z1"}); err != ErrNoNodes {
		t.Fatalf("err=%v, want ErrNoNodes", err)
	}
	if _, err := r.Replicas("k"); err != ErrNoNodes {
		t.Fatalf("err=%v, want ErrNoNodes", err)
	}
}

func TestInvalidConfig(t *testing.T) {
	if _, err := NewRouter(0, 3); err != ErrBadVShard {
		t.Fatalf("err=%v, want ErrBadVShard", err)
	}
	if _, err := NewRouter(-5, 3); err != ErrBadVShard {
		t.Fatalf("err=%v, want ErrBadVShard", err)
	}
	// replicas<=0 is normalized to 1.
	r, err := NewRouter(16, 0)
	if err != nil {
		t.Fatal(err)
	}
	r.AddNode(&Node{ID: "a", Loc: Location{"r1", "z1"}})
	r.AddNode(&Node{ID: "b", Loc: Location{"r1", "z2"}})
	reps, err := r.Replicas("k")
	if err != nil {
		t.Fatal(err)
	}
	if len(reps) != 1 {
		t.Fatalf("replicas=%d, want 1", len(reps))
	}
}

func TestSingleNode(t *testing.T) {
	r, _ := NewRouter(64, 3)
	r.AddNode(&Node{ID: "solo", Loc: Location{"r1", "z1"}})
	for i := 0; i < 100; i++ {
		id, err := r.Route(fmt.Sprintf("key-%d", i))
		if err != nil {
			t.Fatal(err)
		}
		if id != "solo" {
			t.Fatalf("route=%s, want solo", id)
		}
	}
	// Replication factor is capped at node count.
	reps, _ := r.Replicas("k")
	if len(reps) != 1 {
		t.Fatalf("replicas=%d, want 1 (capped)", len(reps))
	}
}

func TestReplicaCountCappedAtMembership(t *testing.T) {
	r, _ := NewRouter(64, 5)
	for _, n := range makeNodes(Location{"r1", "z1"}, Location{"r1", "z2"}) {
		r.AddNode(n)
	}
	reps, err := r.Replicas("k")
	if err != nil {
		t.Fatal(err)
	}
	if len(reps) != 2 {
		t.Fatalf("replicas=%d, want 2 (capped at 2 nodes)", len(reps))
	}
}

func TestDeterministicRouting(t *testing.T) {
	r, _ := NewRouter(256, 3)
	for _, n := range bigTopology() {
		r.AddNode(n)
	}
	for i := 0; i < 50; i++ {
		key := fmt.Sprintf("key-%d", i)
		first, _ := r.Route(key)
		for j := 0; j < 5; j++ {
			got, _ := r.Route(key)
			if got != first {
				t.Fatalf("Route(%q) unstable: %s vs %s", key, first, got)
			}
		}
	}
}

func TestReplicaDiversity(t *testing.T) {
	r, _ := NewRouter(512, 3)
	nodes := bigTopology()
	byID := make(map[string]*Node)
	for _, n := range nodes {
		byID[n.ID] = n
		r.AddNode(n)
	}
	// With 6 zones available and rf=3, every key's replicas must sit in 3
	// distinct zones (and thus spread across regions when possible).
	for i := 0; i < 200; i++ {
		reps, err := r.Replicas(fmt.Sprintf("k%d", i))
		if err != nil {
			t.Fatal(err)
		}
		if len(reps) != 3 {
			t.Fatalf("replicas=%d, want 3", len(reps))
		}
		zones := make(map[string]bool)
		for _, id := range reps {
			z := byID[id].Loc.Zone + "@" + byID[id].Loc.Region
			if zones[z] {
				t.Fatalf("key k%d: zone %s reused in %v", i, z, reps)
			}
			zones[z] = true
		}
	}
}

func TestLocalityAwareRouting(t *testing.T) {
	r, _ := NewRouter(512, 3)
	nodes := bigTopology()
	byID := make(map[string]*Node)
	for _, n := range nodes {
		byID[n.ID] = n
		r.AddNode(n)
	}
	// For every key and every client zone that hosts a replica of the key,
	// RouteLocal must pick a node in the client's own zone.
	for i := 0; i < 300; i++ {
		key := fmt.Sprintf("key-%d", i)
		reps, _ := r.Replicas(key)
		for _, id := range reps {
			loc := byID[id].Loc
			got, err := r.RouteLocal(key, loc)
			if err != nil {
				t.Fatal(err)
			}
			if byID[got].Loc != loc {
				t.Fatalf("key %s: client at %v routed to %v", key, loc, byID[got].Loc)
			}
		}
	}
	// Cross-region clients must get a same-region node when one holds a replica.
	for i := 0; i < 300; i++ {
		key := fmt.Sprintf("key-%d", i)
		reps, _ := r.Replicas(key)
		regions := make(map[string]bool)
		for _, id := range reps {
			regions[byID[id].Loc.Region] = true
		}
		for region := range regions {
			got, _ := r.RouteLocal(key, Location{Region: region, Zone: "nowhere"})
			if byID[got].Loc.Region != region {
				t.Fatalf("key %s: client in %s routed to %v", key, region, byID[got].Loc)
			}
		}
	}
}

func TestLocalityReducesCrossNodeTraffic(t *testing.T) {
	r, _ := NewRouter(1024, 3)
	for _, n := range bigTopology() {
		r.AddNode(n)
	}
	var keys []string
	for i := 0; i < 2000; i++ {
		keys = append(keys, fmt.Sprintf("key-%d", i))
	}
	// Clients spread across all six zones.
	var clients []Location
	for _, region := range []string{"r1", "r2", "r3"} {
		for _, zone := range []string{"z1", "z2"} {
			clients = append(clients, Location{region, zone})
		}
	}
	st := r.Stats(keys, clients)
	// With zone-diverse replicas (rf=3 over 6 zones), at least half of all
	// routings should stay inside the client's zone or region.
	if st.AvgDistance >= 2.0 {
		t.Fatalf("avg distance %.3f too high, locality not effective", st.AvgDistance)
	}
}

func TestBalanceAcrossNodes(t *testing.T) {
	r, _ := NewRouter(1024, 1)
	nodes := bigTopology()
	for _, n := range nodes {
		r.AddNode(n)
	}
	var keys []string
	for i := 0; i < 20000; i++ {
		keys = append(keys, fmt.Sprintf("key-%d", i))
	}
	st := r.Stats(keys, nil)
	ideal := float64(len(keys)) / float64(len(nodes))
	for _, n := range nodes {
		share := float64(st.PrimaryCount[n.ID])
		if share < ideal*0.6 || share > ideal*1.4 {
			t.Fatalf("node %s share %.0f, ideal %.0f (unbalanced)", n.ID, share, ideal)
		}
	}
}

func TestCapacityWeighting(t *testing.T) {
	r, _ := NewRouter(1024, 1)
	r.AddNode(&Node{ID: "small", Loc: Location{"r1", "z1"}, Capacity: 1})
	r.AddNode(&Node{ID: "big", Loc: Location{"r1", "z2"}, Capacity: 4})
	var keys []string
	for i := 0; i < 20000; i++ {
		keys = append(keys, fmt.Sprintf("k%d", i))
	}
	st := r.Stats(keys, nil)
	small, big := float64(st.PrimaryCount["small"]), float64(st.PrimaryCount["big"])
	ratio := big / small
	if ratio < 2.0 || ratio > 8.0 {
		t.Fatalf("capacity ratio 4:1 but share ratio %.2f", ratio)
	}
}

func TestMinimalMovementOnAdd(t *testing.T) {
	r, _ := NewRouter(1024, 1)
	nodes := bigTopology()
	for _, n := range nodes {
		r.AddNode(n)
	}
	before := make(map[string]string)
	for i := 0; i < 20000; i++ {
		k := fmt.Sprintf("k%d", i)
		id, _ := r.Route(k)
		before[k] = id
	}
	r.AddNode(&Node{ID: "newbie", Loc: Location{"r3", "z1"}, Capacity: 1})
	moved := 0
	for k, old := range before {
		id, _ := r.Route(k)
		if id != old {
			moved++
		}
	}
	// Ideal consistent-hashing movement is 1/13 ~ 7.7%; allow slack.
	frac := float64(moved) / float64(len(before))
	if frac > 0.25 {
		t.Fatalf("moved %.1f%% of keys on single node add, want minimal", frac*100)
	}
	if frac == 0 {
		t.Fatal("no keys moved to the new node at all")
	}
}

func TestRemoveNodeRedistributesOnlyItsShards(t *testing.T) {
	r, _ := NewRouter(1024, 1)
	nodes := bigTopology()
	for _, n := range nodes {
		r.AddNode(n)
	}
	before := make(map[string]string)
	for i := 0; i < 20000; i++ {
		k := fmt.Sprintf("k%d", i)
		id, _ := r.Route(k)
		before[k] = id
	}
	r.RemoveNode("n0")
	for k, old := range before {
		id, _ := r.Route(k)
		if old != "n0" && id != old {
			t.Fatalf("key %s moved from %s to %s though n%s survived", k, old, id, old)
		}
		if id == "n0" {
			t.Fatalf("key %s still routed to removed node", k)
		}
	}
}

func TestRebalanceReturnsMovement(t *testing.T) {
	r, _ := NewRouter(128, 2)
	for _, n := range bigTopology() {
		r.AddNode(n)
	}
	moved := r.AddNode(&Node{ID: "x", Loc: Location{"r2", "z2"}})
	if moved <= 0 || moved > 128 {
		t.Fatalf("moved=%d out of range", moved)
	}
	moved = r.RemoveNode("x")
	if moved <= 0 || moved > 128 {
		t.Fatalf("moved=%d out of range", moved)
	}
}

func TestVShardOfBounds(t *testing.T) {
	r, _ := NewRouter(100, 1)
	for i := 0; i < 1000; i++ {
		v := r.VShardOf(fmt.Sprintf("k%d", i))
		if v < 0 || v >= 100 {
			t.Fatalf("vshard %d out of bounds", v)
		}
	}
}

func TestConcurrentRoutingAndRebalance(t *testing.T) {
	r, _ := NewRouter(512, 3)
	for _, n := range bigTopology() {
		r.AddNode(n)
	}
	var wg sync.WaitGroup
	// Concurrent readers.
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				key := fmt.Sprintf("w%d-k%d", w, i)
				if _, err := r.Route(key); err != nil {
					t.Errorf("route: %v", err)
					return
				}
				if _, err := r.RouteLocal(key, Location{"r1", "z1"}); err != nil {
					t.Errorf("routeLocal: %v", err)
					return
				}
				if _, err := r.Replicas(key); err != nil {
					t.Errorf("replicas: %v", err)
					return
				}
			}
		}(w)
	}
	// Concurrent membership churn.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			id := fmt.Sprintf("churn-%d", i%5)
			r.AddNode(&Node{ID: id, Loc: Location{"r1", "z1"}})
			r.RemoveNode(id)
		}
	}()
	wg.Wait()
}

func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	r, _ := NewRouter(256, 3)
	for _, n := range bigTopology() {
		r.AddNode(n)
	}
	for i := 0; i < 5000; i++ {
		r.Route(fmt.Sprintf("k%d", i))
		r.RouteLocal(fmt.Sprintf("k%d", i), Location{"r2", "z1"})
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}
