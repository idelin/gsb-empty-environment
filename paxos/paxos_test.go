package paxos

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

const testLease = time.Hour // effectively non-expiring for logic tests

func elect(t *testing.T, nd *Node, term uint64) {
	t.Helper()
	if !nd.Elect(term) {
		t.Fatalf("node %d failed to elect itself at term %d", nd.ID(), term)
	}
}

func TestBasicElectionAndCommit(t *testing.T) {
	c := NewCluster(5, testLease, nil)
	elect(t, c.Nodes[0], 1)

	for i := 0; i < 3; i++ {
		slot, ok := c.Nodes[0].Propose(fmt.Sprintf("k%d", i), fmt.Sprintf("v%d", i))
		if !ok {
			t.Fatalf("proposal %d failed", i)
		}
		if slot != i+1 {
			t.Fatalf("expected slot %d, got %d", i+1, slot)
		}
	}

	for _, nd := range c.Nodes {
		if nd.CommitIndex() != 3 {
			t.Fatalf("node %d commitIndex=%d, want 3", nd.ID(), nd.CommitIndex())
		}
	}
	if err := c.CheckConsistency(); err != nil {
		t.Fatal(err)
	}
	com := c.Nodes[0].Committed()
	if len(com) != 3 || com[0].Key != "k0" || com[2].Value != "v2" {
		t.Fatalf("unexpected committed log: %v", com)
	}
}

func TestLeaseSkipsPhase1(t *testing.T) {
	c := NewCluster(3, testLease, nil)
	elect(t, c.Nodes[0], 1)
	preparesAfterElection := c.Net.Stats.Prepares

	for i := 0; i < 5; i++ {
		if _, ok := c.Nodes[0].Propose("k", "v"); !ok {
			t.Fatalf("proposal %d failed", i)
		}
	}
	if c.Net.Stats.Prepares != preparesAfterElection {
		t.Fatalf("Phase 1 was not skipped under lease: prepares %d -> %d",
			preparesAfterElection, c.Net.Stats.Prepares)
	}
}

func TestLeaseExpiryBlocksProposals(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	c := NewCluster(3, 50*time.Millisecond, clock)
	elect(t, c.Nodes[0], 1)
	if _, ok := c.Nodes[0].Propose("a", "1"); !ok {
		t.Fatal("proposal within lease should succeed")
	}
	now = now.Add(time.Second) // lease expired
	if _, ok := c.Nodes[0].Propose("b", "2"); ok {
		t.Fatal("proposal after lease expiry should fail")
	}
}

func TestNonLeaderCannotPropose(t *testing.T) {
	c := NewCluster(3, testLease, nil)
	elect(t, c.Nodes[0], 1)
	if _, ok := c.Nodes[1].Propose("x", "y"); ok {
		t.Fatal("follower must not accept client proposals")
	}
}

// Acceptance scenario: 5-node cluster, network partition isolates the old
// leader, a new majority leader takes over, repairs holes via Phase 1 and
// safely overwrites dirty minority log entries.
func TestPartitionOldLeaderReplacedAndDirtyLogOverwritten(t *testing.T) {
	c := NewCluster(5, testLease, nil)
	old, newcomer := c.Nodes[0], c.Nodes[2]

	// 1. Old leader commits two entries cluster-wide.
	elect(t, old, 1)
	if _, ok := old.Propose("a", "1"); !ok {
		t.Fatal("commit slot 1")
	}
	if _, ok := old.Propose("b", "2"); !ok {
		t.Fatal("commit slot 2")
	}

	// 2. Partition: {0,1} | {2,3,4}. Old leader's next write reaches only
	//    node 1 -> dirty, uncommitted slot 3 on the minority side.
	c.Net.Partition([]int{0, 1}, []int{2, 3, 4})
	slot, ok := old.Propose("c", "dirty")
	if ok {
		t.Fatal("minority leader must not commit without a majority")
	}
	if slot != 3 {
		t.Fatalf("dirty write landed in slot %d, want 3", slot)
	}
	for _, id := range []int{0, 1} {
		s, _ := c.Nodes[id].SlotState(3)
		if !s.Accepted || s.Committed || s.Entry.Value != "dirty" {
			t.Fatalf("node %d: expected dirty uncommitted slot 3, got %+v", id, s)
		}
	}

	// 3. Heal briefly so the old leader commits slot 4 on everyone while
	//    slot 3 stays uncommitted: a genuine hole in the committed prefix.
	c.Net.Heal()
	if slot, ok := old.Propose("d", "4"); !ok || slot != 4 {
		t.Fatalf("slot 4 commit failed: slot=%d ok=%v", slot, ok)
	}
	for _, nd := range c.Nodes {
		if nd.CommitIndex() != 2 {
			t.Fatalf("node %d commitIndex=%d, want 2 (hole at slot 3)", nd.ID(), nd.CommitIndex())
		}
	}

	// 4. Partition again and elect a new majority leader at a higher term.
	//    Phase 1 learns slot 4's chosen value from the majority, finds the
	//    hole at slot 3 and repairs it with a no-op, then re-chooses slot 4.
	c.Net.Partition([]int{0, 1}, []int{2, 3, 4})
	elect(t, newcomer, 2)
	s, _ := newcomer.SlotState(3)
	if !s.Committed || !s.Entry.Noop {
		t.Fatalf("slot 3 should be a committed no-op after repair, got %+v", s)
	}
	if s, _ := newcomer.SlotState(4); !s.Committed || s.Entry.Key != "d" {
		t.Fatalf("slot 4 chosen value must be preserved, got %+v", s)
	}
	if newcomer.CommitIndex() != 4 {
		t.Fatalf("new leader commitIndex=%d, want 4 after hole repair", newcomer.CommitIndex())
	}

	// 5. New leader commits a fresh entry in slot 5 under its lease.
	if slot, ok := newcomer.Propose("e", "5"); !ok || slot != 5 {
		t.Fatalf("new leader proposal failed: slot=%d ok=%v", slot, ok)
	}

	// 6. Heal. Old leader's stale proposal is rejected and it steps down;
	//    the new leader's sync overwrites the minority's dirty slot 3.
	c.Net.Heal()
	if _, ok := old.Propose("z", "26"); ok {
		t.Fatal("old leader must not commit after losing the majority")
	}
	if old.IsLeader() {
		t.Fatal("old leader should have stepped down after seeing term 2")
	}
	newcomer.SyncFollowers()

	// 7. Every node converges: [a=1, b=2, noop, d=4, e=5], no holes, and the
	//    dirty minority value never leaks into the committed log.
	for _, nd := range c.Nodes {
		if nd.CommitIndex() != 5 {
			t.Fatalf("node %d commitIndex=%d, want 5", nd.ID(), nd.CommitIndex())
		}
		com := nd.Committed()
		if len(com) != 5 {
			t.Fatalf("node %d committed %d entries, want 5", nd.ID(), len(com))
		}
		if com[0].Key != "a" || com[1].Key != "b" || !com[2].Noop || com[3].Key != "d" || com[4].Key != "e" {
			t.Fatalf("node %d unexpected log: %v", nd.ID(), com)
		}
		if s, _ := nd.SlotState(3); s.Entry.Value == "dirty" {
			t.Fatalf("node %d still holds dirty value in slot 3", nd.ID())
		}
	}
	if err := c.CheckConsistency(); err != nil {
		t.Fatal(err)
	}
}

