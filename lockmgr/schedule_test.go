package lockmgr

import (
	"testing"
	"time"
)

func TestUpgradeJumpsAheadOfNewWaiters(t *testing.T) {
	m := newManager(t)
	db, table, page, row := testResources()

	// Two transactions hold S on the same row; a third, ordinary reader
	// arrives and is granted immediately while the queue is empty (normal
	// reader concurrency). An incompatible writer then becomes the queue head.
	older := m.Begin(1)
	younger := m.Begin(2)
	acquireHierarchy(t, m, older, db, table, page, S)
	acquireHierarchy(t, m, younger, db, table, page, S)
	mustLock(t, m, older, row, S)
	mustLock(t, m, younger, row, S)

	writer := m.Begin(3)
	acquireHierarchy(t, m, writer, db, table, page, X)
	writerCh := asyncLock(m, writer, row, X)
	waitFor(t, time.Second, func() bool { return len(m.Snapshot(row)) == 1 })

	lateReader := m.Begin(4)
	acquireHierarchy(t, m, lateReader, db, table, page, S)
	lateCh := asyncLock(m, lateReader, row, S)
	waitFor(t, time.Second, func() bool { return len(m.Snapshot(row)) == 2 })

	// The reader behind the head must stay queued: no blind reader passing.
	snap := m.Snapshot(row)
	if len(snap) != 2 || snap[1].TxnID != lateReader.id || snap[1].Upgrade {
		t.Fatalf("queue = %+v, want ordinary late reader behind the writer", snap)
	}

	// Both S holders concurrently upgrade to X: an immediate two-node
	// upgrade deadlock. The upgrade requests are inserted ahead of the
	// ordinary late reader, and the later-started holder is the victim.
	upOlder := asyncLock(m, older, row, X)
	upYounger := asyncLock(m, younger, row, X)

	_ = writerCh
	upgradeResults := collectResults(t, upOlder, upYounger)
	if err, id := victimResult(upgradeResults); !isDeadlock(err) {
		t.Fatalf("expected one deadlock victim, got id %d err %v", id, err)
	}

	deadline := time.Now().Add(2 * time.Second)
	var olderOK bool
	for time.Now().Before(deadline) {
		if m.HeldLock(older, row) == X || m.HeldLock(younger, row) == X {
			olderOK = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !olderOK {
		t.Fatal("no S holder completed its X upgrade after arbitration")
	}

	// Exactly one upgrade was the deadlock victim; the writer and late reader
	// remain ordinary waiters, with the upgrade still ahead of the late reader.
	res := m.Resolutions()
	if len(res) != 1 || len(res[0].Cycle) != 2 {
		t.Fatalf("resolutions = %+v, want one two-node upgrade cycle", res)
	}
	snap = m.Snapshot(row)
	foundLate := false
	for _, info := range snap {
		if info.TxnID == lateReader.id && !info.Upgrade {
			foundLate = true
		}
	}
	if !foundLate {
		t.Fatalf("ordinary late reader lost from queue: %+v", snap)
	}

	if err := m.Abort(older); err != nil {
		t.Fatal(err)
	}

	// The queued writer reaches the head and takes X; the late reader still
	// cannot pass it (the anti-starvation head rule).
	select {
	case res := <-writerCh:
		if res.err != nil {
			t.Fatalf("queued writer grant: %v", res.err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued writer was not granted after the upgrade released")
	}
	if m.HeldLock(writer, row) != X {
		t.Fatalf("writer mode = %s, want X", m.HeldLock(writer, row))
	}

	if err := m.Abort(writer); err != nil {
		t.Fatal(err)
	}
	select {
	case res := <-lateCh:
		if res.err != nil {
			t.Fatalf("late reader should be granted after writer release: %v", res.err)
		}
		if m.HeldLock(lateReader, row) != S {
			t.Fatalf("late reader mode = %s, want S", m.HeldLock(lateReader, row))
		}
	case <-time.After(time.Second):
		t.Fatal("late reader was never granted")
	}
	if younger.Active() {
		_ = m.Abort(younger)
	}
}

func TestNoBlindReaderPassing(t *testing.T) {
	m := newManager(t)
	db, table, page, row := testResources()

	owner := m.Begin(1)
	acquireHierarchy(t, m, owner, db, table, page, X)
	mustLock(t, m, owner, row, X)

	writer := m.Begin(2)
	acquireHierarchy(t, m, writer, db, table, page, X)
	wCh := asyncLock(m, writer, row, X)
	waitFor(t, time.Second, func() bool { return len(m.Snapshot(row)) == 1 })

	reader := m.Begin(3)
	acquireHierarchy(t, m, reader, db, table, page, S)
	rCh := asyncLock(m, reader, row, S)
	waitFor(t, time.Second, func() bool { return len(m.Snapshot(row)) == 2 })

	// Releasing the X owner must grant the queued writer first, never skip to
	// the compatible reader behind it.
	if err := m.Abort(owner); err != nil {
		t.Fatal(err)
	}
	select {
	case res := <-wCh:
		if res.err != nil {
			t.Fatalf("writer grant: %v", res.err)
		}
		if m.HeldLock(writer, row) != X {
			t.Fatalf("writer holds %s, want X", m.HeldLock(writer, row))
		}
	case <-time.After(time.Second):
		t.Fatal("queued writer was not granted at head")
	}

	if m.Pending(reader) == false {
		t.Fatal("reader must still wait behind the granted writer")
	}
	if err := m.Abort(writer); err != nil {
		t.Fatal(err)
	}
	select {
	case res := <-rCh:
		if res.err != nil {
			t.Fatalf("reader grant: %v", res.err)
		}
	case <-time.After(time.Second):
		t.Fatal("reader was not granted after writer release")
	}
}

func TestReentrantCompatibleRequestSucceeds(t *testing.T) {
	m := newManager(t)
	db, table, page, row := testResources()
	txn := m.Begin(1)
	acquireHierarchy(t, m, txn, db, table, page, S)
	mustLock(t, m, txn, row, S)

	// Re-entrant IS / S requests covered by the held S succeed immediately,
	// even with an incompatible waiter queued ahead.
	blocker := m.Begin(2)
	acquireHierarchy(t, m, blocker, db, table, page, X)
	bCh := asyncLock(m, blocker, row, X)
	waitFor(t, time.Second, func() bool { return len(m.Snapshot(row)) == 1 })

	if err := m.Lock(txn, row, IS); err != nil {
		t.Fatalf("re-entrant IS under S: %v", err)
	}
	if err := m.Lock(txn, row, S); err != nil {
		t.Fatalf("re-entrant S: %v", err)
	}
	if m.HeldLock(txn, row) != S {
		t.Fatalf("mode changed by re-entry: %s", m.HeldLock(txn, row))
	}

	if err := m.Abort(blocker); err != nil {
		t.Fatal(err)
	}
	<-bCh
}
