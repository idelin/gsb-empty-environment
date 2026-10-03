package shard

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"hexgsb/hexgrid"
)

var testNodes = []string{"n1", "n2", "n3", "n4", "n5", "n6", "n7", "n8"}

func testRouter() *Router {
	return NewRouter(4096, 5, testNodes)
}

func beijingDisk() []hexgrid.Cell {
	return hexgrid.KRingDisk(hexgrid.Encode(39.9042, 116.4074, 8), 3)
}

func TestShardLocality(t *testing.T) {
	r := testRouter()
	disk := beijingDisk()
	shards := make(map[uint32]bool)
	for _, c := range disk {
		shards[r.ShardOf(c)] = true
	}
	if len(shards) > 8 {
		t.Fatalf("k=3 disk spread over %d shards, want <= 8", len(shards))
	}
}

func TestShardDeterminism(t *testing.T) {
	r1 := testRouter()
	reversed := make([]string, len(testNodes))
	for i, n := range testNodes {
		reversed[len(testNodes)-1-i] = n
	}
	r2 := NewRouter(4096, 5, reversed)
	c := hexgrid.Encode(39.9042, 116.4074, 8)
	if r1.ShardOf(c) != r2.ShardOf(c) {
		t.Fatal("shard assignment is not deterministic")
	}
	n1, ok1 := r1.NodeForCell(c)
	n2, ok2 := r2.NodeForCell(c)
	if !ok1 || !ok2 || n1 != n2 {
		t.Fatalf("node assignment differs: %s vs %s", n1, n2)
	}
}

func TestRoutePlanCoversAllCells(t *testing.T) {
	r := testRouter()
	disk := beijingDisk()
	plan := r.Route(disk)
	if plan.Cells != len(disk) {
		t.Fatalf("plan.Cells = %d, want %d", plan.Cells, len(disk))
	}
	total := 0
	for node, cells := range plan.Assignments {
		total += len(cells)
		for _, c := range cells {
			owner, ok := r.NodeForCell(c)
			if !ok || owner != node {
				t.Fatalf("cell %s routed to %s but owned by %s", c, node, owner)
			}
		}
	}
	if total != len(disk) {
		t.Fatalf("routed %d cells, want %d", total, len(disk))
	}
	if plan.Shards == 0 || plan.Shards > len(disk) {
		t.Fatalf("implausible shard count %d", plan.Shards)
	}
}

func TestRendezvousMinimalMovement(t *testing.T) {
	nodes := make([]string, 0, 10)
	for i := 0; i < 10; i++ {
		nodes = append(nodes, fmt.Sprintf("node-%02d", i))
	}
	r := NewRouter(4096, 5, nodes)
	before := make(map[uint32]string, r.NumShards())
	for s := 0; s < r.NumShards(); s++ {
		n, ok := r.NodeForShard(uint32(s))
		if !ok {
			t.Fatal("no node for shard")
		}
		before[uint32(s)] = n
	}
	if !r.AddNode("node-10") {
		t.Fatal("AddNode reported duplicate")
	}
	moved := 0
	for s := 0; s < r.NumShards(); s++ {
		n, _ := r.NodeForShard(uint32(s))
		if n != before[uint32(s)] {
			moved++
		}
	}
	frac := float64(moved) / float64(r.NumShards())
	// Ideal rendezvous movement is 1/11 ~ 0.091; allow generous slack.
	if frac > 0.2 {
		t.Fatalf("moved %.3f of shards, want <= 0.2", frac)
	}
}

func TestShardBalance(t *testing.T) {
	r := testRouter()
	counts := make(map[string]int)
	for s := 0; s < r.NumShards(); s++ {
		n, _ := r.NodeForShard(uint32(s))
		counts[n]++
	}
	avg := float64(r.NumShards()) / float64(len(testNodes))
	for _, n := range testNodes {
		c := float64(counts[n])
		if c > avg*2 || c < avg*0.25 {
			t.Fatalf("node %s holds %.0f shards, avg %.0f", n, c, avg)
		}
	}
}

func TestNodeMembership(t *testing.T) {
	r := NewRouter(1024, 5, []string{"a", "b", "a", ""})
	if got := r.Nodes(); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("Nodes = %v", got)
	}
	if r.AddNode("a") {
		t.Fatal("duplicate AddNode reported success")
	}
	if r.AddNode("") {
		t.Fatal("empty AddNode reported success")
	}
	if !r.AddNode("c") {
		t.Fatal("AddNode(c) failed")
	}
	if !r.RemoveNode("c") {
		t.Fatal("RemoveNode(c) failed")
	}
	if r.RemoveNode("c") {
		t.Fatal("RemoveNode of missing node reported success")
	}
	if got := r.Nodes(); len(got) != 2 {
		t.Fatalf("Nodes after churn = %v", got)
	}
}

func TestEmptyRouter(t *testing.T) {
	r := NewRouter(1024, 5, nil)
	if _, ok := r.NodeForShard(0); ok {
		t.Fatal("empty router resolved a node")
	}
	plan := r.Route([]hexgrid.Cell{hexgrid.Encode(0, 0, 8)})
	if len(plan.Assignments) != 0 {
		t.Fatalf("empty router assigned cells: %v", plan.Assignments)
	}
}

func TestNewRouterPanicsOnBadShards(t *testing.T) {
	for _, n := range []int{0, -4, 3, 1000} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("NewRouter(%d): expected panic", n)
				}
			}()
			NewRouter(n, 5, testNodes)
		}()
	}
}

func TestLocalityScore(t *testing.T) {
	r := testRouter()
	total, cross := r.LocalityScore(beijingDisk())
	if total == 0 {
		t.Fatal("no adjacent pairs counted")
	}
	ratio := float64(cross) / float64(total)
	if ratio > 0.34 {
		t.Fatalf("cross-node ratio %.3f too high (%d/%d)", ratio, cross, total)
	}
}

func TestRouterConcurrencyAndLeak(t *testing.T) {
	base := runtime.NumGoroutine()
	r := testRouter()
	disk := beijingDisk()

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				plan := r.Route(disk)
				if plan.Cells != len(disk) {
					t.Error("bad plan")
					return
				}
				if _, ok := r.NodeForCell(disk[0]); !ok {
					t.Error("node lookup failed")
					return
				}
				_ = r.Nodes()
			}
		}()
	}
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				name := fmt.Sprintf("tmp-%d-%d", id, i)
				r.AddNode(name)
				r.RemoveNode(name)
			}
		}(g)
	}
	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()

	// The router must not leak goroutines.
	for i := 0; i < 200; i++ {
		if runtime.NumGoroutine() <= base {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > base {
		t.Fatalf("goroutine leak: before=%d after=%d", base, got)
	}
}
