package paxos

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

func mustBecomeLeader(t *testing.T, n *Node) {
	t.Helper()
	if err := n.BecomeLeader(); err != nil {
		t.Fatalf("node %d BecomeLeader: %v", n.ID(), err)
	}
}

func mustPropose(t *testing.T, n *Node, v string) int {
	t.Helper()
	slot, err := n.Propose(v)
	if err != nil {
		t.Fatalf("node %d Propose(%q): %v", n.ID(), v, err)
	}
	return slot
}

func committedValue(t *testing.T, n *Node, slot int) string {
	t.Helper()
	snap := n.Snapshot()
	if slot >= len(snap.Log) || !snap.Log[slot].Committed {
		t.Fatalf("node %d slot %d not committed", n.ID(), slot)
	}
	return snap.Log[slot].Entry.Value
}

func assertAllCommitted(t *testing.T, c *Cluster, slot int, want string) {
	t.Helper()
	for _, n := range c.Nodes() {
		if got := committedValue(t, n, slot); got != want {
			t.Fatalf("node %d slot %d = %q, want %q", n.ID(), slot, got, want)
		}
	}
}

func assertClean(t *testing.T, c *Cluster) {
	t.Helper()
	if vs := AssertInvariants(c.Nodes()); len(vs) > 0 {
		t.Fatalf("invariant violations: %v", vs)
	}
	if rep := Inspect(c.Nodes()); !rep.OK() {
		t.Fatalf("inspection not clean:\n%s", rep)
	}
}

// TestBasicCommitWithLease verifies that a leader with a valid lease
// commits consecutive entries with Phase 2 only (no Prepare messages).
func TestBasicCommitWithLease(t *testing.T) {
	c := NewCluster(5)
	leader := c.Node(0)
	mustBecomeLeader(t, leader)

	preparesBefore, _ := leader.Stats()
	for i := 1; i <= 3; i++ {
		slot := mustPropose(t, leader, fmt.Sprintf("cmd-%d", i))
		if slot != i {
			t.Fatalf("slot = %d, want %d", slot, i)
		}
	}
	preparesAfter, _ := leader.Stats()
	if preparesAfter != preparesBefore {
		t.Fatalf("leader ran Phase 1 despite valid lease: prepares %d -> %d",
			preparesBefore, preparesAfter)
	}
	for i := 1; i <= 3; i++ {
		assertAllCommitted(t, c, i, fmt.Sprintf("cmd-%d", i))
	}
	assertClean(t, c)
}

// TestLeaseExpiryForcesPhase1 verifies that an expired lease makes the
// leader fall back to the full Prepare/Promise round for the next slot.
func TestLeaseExpiryForcesPhase1(t *testing.T) {
	c := NewCluster(3)
	leader := c.Node(0)
	mustBecomeLeader(t, leader)
	mustPropose(t, leader, "a")

	time.Sleep(2 * LeaseDuration)
	if leader.LeaseValid() {
		t.Fatal("lease should have expired")
	}
	preparesBefore, _ := leader.Stats()
	mustPropose(t, leader, "b")
	preparesAfter, _ := leader.Stats()
	if preparesAfter <= preparesBefore {
		t.Fatal("expected a Prepare round after lease expiry")
	}
	assertAllCommitted(t, c, 2, "b")
	assertClean(t, c)
}

// TestProposeOnFollowerRejected checks the not-leader guard.
func TestProposeOnFollowerRejected(t *testing.T) {
	c := NewCluster(3)
	mustBecomeLeader(t, c.Node(0))
	if _, err := c.Node(1).Propose("x"); err != ErrNotLeader {
		t.Fatalf("err = %v, want ErrNotLeader", err)
	}
}

// TestNoQuorumCannotElect checks that a minority node cannot become leader.
func TestNoQuorumCannotElect(t *testing.T) {
	c := NewCluster(5)
	c.Partition([]int{0, 1}, []int{2, 3, 4})
	if err := c.Node(0).BecomeLeader(); err != ErrNoQuorum {
		t.Fatalf("err = %v, want ErrNoQuorum", err)
	}
	if err := c.Node(2).BecomeLeader(); err != nil {
		t.Fatalf("majority side should elect: %v", err)
	}
}

// TestPartitionFailover is the acceptance scenario: in a 5-node cluster
// the old leader is partitioned into the minority, a new leader is
// elected by the majority, completes uncommitted slots via Phase 1 and
// safely overwrites the minority's dirty log after healing.
func TestPartitionFailover(t *testing.T) {
	c := NewCluster(5)
	oldLeader := c.Node(0)
	mustBecomeLeader(t, oldLeader)
	mustPropose(t, oldLeader, "x1")
	mustPropose(t, oldLeader, "x2")

	// Partition: old leader + one follower vs. the majority.
	c.Partition([]int{0, 1}, []int{2, 3, 4})

	// Old leader still holds its lease: the dirty value is accepted by
	// the minority only and can never commit.
	if _, err := oldLeader.Propose("dirty"); err != ErrNoQuorum {
		t.Fatalf("old leader propose err = %v, want ErrNoQuorum", err)
	}
	for _, id := range []int{0, 1} {
		snap := c.Node(id).Snapshot()
		if len(snap.Log) <= 3 || !snap.Log[3].HasAccepted || snap.Log[3].Entry.Value != "dirty" {
			t.Fatalf("node %d should hold the dirty accepted value at slot 3", id)
		}
		if snap.Log[3].Committed {
			t.Fatalf("node %d must not commit the minority value", id)
		}
	}

	// Majority elects a new leader; Phase 1 finds slots 1-2 already
	// committed and nothing at slot 3.
	newLeader := c.Node(2)
	mustBecomeLeader(t, newLeader)
	if got := mustPropose(t, newLeader, "v3"); got != 3 {
		t.Fatalf("new leader proposed at slot %d, want 3", got)
	}
	for _, id := range []int{2, 3, 4} {
		if got := committedValue(t, c.Node(id), 3); got != "v3" {
			t.Fatalf("node %d slot 3 = %q, want v3", id, got)
		}
	}
	// Even mid-partition the safety invariants must hold.
	if vs := AssertInvariants(c.Nodes()); len(vs) > 0 {
		t.Fatalf("invariant violations during partition: %v", vs)
	}

	// Heal: the new leader's heartbeat converges the minority and
	// overwrites the dirty slot 3 with the committed value.
	c.Heal()
	newLeader.SendHeartbeats()
	for _, id := range []int{0, 1} {
		if got := committedValue(t, c.Node(id), 3); got != "v3" {
			t.Fatalf("node %d dirty slot 3 not overwritten: got %q, want v3", id, got)
		}
	}
	if oldLeader.Snapshot().IsLeader {
		t.Fatal("old leader should have stepped down after seeing the higher term")
	}

	// The cluster is fully functional again on all 5 nodes.
	mustPropose(t, newLeader, "v4")
	assertAllCommitted(t, c, 4, "v4")
	assertClean(t, c)
}

