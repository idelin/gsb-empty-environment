package paxos

import (
	"reflect"
	"testing"
)

// TestElectionAndBasicReplication covers the happy path: deterministic
// election, Phase-2 replication, majority commit and in-order apply.
func TestElectionAndBasicReplication(t *testing.T) {
	_, nodes := NewCluster(5)
	leader := tickUntilLeader(nodes, 50)
	if leader == nil {
		t.Fatal("no leader elected")
	}
	if leader.Snapshot().ID != 0 {
		t.Fatalf("expected node 0 (lowest timeout) to win, got node %d", leader.Snapshot().ID)
	}
	mustPropose(t, leader, "a")
	mustPropose(t, leader, "b")
	mustPropose(t, leader, "c")
	tickAll(nodes, 5)
	assertConverged(t, nodes, []string{"a", "b", "c"})
}

// TestLeaseBypassesPhase1 proves that a lease-holding leader commits new
// entries with Phase 2 only: no Prepare/Promise traffic is generated.
func TestLeaseBypassesPhase1(t *testing.T) {
	net, nodes := NewCluster(5)
	leader := tickUntilLeader(nodes, 50)
	if leader == nil {
		t.Fatal("no leader elected")
	}
	net.ResetCounts()
	if _, err := leader.Propose([]byte("x")); err != nil {
		t.Fatalf("propose under valid lease: %v", err)
	}
	if got := net.Count(MsgPrepare); got != 0 {
		t.Fatalf("Prepare messages under valid lease: %d", got)
	}
	if got := net.Count(MsgPromise); got != 0 {
		t.Fatalf("Promise messages under valid lease: %d", got)
	}
	if got := net.Count(MsgAccept); got != 4 {
		t.Fatalf("expected 4 Accept broadcasts, got %d", got)
	}
	if got := net.Count(MsgAccepted); got != 4 {
		t.Fatalf("expected 4 Accepted replies, got %d", got)
	}
	tickAll(nodes, 5)
	assertConverged(t, nodes, []string{"x"})
}

// TestProposeRejectedWithoutLease: followers and lease-expired leaders must
// reject client proposals with ErrNotLeader.
func TestProposeRejectedWithoutLease(t *testing.T) {
	net, nodes := NewCluster(5)
	if _, err := nodes[3].Propose([]byte("x")); err != ErrNotLeader {
		t.Fatalf("follower propose: got %v, want ErrNotLeader", err)
	}
	leader := tickUntilLeader(nodes, 50)
	if leader == nil {
		t.Fatal("no leader elected")
	}
	// Isolate the leader so its lease cannot be renewed, then let it expire.
	net.SetPartition([]int{0}, []int{1, 2, 3, 4})
	tickAll(nodes, 30)
	if _, err := nodes[0].Propose([]byte("x")); err != ErrNotLeader {
		t.Fatalf("lease-expired leader propose: got %v, want ErrNotLeader", err)
	}
	if nodes[0].Snapshot().Role == Leader {
		t.Fatal("node 0 should have stepped down after lease expiry")
	}
}

// TestPrepareDoesNotLowerBallot: a Prepare with a lower ballot must not move
// the acceptor's promised ballot backwards; the reply carries the current
// (higher) ballot so the stale proposer backs off.
func TestPrepareDoesNotLowerBallot(t *testing.T) {
	net, nodes := NewCluster(5)
	tickUntilLeader(nodes, 50)
	st := nodes[2].Snapshot()
	high := st.Ballot
	low := Ballot{N: high.N, ID: 0}
	if !high.Greater(low) {
		low = Ballot{N: high.N - 1, ID: 99}
	}
	net.Send(Message{Kind: MsgPrepare, From: 4, To: 2, Ballot: low})
	st2 := nodes[2].Snapshot()
	if st2.Ballot != high {
		t.Fatalf("ballot moved backwards: %v -> %v", high, st2.Ballot)
	}
}

// TestAcceptRejectsLowerBallot: an Accept carrying a stale ballot is nacked
// and must not modify the log.
func TestAcceptRejectsLowerBallot(t *testing.T) {
	net, nodes := NewCluster(5)
	leader := tickUntilLeader(nodes, 50)
	mustPropose(t, leader, "a")
	st := nodes[1].Snapshot()
	stale := Ballot{N: st.Ballot.N - 1, ID: 99}
	if st.Ballot.N == 0 || !st.Ballot.Greater(stale) {
		t.Fatalf("test setup: node ballot %v not greater than %v", st.Ballot, stale)
	}
	before := nodes[1].Snapshot().Log
	net.Send(Message{
		Kind:   MsgAccept,
		From:   0,
		To:     1,
		Ballot: stale,
		Index:  7,
		Entry:  Entry{Ballot: stale, Cmd: []byte("junk")},
	})
	after := nodes[1].Snapshot().Log
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("stale Accept modified log: %v -> %v", before, after)
	}
}

