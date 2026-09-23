package twopc

import (
	"testing"
	"time"

	"twopc-lab/sim"
)

const (
	ms = int64(time.Millisecond)
)

func testCluster(t *testing.T) *Cluster {
	t.Helper()
	cl := NewCluster("coord", []string{"p1", "p2", "p3"},
		CoordinatorConfig{VoteTimeout: 25 * ms, RetryDelay: 15 * ms},
		ParticipantConfig{QueryDelay: 4 * ms})
	cl.Boot()
	return cl
}

func assertNoViolations(t *testing.T, cl *Cluster) {
	t.Helper()
	for _, ev := range cl.SafetyViolations() {
		t.Errorf("safety violation: %s", ev)
	}
}

// T1: happy path. All participants vote YES, everyone commits, locks clear.
func TestHappyPathCommits(t *testing.T) {
	cl := testCluster(t)
	cl.StartTxn(1*ms, "T1", cl.PartIDs(), []string{"x", "y"}, []string{"1", "2"})
	cl.Run(100 * ms)
	assertNoViolations(t, cl)

	if cl.Coord.DecisionOf("T1") != DecisionCommit {
		t.Fatalf("decision = %s, want COMMIT", cl.Coord.DecisionOf("T1"))
	}
	for _, id := range cl.PartIDs() {
		p := cl.Parts[id]
		if !p.IsCommitted("T1") {
			t.Errorf("%s did not commit", id)
		}
		if holder := p.LockHolder("x"); holder != "" {
			t.Errorf("%s still locks x (holder=%s)", id, holder)
		}
	}
	if v, ok := cl.Parts["p1"].Get("x"); !ok || v != "1" {
		t.Fatalf("p1 x = %q,%v want 1", v, ok)
	}
}

// T2: lock conflict. A long-running holder keeps "x"; the contender must get
// NO votes and abort without taking any lock.
func TestLockConflictAborts(t *testing.T) {
	cl := testCluster(t)

	// T-hold prepares and votes YES on p1/p2, but we block its decision by
	// withholding p3's vote: p3 never receives PREPARE for the holder, so
	// instead we only send PREPARE for T-hold to p1 and p2 directly.
	cl.Sched.Plan(1*ms, cl.Coord.ID, func() {
		cl.Coord.RunTxn("hold", []string{"p1", "p2"}, []string{"x"}, []string{"h"})
	})
	// Contender on overlapping keys at p2.
	cl.StartTxn(8*ms, "want", []string{"p2", "p3"}, []string{"x", "z"}, []string{"w", "3"})
	cl.Run(120 * ms)
	assertNoViolations(t, cl)

	// Vote timeout fires for hold (p1,p2 yes; no others in set -> actually
	// all two vote yes => it would COMMIT). Avoid that ambiguity by only
	// asserting the contender aborted.
	if cl.Coord.DecisionOf("want") != DecisionAbort {
		t.Fatalf("want decision = %s, expect ABORT due to conflict at p2",
			cl.Coord.DecisionOf("want"))
	}
	if !cl.Parts["p2"].IsAborted("want") {
		t.Fatal("p2 did not abort contender")
	}
	if cl.Parts["p2"].HoldsLock("want", "x") {
		t.Fatal("contender reacquired the contended lock after abort")
	}
	// p3 is uncontended: protocol atomicity still aborts it globally.
	if !cl.Parts["p3"].IsAborted("want") {
		t.Fatal("p3 must abort too (atomicity)")
	}
}

