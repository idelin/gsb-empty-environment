package paxos

import (
	"bytes"
	"fmt"
	"testing"
	"time"
)

// newCluster builds an n-node cluster with long leases so tests are
// deterministic (no wall-clock expiry mid-test).
func newCluster(t *testing.T, n int) (*Network, []*Node) {
	t.Helper()
	net := NewNetwork()
	nodes := make([]*Node, n)
	for i := 0; i < n; i++ {
		nodes[i] = NewNode(Config{ID: i + 1, LeaseDuration: time.Hour}, net)
		net.Add(nodes[i])
	}
	return net, nodes
}

// assertCommittedPrefix verifies that every node has the identical,
// gap-free committed prefix [1..commitIndex].
func assertCommittedPrefix(t *testing.T, nodes []*Node, commitIndex uint64, want [][]byte) {
	t.Helper()
	for _, nd := range nodes {
		if got := nd.CommitIndex(); got != commitIndex {
			t.Fatalf("node %d: commitIndex=%d, want %d", nd.ID(), got, commitIndex)
		}
		for s := uint64(1); s <= commitIndex; s++ {
			e := nd.Entry(s)
			if e == nil {
				t.Fatalf("node %d: hole at committed slot %d", nd.ID(), s)
			}
			if e.State != StateCommitted {
				t.Fatalf("node %d: slot %d not committed", nd.ID(), s)
			}
			if !bytes.Equal(e.Value, want[s-1]) {
				t.Fatalf("node %d: slot %d = %q, want %q", nd.ID(), s, e.Value, want[s-1])
			}
		}
	}
}

// TestBasicElectionAndCommit: a leader is elected and commits consecutive
// entries which replicate to every follower in order.
func TestBasicElectionAndCommit(t *testing.T) {
	_, nodes := newCluster(t, 5)
	leader := nodes[0]
	if err := leader.Campaign(); err != nil {
		t.Fatalf("campaign: %v", err)
	}
	if !leader.IsLeader() {
		t.Fatal("node 1 should be leader")
	}
	vals := []string{"alpha", "beta", "gamma"}
	for i, v := range vals {
		slot, err := leader.Propose([]byte(v))
		if err != nil {
			t.Fatalf("propose %q: %v", v, err)
		}
		if slot != uint64(i+1) {
			t.Fatalf("slot=%d, want %d", slot, i+1)
		}
	}
	want := [][]byte{[]byte("alpha"), []byte("beta"), []byte("gamma")}
	assertCommittedPrefix(t, nodes, 3, want)
}

// TestLeaseSkipsPhase1: while the lease holds, consecutive proposals are
// committed with Phase 2 only — zero additional Prepare messages.
func TestLeaseSkipsPhase1(t *testing.T) {
	net, nodes := newCluster(t, 5)
	leader := nodes[0]
	if err := leader.Campaign(); err != nil {
		t.Fatalf("campaign: %v", err)
	}
	preparesAfterElection := net.PrepareCount()
	for i := 0; i < 8; i++ {
		if _, err := leader.Propose([]byte(fmt.Sprintf("v%d", i))); err != nil {
			t.Fatalf("propose %d: %v", i, err)
		}
	}
	if got := net.PrepareCount(); got != preparesAfterElection {
		t.Fatalf("Phase 1 ran under a valid lease: prepares %d -> %d", preparesAfterElection, got)
	}
	if got := net.AcceptCount(); got != int64(8*5) {
		t.Fatalf("accepts=%d, want %d (8 entries x 5 nodes)", got, 8*5)
	}
	if got := leader.CommitIndex(); got != 8 {
		t.Fatalf("commitIndex=%d, want 8", got)
	}
}

// TestFollowerRejectsPropose: only the lease-holding leader may propose.
func TestFollowerRejectsPropose(t *testing.T) {
	_, nodes := newCluster(t, 5)
	if err := nodes[0].Campaign(); err != nil {
		t.Fatalf("campaign: %v", err)
	}
	if _, err := nodes[2].Propose([]byte("nope")); err != ErrNotLeader {
		t.Fatalf("err=%v, want ErrNotLeader", err)
	}
}