// TestCommittedSlotImmutable: even a higher-ballot Accept may not overwrite
// an already committed slot.
func TestCommittedSlotImmutable(t *testing.T) {
	net, nodes := NewCluster(5)
	leader := tickUntilLeader(nodes, 50)
	mustPropose(t, leader, "committed")
	tickAll(nodes, 5)
	st := nodes[3].Snapshot()
	if st.CommitIndex != 1 {
		t.Fatalf("setup: commitIndex=%d, want 1", st.CommitIndex)
	}
	higher := Ballot{N: st.Ballot.N + 10, ID: 4}
	net.Send(Message{
		Kind:   MsgAccept,
		From:   4,
		To:     3,
		Ballot: higher,
		Index:  0,
		Entry:  Entry{Ballot: higher, Cmd: []byte("evil")},
	})
	st = nodes[3].Snapshot()
	if string(st.Applied[0]) != "committed" {
		t.Fatalf("committed slot overwritten: %q", st.Applied[0])
	}
	if string(st.Log[0].Cmd) != "committed" {
		t.Fatalf("committed log entry mutated: %q", st.Log[0].Cmd)
	}
}

// TestUncommittedSlotOverwriteable: an uncommitted slot may be overwritten
// by a higher-ballot entry (dirty-log cleanup is legal).
func TestUncommittedSlotOverwriteable(t *testing.T) {
	net, nodes := NewCluster(5)
	leader := tickUntilLeader(nodes, 50)
	mustPropose(t, leader, "a")
	st := nodes[4].Snapshot()
	higher := Ballot{N: st.Ballot.N + 10, ID: 2}
	net.Send(Message{
		Kind:   MsgAccept,
		From:   2,
		To:     4,
		Ballot: higher,
		Index:  1,
		Entry:  Entry{Ballot: higher, Cmd: []byte("v1")},
	})
	net.Send(Message{
		Kind:   MsgAccept,
		From:   2,
		To:     4,
		Ballot: Ballot{N: higher.N + 1, ID: 3},
		Index:  1,
		Entry:  Entry{Ballot: Ballot{N: higher.N + 1, ID: 3}, Cmd: []byte("v2")},
	})
	got := nodes[4].Snapshot().Log[1]
	if string(got.Cmd) != "v2" {
		t.Fatalf("uncommitted slot not overwritten by higher ballot: %q", got.Cmd)
	}
}

// TestSingleNodeCluster: a cluster of one is its own majority.
func TestSingleNodeCluster(t *testing.T) {
	_, nodes := NewCluster(1)
	leader := tickUntilLeader(nodes, 30)
	if leader == nil {
		t.Fatal("no leader in single-node cluster")
	}
	mustPropose(t, leader, "solo")
	assertConverged(t, nodes, []string{"solo"})
}

// TestSplitVoteResolves: deterministic staggered timeouts guarantee that
// even after a failed/split election some node becomes leader.
func TestSplitVoteResolves(t *testing.T) {
	_, nodes := NewCluster(5)
	// Force all nodes to campaign simultaneously with the same tick.
	for _, n := range nodes {
		n.Tick() // now=1
	}
	// Manually trigger elections on nodes 1 and 3 in the same tick window.
	for _, id := range []int{1, 3} {
		n := nodes[id]
		n.mu.Lock()
		out := n.startElectionLocked()
		n.mu.Unlock()
		n.net.sendAll(out)
	}
	leader := tickUntilLeader(nodes, 200)
	if leader == nil {
		t.Fatal("split vote never resolved")
	}
	mustPropose(t, leader, "after-split")
	tickAll(nodes, 5)
	assertConverged(t, nodes, []string{"after-split"})
}

// TestDeterministicClock: identical tick sequences produce identical states.
func TestDeterministicClock(t *testing.T) {
	run := func() []Status {
		_, nodes := NewCluster(5)
		tickAll(nodes, 12)
		leader := leaderAmong(nodes, 0, 1, 2, 3, 4)
		if leader == nil {
			t.Fatal("no leader")
		}
		mustPropose(t, leader, "d1")
		tickAll(nodes, 3)
		mustPropose(t, leader, "d2")
		tickAll(nodes, 20)
		sts := make([]Status, len(nodes))
		for i, n := range nodes {
			sts[i] = n.Snapshot()
		}
		return sts
	}
	a, b := run(), run()
	if !reflect.DeepEqual(a, b) {
		t.Fatal("same tick sequence produced different states")
	}
}

// TestApplyOrderIsLogOrder: the state machine applies commands strictly in
// log order even when commits land in bursts.
func TestApplyOrderIsLogOrder(t *testing.T) {
	_, nodes := NewCluster(5)
	leader := tickUntilLeader(nodes, 50)
	want := []string{}
	for i := 0; i < 20; i++ {
		cmd := string(rune('a' + i))
		mustPropose(t, leader, cmd)
		want = append(want, cmd)
	}
	tickAll(nodes, 10)
	assertConverged(t, nodes, want)
}
