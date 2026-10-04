package merge

import (
	"testing"
	"time"
)

// buildSplitBrain creates 10 nodes, closes [1..5] and [6..10] into two
// clusters, partitions the network between them and lets each side evolve
// independently for the given number of gossip rounds.
func buildSplitBrain(t *testing.T, rounds int) (*Network, []*Node) {
	t.Helper()
	net := NewNetwork()
	nodes := make([]*Node, 0, 10)
	for id := 1; id <= 10; id++ {
		nodes = append(nodes, NewNode(id, net))
	}
	FormCluster(nodes[0:5]...)
	FormCluster(nodes[5:10]...)

	net.Partition([]int{1, 2, 3, 4, 5}, []int{6, 7, 8, 9, 10})

	// Each partition evolves independently: clocks tick, membership spreads.
	for i := 0; i < rounds; i++ {
		for _, n := range nodes {
			n.GossipAll()
		}
		PumpAll(net)
	}
	return net, nodes
}

func maxLamport(nodes []*Node) uint64 {
	var max uint64
	for _, n := range nodes {
		if l := n.Lamport(); l > max {
			max = l
		}
	}
	return max
}

// TestPartitionMerge is the acceptance scenario: two isolated partitions
// evolve on their own, then the first successful cross-partition PingReq
// triggers the merge handshake and both sides fuse smoothly.
func TestPartitionMerge(t *testing.T) {
	net, nodes := buildSplitBrain(t, 5)

	// Pre-merge: each side only knows its own 5 members, distinct clusters.
	for _, n := range nodes {
		want := "cluster-1"
		if n.ID() >= 6 {
			want = "cluster-6"
		}
		if got := n.ClusterID(); got != want {
			t.Fatalf("node %d: cluster = %q, want %q", n.ID(), got, want)
		}
		if got := len(n.Members()); got != 5 {
			t.Fatalf("node %d: knows %d members, want 5", n.ID(), got)
		}
	}

	// Cross-partition traffic is dropped while partitioned.
	if ok := nodes[0].PingReq(6); ok {
		t.Fatal("PingReq across the partition should have been dropped")
	}

	preMergeMax := maxLamport(nodes)

	// Heal, then the first successful PingReq triggers the merge handshake.
	net.Heal()
	if ok := nodes[0].PingReq(6); !ok {
		t.Fatal("PingReq after heal should have been delivered")
	}
	PumpAll(net)

	if !Converged(net) {
		t.Fatal("cluster did not converge after merge")
	}
	for _, n := range nodes {
		if got := n.ClusterID(); got != "cluster-1" {
			t.Errorf("node %d: cluster = %q, want cluster-1 (smaller ID survives)", n.ID(), got)
		}
		if got := n.Epoch(); got != 2 {
			t.Errorf("node %d: epoch = %d, want 2 (max(1,1)+1)", n.ID(), got)
		}
		if got := len(n.Members()); got != 10 {
			t.Errorf("node %d: knows %d members, want 10", n.ID(), got)
		}
	}

	// Clock correction: after merge + gossip, every node's Lamport clock
	// must be ahead of the highest pre-merge clock of either partition.
	for _, n := range nodes {
		n.GossipAll()
	}
	PumpAll(net)
	for _, n := range nodes {
		if l := n.Lamport(); l <= preMergeMax {
			t.Errorf("node %d: lamport = %d, want > pre-merge max %d", n.ID(), l, preMergeMax)
		}
	}
}

// TestSingleDriver verifies the deterministic tie-break: even when the larger
// cluster's node probes first, only the smaller-cluster side drives the merge
// and the epoch advances exactly once.
func TestSingleDriver(t *testing.T) {
	net, nodes := buildSplitBrain(t, 3)
	net.Heal()

	// Node 6 (cluster-6) probes node 1 (cluster-1): node 1 must drive.
	if ok := nodes[5].PingReq(1); !ok {
		t.Fatal("PingReq after heal should have been delivered")
	}
	PumpAll(net)

	if !Converged(net) {
		t.Fatal("cluster did not converge")
	}
	for _, n := range nodes {
		if got := n.Epoch(); got != 2 {
			t.Errorf("node %d: epoch = %d, want exactly 2 (single merge, no dual drive)", n.ID(), got)
		}
	}
}

// TestMergeIdempotent verifies that probes after the merge are harmless:
// same cluster on both sides means no new merge is triggered.
func TestMergeIdempotent(t *testing.T) {
	net, nodes := buildSplitBrain(t, 3)
	net.Heal()
	nodes[0].PingReq(6)
	PumpAll(net)
	if !Converged(net) {
		t.Fatal("cluster did not converge")
	}

	// Further probes in both directions must not bump the epoch again.
	nodes[9].PingReq(1)
	nodes[0].PingReq(10)
	PumpAll(net)
	for _, n := range nodes {
		if got := n.Epoch(); got != 2 {
			t.Errorf("node %d: epoch = %d after post-merge probes, want 2", n.ID(), got)
		}
		if got := n.ClusterID(); got != "cluster-1" {
			t.Errorf("node %d: cluster = %q, want cluster-1", n.ID(), got)
		}
	}
}

// TestConcurrentMerge runs the whole scenario on live goroutines (real
// timers, real channels) to shake out deadlocks and data races. Run with
// `go test -race`.
func TestConcurrentMerge(t *testing.T) {
	net := NewNetwork()
	nodes := make([]*Node, 0, 10)
	for id := 1; id <= 10; id++ {
		nodes = append(nodes, NewNode(id, net))
	}
	FormCluster(nodes[0:5]...)
	FormCluster(nodes[5:10]...)
	net.Partition([]int{1, 2, 3, 4, 5}, []int{6, 7, 8, 9, 10})

	for _, n := range nodes {
		n.Run(5*time.Millisecond, 2)
	}
	defer func() {
		for _, n := range nodes {
			n.Stop()
		}
	}()

	// Let both partitions evolve independently.
	time.Sleep(150 * time.Millisecond)

	// Heal and fire the first cross-partition PingReq.
	net.Heal()
	if ok := nodes[0].PingReq(6); !ok {
		t.Fatal("PingReq after heal should have been delivered")
	}

	deadline := time.Now().Add(5 * time.Second)
	for !Converged(net) {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for merge convergence (possible deadlock)")
		}
		time.Sleep(10 * time.Millisecond)
	}

	for _, n := range nodes {
		if got := n.ClusterID(); got != "cluster-1" {
			t.Errorf("node %d: cluster = %q, want cluster-1", n.ID(), got)
		}
		if got := len(n.Members()); got != 10 {
			t.Errorf("node %d: knows %d members, want 10", n.ID(), got)
		}
	}
}
