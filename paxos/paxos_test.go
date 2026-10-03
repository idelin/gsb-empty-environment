package paxos

import (
	"bytes"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

func newCluster(t *testing.T, n int) (*Network, []*Node) {
	t.Helper()
	net := NewNetwork()
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("n%d", i)
	}
	nodes := make([]*Node, n)
	for i, id := range ids {
		nodes[i] = NewNode(id, ids, net)
	}
	return net, nodes
}

func totalPrepares(nodes []*Node) int {
	total := 0
	for _, n := range nodes {
		total += n.PreparesHandled()
	}
	return total
}

func assertCommittedEqual(t *testing.T, nodes []*Node, want [][]byte) {
	t.Helper()
	for _, n := range nodes {
		got := n.CommittedEntries()
		if len(got) != len(want) {
			t.Fatalf("%s: committed %d entries, want %d (%q)", n.ID(), len(got), len(want), got)
		}
		for i := range want {
			if !bytes.Equal(got[i], want[i]) {
				t.Fatalf("%s: slot %d = %q, want %q", n.ID(), i+1, got[i], want[i])
			}
		}
	}
}

func TestBasicMajorityCommit(t *testing.T) {
	_, nodes := newCluster(t, 3)
	slot, ok := nodes[0].Propose([]byte("v1"))
	if !ok || slot != 1 {
		t.Fatalf("propose failed: slot=%d ok=%v", slot, ok)
	}
	for _, n := range nodes {
		if n.CommitIndex() != 1 {
			t.Fatalf("%s: commitIndex=%d, want 1", n.ID(), n.CommitIndex())
		}
	}
	assertCommittedEqual(t, nodes, [][]byte{[]byte("v1")})
}

func TestLeaseSkipsPhaseOne(t *testing.T) {
	_, nodes := newCluster(t, 3)
	leader := nodes[0]

	if _, ok := leader.Propose([]byte("a")); !ok {
		t.Fatal("first propose failed")
	}
	preparesAfterFirst := totalPrepares(nodes)
	if preparesAfterFirst == 0 {
		t.Fatal("expected phase 1 on first propose")
	}

	// Lease is valid: subsequent proposes must go straight to Phase 2.
	for i := 0; i < 5; i++ {
		if _, ok := leader.Propose([]byte(fmt.Sprintf("b%d", i))); !ok {
			t.Fatalf("propose %d failed", i)
		}
	}
	if got := totalPrepares(nodes); got != preparesAfterFirst {
		t.Fatalf("lease violated: prepares grew %d -> %d", preparesAfterFirst, got)
	}
	if leader.CommitIndex() != 6 {
		t.Fatalf("commitIndex=%d, want 6", leader.CommitIndex())
	}
}

func TestLeaseExpiryForcesNewCampaign(t *testing.T) {
	_, nodes := newCluster(t, 3)
	leader := nodes[0]
	leader.SetLeaseDuration(20 * time.Millisecond)

	if _, ok := leader.Propose([]byte("a")); !ok {
		t.Fatal("propose failed")
	}
	before := totalPrepares(nodes)
	time.Sleep(30 * time.Millisecond) // let the lease expire
	if _, ok := leader.Propose([]byte("b")); !ok {
		t.Fatal("propose after expiry failed")
	}
	if totalPrepares(nodes) <= before {
		t.Fatal("expected a new campaign after lease expiry")
	}
}

