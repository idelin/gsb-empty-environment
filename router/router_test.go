package router

import (
	"fmt"
	"runtime"
	"strconv"
	"sync"
	"testing"
)

func mustRouter(t *testing.T, shards int) *Router {
	t.Helper()
	r, err := New(shards)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func addNodes(t *testing.T, r *Router, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		loc := Locality{Region: fmt.Sprintf("region-%d", i%2), Zone: fmt.Sprintf("zone-%d", i%4)}
		if err := r.AddNode(Node{ID: fmt.Sprintf("node-%d", i), Locality: loc}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInvalidShardCount(t *testing.T) {
	if _, err := New(0); err != ErrInvalidShards {
		t.Fatalf("want ErrInvalidShards, got %v", err)
	}
	if _, err := New(-7); err != ErrInvalidShards {
		t.Fatalf("want ErrInvalidShards, got %v", err)
	}
}

func TestEmptyRouter(t *testing.T) {
	r := mustRouter(t, 64)
	if _, err := r.Route("k", Locality{}, 1); err != ErrNoNodes {
		t.Fatalf("want ErrNoNodes, got %v", err)
	}
	if _, ok := r.OwnerOf(0); ok {
		t.Fatal("empty router must have no owners")
	}
	if err := r.RemoveNode("ghost"); err != ErrNodeNotFound {
		t.Fatalf("want ErrNodeNotFound, got %v", err)
	}
}

func TestNodeValidation(t *testing.T) {
	r := mustRouter(t, 64)
	if err := r.AddNode(Node{}); err != ErrEmptyNodeID {
		t.Fatalf("want ErrEmptyNodeID, got %v", err)
	}
	if err := r.AddNode(Node{ID: "a", Weight: -1}); err != ErrInvalidWeight {
		t.Fatalf("want ErrInvalidWeight, got %v", err)
	}
	if err := r.AddNode(Node{ID: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := r.AddNode(Node{ID: "a"}); err != ErrDuplicateNode {
		t.Fatalf("want ErrDuplicateNode, got %v", err)
	}
	if _, err := r.Route("k", Locality{}, 0); err != ErrInvalidFanout {
		t.Fatalf("want ErrInvalidFanout, got %v", err)
	}
}

func TestShardStability(t *testing.T) {
	r := mustRouter(t, 1024)
	addNodes(t, r, 8)
	for i := 0; i < 1000; i++ {
		key := "key-" + strconv.Itoa(i)
		if r.ShardOf(key) != r.ShardOf(key) {
			t.Fatalf("shard mapping not deterministic for %q", key)
		}
		s := r.ShardOf(key)
		if s < 0 || s >= 1024 {
			t.Fatalf("shard %d out of range", s)
		}
	}
}

func TestDistributionFairness(t *testing.T) {
	const shards, nodes = 4096, 10
	r := mustRouter(t, shards)
	addNodes(t, r, nodes)
	dist := r.Distribution()
	if len(dist) != nodes {
		t.Fatalf("want %d nodes in distribution, got %d", nodes, len(dist))
	}
	total := 0
	for _, c := range dist {
		total += c
	}
	if total != shards {
		t.Fatalf("shards not fully assigned: %d/%d", total, shards)
	}
	mean := float64(shards) / nodes
	for id, c := range dist {
		ratio := float64(c) / mean
		if ratio < 0.5 || ratio > 2.0 {
			t.Fatalf("node %s holds %d shards (%.2fx mean), unfair", id, c, ratio)
		}
	}
}

func TestWeightedDistribution(t *testing.T) {
	const shards = 4096
	r := mustRouter(t, shards)
	if err := r.AddNode(Node{ID: "heavy", Weight: 3}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := r.AddNode(Node{ID: fmt.Sprintf("light-%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	dist := r.Distribution()
	lightMean := float64(dist["light-0"]+dist["light-1"]+dist["light-2"]) / 3
	ratio := float64(dist["heavy"]) / lightMean
	if ratio < 1.8 || ratio > 4.5 {
		t.Fatalf("weight-3 node should hold ~3x shards, got %.2fx (%v)", ratio, dist)
	}
}

// TestMinimalMovementOnAdd verifies the rendezvous-hashing property:
// adding a node only moves shards *to* the new node, never between old
// nodes, and moves roughly 1/(n+1) of the keyspace.
func TestMinimalMovementOnAdd(t *testing.T) {
	const shards, nodes = 4096, 8
	r := mustRouter(t, shards)
	addNodes(t, r, nodes)
	before := make(map[int]string, shards)
	for s := 0; s < shards; s++ {
		n, _ := r.OwnerOf(s)
		before[s] = n.ID
	}
	if err := r.AddNode(Node{ID: "node-new"}); err != nil {
		t.Fatal(err)
	}
	moved, movedToOld := 0, 0
	for s := 0; s < shards; s++ {
		n, _ := r.OwnerOf(s)
		if n.ID != before[s] {
			moved++
			if n.ID != "node-new" {
				movedToOld++
			}
		}
	}
	if movedToOld != 0 {
		t.Fatalf("%d shards moved between old nodes; only moves to the new node are allowed", movedToOld)
	}
	want := float64(shards) / (nodes + 1)
	if float64(moved) < want*0.4 || float64(moved) > want*2.5 {
		t.Fatalf("moved %d shards, expected roughly %.0f", moved, want)
	}
	t.Logf("moved %d/%d shards (%.1f%%), ideal %.1f%%", moved, shards,
		100*float64(moved)/shards, 100.0/(nodes+1))
}

// TestMinimalMovementOnRemove: removing a node must only reassign the
// shards it owned; every other shard keeps its owner.
func TestMinimalMovementOnRemove(t *testing.T) {
	const shards, nodes = 4096, 8
	r := mustRouter(t, shards)
	addNodes(t, r, nodes)
	before := make(map[int]string, shards)
	for s := 0; s < shards; s++ {
		n, _ := r.OwnerOf(s)
		before[s] = n.ID
	}
	if err := r.RemoveNode("node-3"); err != nil {
		t.Fatal(err)
	}
	for s := 0; s < shards; s++ {
		n, _ := r.OwnerOf(s)
		if before[s] != "node-3" && n.ID != before[s] {
			t.Fatalf("shard %d moved from %s to %s although its owner survived", s, before[s], n.ID)
		}
		if n.ID == "node-3" {
			t.Fatalf("shard %d still owned by removed node", s)
		}
	}
}

func TestLocalityPreference(t *testing.T) {
	r := mustRouter(t, 1024)
	nodes := []Node{
		{ID: "a-zone", Locality: Locality{Region: "r1", Zone: "z1"}},
		{ID: "b-zone", Locality: Locality{Region: "r1", Zone: "z2"}},
		{ID: "c-remote", Locality: Locality{Region: "r2", Zone: "z9"}},
	}
	for _, n := range nodes {
		if err := r.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	client := Locality{Region: "r1", Zone: "z1"}
	for i := 0; i < 500; i++ {
		key := "k" + strconv.Itoa(i)
		got, err := r.Route(key, client, 3)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 3 {
			t.Fatalf("want 3 candidates, got %d", len(got))
		}
		// Rank 0 is always the shard owner.
		owner, _ := r.OwnerOf(r.ShardOf(key))
		if got[0].ID != owner.ID {
			t.Fatalf("rank0 must be shard owner %s, got %s", owner.ID, got[0].ID)
		}
		// Fallbacks (rank >= 1) must be locality-ordered: scores are
		// non-increasing, so same-region always outranks remote.
		prev := 3
		for _, n := range got[1:] {
			s := localityScore(client, n.Locality)
			if s > prev {
				t.Fatalf("key %s: fallbacks not locality-ordered: %v", key, got)
			}
			prev = s
		}
	}
}

func TestRouteFanoutClamped(t *testing.T) {
	r := mustRouter(t, 64)
	addNodes(t, r, 2)
	got, err := r.Route("k", Locality{}, 99)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("fanout must clamp to node count, got %d", len(got))
	}
}

func TestRouteDeterministic(t *testing.T) {
	r := mustRouter(t, 256)
	addNodes(t, r, 6)
	a, err := r.Route("stable-key", Locality{Region: "region-1"}, 4)
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.Route("stable-key", Locality{Region: "region-1"}, 4)
	if err != nil {
		t.Fatal(err)
	}
	for i := range a {
		if a[i].ID != b[i].ID {
			t.Fatalf("routing not deterministic: %v vs %v", a, b)
		}
	}
}

func TestVersionBumps(t *testing.T) {
	r := mustRouter(t, 64)
	v0 := r.Version()
	addNodes(t, r, 3)
	v1 := r.Version()
	if v1 != v0+3 {
		t.Fatalf("version should bump per add: %d -> %d", v0, v1)
	}
	if err := r.RemoveNode("node-1"); err != nil {
		t.Fatal(err)
	}
	if r.Version() != v1+1 {
		t.Fatal("version should bump on remove")
	}
}

// TestConcurrency hammers the router with readers and writers; run with
// -race to prove data-race freedom.
func TestConcurrency(t *testing.T) {
	r := mustRouter(t, 2048)
	addNodes(t, r, 8)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			client := Locality{Region: fmt.Sprintf("region-%d", g%2), Zone: fmt.Sprintf("zone-%d", g%4)}
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				key := fmt.Sprintf("g%d-key%d", g, i)
				if _, err := r.Route(key, client, 3); err != nil && err != ErrNoNodes {
					t.Errorf("route: %v", err)
					return
				}
				r.ShardOf(key)
				r.Distribution()
				r.Nodes()
				r.Version()
			}
		}(g)
	}
	// Writers: churn membership while readers run.
	for i := 0; i < 50; i++ {
		id := fmt.Sprintf("churn-%d", i)
		if err := r.AddNode(Node{ID: id}); err != nil {
			t.Errorf("add: %v", err)
		}
		if err := r.RemoveNode(id); err != nil {
			t.Errorf("remove: %v", err)
		}
	}
	close(stop)
	wg.Wait()
	if len(r.Nodes()) != 8 {
		t.Fatalf("want 8 stable nodes after churn, got %d", len(r.Nodes()))
	}
}

// TestNoGoroutineLeak asserts the router spawns no background
// goroutines: the goroutine count returns to baseline after use.
func TestNoGoroutineLeak(t *testing.T) {
	base := runtime.NumGoroutine()
	for cycle := 0; cycle < 3; cycle++ {
		r := mustRouter(t, 512)
		addNodes(t, r, 16)
		for i := 0; i < 2000; i++ {
			if _, err := r.Route(strconv.Itoa(i), Locality{Region: "region-0"}, 2); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < 16; i++ {
			if err := r.RemoveNode(fmt.Sprintf("node-%d", i)); err != nil {
				t.Fatal(err)
			}
		}
	}
	runtime.GC()
	if got := runtime.NumGoroutine(); got != base {
		t.Fatalf("goroutine leak: baseline %d, after %d", base, got)
	}
}
