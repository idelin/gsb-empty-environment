package paxos

import (
	"testing"
)

// TestPartitionOldLeaderReplaced is the acceptance scenario:
//
//  1. 5-node cluster, node 0 is leader; entry "A" is committed by all.
//  2. Network partition {0,1} | {2,3,4}: node 0 accepts dirty entry "B"
//     (replicated to node 1 only, never committed).
//  3. Node 0's lease expires without majority acks; it steps down.
//  4. A majority-side leader is elected, runs Phase 1 over the majority,
//     and commits "C" at the same slot where the minority holds dirty "B".
//  5. After healing, the dirty logs are safely overwritten and every node
//     converges to the identical committed prefix [A, C].
func TestPartitionOldLeaderReplaced(t *testing.T) {
	net, nodes := NewCluster(5)
	leader := tickUntilLeader(nodes, 50)
	if leader == nil || leader.Snapshot().ID != 0 {
		t.Fatalf("expected node 0 as leader, got %v", leader)
	}
	mustPropose(t, leader, "A")
	tickAll(nodes, 5)
	assertConverged(t, nodes, []string{"A"})

	// Partition: old leader kept in the minority side.
	net.SetPartition([]int{0, 1}, []int{2, 3, 4})

	// Dirty write: accepted by {0,1} only, can never commit.
	if _, err := nodes[0].Propose([]byte("B")); err != nil {
		t.Fatalf("minority-side propose should still be accepted locally: %v", err)
	}
	tickAll(nodes, 3)
	for _, id := range []int{0, 1} {
		st := nodes[id].Snapshot()
		if st.CommitIndex != 1 {
			t.Fatalf("minority node %d committed without majority: commitIndex=%d", id, st.CommitIndex)
		}
		if len(st.Log) < 2 || string(st.Log[1].Cmd) != "B" {
			t.Fatalf("minority node %d missing dirty entry B: %v", id, st.Log)
		}
	}

	// The old leader's lease expires without majority heartbeat acks.
	tickAll(nodes, 30)
	if nodes[0].Snapshot().Role == Leader {
		t.Fatal("old leader still claims leadership after lease expiry")
	}

	// A majority-side leader takes over.
	newLeader := leaderAmong(nodes, 2, 3, 4)
	if newLeader == nil {
		t.Fatal("no new leader elected on majority side")
	}
	if newLeader.Snapshot().ID == 0 {
		t.Fatal("new leader must come from the majority side")
	}

	// Phase 1 already ran during election; the majority log must contain
	// exactly [A] and no trace of uncommitted B.
	for _, id := range []int{2, 3, 4} {
		st := nodes[id].Snapshot()
		if st.CommitIndex != 1 || len(st.Applied) != 1 || string(st.Applied[0]) != "A" {
			t.Fatalf("majority node %d corrupted committed prefix: %v", id, appliedStrings(st))
		}
	}

	// New leader commits C at slot 1 via lease-bypassed Phase 2.
	mustPropose(t, newLeader, "C")
	tickAll(nodes, 5)
	for _, id := range []int{2, 3, 4} {
		st := nodes[id].Snapshot()
		if got := appliedStrings(st); len(got) != 2 || got[0] != "A" || got[1] != "C" {
			t.Fatalf("majority node %d applied %v, want [A C]", id, got)
		}
	}

	// Heal: dirty B on nodes {0,1} must be safely overwritten by C.
	net.Heal()
	tickAll(nodes, 80)
	assertConverged(t, nodes, []string{"A", "C"})
	for _, n := range nodes {
		for _, a := range n.Snapshot().Applied {
			if string(a) == "B" {
				t.Fatalf("node %d committed dirty minority entry B", n.Snapshot().ID)
			}
		}
	}
}