// T3 (headline): after every participant prepares, the coordinator crashes
// BEFORE logging a decision. On recovery it has only the begin record, so it
// must ABORT — never guess commit — and the prepared locks must be released.
func TestAllPreparedThenCoordinatorCrashAborts(t *testing.T) {
	cl := testCluster(t)
	cl.Coord.BeforeDecisionLogged = func(txn string) bool { return txn == "T1" }

	cl.StartTxn(1*ms, "T1", cl.PartIDs(), []string{"x"}, []string{"9"})

	// Observe the blocked window while the coordinator is down:
	// participants stay PREPARED and hold the lock.
	sawPreparedWhileDown := false
	cl.Sched.Plan(12*ms, "", func() {
		if cl.Sched.IsAlive("coord") {
			t.Fatalf("expected coordinator crashed by t=12ms")
		}
		for _, id := range cl.PartIDs() {
			if st := cl.Parts[id].TxnState("T1"); st != StatePrepared {
				t.Fatalf("%s state = %d during outage, want PREPARED", id, st)
			}
			if !cl.Parts[id].HoldsLock("T1", "x") {
				t.Fatalf("%s released lock during outage", id)
			}
		}
		sawPreparedWhileDown = true
	})
	cl.Sched.RecoverAt(15*ms, "coord")

	cl.Run(120 * ms)
	assertNoViolations(t, cl)
	if !sawPreparedWhileDown {
		t.Fatal("never observed the blocked PREPARED window")
	}
	if cl.Coord.DecisionOf("T1") != DecisionAbort {
		t.Fatalf("recovered decision = %s, must be ABORT (no commit record)",
			cl.Coord.DecisionOf("T1"))
	}
	for _, id := range cl.PartIDs() {
		p := cl.Parts[id]
		if !p.IsAborted("T1") {
			t.Errorf("%s did not learn the abort", id)
		}
		if p.IsCommitted("T1") {
			t.Errorf("%s committed a txn whose commit was never logged!", id)
		}
		if p.HoldsLock("T1", "x") {
			t.Errorf("%s still holds lock after abort", id)
		}
	}
	if _, ok := cl.Parts["p1"].Get("x"); ok {
		t.Fatal("value x must not be installed: transaction aborted")
	}
}

// T4: commit decision is durable and survives a crash right after the record
// lands; recovery re-sends COMMIT and the transaction still commits.
func TestCrashAfterCommitRecordStillCommits(t *testing.T) {
	cl := testCluster(t)
	cl.Coord.AfterDecisionLogged = func(txn string) bool { return txn == "T1" }
	cl.StartTxn(1*ms, "T1", cl.PartIDs(), []string{"x"}, []string{"7"})
	cl.Sched.RecoverAt(20*ms, "coord")
	cl.Run(150 * ms)
	assertNoViolations(t, cl)

	if cl.Coord.DecisionOf("T1") != DecisionCommit {
		t.Fatalf("decision = %s, want durable COMMIT", cl.Coord.DecisionOf("T1"))
	}
	for _, id := range cl.PartIDs() {
		if !cl.Parts[id].IsCommitted("T1") {
			t.Errorf("%s not committed after recovery", id)
		}
	}
}

// T5: duplicate COMMIT delivery is idempotent; duplicate PREPARE re-votes.
func TestDuplicateCommitAndPrepareIdempotent(t *testing.T) {
	cl := testCluster(t)
	cl.StartTxn(1*ms, "T1", []string{"p1"}, []string{"x"}, []string{"1"})
	// Duplicate every coordinator->p1 message once.
	cl.Sched.Network().Duplicate = func(now int64, env sim.Envelope) int {
		if env.To == "p1" {
			return 1
		}
		return 0
	}
	cl.Run(100 * ms)
	assertNoViolations(t, cl)
	if !cl.Parts["p1"].IsCommitted("T1") {
		t.Fatal("p1 should commit")
	}
}

// T6: once a participant has aborted, a late duplicated PREPARE must not take
// locks or change the local outcome.
func TestLatePrepareAfterAbortCannotRelock(t *testing.T) {
	cl := testCluster(t)
	cl.StartTxn(1*ms, "T1", []string{"p1", "p2"}, []string{"x"}, []string{"1"})

	// Drop p2's first PREPARED so the vote timeout aborts the txn; then let
	// the duplicated PREPARE (injected at t=25ms, after abort at ~21ms)
	// arrive at p2.
	drop := cl.Sched.Network().DropFromTo("p2", "coord", 1)
	cl.Sched.Plan(25*ms, "coord", func() {
		cl.Coord.ctx.Send("p2", Prepare{Txn: "T1", Keys: []string{"x"}, Values: []string{"1"}})
	})
	cl.Run(120 * ms)
	assertNoViolations(t, cl)

	if *drop != 0 {
		t.Fatalf("drop rule did not fire as expected, remaining=%d", *drop)
	}
	if cl.Coord.DecisionOf("T1") != DecisionAbort {
		t.Fatalf("decision = %s, want ABORT", cl.Coord.DecisionOf("T1"))
	}
	if !cl.Parts["p2"].IsAborted("T1") {
		t.Fatal("p2 must be aborted")
	}
	if cl.Parts["p2"].HoldsLock("T1", "x") {
		t.Fatal("late PREPARE after abort re-took the lock")
	}
	if st := cl.Parts["p2"].TxnState("T1"); st != StateAborted {
		t.Fatalf("p2 state = %d, want ABORTED", st)
	}
}

