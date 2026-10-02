package shard

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

// buildGrid creates zones x racks x nodesPerRack nodes.
func buildGrid(r *Router, zones, racks, nodesPerRack int) []NodeInfo {
	var nodes []NodeInfo
	id := 1
	for z := 0; z < zones; z++ {
		for rk := 0; rk < racks; rk++ {
			for i := 0; i < nodesPerRack; i++ {
				n := NodeInfo{
					ID:   id,
					Zone: fmt.Sprintf("zone-%c", 'a'+z),
					Rack: fmt.Sprintf("rack-%d", rk),
				}
				nodes = append(nodes, n)
				r.AddNode(n)
				id++
			}
		}
	}
	return nodes
}

func TestPlacementSpreadsAcrossZones(t *testing.T) {
	r := NewRouter(512, 3)
	buildGrid(r, 3, 2, 2) // 3 zones, 12 nodes

	for shard := 0; shard < r.NumShards(); shard++ {
		replicas, err := r.Replicas(shard)
		if err != nil {
			t.Fatalf("shard %d: %v", shard, err)
		}
		if len(replicas) != 3 {
			t.Fatalf("shard %d has %d replicas, want 3", shard, len(replicas))
		}
		zones := map[string]bool{}
		ids := map[int]bool{}
		for _, n := range replicas {
			if zones[n.Zone] {
				t.Fatalf("shard %d: zone %s used twice: %v", shard, n.Zone, replicas)
			}
			if ids[n.ID] {
				t.Fatalf("shard %d: node %d used twice", shard, n.ID)
			}
			zones[n.Zone] = true
			ids[n.ID] = true
		}
	}
}

func TestPlacementSpreadsAcrossRacksWhenZonesExhausted(t *testing.T) {
	r := NewRouter(256, 4)
	buildGrid(r, 2, 3, 2) // 2 zones x 3 racks x 2 nodes

	for shard := 0; shard < r.NumShards(); shard++ {
		replicas, _ := r.Replicas(shard)
		if len(replicas) != 4 {
			t.Fatalf("shard %d has %d replicas, want 4", shard, len(replicas))
		}
		// With only 2 zones, at most 2 replicas may share a zone, and the
		// two replicas inside one zone must sit in different racks.
		byZone := map[string][]string{}
		for _, n := range replicas {
			byZone[n.Zone] = append(byZone[n.Zone], n.Rack)
		}
		for z, racks := range byZone {
			if len(racks) > 2 {
				t.Fatalf("shard %d: %d replicas in zone %s", shard, len(racks), z)
			}
			if len(racks) == 2 && racks[0] == racks[1] {
				t.Fatalf("shard %d: same rack %s twice in zone %s", shard, racks[0], z)
			}
		}
	}
}

func TestPlacementDeterministic(t *testing.T) {
	build := func() [][]int {
		r := NewRouter(128, 3)
		buildGrid(r, 3, 2, 2)
		return r.Snapshot()
	}
	a, b := build(), build()
	if fmt.Sprint(a) != fmt.Sprint(b) {
		t.Fatal("placement is not deterministic across identical builds")
	}
}

func TestRouteDeterministicAndConsistent(t *testing.T) {
	r := NewRouter(128, 3)
	buildGrid(r, 3, 2, 2)
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("key-%d", i)
		s1, rep1, err := r.Route(key)
		if err != nil {
			t.Fatal(err)
		}
		s2, rep2, err := r.Route(key)
		if err != nil {
			t.Fatal(err)
		}
		if s1 != s2 || fmt.Sprint(rep1) != fmt.Sprint(rep2) {
			t.Fatalf("key %q routed inconsistently", key)
		}
		if s1 != r.ShardOf(key) {
			t.Fatalf("Route shard %d != ShardOf %d", s1, r.ShardOf(key))
		}
	}
}

