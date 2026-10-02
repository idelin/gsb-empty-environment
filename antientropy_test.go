package antientropy

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestStoreLastWriteWins(t *testing.T) {
	s := NewStore()
	if !s.Apply(State{NodeID: "n1", Status: StatusOffline, Version: 2}) {
		t.Fatal("first apply should change the store")
	}
	if s.Apply(State{NodeID: "n1", Status: StatusAlive, Version: 1}) {
		t.Fatal("stale version must not overwrite newer state")
	}
	st, _ := s.Get("n1")
	if st.Status != StatusOffline || st.Version != 2 {
		t.Fatalf("expected offline@v2, got %v", st)
	}
}

func TestMerkleDiffLocatesDivergentBuckets(t *testing.T) {
	a, b := NewStore(), NewStore()
	for i := 0; i < 50; i++ {
		st := State{NodeID: fmt.Sprintf("node-%02d", i), Status: StatusAlive, Version: 1}
		a.Apply(st)
		b.Apply(st)
	}
	// Two divergent facts only.
	a.Apply(State{NodeID: "node-07", Status: StatusOffline, Version: 2})
	a.Apply(State{NodeID: "node-33", Status: StatusOffline, Version: 2})

	diff := Diff(BuildTree(a.Snapshot()), BuildTree(b.Snapshot()))
	want := map[int]bool{bucketOf("node-07"): true, bucketOf("node-33"): true}
	if len(diff.Buckets) != len(want) {
		t.Fatalf("expected %d divergent buckets, got %v", len(want), diff.Buckets)
	}
	for _, bk := range diff.Buckets {
		if !want[bk] {
			t.Fatalf("unexpected bucket %d in diff %v", bk, diff.Buckets)
		}
	}
	if len(diff.LevelStats) == 0 || diff.LevelStats[0] != 1 {
		t.Fatalf("diff must start at the root, got %v", diff.LevelStats)
	}
	if diff.Compared >= BucketCount {
		t.Fatalf("tree pruning failed: compared %d nodes for 2 divergent states", diff.Compared)
	}
	t.Logf("depth-wise mismatches per level: %v (compared %d node pairs)", diff.LevelStats, diff.Compared)

	// Identical trees must yield an empty diff.
	same := Diff(BuildTree(a.Snapshot()), BuildTree(a.Snapshot()))
	if len(same.Buckets) != 0 {
		t.Fatalf("identical trees should not diverge, got %v", same.Buckets)
	}
}

func TestSyncStoresConvergesBothWays(t *testing.T) {
	a, b := NewStore(), NewStore()
	a.Apply(State{NodeID: "n1", Status: StatusOffline, Version: 5})
	b.Apply(State{NodeID: "n2", Status: StatusOffline, Version: 3})
	res := SyncStores(a, b)
	if res.Converged {
		t.Fatal("stores diverged, sync must report non-converged pre-merge")
	}
	if len(res.ALearned) != 1 || len(res.BLearned) != 1 {
		t.Fatalf("each side should learn exactly 1 state, got %v / %v", res.ALearned, res.BLearned)
	}
	if a.RootHash() != b.RootHash() {
		t.Fatal("stores must converge after one sync session")
	}
}

// TestAntiEntropyRepairsGossipLoss is the acceptance scenario:
// 90% of gossip piggyback packets are dropped, leaving nodes that
// miss 2 node-offline events; the background anti-entropy goroutines
// must locate and repair exactly those events via Merkle tree sync.
func TestAntiEntropyRepairsGossipLoss(t *testing.T) {
	const nodeCount = 6
	net := NewNetwork(0.9, 99)

	nodes := make([]*Node, nodeCount)
	for i := 0; i < nodeCount; i++ {
		nodes[i] = NewNode(fmt.Sprintf("n%d", i), net, int64(100+i))
	}
	// Identical baseline membership on every node.
	for _, n := range nodes {
		for i := 0; i < nodeCount; i++ {
			n.Store.Apply(State{NodeID: fmt.Sprintf("n%d", i), Status: StatusAlive, Version: 1})
		}
	}

	// Two node-offline events observed by n0 only.
	events := []State{
		{NodeID: "n2", Status: StatusOffline, Version: 2},
		{NodeID: "n5", Status: StatusOffline, Version: 2},
	}
	for _, ev := range events {
		nodes[0].Publish(ev)
	}

	// Lightweight gossip phase with 90% packet loss.
	for round := 0; round < 8; round++ {
		for _, n := range nodes {
			n.GossipRound(nodes)
		}
	}
	t.Logf("gossip: sent=%d dropped=%d (%.0f%% loss)", net.Sent, net.Dropped,
		100*float64(net.Dropped)/float64(net.Sent))

	// Gossip alone must NOT have converged the cluster.
	lagging := missingEvents(nodes, events)
	if len(lagging) == 0 {
		t.Fatal("expected at least one node to miss the offline events after lossy gossip")
	}
	t.Logf("nodes missing the offline events after gossip: %v", lagging)

	// Start the background anti-entropy goroutines.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, n := range nodes {
		n.StartAntiEntropy(ctx, nodes, 5*time.Millisecond)
	}

	waitForConvergence(t, nodes, 5*time.Second)

	// Every node must now hold the 2 offline events.
	for _, n := range nodes {
		for _, ev := range events {
			st, ok := n.Store.Get(ev.NodeID)
			if !ok || st.Status != StatusOffline || st.Version != ev.Version {
				t.Fatalf("node %s did not converge: %v=%v", n.ID, ev.NodeID, st)
			}
		}
	}

	// A node that missed everything must have repaired exactly the 2 events.
	for _, idx := range lagging {
		repairs := nodes[idx].Repairs()
		if len(repairs) != 2 {
			t.Fatalf("node %s repaired %d states, want exactly the 2 offline events: %v",
				nodes[idx].ID, len(repairs), repairs)
		}
		seen := map[string]bool{}
		for _, r := range repairs {
			seen[r.NodeID] = true
			if r.Status != StatusOffline {
				t.Fatalf("unexpected repair %v on node %s", r, nodes[idx].ID)
			}
		}
		for _, ev := range events {
			if !seen[ev.NodeID] {
				t.Fatalf("node %s never repaired %s", nodes[idx].ID, ev.NodeID)
			}
		}
		t.Logf("node %s repaired exactly: %v", nodes[idx].ID, repairs)
	}
}

func missingEvents(nodes []*Node, events []State) []int {
	var out []int
	for i, n := range nodes {
		missing := 0
		for _, ev := range events {
			st, ok := n.Store.Get(ev.NodeID)
			if !ok || st.Version < ev.Version {
				missing++
			}
		}
		if missing == len(events) {
			out = append(out, i)
		}
	}
	return out
}

func waitForConvergence(t *testing.T, nodes []*Node, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	root := nodes[0].Store.RootHash()
	for time.Now().Before(deadline) {
		converged := true
		for _, n := range nodes[1:] {
			if n.Store.RootHash() != root {
				converged = false
				break
			}
		}
		if converged {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("cluster did not converge via anti-entropy before deadline")
}
