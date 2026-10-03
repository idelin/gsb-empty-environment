package paxos

import (
	"fmt"
	"runtime"
	"testing"
	"time"
)

const testLease = 2 * time.Second

func newCluster(t *testing.T, size int) *Network {
	t.Helper()
	return NewNetwork(size, testLease)
}

// waitLeader waits until exactly one of the given nodes holds a valid lease.
func waitLeader(t *testing.T, net *Network, ids ...int) *Node {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, id := range ids {
			if net.Node(id).IsLeader() {
				return net.Node(id)
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no leader emerged")
	return nil
}

// assertCommittedPrefix checks the gap-free committed prefix of every node
// equals want.
func assertCommittedPrefix(t *testing.T, net *Network, want []Entry) {
	t.Helper()
	for id := 0; id < 5; id++ {
		n := net.Node(id)
		log := n.Log()
		committed := n.Committed()
		if committed < len(want) {
			t.Fatalf("node %d: committed=%d, want >= %d (log=%v)", id, committed, len(want), log)
		}
		for i, e := range want {
			if log[i] != e {
				t.Fatalf("node %d: slot %d = %v, want %v (log=%v)", id, i, log[i], e, log)
			}
		}
		// Gap-free: no zero Entry{} holes anywhere in the log.
		for i, e := range log {
			if e == (Entry{}) {
				t.Fatalf("node %d: hole at slot %d (log=%v)", id, i, log)
			}
		}
	}
}

// TestElectionAndLeaseCommit verifies Phase 1 elects a leader and that, under
// a valid lease, subsequent proposals skip Phase 1 entirely.
func TestElectionAndLeaseCommit(t *testing.T) {
	net := newCluster(t, 5)
	defer net.Close()

	net.Node(0).Campaign()
	leader := waitLeader(t, net, 0)
	if leader.ID() != 0 {
		t.Fatalf("want node 0 as leader, got %d", leader.ID())
	}

	// Election itself commits the anchor no-op.
	if !leader.WaitCommitted(1, 2*time.Second) {
		t.Fatalf("anchor noop not committed, committed=%d", leader.Committed())
	}

	net.ResetStats()
	const proposals = 8
	for i := 0; i < proposals; i++ {
		if _, err := leader.Propose(fmt.Sprintf("cmd-%d", i)); err != nil {
			t.Fatalf("propose %d: %v", i, err)
		}
	}
	if !leader.WaitCommitted(1+proposals, 2*time.Second) {
		t.Fatalf("not all proposals committed: committed=%d", leader.Committed())
	}
	stats := net.Stats()
	if stats[MsgPrepare] != 0 || stats[MsgPromise] != 0 {
		t.Fatalf("Phase 1 messages sent under valid lease: %v", stats)
	}
	if stats[MsgAccept] == 0 {
		t.Fatal("expected Phase 2 traffic")
	}
	// Followers converge to the same committed prefix.
	for id := 1; id < 5; id++ {
		if !net.Node(id).WaitCommitted(1+proposals, 2*time.Second) {
			t.Fatalf("node %d committed=%d, want %d", id, net.Node(id).Committed(), 1+proposals)
		}
	}
}

// TestPartitionReelectionAndDirtyOverwrite is the acceptance scenario:
// 5 nodes, network partition isolates the old leader with a minority; a new
// leader in the majority partition completes uncommitted slots via Phase 1
// and, after healing, safely overwrites the minority's dirty log.
func TestPartitionReelectionAndDirtyOverwrite(t *testing.T) {
	net := newCluster(t, 5)
	defer net.Close()

	// --- Term 1: node 0 leads, commits two entries.
	net.Node(0).Campaign()
	old := waitLeader(t, net, 0)
	old.Propose("A") // slot 1 (slot 0 is the anchor noop)
	old.Propose("B") // slot 2
	if !old.WaitCommitted(3, 2*time.Second) {
		t.Fatalf("term-1 entries not committed: committed=%d", old.Committed())
	}
	for id := 0; id < 5; id++ {
		if !net.Node(id).WaitCommitted(3, 2*time.Second) {
			t.Fatalf("node %d did not commit prefix", id)
		}
	}

	// --- Partial cut: "C" (slot 3) reaches only node 2, so it stays
	// uncommitted but lands inside the future majority partition.
	net.Partition([]int{0}, []int{1, 3, 4})
	if _, err := old.Propose("C"); err != nil {
		t.Fatalf("propose C: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if c := old.Committed(); c > 3 {
		t.Fatalf("C committed without majority: committed=%d", c)
	}
	net.Heal()

	// --- Full partition: {0,1} vs {2,3,4}. A dirty entry stays minority-only.
	net.Partition([]int{0, 1}, []int{2, 3, 4})
	if _, err := old.Propose("DIRTY-D"); err != nil { // slot 4: reaches node 1 only
		t.Fatalf("old leader propose: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	// C (slot 3) may legitimately commit on the old leader once backfill
	// lets node 1 accept it: acceptors {0,1,2} form a quorum intersecting
	// the new majority at node 2, which preserves C. But the minority-only
	// DIRTY-D (slot 4) must never commit.
	if c := old.Committed(); c > 4 {
		t.Fatalf("minority leader committed dirty entry through partition: committed=%d", c)
	}

	// --- Term 2: node 2 campaigns in the majority partition.
	net.Node(2).Campaign()
	newLeader := waitLeader(t, net, 2, 3, 4)
	if newLeader.ID() != 2 {
		t.Fatalf("want node 2 as new leader, got %d", newLeader.ID())
	}
	// Phase 1 learned the uncommitted slot 3 ("C") from node 2's promise;
	// the term-2 anchor no-op at slot 4 commits the repaired prefix.
	if !newLeader.WaitCommitted(5, 2*time.Second) {
		t.Fatalf("new leader did not commit repaired log: committed=%d log=%v",
			newLeader.Committed(), newLeader.Log())
	}
	if _, err := newLeader.Propose("F"); err != nil { // slot 5
		t.Fatalf("new leader propose: %v", err)
	}
	if !newLeader.WaitCommitted(6, 2*time.Second) {
		t.Fatalf("F not committed: committed=%d", newLeader.Committed())
	}

	// Majority nodes must agree: A, B, C preserved; anchor no-op; F committed.
	t2 := newLeader.Log()[4].Term
	want := []Entry{
		{Term: 64, Noop: true}, // term-1 anchor
		{Term: 64, Value: "A"}, // committed pre-partition
		{Term: 64, Value: "B"}, // committed pre-partition
		{Term: 64, Value: "C"}, // uncommitted slot completed via Phase 1
		{Term: t2, Noop: true}, // term-2 anchor; overwrites DIRTY-D slot
		{Term: t2, Value: "F"}, // post-repair proposal; overwrites DIRTY-E slot
	}
	for id := 2; id < 5; id++ {
		if !net.Node(id).WaitCommitted(6, 2*time.Second) {
			t.Fatalf("majority node %d committed=%d, want 6", id, net.Node(id).Committed())
		}
	}

	// --- Heal: the new leader overwrites the minority's dirty log.
	net.Heal()
	newLeader.Sync()
	for id := 0; id < 2; id++ {
		if !net.Node(id).WaitCommitted(6, 2*time.Second) {
			t.Fatalf("minority node %d not repaired: committed=%d log=%v",
				id, net.Node(id).Committed(), net.Node(id).Log())
		}
	}
	assertCommittedPrefix(t, net, want)

	// The old leader must have stepped down and lost its dirty entries.
	if old.IsLeader() {
		t.Fatal("old leader still believes it leads after higher-term Accept")
	}
	for _, e := range old.Log() {
		if e.Value == "DIRTY-D" {
			t.Fatalf("dirty entry survived on minority node: %v", old.Log())
		}
	}

	// All five logs are identical, gap-free, fully committed.
	for id := 1; id < 5; id++ {
		a, b := net.Node(0).Log(), net.Node(id).Log()
		if len(a) != len(b) {
			t.Fatalf("log length mismatch: node 0 has %d, node %d has %d", len(a), id, len(b))
		}
		for i := range a {
			if a[i] != b[i] {
				t.Fatalf("log divergence at slot %d: node 0 %v vs node %d %v", i, a[i], id, b[i])
			}
		}
	}
}

// TestStaleLeaderCannotCommit verifies a partitioned old leader cannot
// commit even after healing, because its ballot is stale.
func TestStaleLeaderCannotCommit(t *testing.T) {
	net := newCluster(t, 5)
	defer net.Close()

	net.Node(1).Campaign()
	old := waitLeader(t, net, 1)
	old.Propose("x")
	if !old.WaitCommitted(2, 2*time.Second) {
		t.Fatal("setup commit failed")
	}

	net.Partition([]int{1}, []int{0, 2, 3, 4})
	net.Node(3).Campaign()
	waitLeader(t, net, 3)

	net.Heal()
	// Old leader's lease may still be valid locally, but its Accepts carry a
	// stale term and must be rejected by the majority.
	if _, err := old.Propose("stale"); err != nil {
		t.Fatalf("propose on stale leader: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if c := old.Committed(); c > 2 {
		t.Fatalf("stale leader committed entry: committed=%d", c)
	}
	if got := net.Node(3).Log(); len(got) > 2 && got[len(got)-1].Value == "stale" {
		t.Fatalf("stale entry leaked into majority log: %v", got)
	}
}

// TestConcurrentProposals checks commit correctness with concurrent client
// proposals on the lease-holding leader.
func TestConcurrentProposals(t *testing.T) {
	net := newCluster(t, 5)
	defer net.Close()

	net.Node(4).Campaign()
	leader := waitLeader(t, net, 4)
	leader.WaitCommitted(1, 2*time.Second)

	const clients = 8
	const perClient = 10
	done := make(chan struct{}, clients)
	for c := 0; c < clients; c++ {
		go func(c int) {
			defer func() { done <- struct{}{} }()
			for i := 0; i < perClient; i++ {
				for {
					_, err := leader.Propose(fmt.Sprintf("c%d-%d", c, i))
					if err == ErrNotLeader {
						time.Sleep(time.Millisecond)
						continue
					}
					if err != nil {
						t.Errorf("propose: %v", err)
					}
					break
				}
			}
		}(c)
	}
	for c := 0; c < clients; c++ {
		<-done
	}
	total := 1 + clients*perClient
	if !leader.WaitCommitted(total, 5*time.Second) {
		t.Fatalf("committed=%d, want %d", leader.Committed(), total)
	}
	for id := 0; id < 5; id++ {
		if !net.Node(id).WaitCommitted(total, 5*time.Second) {
			t.Fatalf("node %d committed=%d, want %d", id, net.Node(id).Committed(), total)
		}
		log := net.Node(id).Log()
		for i, e := range log {
			if e == (Entry{}) {
				t.Fatalf("node %d: hole at slot %d", id, i)
			}
		}
	}
}

// TestNoGoroutineLeak asserts the cluster leaves no goroutines behind.
func TestNoGoroutineLeak(t *testing.T) {
	base := runtime.NumGoroutine()
	for round := 0; round < 3; round++ {
		net := NewNetwork(5, 50*time.Millisecond)
		net.Node(0).Campaign()
		time.Sleep(50 * time.Millisecond)
		net.Node(0).Propose("v")
		time.Sleep(50 * time.Millisecond)
		net.Close()
	}
	// Allow transient goroutines to exit.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= base {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: base=%d now=%d", base, runtime.NumGoroutine())
}

// TestFollowerCatchUp verifies hole repair: a follower that misses an entry
// rejects out-of-range Accepts, and the leader backfills the gap so the log
// stays gap-free.
func TestFollowerCatchUp(t *testing.T) {
	net := newCluster(t, 5)
	defer net.Close()

	net.Node(0).Campaign()
	leader := waitLeader(t, net, 0)
	leader.Propose("one") // slot 1
	if !leader.WaitCommitted(2, 2*time.Second) {
		t.Fatal("setup commit failed")
	}

	// Node 3 misses slot 2 while everyone else receives it.
	net.Partition([]int{3}, []int{0, 1, 2, 4})
	leader.Propose("two") // slot 2
	if !leader.WaitCommitted(3, 2*time.Second) {
		t.Fatal("slot 2 not committed")
	}
	net.Heal()

	// Slot 3 cannot be appended directly on node 3 (hole at slot 2); the
	// leader must backfill from node 3's reported log length.
	leader.Propose("three") // slot 3
	for id := 0; id < 5; id++ {
		if !net.Node(id).WaitCommitted(4, 2*time.Second) {
			t.Fatalf("node %d committed=%d, want 4 (log=%v)", id, net.Node(id).Committed(), net.Node(id).Log())
		}
		log := net.Node(id).Log()
		if len(log) != 4 {
			t.Fatalf("node %d log length=%d, want 4 (log=%v)", id, len(log), log)
		}
		for i, want := range []string{"", "one", "two", "three"} {
			if log[i].Value != want {
				t.Fatalf("node %d slot %d = %q, want %q (log=%v)", id, i, log[i].Value, want, log)
			}
		}
	}
}