func TestRouteLocalPrefersClosestReplica(t *testing.T) {
	r := NewRouter(512, 3)
	nodes := buildGrid(r, 3, 2, 2)

	// Client located on a specific node must get distance 0 (itself) when
	// it holds a replica, else the closest available replica.
	sameZone, sameRack, self, remote := 0, 0, 0, 0
	for i := 0; i < 5000; i++ {
		key := fmt.Sprintf("k-%d", i)
		_, replicas, _ := r.Route(key)
		for _, client := range nodes {
			best, err := r.RouteLocal(key, client)
			if err != nil {
				t.Fatal(err)
			}
			// Verify minimality against brute force.
			want := 4
			for _, rep := range replicas {
				if d := Distance(client, rep); d < want {
					want = d
				}
			}
			if got := Distance(client, best); got != want {
				t.Fatalf("key %q client %+v: distance %d, want minimal %d", key, client, got, want)
			}
		}
		// Tally locality for a client pinned to the primary's rack.
		primary := replicas[0]
		best, _ := r.RouteLocal(key, primary)
		switch Distance(primary, best) {
		case 0:
			self++
		case 1:
			sameRack++
		case 2:
			sameZone++
		case 3:
			remote++
		}
	}
	if remote > 0 {
		t.Fatalf("client on a replica node got remote routes: %d", remote)
	}
	if self+sameRack+sameZone != 5000 {
		t.Fatalf("unexpected tally: self=%d rack=%d zone=%d", self, sameRack, sameZone)
	}
}

func TestRouteLocalDeterministicTieBreak(t *testing.T) {
	r := NewRouter(256, 3)
	buildGrid(r, 3, 2, 2)
	client := NodeInfo{ID: 9999, Zone: "zone-a", Rack: "rack-0"}
	for i := 0; i < 500; i++ {
		key := fmt.Sprintf("tie-%d", i)
		a, _ := r.RouteLocal(key, client)
		b, _ := r.RouteLocal(key, client)
		if a != b {
			t.Fatalf("RouteLocal not deterministic for %q: %+v vs %+v", key, a, b)
		}
	}
}

func TestRebalanceMinimalMovement(t *testing.T) {
	const numShards = 1024
	r := NewRouter(numShards, 3)
	nodes := buildGrid(r, 3, 3, 1) // 9 nodes
	before := r.Snapshot()

	r.AddNode(NodeInfo{ID: 100, Zone: "zone-a", Rack: "rack-9"})
	after := r.Snapshot()

	moved := 0
	for s := 0; s < numShards; s++ {
		if fmt.Sprint(before[s]) != fmt.Sprint(after[s]) {
			moved++
		}
	}
	frac := float64(moved) / numShards
	// Ideal rendezvous movement is ~replicas/(N+1) = 30%; allow slack.
	if frac > 0.45 {
		t.Fatalf("moved %.1f%% of shards on single node add, want <= 45%%", frac*100)
	}
	if moved == 0 {
		t.Fatal("no shards moved at all; new node receives no load")
	}
	_ = nodes
}

func TestNodeRemovalReassignsShards(t *testing.T) {
	r := NewRouter(256, 3)
	buildGrid(r, 3, 2, 2)
	v0 := r.Version()

	r.RemoveNode(1)
	if r.Version() == v0 {
		t.Fatal("version not bumped after removal")
	}
	for shard := 0; shard < r.NumShards(); shard++ {
		replicas, err := r.Replicas(shard)
		if err != nil {
			t.Fatalf("shard %d: %v", shard, err)
		}
		if len(replicas) != 3 {
			t.Fatalf("shard %d has %d replicas after removal, want 3", shard, len(replicas))
		}
		for _, n := range replicas {
			if n.ID == 1 {
				t.Fatalf("shard %d still routed to removed node 1", shard)
			}
		}
	}
}

func TestLoadBalanceAcrossNodes(t *testing.T) {
	r := NewRouter(1024, 3)
	buildGrid(r, 3, 2, 2) // 12 nodes
	dist := r.Distribution()
	if len(dist) != 12 {
		t.Fatalf("distribution covers %d nodes, want 12", len(dist))
	}
	total := 0
	min, max := 1<<30, 0
	for _, c := range dist {
		total += c
		if c < min {
			min = c
		}
		if c > max {
			max = c
		}
	}
	if total != 1024*3 {
		t.Fatalf("total replica slots = %d, want %d", total, 1024*3)
	}
	avg := float64(total) / 12
	if float64(max) > avg*1.6 {
		t.Fatalf("hot node: max=%d avg=%.0f (min=%d)", max, avg, min)
	}
}

