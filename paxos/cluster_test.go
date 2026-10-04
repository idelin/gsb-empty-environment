package paxos

import (
	"bytes"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

func makeCluster(net *Network, n int) []*Node {
	nodes := make([]*Node, n+1) // 1-indexed
	for i := 1; i <= n; i++ {
		nodes[i] = NewNode(i, net, 0, nil)
	}
	return nodes
}

func mustCampaign(t *testing.T, n *Node) {
	t.Helper()
	if err := n.Campaign(); err != nil {
		t.Fatalf("node %d campaign: %v", n.ID(), err)
	}
	if !n.IsLeader() {
		t.Fatalf("node %d should be leader", n.ID())
	}
}

func mustPropose(t *testing.T, n *Node, v string) uint64 {
	t.Helper()
	slot, err := n.Propose([]byte(v))
	if err != nil {
		t.Fatalf("node %d propose %q: %v", n.ID(), v, err)
	}
	return slot
}

func assertAppliedEqual(t *testing.T, nodes []*Node, want [][]byte) {
	t.Helper()
	for i := 1; i < len(nodes); i++ {
		got := nodes[i].AppliedValues()
		if len(got) != len(want) {
			t.Fatalf("node %d applied %d entries, want %d: %q", i, len(got), len(want), got)
		}
		for j := range want {
			if !bytes.Equal(got[j], want[j]) {
				t.Fatalf("node %d slot %d = %q, want %q", i, j+1, got[j], want[j])
			}
		}
	}
}

func vals(ss ...string) [][]byte {
	out := make([][]byte, len(ss))
	for i, s := range ss {
		out[i] = []byte(s)
	}
	return out
}

// Acceptance scenario: 5-node cluster, the old leader is partitioned away,
// a new leader is elected by the majority, completes the open slots via
// Phase 1 and safely overwrites the minority's dirty log after healing.
func TestPartitionNewLeaderOverwritesMinorityDirtyLog(t *testing.T) {
	net := NewNetwork()
	nodes := makeCluster(net, 5)

	mustCampaign(t, nodes[1])
	mustPropose(t, nodes[1], "a")
	mustPropose(t, nodes[1], "b")
	mustPropose(t, nodes[1], "c")

	// Partition: {1,2} minority, {3,4,5} majority.
	net.Partition([]int{1, 2}, []int{3, 4, 5})

	// Old leader keeps its lease but cannot reach a quorum: its proposal is
	// accepted only by the minority (nodes 1,2) and stays uncommitted.
	if _, err := nodes[1].Propose([]byte("dirty")); err != ErrNoQuorum {
		t.Fatalf("old leader propose in minority = %v, want ErrNoQuorum", err)
	}
	if nodes[1].Applied() != 3 {
		t.Fatalf("old leader applied %d, want 3 (dirty must not commit)", nodes[1].Applied())
	}
	if e, ok := nodes[2].Entry(4); !ok || string(e.Value) != "dirty" {
		t.Fatalf("node 2 should hold dirty uncommitted entry at slot 4, got %+v ok=%v", e, ok)
	}

	// Majority elects a new leader; Phase 1 completes open slots.
	mustCampaign(t, nodes[3])
	mustPropose(t, nodes[3], "x") // takes slot 4
	mustPropose(t, nodes[3], "y")

	// Heal. The stale leader must not be able to commit with its old term.
	net.HealAll()
	if _, err := nodes[1].Propose([]byte("stale")); err == nil {
		t.Fatal("stale leader must not commit after healing")
	}
	if nodes[1].IsLeader() {
		t.Fatal("old leader should have stepped down after seeing higher term")
	}

	// Minority catches up; dirty entries are overwritten by committed ones.
	if err := nodes[1].SyncFrom(3); err != nil {
		t.Fatal(err)
	}
	if err := nodes[2].SyncFrom(3); err != nil {
		t.Fatal(err)
	}

	assertAppliedEqual(t, nodes, vals("a", "b", "c", "x", "y"))
	if e, _ := nodes[1].Entry(4); string(e.Value) != "x" {
		t.Fatalf("node 1 slot 4 = %q, want committed %q (dirty overwritten)", e.Value, "x")
	}
	if e, _ := nodes[2].Entry(4); string(e.Value) != "x" {
		t.Fatalf("node 2 slot 4 = %q, want committed %q (dirty overwritten)", e.Value, "x")
	}
}

// Under a valid lease the leader commits consecutive entries with Phase 2
// only: zero additional Prepare messages after the election.
func TestLeaderLeaseSkipsPhase1(t *testing.T) {
	net := NewNetwork()
	nodes := makeCluster(net, 5)

	mustCampaign(t, nodes[1])
	preparesAfterElection, _, _ := net.Stats()

	for i := 0; i < 10; i++ {
		mustPropose(t, nodes[1], fmt.Sprintf("v%d", i))
	}
	prepares, accepts, _ := net.Stats()
	if prepares != preparesAfterElection {
		t.Fatalf("Phase 1 ran under valid lease: prepares %d -> %d", preparesAfterElection, prepares)
	}
	// Each of the 10 proposals is one Accept RPC to each of the 4 peers.
	if accepts != 10*4 {
		t.Fatalf("accepts = %d, want %d", accepts, 40)
	}
	if nodes[1].Applied() != 10 {
		t.Fatalf("applied = %d, want 10", nodes[1].Applied())
	}

	// Non-leaders refuse proposals.
	if _, err := nodes[2].Propose([]byte("nope")); err != ErrNotLeader {
		t.Fatalf("follower propose = %v, want ErrNotLeader", err)
	}
}

// Lease expiry forces the leader to refuse proposals until re-election.
func TestLeaseExpiry(t *testing.T) {
	net := NewNetwork()
	nodes := make([]*Node, 2)
	for i := 1; i <= 1; i++ {
		nodes[i] = NewNode(i, net, 20*time.Millisecond, nil)
	}
	// Single-node cluster: majority of 1.
	mustCampaign(t, nodes[1])
	mustPropose(t, nodes[1], "a")
	time.Sleep(30 * time.Millisecond)
	if _, err := nodes[1].Propose([]byte("b")); err != ErrNotLeader {
		t.Fatalf("propose after lease expiry = %v, want ErrNotLeader", err)
	}
	mustCampaign(t, nodes[1]) // re-election renews the lease
	mustPropose(t, nodes[1], "b")
	assertAppliedEqual(t, nodes, vals("a", "b"))
}

// Hole repair: the old leader scattered different uncommitted values for
// slots 4..6 across the cluster, then got partitioned away. The new leader
// must adopt the highest-ballot values where they exist, fill the true hole
// (slot 5) with a no-op, and commit a contiguous log.
func TestHoleRepairWithNoopFill(t *testing.T) {
	net := NewNetwork()
	nodes := makeCluster(net, 5)

	mustCampaign(t, nodes[1])
	mustPropose(t, nodes[1], "s1")
	mustPropose(t, nodes[1], "s2")
	mustPropose(t, nodes[1], "s3")

	// Scatter dirty entries: slot4 -> {1,2}, slot5 -> {1,3}, slot6 -> {1,4}.
	scatter := func(value string, reachablePeers ...int) {
		t.Helper()
		allowed := map[int]bool{}
		for _, p := range reachablePeers {
			allowed[p] = true
		}
		for _, p := range []int{2, 3, 4, 5} {
			if !allowed[p] {
				net.Block(1, p)
			}
		}
		if _, err := nodes[1].Propose([]byte(value)); err != ErrNoQuorum {
			t.Fatalf("scatter propose %q = %v, want ErrNoQuorum", value, err)
		}
		net.HealAll()
	}
	scatter("d4", 2)
	scatter("d5", 3)
	scatter("d6", 4)

	// Partition {1,3} | {2,4,5}: the majority sees slots 4 and 6 but not 5.
	net.Partition([]int{1, 3}, []int{2, 4, 5})
	mustCampaign(t, nodes[2])

	// Repair must have committed slots 4..6 contiguously on the majority.
	for _, id := range []int{2, 4, 5} {
		if nodes[id].Applied() != 6 {
			t.Fatalf("node %d applied %d, want 6 after repair", id, nodes[id].Applied())
		}
	}
	want := vals("s1", "s2", "s3", "d4", string(Noop), "d6")
	for _, id := range []int{2, 4, 5} {
		got := nodes[id].AppliedValues()
		for j := range want {
			if !bytes.Equal(got[j], want[j]) {
				t.Fatalf("node %d slot %d = %q, want %q", id, j+1, got[j], want[j])
			}
		}
	}

	// Heal and catch up the minority; node 1's dirty d5 is overwritten by
	// the committed no-op, node 3's dirty d5 as well.
	net.HealAll()
	if err := nodes[1].SyncFrom(2); err != nil {
		t.Fatal(err)
	}
	if err := nodes[3].SyncFrom(2); err != nil {
		t.Fatal(err)
	}
	assertAppliedEqual(t, nodes, want)
	if e, _ := nodes[1].Entry(5); !IsNoop(e.Value) {
		t.Fatalf("node 1 slot 5 = %q, want noop", e.Value)
	}
}

// Concurrent client proposals to one leader all commit exactly once, in a
// consistent order on every node.
func TestConcurrentPropose(t *testing.T) {
	net := NewNetwork()
	nodes := makeCluster(net, 5)
	mustCampaign(t, nodes[1])

	const clients = 8
	const perClient = 25
	var wg sync.WaitGroup
	for c := 0; c < clients; c++ {
		wg.Add(1)
		go func(c int) {
			defer wg.Done()
			for i := 0; i < perClient; i++ {
				v := []byte(fmt.Sprintf("c%d-%d", c, i))
				if _, err := nodes[1].Propose(v); err != nil {
					t.Errorf("propose: %v", err)
					return
				}
			}
		}(c)
	}
	wg.Wait()

	want := clients * perClient
	for i := 1; i <= 5; i++ {
		if nodes[i].Applied() != uint64(want) {
			t.Fatalf("node %d applied %d, want %d", i, nodes[i].Applied(), want)
		}
	}
	// Every node applied the identical sequence.
	ref := nodes[1].AppliedValues()
	for i := 2; i <= 5; i++ {
		got := nodes[i].AppliedValues()
		for j := range ref {
			if !bytes.Equal(got[j], ref[j]) {
				t.Fatalf("node %d diverges at slot %d", i, j+1)
			}
		}
	}
}

// A committed value is never overwritten, even by a later term's accept.
func TestCommittedEntryNeverOverwritten(t *testing.T) {
	net := NewNetwork()
	nodes := makeCluster(net, 3)
	mustCampaign(t, nodes[1])
	mustPropose(t, nodes[1], "committed")

	// Force a conflicting accept with a higher ballot directly at slot 1.
	resp := nodes[2].handleAccept(AcceptRequest{Entry: Entry{
		Slot:   1,
		Ballot: Ballot{Term: 99, Node: 3},
		Value:  []byte("evil"),
	}})
	if !resp.Ok {
		t.Fatal("accept of already-committed slot should still be acked")
	}
	if e, _ := nodes[2].Entry(1); string(e.Value) != "committed" {
		t.Fatalf("committed entry overwritten: %q", e.Value)
	}
}

// The cluster spawns no background goroutines: tearing down is leak-free.
func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	net := NewNetwork()
	nodes := makeCluster(net, 5)
	mustCampaign(t, nodes[1])
	for i := 0; i < 20; i++ {
		mustPropose(t, nodes[1], fmt.Sprintf("v%d", i))
	}
	net.Partition([]int{1, 2}, []int{3, 4, 5})
	mustCampaign(t, nodes[3])
	mustPropose(t, nodes[3], "x")
	net.HealAll()
	_ = nodes[1].SyncFrom(3)
	_ = nodes[2].SyncFrom(3)
	if after := runtime.NumGoroutine(); after != before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}