// TestNewLeaderLearnsAcceptedValue verifies that a value accepted by a
// previous term is re-proposed (not discarded) by the new leader's
// Phase 1 catch-up.
func TestNewLeaderLearnsAcceptedValue(t *testing.T) {
	c := NewCluster(5)
	n0 := c.Node(0)
	mustBecomeLeader(t, n0)
	mustPropose(t, n0, "a")

	// Isolate the leader; its next value lands on itself only.
	c.Partition([]int{0}, []int{1, 2, 3, 4})
	if _, err := n0.Propose("b"); err != ErrNoQuorum {
		t.Fatalf("err = %v, want ErrNoQuorum", err)
	}
	c.Heal()

	// New leader must learn "b" from the quorum and commit it.
	n1 := c.Node(1)
	mustBecomeLeader(t, n1)
	assertAllCommitted(t, c, 2, "b")
	mustPropose(t, n1, "c")
	assertAllCommitted(t, c, 3, "c")
	assertClean(t, c)
}

// TestHoleRepairViaHeartbeat verifies that a follower missing a
// committed slot (hole below its latest accepted slot) is repaired by
// the leader's heartbeat and that commitIndex only advances contiguously.
func TestHoleRepairViaHeartbeat(t *testing.T) {
	c := NewCluster(5)
	leader := c.Node(0)
	mustBecomeLeader(t, leader)
	mustPropose(t, leader, "s1")

	// Node 4 misses the slot-2 round entirely.
	c.SetLink(0, 4, false)
	mustPropose(t, leader, "s2")
	c.SetLink(0, 4, true)

	// Let the lease expire so the next proposal runs a full Phase 1
	// (which does not carry committed entries) towards every node.
	time.Sleep(2 * LeaseDuration)

	// Slot 3 is accepted by node 4 while slot 2 is still a hole.
	mustPropose(t, leader, "s3")
	snap := c.Node(4).Snapshot()
	if !snap.Log[3].Committed {
		t.Fatal("node 4 should have committed slot 3")
	}
	if snap.CommitIndex != 1 {
		t.Fatalf("node 4 commitIndex = %d, want 1 (hole at slot 2 blocks advance)", snap.CommitIndex)
	}

	leader.SendHeartbeats()
	snap = c.Node(4).Snapshot()
	if !snap.Log[2].Committed || snap.Log[2].Entry.Value != "s2" {
		t.Fatal("heartbeat did not repair the hole at slot 2")
	}
	if snap.CommitIndex != 3 {
		t.Fatalf("node 4 commitIndex = %d, want 3 after repair", snap.CommitIndex)
	}
	assertClean(t, c)
}

// TestConcurrentProposals hammers the leader from many goroutines while
// heartbeats, snapshots and inspections run concurrently. Run with
// -race. The library spawns no goroutines of its own, so the goroutine
// count must return to baseline afterwards (strict no-leak assertion).
func TestConcurrentProposals(t *testing.T) {
	baseline := runtime.NumGoroutine()

	c := NewCluster(5)
	leader := c.Node(0)
	mustBecomeLeader(t, leader)

	const workers = 8
	const perWorker = 5
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				if _, err := leader.Propose(fmt.Sprintf("w%d-%d", w, i)); err != nil {
					t.Errorf("propose: %v", err)
					return
				}
			}
		}(w)
	}
	// Concurrent readers / heartbeat senders.
	stop := make(chan struct{})
	var readers sync.WaitGroup
	for _, n := range c.Nodes() {
		readers.Add(1)
		go func(n *Node) {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = n.Snapshot()
					_ = Inspect(c.Nodes())
					_ = AssertInvariants(c.Nodes())
				}
			}
		}(n)
	}
	readers.Add(1)
	go func() {
		defer readers.Done()
		for {
			select {
			case <-stop:
				return
			default:
				leader.SendHeartbeats()
			}
		}
	}()

	wg.Wait()
	close(stop)
	readers.Wait()

	snap := leader.Snapshot()
	if snap.CommitIndex != workers*perWorker {
		t.Fatalf("commitIndex = %d, want %d", snap.CommitIndex, workers*perWorker)
	}
	leader.SendHeartbeats()
	assertClean(t, c)

	// Strict leak check: the library must not leave goroutines behind.
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > baseline && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > baseline {
		t.Fatalf("goroutine leak: baseline=%d now=%d", baseline, got)
	}
}