// Edge cases.
func TestEdgeCases(t *testing.T) {
	// Empty router.
	r := NewRouter(64, 3)
	if _, _, err := r.Route("k"); err != ErrNoNodes {
		t.Fatalf("empty router err = %v, want ErrNoNodes", err)
	}
	if _, err := r.RouteLocal("k", NodeInfo{ID: 1}); err != ErrNoNodes {
		t.Fatalf("empty router RouteLocal err = %v, want ErrNoNodes", err)
	}

	// Clamped constructor arguments.
	r = NewRouter(0, 0)
	if r.NumShards() != 1 {
		t.Fatalf("numShards = %d, want clamped to 1", r.NumShards())
	}

	// Single node, replicas > nodes: replica set shrinks to cluster size.
	r = NewRouter(16, 5)
	r.AddNode(NodeInfo{ID: 1, Zone: "z", Rack: "r"})
	replicas, err := r.Replicas(3)
	if err != nil {
		t.Fatal(err)
	}
	if len(replicas) != 1 {
		t.Fatalf("single-node cluster: %d replicas, want 1", len(replicas))
	}
	best, err := r.RouteLocal("anything", NodeInfo{ID: 99, Zone: "far", Rack: "away"})
	if err != nil || best.ID != 1 {
		t.Fatalf("single-node RouteLocal = %+v, %v", best, err)
	}

	// Out-of-range shard id.
	if _, err := r.Replicas(-1); err == nil {
		t.Fatal("negative shard id accepted")
	}
	if _, err := r.Replicas(16); err == nil {
		t.Fatal("out-of-range shard id accepted")
	}

	// Re-adding the same node id replaces it without duplicating.
	r.AddNode(NodeInfo{ID: 1, Zone: "z2", Rack: "r2"})
	if got := len(r.Distribution()); got != 1 {
		t.Fatalf("node replaced, but distribution has %d nodes", got)
	}
}

func TestConcurrentRoutingAndMembership(t *testing.T) {
	r := NewRouter(512, 3)
	buildGrid(r, 3, 2, 2)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Readers: hammer routing from many goroutines.
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			client := NodeInfo{ID: 1000 + w, Zone: fmt.Sprintf("zone-%c", 'a'+w%3), Rack: "rack-0"}
			i := 0
			for {
				select {
				case <-stop:
					return
				default:
				}
				key := fmt.Sprintf("w%d-%d", w, i)
				if _, _, err := r.Route(key); err != nil {
					t.Errorf("route: %v", err)
					return
				}
				if _, err := r.RouteLocal(key, client); err != nil {
					t.Errorf("route local: %v", err)
					return
				}
				_ = r.Distribution()
				_ = r.Snapshot()
				i++
			}
		}(w)
	}

	// Writers: churn membership.
	writersDone := make(chan struct{})
	var wwwg sync.WaitGroup
	for w := 0; w < 3; w++ {
		wwwg.Add(1)
		go func(w int) {
			defer wwwg.Done()
			for i := 0; i < 20; i++ {
				id := 5000 + w*1000 + i
				r.AddNode(NodeInfo{ID: id, Zone: fmt.Sprintf("zone-%c", 'a'+i%3), Rack: fmt.Sprintf("rack-%d", i%2)})
				r.RemoveNode(id)
			}
		}(w)
	}
	go func() { wwwg.Wait(); close(writersDone) }()

	// Let readers hammer the router while writers churn, then stop.
	<-writersDone
	close(stop)
	wg.Wait()

	// Router must still be consistent and functional.
	if _, _, err := r.Route("final"); err != nil {
		t.Fatalf("route after churn: %v", err)
	}
	for shard := 0; shard < r.NumShards(); shard++ {
		replicas, err := r.Replicas(shard)
		if err != nil {
			t.Fatalf("shard %d after churn: %v", shard, err)
		}
		if len(replicas) != 3 {
			t.Fatalf("shard %d has %d replicas after churn", shard, len(replicas))
		}
	}
}

func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()

	r := NewRouter(256, 3)
	buildGrid(r, 3, 2, 2)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_, _, _ = r.Route(fmt.Sprintf("k%d", i))
				_, _ = r.RouteLocal(fmt.Sprintf("k%d", i), NodeInfo{ID: w, Zone: "zone-a", Rack: "rack-0"})
			}
		}(w)
	}
	wg.Wait()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		if runtime.NumGoroutine() <= before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: before=%d after=%d", before, runtime.NumGoroutine())
}
