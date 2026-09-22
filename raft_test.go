package raft

import (
	"strings"
	"testing"
)

// runUntil steps the simulator until cond holds or the step budget runs out.
func runUntil(sim *Sim, budget int, cond func() bool) bool {
	for i := 0; i < budget; i++ {
		if cond() {
			return true
		}
		sim.Step()
	}
	return cond()
}

func leaderInGroup(c *Cluster, ids ...int) int {
	for _, id := range ids {
		if n := c.Node(id); n.Role() == Leader {
			return id
		}
	}
	return -1
}

// --- Liveness evidence -----------------------------------------------------

// With a stable leader and a reachable majority, a read is confirmed by a
// quorum and served within a bounded number of logical time units.
func TestReadLivenessWithMajority(t *testing.T) {
	sim := NewSim()
	c := NewCluster(sim, 3)
	sim.RunFor(300)

	leader := c.LeaderID()
	if leader < 0 {
		t.Fatal("no leader elected")
	}
	c.Propose(leader, "set x 1")
	sim.RunFor(100)

	var res ReadResult
	start := sim.Now()
	doneAt := int64(-1)
	c.Read(leader, func(r ReadResult) { res = r; doneAt = sim.Now() })
	sim.RunFor(200)

	if !res.OK {
		t.Fatalf("read failed: %v", res.Err)
	}
	if !res.Evidence.Verify(3) {
		t.Fatalf("evidence does not verify: %+v", res.Evidence)
	}
	if got := res.State["x"]; got != "1" {
		t.Fatalf("stale read: x=%q, want 1", got)
	}
	if latency := doneAt - start; latency > 50 {
		t.Fatalf("read latency %d exceeds bound 50", latency)
	}
	t.Logf("liveness: read confirmed by quorum %v in term %d, latency=%d",
		res.Evidence.Acks, res.Evidence.Term, doneAt-start)

	// A follower cannot serve reads.
	var fres ReadResult
	follower := (leader + 1) % 3
	c.Read(follower, func(r ReadResult) { fres = r })
	sim.RunFor(10)
	if fres.OK || !strings.Contains(fres.Err, "not leader") {
		t.Fatalf("follower read: got %+v, want not-leader error", fres)
	}
}

// --- Safety evidence -------------------------------------------------------

// A new leader that has not committed an entry in its own term must not
// serve reads of old state.
func TestNewLeaderCannotReadBeforeTermConfirmed(t *testing.T) {
	sim := NewSim()
	c := NewCluster(sim, 3)

	if !runUntil(sim, 1000, func() bool { return c.LeaderID() >= 0 }) {
		t.Fatal("no leader elected")
	}
	leader := c.LeaderID()

	// The no-op of the new term is still in flight: read must be rejected.
	var res ReadResult
	c.Read(leader, func(r ReadResult) { res = r })
	sim.RunFor(0)
	if res.OK {
		t.Fatalf("new leader served read before confirming term: %+v", res.Evidence)
	}
	if !strings.Contains(res.Err, "not yet confirmed") {
		t.Fatalf("unexpected error: %v", res.Err)
	}
	t.Logf("safety: read rejected before term confirmation: %q", res.Err)

	// Once the no-op is committed, reads succeed.
	sim.RunFor(300)
	var res2 ReadResult
	c.Read(leader, func(r ReadResult) { res2 = r })
	sim.RunFor(100)
	if !res2.OK || !res2.Evidence.Verify(3) {
		t.Fatalf("read after term confirmation failed: %+v", res2)
	}
}