// Acceptance scenario: 5-node cluster, network partition isolates the old
// leader; the majority elects a new leader which repairs uncommitted slots
// via Phase 1 and safely overwrites the minority's dirty log after healing.
func TestPartitionNewLeaderRepairsAndOverwritesDirtyLog(t *testing.T) {
	net, nodes := newCluster(t, 5)
	old, n1, n2, n3, n4 := nodes[0], nodes[1], nodes[2], nodes[3], nodes[4]

	if _, ok := old.Propose([]byte("v1")); !ok {
		t.Fatal("initial propose failed")
	}
	assertCommittedEqual(t, nodes, [][]byte{[]byte("v1")})

	// Partition: {old, n1} vs {n2, n3, n4}.
	net.Partition([]string{old.ID(), n1.ID()}, []string{n2.ID(), n3.ID(), n4.ID()})

	// Old leader still holds its lease and writes a dirty entry to the
	// minority side only — it can never reach a majority.
	if _, ok := old.Propose([]byte("dirty")); ok {
		t.Fatal("minority leader must not commit")
	}
	if v, _ := old.SlotValue(2); !bytes.Equal(v, []byte("dirty")) {
		t.Fatalf("old leader slot2 = %q, want dirty (uncommitted)", v)
	}
	if old.CommitIndex() != 1 {
		t.Fatalf("old leader commitIndex=%d, want 1", old.CommitIndex())
	}

	// Majority side elects a new leader. Phase 1 collects promises from
	// {n2, n3, n4}; none of them ever saw the minority's dirty slot 2, so
	// the committed prefix ends at slot 1 and the next entry takes slot 2.
	if !n2.Campaign() {
		t.Fatal("majority campaign failed")
	}
	if n2.CommitIndex() != 1 {
		t.Fatalf("new leader commitIndex=%d, want 1", n2.CommitIndex())
	}
	if slot, ok := n2.Propose([]byte("v2")); !ok || slot != 2 {
		t.Fatal("new leader propose failed")
	}

	// Heal and sync: the minority's dirty slot 2 is safely overwritten.
	net.Heal()
	n2.SyncFollowers()

	want := [][]byte{[]byte("v1"), []byte("v2")}
	assertCommittedEqual(t, nodes, want)
	for _, n := range nodes {
		if n.CommitIndex() != 2 {
			t.Fatalf("%s: commitIndex=%d, want 2", n.ID(), n.CommitIndex())
		}
		if v, _ := n.SlotValue(2); !bytes.Equal(v, []byte("v2")) {
			t.Fatalf("%s: slot2 = %q, dirty value survived", n.ID(), v)
		}
	}

	// The old leader must have stepped down; its stale lease is void, so
	// rejoining requires a fresh Phase 1 campaign with a higher ballot.
	if old.IsLeader() {
		t.Fatal("old leader still believes it leads")
	}
	preparesBefore := totalPrepares(nodes)
	if _, ok := old.Propose([]byte("rejoin")); !ok {
		t.Fatal("rejoined node should campaign and commit legitimately")
	}
	if totalPrepares(nodes) == preparesBefore {
		t.Fatal("stale lease bypassed phase 1")
	}
	assertCommittedEqual(t, nodes, [][]byte{[]byte("v1"), []byte("v2"), []byte("rejoin")})
	_ = n1
}

// Hole repair: slot 3 was accepted by a minority while slot 2 is empty.
// A new leader must fill slot 2 with a no-op and adopt slot 3's value
// (highest ballot seen in promises), committing the contiguous prefix.
func TestHoleRepairAdoptsHighestBallot(t *testing.T) {
	_, nodes := newCluster(t, 3)
	n0, n1, n2 := nodes[0], nodes[1], nodes[2]

	if _, ok := n0.Propose([]byte("a")); !ok { // slot 1 committed everywhere
		t.Fatal("propose failed")
	}
	// Craft a hole: slot 3 accepted on {n0, n1} only, slot 2 empty.
	n0.mu.Lock()
	ballot := n0.ballot
	n0.mu.Unlock()
	hole := &AcceptRequest{Slot: 3, Ballot: ballot, Value: []byte("c"), From: n0.ID()}
	if !n0.handleAccept(hole).OK || !n1.handleAccept(hole).OK {
		t.Fatal("failed to plant uncommitted slot 3")
	}
	if n0.CommitIndex() != 1 {
		t.Fatalf("commitIndex=%d, want 1 (hole must block commit)", n0.CommitIndex())
	}

	if !n2.Campaign() {
		t.Fatal("campaign failed")
	}
	want := [][]byte{[]byte("a"), NoOpValue, []byte("c")}
	assertCommittedEqual(t, nodes, want)
	for _, n := range nodes {
		if n.CommitIndex() != 3 {
			t.Fatalf("%s: commitIndex=%d, want 3", n.ID(), n.CommitIndex())
		}
	}
}

func TestConcurrentPropose(t *testing.T) {
	_, nodes := newCluster(t, 5)
	leader := nodes[0]
	if !leader.Campaign() {
		t.Fatal("campaign failed")
	}

	const writers = 16
	var wg sync.WaitGroup
	results := make([]uint64, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			slot, ok := leader.Propose([]byte(fmt.Sprintf("w%d", i)))
			if !ok {
				t.Errorf("propose %d failed", i)
				return
			}
			results[i] = slot
		}(i)
	}
	wg.Wait()

	seen := make(map[uint64]bool)
	for _, s := range results {
		if s == 0 || seen[s] {
			t.Fatalf("duplicate/zero slot %d in %v", s, results)
		}
		seen[s] = true
	}
	if leader.CommitIndex() != writers {
		t.Fatalf("commitIndex=%d, want %d", leader.CommitIndex(), writers)
	}
	for _, n := range nodes {
		if n.CommitIndex() != writers {
			t.Fatalf("%s: commitIndex=%d, want %d", n.ID(), n.CommitIndex(), writers)
		}
	}
}

// Strict no-leak assertion: the engine spawns no goroutines, so the
// goroutine count must not grow across a full cluster lifecycle.
func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	for round := 0; round < 3; round++ {
		net, nodes := newCluster(t, 5)
		for i := 0; i < 10; i++ {
			if _, ok := nodes[0].Propose([]byte("x")); !ok {
				t.Fatal("propose failed")
			}
		}
		net.Isolate(nodes[0].ID())
		if !nodes[2].Campaign() {
			t.Fatal("campaign failed")
		}
		net.Heal()
		nodes[2].SyncFollowers()
	}
	runtime.GC()
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}