// TestPartitionNewLeaderRepairsAndOverwritesDirtyLog is the acceptance
// scenario:
//
//  1. Node 1 leads and commits slots 1-3.
//  2. Partition {1,2}|{3,4,5}: node 1's proposal X@4 is accepted only by
//     the minority {1,2} — a dirty, uncommitted entry.
//  3. Node 3 campaigns on the majority side, runs Phase 1, plugs slot 4
//     with a no-op and commits fresh entries.
//  4. Heal: the stale leader's proposal is rejected (ErrStaleBallot),
//     and catch-up sync safely overwrites the minority's dirty slot 4.
//     All five nodes converge on the same gap-free committed log.
func TestPartitionNewLeaderRepairsAndOverwritesDirtyLog(t *testing.T) {
	net, nodes := newCluster(t, 5)
	oldLeader, newLeader := nodes[0], nodes[2]

	if err := oldLeader.Campaign(); err != nil {
		t.Fatalf("campaign: %v", err)
	}
	for _, v := range []string{"a1", "a2", "a3"} {
		if _, err := oldLeader.Propose([]byte(v)); err != nil {
			t.Fatalf("propose %q: %v", v, err)
		}
	}
	assertCommittedPrefix(t, nodes, 3, [][]byte{[]byte("a1"), []byte("a2"), []byte("a3")})

	// --- partition: old leader isolated with node 2 ---
	net.Partition([]int{1, 2}, []int{3, 4, 5})

	// Old leader still holds its lease, but cannot reach a majority.
	if _, err := oldLeader.Propose([]byte("X-dirty")); err != ErrNoMajority {
		t.Fatalf("old leader propose under partition: err=%v, want ErrNoMajority", err)
	}
	// The dirty value sits uncommitted on the minority side.
	for _, id := range []int{1, 2} {
		e := nodes[id-1].Entry(4)
		if e == nil || string(e.Value) != "X-dirty" || e.State != StateAccepted {
			t.Fatalf("node %d: expected dirty accepted X-dirty@4, got %+v", id, e)
		}
	}
	if got := oldLeader.CommitIndex(); got != 3 {
		t.Fatalf("old leader commitIndex=%d, want 3 (nothing committed under partition)", got)
	}

	// --- majority side elects a new leader ---
	preparesBefore := net.PrepareCount()
	if err := newLeader.Campaign(); err != nil {
		t.Fatalf("new leader campaign: %v", err)
	}
	if net.PrepareCount() == preparesBefore {
		t.Fatal("new leader must run Phase 1 (Prepare) to repair the log")
	}
	// The majority side never saw slot 4, so the committed prefix is
	// still 3 and the next proposal reuses slot 4, replacing the
	// minority's dirty value.
	if got := newLeader.CommitIndex(); got != 3 {
		t.Fatalf("new leader commitIndex=%d, want 3", got)
	}
	slot, err := newLeader.Propose([]byte("b4"))
	if err != nil {
		t.Fatalf("new leader propose b4: %v", err)
	}
	if slot != 4 {
		t.Fatalf("new leader reused slot %d, want 4 (overwrites minority dirty value)", slot)
	}
	if _, err := newLeader.Propose([]byte("b5")); err != nil {
		t.Fatalf("new leader propose b5: %v", err)
	}

	// --- heal: old leader is stale, minority dirty log is overwritten ---
	net.Heal()
	if _, err := oldLeader.Propose([]byte("stale")); err != ErrStaleBallot {
		t.Fatalf("stale leader propose after heal: err=%v, want ErrStaleBallot", err)
	}
	if oldLeader.IsLeader() {
		t.Fatal("old leader must step down after seeing the higher ballot")
	}

	// Fresh commit on the new leader triggers commit broadcast + catch-up
	// sync on the lagging minority nodes.
	if _, err := newLeader.Propose([]byte("b6")); err != nil {
		t.Fatalf("new leader propose after heal: %v", err)
	}

	want := [][]byte{[]byte("a1"), []byte("a2"), []byte("a3"), []byte("b4"), []byte("b5"), []byte("b6")}
	assertCommittedPrefix(t, nodes, 6, want)

	// The dirty value is gone everywhere: slot 4 now holds b4.
	for _, nd := range nodes {
		if e := nd.Entry(4); e == nil || string(e.Value) != "b4" || e.State != StateCommitted {
			t.Fatalf("node %d: dirty slot 4 not overwritten by b4: %+v", nd.ID(), e)
		}
	}
	if got := net.SyncCount(); got == 0 {
		t.Fatal("minority nodes should have caught up via sync")
	}
}

// TestPromisePreservesAcceptedValue: a value accepted by a majority but
// whose commit notification was lost must be re-proposed (not overwritten)
// by the next leader — the core Paxos safety rule.
func TestPromisePreservesAcceptedValue(t *testing.T) {
	net, nodes := newCluster(t, 5)
	n1, n3 := nodes[0], nodes[2]

	if err := n1.Campaign(); err != nil {
		t.Fatalf("campaign: %v", err)
	}
	// Commit notifications to {3,4,5} will be lost.
	net.DropCommitsTo(1, 3)
	net.DropCommitsTo(1, 4)
	net.DropCommitsTo(1, 5)
	if _, err := n1.Propose([]byte("chosen")); err != nil {
		t.Fatalf("propose: %v", err)
	}
	// Majority accepted "chosen"@1, but {3,4,5} never learned it committed.
	for _, id := range []int{3, 4, 5} {
		e := nodes[id-1].Entry(1)
		if e == nil || e.State != StateAccepted || string(e.Value) != "chosen" {
			t.Fatalf("node %d: expected accepted chosen@1, got %+v", id, e)
		}
		if ci := nodes[id-1].CommitIndex(); ci != 0 {
			t.Fatalf("node %d: commitIndex=%d, want 0", id, ci)
		}
	}

	// Old leader is partitioned away; node 3 takes over.
	net.Partition([]int{1, 2}, []int{3, 4, 5})
	if err := n3.Campaign(); err != nil {
		t.Fatalf("new leader campaign: %v", err)
	}
	// The new leader must have re-proposed the accepted value, not a no-op.
	e := n3.Entry(1)
	if e == nil || e.State != StateCommitted || string(e.Value) != "chosen" {
		t.Fatalf("new leader slot 1 = %+v, want committed %q", e, "chosen")
	}

	net.Heal()
	if _, err := n3.Propose([]byte("next")); err != nil {
		t.Fatalf("propose after heal: %v", err)
	}
	assertCommittedPrefix(t, nodes, 2, [][]byte{[]byte("chosen"), []byte("next")})
}