// A read must wait until the local state machine has applied every entry up
// to the read index; committed-but-not-applied state is never exposed.
func TestReadWaitsForApply(t *testing.T) {
	sim := NewSim()
	c := NewCluster(sim, 3)
	c.ApplyDelay = 50
	sim.RunFor(400)
	leader := c.LeaderID()
	if leader < 0 {
		t.Fatal("no leader elected")
	}
	if !runUntil(sim, 1000, func() bool { return c.Node(leader).AppliedIndex() >= 1 }) {
		t.Fatal("no-op not applied")
	}

	c.Propose(leader, "set k v")
	if !runUntil(sim, 1000, func() bool { return c.Node(leader).CommitIndex() == 2 }) {
		t.Fatal("entry not committed")
	}
	// Committed at index 2, but the applier is ApplyDelay behind.
	if c.Node(leader).AppliedIndex() >= 2 {
		t.Fatal("test premise broken: entry already applied")
	}

	var res ReadResult
	done := false
	c.Read(leader, func(r ReadResult) { res = r; done = true })
	window := 2*c.NetDelay + 5
	sim.RunFor(window) // quorum confirmation completes...
	if done {
		t.Fatal("read served before committed entry was applied")
	}
	t.Logf("safety: read pending while appliedIndex=%d < readIndex=2", c.Node(leader).AppliedIndex())

	sim.RunFor(c.ApplyDelay) // ...and the applier catches up
	if !done || !res.OK {
		t.Fatalf("read not served after apply: %+v", res)
	}
	if res.Evidence.AppliedIndex < res.Evidence.ReadIndex {
		t.Fatalf("evidence applied %d < readIndex %d", res.Evidence.AppliedIndex, res.Evidence.ReadIndex)
	}
	if got := res.State["k"]; got != "v" {
		t.Fatalf("read missed committed write: k=%q", got)
	}
}

// An old leader isolated from the majority can never assemble read evidence;
// after rejoining it steps down and its pending read fails instead of
// returning stale data.
func TestIsolatedOldLeaderGetsNoEvidence(t *testing.T) {
	sim := NewSim()
	c := NewCluster(sim, 3)
	sim.RunFor(300)
	oldLeader := c.LeaderID()
	if oldLeader != 0 {
		t.Fatalf("deterministic election expected node0, got node%d", oldLeader)
	}
	c.Propose(oldLeader, "set x 1")
	sim.RunFor(100)

	// Isolate the old leader; the majority side elects a new one.
	c.Partition([]int{0}, []int{1, 2})
	var oldRes []ReadResult
	c.Read(oldLeader, func(r ReadResult) { oldRes = append(oldRes, r) })
	sim.RunFor(400)
	if len(oldRes) != 0 {
		t.Fatalf("isolated leader produced read result: %+v", oldRes[0])
	}
	t.Log("safety: isolated leader collected no quorum, read stayed pending")

	newLeader := leaderInGroup(c, 1, 2)
	if newLeader < 0 {
		t.Fatal("majority side did not elect a new leader")
	}
	c.Propose(newLeader, "set y 2")
	sim.RunFor(200)

	// Healing the partition must not resurrect the old read: the old leader
	// steps down and the pending read fails.
	c.Heal()
	sim.RunFor(300)
	if len(oldRes) != 1 || oldRes[0].OK {
		t.Fatalf("old leader read after heal: %+v", oldRes)
	}
	t.Logf("safety: old leader read failed with %q", oldRes[0].Err)

	// The new leader serves a read that includes the majority-side write.
	var res ReadResult
	c.Read(newLeader, func(r ReadResult) { res = r })
	sim.RunFor(200)
	if !res.OK || !res.Evidence.Verify(3) {
		t.Fatalf("new leader read failed: %+v", res)
	}
	if got := res.State["y"]; got != "2" {
		t.Fatalf("new leader read missing committed write: y=%q", got)
	}
	if res.Evidence.Term != c.Node(newLeader).Term() {
		t.Fatalf("evidence term %d != leader term %d", res.Evidence.Term, c.Node(newLeader).Term())
	}
}

// ReadEvidence.Verify enforces quorum and applied-index checks.
func TestEvidenceVerify(t *testing.T) {
	good := ReadEvidence{Leader: 0, Term: 2, ReadIndex: 3, AppliedIndex: 3, Acks: []int{0, 1}}
	if !good.Verify(3) {
		t.Fatal("valid evidence rejected")
	}
	cases := map[string]ReadEvidence{
		"minority acks":   {Leader: 0, Term: 2, ReadIndex: 3, AppliedIndex: 3, Acks: []int{0}},
		"unapplied state": {Leader: 0, Term: 2, ReadIndex: 3, AppliedIndex: 2, Acks: []int{0, 1}},
		"zero term":       {Leader: 0, Term: 0, ReadIndex: 3, AppliedIndex: 3, Acks: []int{0, 1}},
		"zero read index": {Leader: 0, Term: 2, ReadIndex: 0, AppliedIndex: 0, Acks: []int{0, 1}},
	}
	for name, ev := range cases {
		if ev.Verify(3) {
			t.Fatalf("%s: invalid evidence accepted: %+v", name, ev)
		}
	}
}
