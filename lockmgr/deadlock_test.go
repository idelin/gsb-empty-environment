package lockmgr

import (
	"testing"
	"time"
)

func TestBidirectionalSXUpgradeDeadlock(t *testing.T) {
	m := newManager(t)
	db, table, page, row := testResources()

	t1 := m.Begin(1)
	t2 := m.Begin(2)
	acquireHierarchy(t, m, t1, db, table, page, S)
	acquireHierarchy(t, m, t2, db, table, page, S)
	mustLock(t, m, t1, row, S)
	mustLock(t, m, t2, row, S)

	ch1 := asyncLock(m, t1, row, X)
	ch2 := asyncLock(m, t2, row, X)

	results := collectResults(t, ch1, ch2)
	if len(results) != 2 {
		t.Fatalf("got %d lock results, want 2", len(results))
	}
	victimErr, victimID := victimResult(results)
	if !isDeadlock(victimErr) {
		t.Fatalf("victim error = %v, want ErrDeadlockRollback", victimErr)
	}
	if victimID != 1 {
		t.Fatalf("victim index = %d, want younger transaction at index 1", victimID)
	}

	resolutions := m.Resolutions()
	if len(resolutions) != 1 {
		t.Fatalf("resolutions = %d, want 1", len(resolutions))
	}
	res := resolutions[0]
	if res.Victim != 2 {
		t.Fatalf("victim = %d, want the younger transaction 2", res.Victim)
	}
	if res.Savepoint != nil {
		t.Fatalf("without savepoints remedy must be full abort, got savepoint %+v", res.Savepoint)
	}

	// Exactly one minimal two-node cycle is detected.
	if len(res.Cycle) != 2 {
		t.Fatalf("cycle length = %d, want 2", len(res.Cycle))
	}

	// The survivor's upgrade completes and ends in X mode.
	waitFor(t, time.Second, func() bool { return m.HeldLock(t1, row) == X })

	if len(m.DeadlockCycles()) != 0 {
		t.Fatal("deadlock cycles remain after arbitration")
	}
}

func TestSavepointRollbackBreaksCycleKeepsEarlyLocks(t *testing.T) {
	m := newManager(t)
	db, table, page, row := testResources()

	t1 := m.Begin(1)
	t2 := m.Begin(2)

	acquireHierarchy(t, m, t1, db, table, page, S)
	acquireHierarchy(t, m, t2, db, table, page, S)

	// Independent early locks on another row that must survive the rollback.
	otherRow := NewRow("db1", "t1", "p1", "r2")
	mustLock(t, m, t1, otherRow, S)
	mustLock(t, m, t2, otherRow, S)

	// Savepoints are taken after the early ancestor / other-row locks and
	// before the deadlock row locks, so rolling back to sp2 releases only the
	// row S plus the pending X upgrade while preserving all earlier work.
	sp1, err := m.CreateSavepoint(t1, "sp1")
	if err != nil {
		t.Fatal(err)
	}
	sp2, err := m.CreateSavepoint(t2, "sp2")
	if err != nil {
		t.Fatal(err)
	}

	mustLock(t, m, t1, row, S)
	mustLock(t, m, t2, row, S)

	ch1 := asyncLock(m, t1, row, X)
	ch2 := asyncLock(m, t2, row, X)

	results := collectResults(t, ch1, ch2)
	err2, victimID := victimResult(results)
	if !isDeadlock(err2) || victimID != 1 {
		t.Fatalf("victim = id %d err %v, want txn 2 with ErrDeadlockRollback", victimID, err2)
	}

	res := m.Resolutions()[0]
	if res.Victim != 2 || res.Savepoint == nil || res.Savepoint.ID() != sp2.ID() {
		t.Fatalf("resolution = %+v, want savepoint rollback of txn 2 to %d", res, sp2.ID())
	}
	if res.Cost != 1 {
		t.Fatalf("rollback cost = %d, want 1 (only the deadlock row lock is released)", res.Cost)
	}

	// t2 stays active, loses only the rolled-back lock and keeps early locks.
	if !t2.Active() {
		t.Fatal("savepoint rollback must not abort the whole transaction")
	}
	if m.HeldLock(t2, row) != None {
		t.Fatalf("t2 still holds %s on the deadlock row", m.HeldLock(t2, row))
	}
	if m.HeldLock(t2, otherRow) != S {
		t.Fatalf("t2 lost unrelated early lock: %s", m.HeldLock(t2, otherRow))
	}
	if m.HeldLock(t2, page) != IS {
		t.Fatalf("t2 lost ancestor intention lock: %s", m.HeldLock(t2, page))
	}

	// The savepoint was consumed, and the transaction can continue without
	// redoing its whole work.
	if err := m.RollbackToSavepoint(t2, sp2); err != ErrSavepointNotFound {
		t.Fatalf("rolled-back savepoint reuse: got %v, want ErrSavepointNotFound", err)
	}
	if _, err := m.CreateSavepoint(t2, "sp2b"); err != nil {
		t.Fatal(err)
	}
	freeRow := NewRow("db1", "t1", "p1", "r3")
	if err := m.TryLock(t2, freeRow, S); err != nil {
		t.Fatalf("continuing work on a free row after partial rollback: %v", err)
	}

	if results[0] != nil {
		t.Fatalf("older txn upgrade failed: %v", results[0])
	}
	if m.HeldLock(t1, row) != X {
		t.Fatalf("t1 mode = %s, want X", m.HeldLock(t1, row))
	}

	_ = sp1
}