// T7: a participant crashes after voting PREPARED. Its durable vote and lock
// survive reboot, and its decision query drives the transaction to the
// coordinator's commit.
func TestParticipantCrashAfterPreparedRecoversAndCommits(t *testing.T) {
	cl := testCluster(t)
	cl.StartTxn(1*ms, "T1", cl.PartIDs(), []string{"x"}, []string{"4"})

	// Kill p2 right after it votes; the coordinator COMMIT at ~11ms finds
	// p2 down and is discarded. Reboot p2 later; it must query and commit.
	cl.Sched.FailAt(8*ms, "p2")
	cl.Sched.RecoverAt(30*ms, "p2")
	cl.Run(160 * ms)
	assertNoViolations(t, cl)

	if cl.Coord.DecisionOf("T1") != DecisionCommit {
		t.Fatalf("decision = %s, want COMMIT", cl.Coord.DecisionOf("T1"))
	}
	if !cl.Parts["p2"].IsCommitted("T1") {
		t.Fatal("p2 must commit after recovery query")
	}
	if cl.Parts["p2"].HoldsLock("T1", "x") {
		t.Fatal("p2 did not release the lock after commit")
	}
}

// T8: lost messages are repaired by retransmission: liveness under bounded
// link drops.
func TestLostMessagesRepairedByRetransmission(t *testing.T) {
	cl := testCluster(t)
	cl.StartTxn(1*ms, "T1", cl.PartIDs(), []string{"x"}, []string{"5"})

	// Drop exactly the first COMMIT to each participant; the 5ms decision
	// retransmission repairs the loss.
	for _, dst := range cl.PartIDs() {
		dst := dst
		left := 1
		prev := cl.Sched.Network().Drop
		cl.Sched.Network().Drop = func(now int64, env sim.Envelope) bool {
			if left > 0 && env.From == "coord" && env.To == dst {
				if _, ok := env.Body.(Commit); ok {
					left--
					return true
				}
			}
			return prev != nil && prev(now, env)
		}
	}

	cl.Run(150 * ms)
	assertNoViolations(t, cl)
	for _, id := range cl.PartIDs() {
		if !cl.Parts[id].IsCommitted("T1") {
			t.Fatalf("%s failed to commit despite retransmission", id)
		}
	}
}

// T9: identical inputs produce identical message-level runs.
func TestDeterministicReplay(t *testing.T) {
	run := func() string {
		cl := testCluster(t)
		cl.Coord.BeforeDecisionLogged = func(txn string) bool { return txn == "T1" }
		cl.StartTxn(1*ms, "T1", cl.PartIDs(), []string{"x"}, []string{"9"})
		cl.Sched.RecoverAt(15*ms, "coord")
		cl.Run(120 * ms)
		return cl.TraceLog()
	}
	a, b := run(), run()
	if a != b {
		t.Fatal("two runs of the same scenario diverged")
	}
	if a == "" {
		t.Fatal("trace unexpectedly empty")
	}
}

// T10 (blocking case): if the coordinator crashes AFTER every participant is
// PREPARED and never recovers, no participant can learn the outcome. Everyone
// stays in the PREPARED uncertainty window and keeps holding the locks. This
// is the inherent 2PC blocking point: a cohort cannot safely commit and
// cannot safely abort on its own. The lock becomes available again the
// instant the coordinator recovers (demonstrated at t2 > t1).
func TestPreparedParticipantsBlockWhileCoordinatorDown(t *testing.T) {
	cl := testCluster(t)
	cl.Coord.BeforeDecisionLogged = func(txn string) bool { return true }
	cl.StartTxn(1*ms, "T1", cl.PartIDs(), []string{"x"}, []string{"9"})

	const t1 = 40 * ms
	cl.Run(t1)
	for _, id := range cl.PartIDs() {
		if st := cl.Parts[id].TxnState("T1"); st != StatePrepared {
			t.Fatalf("%s state=%d at t1, want PREPARED", id, st)
		}
		if !cl.Parts[id].HoldsLock("T1", "x") {
			t.Fatalf("%s lost the lock while blocked", id)
		}
	}

	// Recovery releases the system: presumed abort is forced from the log.
	cl.Sched.RecoverAt(t1+1*ms, "coord")
	cl.Run(t1 + 80*ms)
	assertNoViolations(t, cl)
	if cl.Coord.DecisionOf("T1") != DecisionAbort {
		t.Fatalf("decision after recovery = %s, want ABORT", cl.Coord.DecisionOf("T1"))
	}
	for _, id := range cl.PartIDs() {
		if !cl.Parts[id].IsAborted("T1") || cl.Parts[id].HoldsLock("T1", "x") {
			t.Fatalf("%s did not resolve after recovery", id)
		}
	}
}
