package paxos

import (
	"fmt"
	"testing"
)

// Acceptance scenario: a 5-node cluster is partitioned into a minority
// {0,1} (with the old leader) and a majority {2,3,4}. The old leader
// keeps accepting dirty entries on the minority side until its lease
// expires; the majority elects a new leader that repairs the log via
// phase 1, commits new entries, and after healing safely overwrites the
// minority's dirty log.
func TestPartitionNewLeaderRepairsAndOverwrites(t *testing.T) {
	c := NewCluster(5, DefaultConfig())

	// Phase 1: node 0 is elected and commits v0 on all 5 nodes.
	if !runUntil(c, 100, func() bool { return c.Leader() == 0 }) {
		t.Fatal("node 0 not elected")
	}
	if _, ok := c.Node(0).Propose("v0"); !ok {
		t.Fatal("propose v0 failed")
	}
	if !runUntil(c, 100, func() bool {
		for i := 0; i < 5; i++ {
			if c.Node(i).CommitIndex() != 1 {
				return false
			}
		}
		return true
	}) {
		t.Fatal("v0 not committed everywhere")
	}

	// Phase 2: partition {0,1} | {2,3,4}. The old leader's lease is
	// still valid for a few ticks, so it accepts dirty entries that
	// only ever reach the minority.
	c.Partition([]int{0, 1}, []int{2, 3, 4})
	if _, ok := c.Node(0).Propose("dirty1"); !ok {
		t.Fatal("old leader should still hold its lease briefly")
	}
	if _, ok := c.Node(0).Propose("dirty2"); !ok {
		t.Fatal("old leader should still hold its lease briefly")
	}
	c.TickN(3)
	// Dirty entries sit on the minority, uncommitted.
	for _, id := range []int{0, 1} {
		if got := len(c.Node(id).Log()); got != 3 {
			t.Fatalf("node %d log len = %d, want 3", id, got)
		}
		if ci := c.Node(id).CommitIndex(); ci != 1 {
			t.Fatalf("node %d committed dirty entries: commitIndex=%d", id, ci)
		}
	}

	// Phase 3: the majority elects a new leader (node 2) which repairs
	// via phase 1 and commits new entries without the minority.
	if !runUntil(c, 200, func() bool { return c.Leader() == 2 }) {
		t.Fatal("majority did not elect node 2")
	}
	if _, ok := c.Node(2).Propose("v1"); !ok {
		t.Fatal("new leader propose v1 failed")
	}
	if _, ok := c.Node(2).Propose("v2"); !ok {
		t.Fatal("new leader propose v2 failed")
	}
	if !runUntil(c, 100, func() bool { return c.Node(2).CommitIndex() == 3 }) {
		t.Fatal("new leader did not commit v1,v2")
	}
	// The minority must not advance past its pre-partition commit.
	if ci := c.Node(0).CommitIndex(); ci != 1 {
		t.Fatalf("minority commitIndex = %d, want 1", ci)
	}

	// Phase 4: heal. The new leader's committed prefix overwrites the
	// minority's dirty entries; a fresh proposal proves liveness.
	c.Heal()
	if _, ok := c.Node(2).Propose("post"); !ok {
		t.Fatal("new leader lost its lease after heal")
	}
	if !runUntil(c, 200, func() bool {
		for i := 0; i < 5; i++ {
			if c.Node(i).CommitIndex() != 4 {
				return false
			}
		}
		return true
	}) {
		t.Fatal("cluster did not reconverge after heal")
	}

	want := []string{"v0", "v1", "v2", "post"}
	for i := 0; i < 5; i++ {
		assertGapFree(t, c.Node(i))
		if got := committedValues(c.Node(i)); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("node %d committed %v, want %v", i, got, want)
		}
		// Dirty values must be gone from the committed region.
		for _, s := range c.Node(i).Committed() {
			if s.Value == "dirty1" || s.Value == "dirty2" {
				t.Fatalf("node %d committed a dirty minority entry", i)
			}
		}
	}
	if c.Leader() != 2 {
		t.Fatal("leadership flapped after heal")
	}
}