func TestRollbackToSavepointKeepsEarlierLockAndReleasesLaterOne(t *testing.T) {
	m := newManager(t)
	db, table, page, row := testResources()

	otherRow := NewRow("db1", "t1", "p1", "r2")

	younger := m.Begin(2)
	acquireHierarchy(t, m, younger, db, table, page, S)

	// Lock acquired before the savepoint.
	mustLock(t, m, younger, otherRow, S)
	sp, err := m.CreateSavepoint(younger, "mid")
	if err != nil {
		t.Fatal(err)
	}
	// Lock acquired after the savepoint.
	mustLock(t, m, younger, row, S)

	// A user rollback keeps the earlier lock and releases the later one.
	if err := m.RollbackToSavepoint(younger, sp); err != nil {
		t.Fatal(err)
	}
	if m.HeldLock(younger, otherRow) != S {
		t.Fatalf("pre-savepoint lock was lost: %s", m.HeldLock(younger, otherRow))
	}
	if m.HeldLock(younger, row) != None {
		t.Fatalf("post-savepoint lock survived rollback: %s", m.HeldLock(younger, row))
	}
	if m.HeldLock(younger, page) != IS {
		t.Fatalf("pre-savepoint ancestor lock was lost: %s", m.HeldLock(younger, page))
	}
}

func TestVictimTieBreakByStartThenID(t *testing.T) {
	m := newManager(t)
	db, table, page, row := testResources()

	// Decouple transaction id from start order: the later-started txn loses
	// even though its id is smaller.
	olderLargeID := m.Begin(20)
	youngerSmallID := m.Begin(10)

	acquireHierarchy(t, m, olderLargeID, db, table, page, S)
	acquireHierarchy(t, m, youngerSmallID, db, table, page, S)
	mustLock(t, m, olderLargeID, row, S)
	mustLock(t, m, youngerSmallID, row, S)

	chOld := asyncLock(m, olderLargeID, row, X)
	chYoung := asyncLock(m, youngerSmallID, row, X)

	results := collectResults(t, chOld, chYoung)
	err, victimID := victimResult(results)
	if !isDeadlock(err) || victimID != 1 {
		t.Fatalf("victim = index %d err %v, want younger txn at index 1", victimID, err)
	}
	if m.Resolutions()[0].Victim != 10 {
		t.Fatalf("victim = %d, want later-started txn 10", m.Resolutions()[0].Victim)
	}
	if results[0] != nil {
		t.Fatalf("older survivor error: %v", results[0])
	}
	if m.HeldLock(olderLargeID, row) != X {
		t.Fatalf("older txn mode = %s, want X", m.HeldLock(olderLargeID, row))
	}
}