// TestGapRepairNoHoleCommit forces a sparse acceptor log (a hole at slot 1,
// a value at slot 2) and proves the new leader back-fills the hole with a
// committed no-op so the commit index never skips a slot.
func TestGapRepairNoHoleCommit(t *testing.T) {
	net, nodes := NewCluster(5)
	leader := tickUntilLeader(nodes, 50)
	mustPropose(t, leader, "A") // slot 0, committed everywhere
	tickAll(nodes, 5)

	// Inject an out-of-order accept directly into node 4: slot 2 filled,
	// slot 1 left as a placeholder hole. Use a fresh high ballot so the
	// accept is not rejected.
	st := nodes[4].Snapshot()
	injectBallot := Ballot{N: st.Ballot.N + 5, ID: 3}
	net.Send(Message{
		Kind:   MsgAccept,
		From:   3,
		To:     4,
		Ballot: injectBallot,
		Index:  2,
		Entry:  Entry{Ballot: injectBallot, Cmd: []byte("X")},
	})
	st = nodes[4].Snapshot()
	if len(st.Log) != 3 || !st.Log[1].isHole() || string(st.Log[2].Cmd) != "X" {
		t.Fatalf("injection failed: %+v", st.Log)
	}
	if st.CommitIndex != 1 {
		t.Fatalf("commit index jumped over hole: %d", st.CommitIndex)
	}

	// Partition the old leader away; node 4's high ballot forces the
	// majority side to campaign with an even higher ballot.
	net.SetPartition([]int{0, 1}, []int{2, 3, 4})
	tickAll(nodes, 120)

	newLeader := leaderAmong(nodes, 2, 3, 4)
	if newLeader == nil {
		t.Fatal("no majority-side leader after partition")
	}
	mustPropose(t, newLeader, "D")
	tickAll(nodes, 10)

	// On the majority side the committed prefix must be contiguous:
	// slot 1 was repaired (no-op filler or recovered value), never skipped.
	for _, id := range []int{2, 3, 4} {
		st := nodes[id].Snapshot()
		assertNoHoles(t, st)
		if st.CommitIndex < 3 {
			t.Fatalf("node %d: commitIndex %d, repair did not cover the hole", id, st.CommitIndex)
		}
		if string(st.Applied[0]) != "A" {
			t.Fatalf("node %d: committed prefix corrupted: %v", id, appliedStrings(st))
		}
	}

	net.Heal()
	tickAll(nodes, 80)
	assertCommittedPrefixesAgree(t, nodes)
	for _, n := range nodes {
		assertNoHoles(t, n.Snapshot())
	}
}

// TestCommittedEntrySurvivesLeaderChanges: once committed, an entry is
// adopted by every subsequent leader (Phase-1 merge picks the
// highest-ballot entry, and committed entries always win).
func TestCommittedEntrySurvivesLeaderChanges(t *testing.T) {
	net, nodes := NewCluster(5)
	leader := tickUntilLeader(nodes, 50)
	mustPropose(t, leader, "committed")
	tickAll(nodes, 5)

	// Roll leadership through several partitions.
	for round := 0; round < 3; round++ {
		cur := leaderAmong(nodes, 0, 1, 2, 3, 4)
		if cur == nil {
			t.Fatalf("round %d: no leader", round)
		}
		id := cur.Snapshot().ID
		// Isolate the current leader alone.
		others := []int{}
		for i := 0; i < 5; i++ {
			if i != id {
				others = append(others, i)
			}
		}
		net.SetPartition([]int{id}, others)
		tickAll(nodes, 60)
		net.Heal()
		tickAll(nodes, 5)
	}
	tickAll(nodes, 60)
	assertCommittedPrefixesAgree(t, nodes)
	for _, n := range nodes {
		st := n.Snapshot()
		if st.CommitIndex < 1 || string(st.Applied[0]) != "committed" {
			t.Fatalf("node %d lost committed entry: %v", st.ID, appliedStrings(st))
		}
	}
}

// TestMinorityCannotCommit: a partitioned minority must never advance its
// commit index, even though it can still accept entries locally.
func TestMinorityCannotCommit(t *testing.T) {
	net, nodes := NewCluster(5)
	leader := tickUntilLeader(nodes, 50)
	mustPropose(t, leader, "A")
	tickAll(nodes, 5)

	net.SetPartition([]int{0, 1}, []int{2, 3, 4})
	for i := 0; i < 5; i++ {
		if _, err := nodes[0].Propose([]byte("m")); err != nil {
			t.Fatalf("minority propose %d: %v", i, err)
		}
	}
	tickAll(nodes, 40)
	for _, id := range []int{0, 1} {
		if st := nodes[id].Snapshot(); st.CommitIndex != 1 {
			t.Fatalf("minority node %d advanced commitIndex to %d under partition", id, st.CommitIndex)
		}
	}
}

// TestStaleLeaderStepsDownOnHigherBallot: when the partitioned old leader
// rejoins and sees a higher ballot, it must step down and adopt the
// majority's committed state.
func TestStaleLeaderStepsDownOnHigherBallot(t *testing.T) {
	net, nodes := NewCluster(5)
	leader := tickUntilLeader(nodes, 50)
	mustPropose(t, leader, "A")
	tickAll(nodes, 5)

	net.SetPartition([]int{0}, []int{1, 2, 3, 4})
	tickAll(nodes, 40)
	newLeader := leaderAmong(nodes, 1, 2, 3, 4)
	if newLeader == nil {
		t.Fatal("no new leader on majority side")
	}
	mustPropose(t, newLeader, "B")
	tickAll(nodes, 5)

	net.Heal()
	tickAll(nodes, 80)
	assertConverged(t, nodes, []string{"A", "B"})
	// Exactly one lease-valid leader at a time after convergence.
	leaders := 0
	for _, n := range nodes {
		if n.LeaseValid() {
			leaders++
		}
	}
	if leaders != 1 {
		t.Fatalf("expected exactly 1 lease-valid leader, got %d", leaders)
	}
}