// A committed value must survive a later leader change: the new leader's
// Phase 1 sees it through majority intersection and re-chooses it.
func TestCommittedValueSurvivesLeaderChange(t *testing.T) {
	c := NewCluster(5, testLease, nil)
	elect(t, c.Nodes[0], 1)
	if _, ok := c.Nodes[0].Propose("persist", "me"); !ok {
		t.Fatal("commit slot 1")
	}

	// Isolate the old leader entirely; the remaining 4 still hold slot 1.
	c.Net.Partition([]int{0}, []int{1, 2, 3, 4})
	elect(t, c.Nodes[1], 2)
	s, _ := c.Nodes[1].SlotState(1)
	if !s.Committed || s.Entry.Value != "me" {
		t.Fatalf("committed value lost after leader change: %+v", s)
	}
	if _, ok := c.Nodes[1].Propose("next", "2"); !ok {
		t.Fatal("new leader should continue committing")
	}
	c.Net.Heal()
	c.Nodes[1].SyncFollowers()
	if err := c.CheckConsistency(); err != nil {
		t.Fatal(err)
	}
	if c.Nodes[0].CommitIndex() != 2 {
		t.Fatalf("isolated old leader did not catch up: commitIndex=%d", c.Nodes[0].CommitIndex())
	}
}

// Higher-term acceptor must reject a stale leader's Phase 2.
func TestStaleLeaderRejected(t *testing.T) {
	c := NewCluster(3, testLease, nil)
	elect(t, c.Nodes[0], 1)
	elect(t, c.Nodes[1], 2) // newer term on the majority
	if _, ok := c.Nodes[0].Propose("stale", "x"); ok {
		t.Fatal("stale leader must fail to commit")
	}
	if c.Nodes[0].IsLeader() {
		t.Fatal("stale leader should step down after rejection")
	}
}

// Concurrent client proposals on one leader stay consistent and hole-free.
func TestConcurrentProposals(t *testing.T) {
	c := NewCluster(5, testLease, nil)
	elect(t, c.Nodes[0], 1)

	const writers = 8
	const perWriter = 10
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				if _, ok := c.Nodes[0].Propose(fmt.Sprintf("w%d", w), fmt.Sprintf("%d", i)); !ok {
					t.Errorf("writer %d proposal %d failed", w, i)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	want := writers * perWriter
	for _, nd := range c.Nodes {
		if nd.CommitIndex() != want {
			t.Fatalf("node %d commitIndex=%d, want %d", nd.ID(), nd.CommitIndex(), want)
		}
	}
	if err := c.CheckConsistency(); err != nil {
		t.Fatal(err)
	}
}

// Concurrent elections: exactly the reachable majority candidate wins, and
// the log stays consistent afterwards.
func TestConcurrentElectionAndConsistency(t *testing.T) {
	c := NewCluster(5, testLease, nil)
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c.Nodes[i].Elect(uint64(i + 1))
		}(i)
	}
	wg.Wait()

	leaders := 0
	var leader *Node
	for _, nd := range c.Nodes {
		if nd.IsLeader() {
			leaders++
			leader = nd
		}
	}
	if leaders != 1 {
		t.Fatalf("expected exactly 1 leader, got %d", leaders)
	}
	if _, ok := leader.Propose("after", "election"); !ok {
		t.Fatal("winner should commit new entries")
	}
	if err := c.CheckConsistency(); err != nil {
		t.Fatal(err)
	}
}

// Minority node cannot elect itself without a majority.
func TestMinorityCannotElect(t *testing.T) {
	c := NewCluster(5, testLease, nil)
	c.Net.Partition([]int{0, 1}, []int{2, 3, 4})
	if c.Nodes[0].Elect(1) {
		t.Fatal("minority partition must not elect a leader")
	}
	if !c.Nodes[2].Elect(1) {
		t.Fatal("majority partition should elect a leader")
	}
}