// Safety scenario: a value accepted by a majority but never reported as
// committed must survive into the next term — the new leader's phase 1
// must find it and re-propose it rather than overwriting it.
func TestMajorityAcceptedValueSurvives(t *testing.T) {
	c := NewCluster(5, DefaultConfig())
	if !runUntil(c, 100, func() bool { return c.Leader() == 0 }) {
		t.Fatal("node 0 not elected")
	}

	// Block Accepted replies from 2,3,4 so the leader cannot learn that
	// slot 0 reached a majority.
	for _, id := range []int{2, 3, 4} {
		c.Block(id, 0)
	}
	if _, ok := c.Node(0).Propose("chosen"); !ok {
		t.Fatal("propose failed")
	}
	c.TickN(4)
	// Accepted by {0,2,3,4} (a majority) but not committed anywhere.
	for _, id := range []int{2, 3, 4} {
		log := c.Node(id).Log()
		if len(log) != 1 || log[0].Value != "chosen" {
			t.Fatalf("node %d did not accept the value: %v", id, log)
		}
	}
	if ci := c.Node(0).CommitIndex(); ci != 0 {
		t.Fatalf("leader committed without majority acks: %d", ci)
	}

	// Partition so the majority side {2,3,4} holds acceptors of the
	// uncommitted value; they elect a new leader.
	c.Heal()
	c.Partition([]int{0, 1}, []int{2, 3, 4})
	if !runUntil(c, 200, func() bool { return c.Leader() == 2 }) {
		t.Fatal("majority did not elect node 2")
	}
	if !runUntil(c, 100, func() bool { return c.Node(2).CommitIndex() == 1 }) {
		t.Fatal("new leader did not commit the inherited value")
	}
	if got := committedValues(c.Node(2)); fmt.Sprint(got) != "[chosen]" {
		t.Fatalf("new leader overwrote a majority-accepted value: %v", got)
	}

	// Heal: every node must converge on the inherited value.
	c.Heal()
	if !runUntil(c, 200, func() bool {
		for i := 0; i < 5; i++ {
			if c.Node(i).CommitIndex() != 1 {
				return false
			}
		}
		return true
	}) {
		t.Fatal("cluster did not reconverge")
	}
	for i := 0; i < 5; i++ {
		assertGapFree(t, c.Node(i))
		if got := committedValues(c.Node(i)); fmt.Sprint(got) != "[chosen]" {
			t.Fatalf("node %d committed %v, want [chosen]", i, got)
		}
	}
}

// A partitioned old leader must not be able to commit anything while cut
// off from the majority, even though it still serves client proposals
// until its lease expires.
func TestOldLeaderCannotCommitInMinority(t *testing.T) {
	c := NewCluster(5, DefaultConfig())
	if !runUntil(c, 100, func() bool { return c.Leader() == 0 }) {
		t.Fatal("node 0 not elected")
	}
	c.Partition([]int{0, 1}, []int{2, 3, 4})
	for i := 0; i < 3; i++ {
		if _, ok := c.Node(0).Propose(fmt.Sprintf("d%d", i)); !ok {
			t.Fatalf("proposal %d rejected while lease valid", i)
		}
	}
	c.TickN(10)
	if ci := c.Node(0).CommitIndex(); ci != 0 {
		t.Fatalf("minority leader committed %d entries", ci)
	}
	// After the lease expires the old leader steps down and stops
	// accepting proposals.
	if !runUntil(c, 100, func() bool { return c.Node(0).Role() != Leader }) {
		t.Fatal("old leader kept its lease")
	}
	if _, ok := c.Node(0).Propose("late"); ok {
		t.Fatal("proposal accepted after lease expiry")
	}
}