// TestGapRepairFillsHoles: the new leader's Phase 1 repairs an accepted
// gap so the committed prefix stays contiguous.
func TestGapRepairFillsHoles(t *testing.T) {
	net, nodes := newCluster(t, 5)
	n1, n3 := nodes[0], nodes[2]

	if err := n1.Campaign(); err != nil {
		t.Fatalf("campaign: %v", err)
	}
	if _, err := n1.Propose([]byte("v1")); err != nil {
		t.Fatalf("propose v1: %v", err)
	}

	// Slot 2 is accepted only by the minority {1,2} -> a hole on the
	// majority side. Slot 3 is then accepted by everyone but cannot be
	// committed past the hole.
	net.Partition([]int{1, 2}, []int{3, 4, 5})
	if _, err := n1.Propose([]byte("v2")); err != ErrNoMajority {
		t.Fatalf("propose v2 under partition: err=%v, want ErrNoMajority", err)
	}
	net.Heal()
	if _, err := n1.Propose([]byte("v3")); err != nil {
		t.Fatalf("propose v3: %v", err)
	}
	if got := n1.CommitIndex(); got != 1 {
		t.Fatalf("commitIndex=%d, want 1 (slot 2 is a hole)", got)
	}

	// New leader on the majority side repairs slot 2 with a no-op and
	// re-proposes the accepted v3, then commits everything in order.
	net.Partition([]int{1, 2}, []int{3, 4, 5})
	if err := n3.Campaign(); err != nil {
		t.Fatalf("campaign: %v", err)
	}
	if got := n3.CommitIndex(); got != 3 {
		t.Fatalf("new leader commitIndex=%d, want 3 after gap repair", got)
	}
	if e := n3.Entry(2); e == nil || !e.IsNoop() || e.State != StateCommitted {
		t.Fatalf("slot 2 = %+v, want committed no-op", e)
	}
	if e := n3.Entry(3); e == nil || string(e.Value) != "v3" || e.State != StateCommitted {
		t.Fatalf("slot 3 = %+v, want committed v3", e)
	}

	net.Heal()
	if _, err := n3.Propose([]byte("v4")); err != nil {
		t.Fatalf("propose v4: %v", err)
	}
	want := [][]byte{[]byte("v1"), nil, []byte("v3"), []byte("v4")}
	assertCommittedPrefix(t, nodes, 4, want)
}

// TestNoMajorityCampaignFails: a node on the minority side of a partition
// cannot become leader.
func TestNoMajorityCampaignFails(t *testing.T) {
	net, nodes := newCluster(t, 5)
	net.Partition([]int{1, 2}, []int{3, 4, 5})
	if err := nodes[0].Campaign(); err != ErrNoMajority {
		t.Fatalf("err=%v, want ErrNoMajority", err)
	}
	if nodes[0].IsLeader() {
		t.Fatal("minority-side node must not become leader")
	}
}

// TestCommittedValueNeverChanges: once committed, a slot's value is
// immutable across leadership changes and partitions.
func TestCommittedValueNeverChanges(t *testing.T) {
	net, nodes := newCluster(t, 5)
	if err := nodes[0].Campaign(); err != nil {
		t.Fatalf("campaign: %v", err)
	}
	if _, err := nodes[0].Propose([]byte("immutable")); err != nil {
		t.Fatalf("propose: %v", err)
	}
	// Several leadership changes across partitions.
	for round := 0; round < 3; round++ {
		net.Partition([]int{1, 2}, []int{3, 4, 5})
		if err := nodes[2+round].Campaign(); err != nil {
			t.Fatalf("campaign round %d: %v", round, err)
		}
		if _, err := nodes[2+round].Propose([]byte(fmt.Sprintf("r%d", round))); err != nil {
			t.Fatalf("propose round %d: %v", round, err)
		}
		net.Heal()
	}
	for _, nd := range nodes {
		e := nd.Entry(1)
		if e == nil || string(e.Value) != "immutable" || e.State != StateCommitted {
			t.Fatalf("node %d: committed slot 1 changed: %+v", nd.ID(), e)
		}
	}
}
