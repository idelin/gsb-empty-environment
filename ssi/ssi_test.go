package ssi

import (
	"errors"
	"sync"
	"testing"
)

func isSerialization(err error) bool {
	return errors.Is(err, ErrSerialization)
}

// TestWriteSkewTwoTxnCycle covers the classic two-transaction write skew
// ring: each transaction reads the other's write set and writes a disjoint
// key, forming T1 -rw-> T2 -rw-> T1.
func TestWriteSkewTwoTxnCycle(t *testing.T) {
	m := NewManager()

	t1 := m.Begin()
	t2 := m.Begin()

	if _, _, err := m.Read(t1, "doctors", "a"); err != nil {
		t.Fatalf("t1 read: %v", err)
	}
	if _, _, err := m.Read(t2, "doctors", "b"); err != nil {
		t.Fatalf("t2 read: %v", err)
	}

	if err := m.Write(t1, "doctors", "b", "off"); err != nil && !isSerialization(err) {
		t.Fatalf("t1 write: %v", err)
	}
	if err := m.Write(t2, "doctors", "a", "off"); err != nil && !isSerialization(err) {
		t.Fatalf("t2 write: %v", err)
	}

	err1 := m.Commit(t1)
	err2 := m.Commit(t2)

	commits := 0
	for _, err := range []error{err1, err2} {
		if err == nil {
			commits++
		} else if !isSerialization(err) {
			t.Fatalf("unexpected commit error: %v", err)
		}
	}
	if commits != 1 {
		t.Fatalf("expected exactly one survivor, got %d (err1=%v err2=%v)", commits, err1, err2)
	}
}

// TestThreeTxnDangerousStructure covers T1 -rw-> T2 -rw-> T3 where T3
// commits first; the pivot T2 must be aborted.
func TestThreeTxnDangerousStructure(t *testing.T) {
	m := NewManager()

	t1 := m.Begin()
	t2 := m.Begin()
	t3 := m.Begin()

	if _, _, err := m.Read(t1, "t", "x"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Read(t2, "t", "y"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Read(t3, "t", "z"); err != nil {
		t.Fatal(err)
	}

	// t2 writes x (read by t1): t1 -rw-> t2.
	if err := m.Write(t2, "t", "x", "1"); err != nil {
		t.Fatalf("t2 write x: %v", err)
	}
	// t3 writes y (read by t2): t2 -rw-> t3.
	if err := m.Write(t3, "t", "y", "2"); err != nil {
		t.Fatalf("t3 write y: %v", err)
	}

	// T3 is the first committer, making the structure dangerous.
	if err := m.Commit(t3); err != nil {
		t.Fatalf("t3 commit: %v", err)
	}

	err2 := m.Commit(t2)
	if !isSerialization(err2) {
		t.Fatalf("expected pivot t2 to be aborted, got %v", err2)
	}
	if t2.Status() != StatusAborted {
		t.Fatalf("t2 status = %v, want aborted", t2.Status())
	}

	// t1 must not be blocked by the aborted pivot.
	if err := m.Write(t1, "t", "w", "3"); err != nil && !isSerialization(err) {
		t.Fatalf("t1 write: %v", err)
	}
	if err := m.Commit(t1); err != nil && !isSerialization(err) {
		t.Fatalf("t1 commit: %v", err)
	}
}

// TestReadOnlySafeSnapshotExemption covers both the safe-snapshot exemption
// and the case where a read-only transaction starts after a conflict exists
// and therefore remains abortable.
func TestReadOnlySafeSnapshotExemption(t *testing.T) {
	t.Run("safe", func(t *testing.T) {
		m := NewManager()
		rw := m.Begin()
		ro := m.Begin(WithReadOnly())

		if !ro.SafeSnapshot() {
			t.Fatal("read-only transaction should start with a safe snapshot")
		}
		if _, err := m.RangeRead(ro, "t", "", ""); err != nil {
			t.Fatal(err)
		}
		if _, _, err := m.Read(ro, "t", "k"); err != nil {
			t.Fatal(err)
		}

		if err := m.Write(rw, "t", "k", "v"); err != nil {
			t.Fatalf("rw write: %v", err)
		}
		if err := m.Commit(rw); err != nil {
			t.Fatalf("rw commit: %v", err)
		}
		if err := m.Commit(ro); err != nil {
			t.Fatalf("safe read-only commit: %v", err)
		}
		if m.EdgeCount() != 0 {
			t.Fatalf("safe snapshot produced %d edges", m.EdgeCount())
		}
	})

	t.Run("unsafe", func(t *testing.T) {
		m := NewManager()
		a := m.Begin()
		b := m.Begin()

		if _, _, err := m.Read(a, "t", "k1"); err != nil {
			t.Fatal(err)
		}
		if err := m.Write(b, "t", "k1", "v"); err != nil {
			t.Fatal(err)
		} // a -rw-> b

		ro := m.Begin(WithReadOnly())
		if ro.SafeSnapshot() {
			t.Fatal("snapshot must be unsafe while a concurrent rw txn carries a conflict")
		}
		if _, _, err := m.Read(ro, "t", "k2"); err != nil {
			t.Fatal(err)
		}
		if err := m.Write(a, "t", "k2", "v"); err != nil {
			t.Fatalf("a write k2: %v", err)
		} // ro -rw-> a, giving ro -rw-> a -rw-> b

		// b commits first (T3 earlier in commit order), then pivot a commits.
		if err := m.Commit(b); err != nil {
			t.Fatalf("b commit: %v", err)
		}
		if err := m.Commit(a); err != nil && !isSerialization(err) {
			t.Fatalf("pivot a commit: %v", err)
		}

		// The pivot (a) is the preferred victim while active; ro is the
		// fallback victim after the pivot commits. Either way the unsafe
		// read-only transaction must never commit without a serialization
		// failure somewhere in the active part of the structure.
		if a.Status() == StatusAborted {
			if err := m.Commit(ro); err != nil {
				t.Fatalf("ro should commit after pivot abort: %v", err)
			}
		} else if err := m.Commit(ro); !isSerialization(err) {
			t.Fatalf("expected unsafe read-only txn to be aborted, got %v", err)
		}
	})
}

// TestAbortedTxnEdgesRetracted verifies that aborting a transaction removes
// its rw edges and locks so it cannot cause false positives later.
func TestAbortedTxnEdgesRetracted(t *testing.T) {
	m := NewManager()

	t1 := m.Begin()
	t2 := m.Begin()

	if _, _, err := m.Read(t1, "t", "k"); err != nil {
		t.Fatal(err)
	}
	if err := m.Write(t2, "t", "k", "v"); err != nil {
		t.Fatal(err)
	}
	if !m.HasEdge(t1.ID(), t2.ID()) {
		t.Fatal("expected t1 -rw-> t2 before abort")
	}

	if err := m.Abort(t2); err != nil {
		t.Fatalf("abort t2: %v", err)
	}
	if m.HasEdge(t1.ID(), t2.ID()) || m.EdgeCount() != 0 {
		t.Fatalf("edges were not retracted: count=%d", m.EdgeCount())
	}

	if err := m.Write(t1, "t", "other", "v"); err != nil {
		t.Fatalf("t1 write after t2 abort: %v", err)
	}
	if err := m.Commit(t1); err != nil {
		t.Fatalf("t1 commit must not be blocked: %v", err)
	}

	t3 := m.Begin()
	if err := m.Write(t3, "t", "k", "new"); err != nil {
		t.Fatalf("t3 write: %v", err)
	}
	if err := m.Commit(t3); err != nil {
		t.Fatalf("t3 commit: %v", err)
	}
}

// TestSIREADLockEscalation verifies row-to-table escalation and that the
// table lock catches writes to keys never explicitly read.
func TestSIREADLockEscalation(t *testing.T) {
	m := NewManager(WithEscalationThreshold(3))

	reader := m.Begin()
	for _, key := range []string{"k1", "k2", "k3", "k4"} {
		if _, _, err := m.Read(reader, "t", key); err != nil {
			t.Fatal(err)
		}
	}
	if !m.HoldsTableLock(reader, "t") {
		t.Fatal("expected row locks to escalate to a table SIREAD lock")
	}
	if got := m.RowLockCount(reader, "t"); got != 0 {
		t.Fatalf("row locks after escalation = %d, want 0", got)
	}

	// Two disjoint writers both hit the escalated table lock, giving
	// reader -rw-> w1 and reader -rw-> w2. The second writer also reads a
	// key the first writer updated, closing w2 -rw-> w1.
	w1 := m.Begin()
	w2 := m.Begin()

	if err := m.Write(w1, "t", "never-read-key", "1"); err != nil {
		t.Fatalf("w1 write: %v", err)
	}
	if !m.HasEdge(reader.ID(), w1.ID()) {
		t.Fatal("table lock failed to capture conflict on an unread key")
	}

	if _, _, err := m.Read(w2, "t", "never-read-key"); err != nil {
		t.Fatal(err)
	}
	if err := m.Write(w2, "t", "other-unread-key", "2"); err != nil {
		t.Fatalf("w2 write: %v", err)
	}
	if !m.HasEdge(reader.ID(), w2.ID()) {
		t.Fatal("table lock failed to capture the second writer")
	}
	if !m.HasEdge(w2.ID(), w1.ID()) {
		t.Fatal("expected closing edge w2 -rw-> w1")
	}

	if err := m.Commit(w1); err != nil {
		t.Fatalf("w1 commit: %v", err)
	}
	if err := m.Commit(w2); !isSerialization(err) {
		t.Fatalf("expected w2 abort via table-lock structure, got %v", err)
	}
}

// TestRangeLockConflict verifies index-range SIREAD locks intersect writes
// whose keys fall inside the range.
func TestRangeLockConflict(t *testing.T) {
	m := NewManager()

	reader := m.Begin()
	if _, err := m.RangeRead(reader, "t", "a", "c"); err != nil {
		t.Fatal(err)
	}

	writer := m.Begin()
	if err := m.Write(writer, "t", "b", "v"); err != nil {
		t.Fatal(err)
	}
	if !m.HasEdge(reader.ID(), writer.ID()) {
		t.Fatal("range SIREAD lock failed to capture in-range write")
	}

	outside := m.Begin()
	if err := m.Write(outside, "t", "z", "v"); err != nil {
		t.Fatal(err)
	}
	if m.HasEdge(reader.ID(), outside.ID()) {
		t.Fatal("range lock produced a false conflict outside its bounds")
	}
}

// TestEscalationPreservesOtherLocks verifies that one transaction's
// escalation to a table lock does not discard row/range SIREAD locks held
// by concurrent transactions on the same table.
func TestEscalationPreservesOtherLocks(t *testing.T) {
	m := NewManager(WithEscalationThreshold(2))

	other := m.Begin()
	escalating := m.Begin()

	if _, _, err := m.Read(other, "t", "shared"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.RangeRead(other, "t", "r1", "r2"); err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{"a", "b", "c"} {
		if _, _, err := m.Read(escalating, "t", key); err != nil {
			t.Fatal(err)
		}
	}
	if !m.HoldsTableLock(escalating, "t") {
		t.Fatal("expected escalating txn to hold a table lock")
	}

	writer := m.Begin()
	if err := m.Write(writer, "t", "shared", "v"); err != nil {
		t.Fatalf("write shared: %v", err)
	}
	if !m.HasEdge(other.ID(), writer.ID()) {
		t.Fatal("other txn's row SIREAD lock was lost during escalation")
	}
	if err := m.Write(writer, "t", "r1", "v"); err != nil {
		t.Fatalf("write r1: %v", err)
	}
	if !m.HasEdge(other.ID(), writer.ID()) {
		t.Fatal("other txn's range SIREAD lock was lost during escalation")
	}
}

// TestCommittedSIREADRetention verifies that a committed transaction keeps
// its SIREAD locks until every concurrent transaction finishes; otherwise
// a later writer would miss the edge that completes a dangerous cycle.
func TestCommittedSIREADRetention(t *testing.T) {
	m := NewManager()

	a := m.Begin()
	b := m.Begin()

	// Disjoint read sets, classic write skew shape.
	if _, _, err := m.Read(a, "t", "x"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Read(b, "t", "y"); err != nil {
		t.Fatal(err)
	}
	// a writes what b read: b -rw-> a.
	if err := m.Write(a, "t", "y", "1"); err != nil {
		t.Fatal(err)
	}
	if !m.HasEdge(b.ID(), a.ID()) {
		t.Fatal("expected b -rw-> a after a's write")
	}

	// a commits while b is still in flight: its SIREAD lock on x must be
	// retained rather than reclaimed.
	if err := m.Commit(a); err != nil {
		t.Fatalf("a commit: %v", err)
	}
	if m.RowLockCount(a, "t") != 1 {
		t.Fatal("committed SIREAD lock was reclaimed while a concurrent txn was active")
	}

	// b now writes what a read before committing. Only the retained SIREAD
	// lock can create a -rw-> b, closing the cycle a -rw-> b -rw-> a.
	writeErr := m.Write(b, "t", "x", "2")
	if isSerialization(writeErr) {
		// Detection may kill b immediately at write time.
		if b.Status() != StatusAborted {
			t.Fatal("b should be marked aborted after serialization failure")
		}
		if err := m.Abort(b); err != nil {
			t.Fatalf("b cleanup abort: %v", err)
		}
	} else if writeErr != nil {
		t.Fatalf("b write: %v", writeErr)
	} else {
		if !m.HasEdge(a.ID(), b.ID()) {
			t.Fatal("retained SIREAD lock failed to produce a -rw-> b")
		}
		if err := m.Commit(b); !isSerialization(err) {
			t.Fatalf("expected b to be aborted via retained SIREAD edge, got %v", err)
		}
	}

	if m.ActiveCount() != 0 {
		t.Fatalf("active count after shutdown = %d", m.ActiveCount())
	}
	if m.RowLockCount(a, "t") != 0 {
		t.Fatal("SIREAD locks must be released once no concurrent txn remains")
	}
}

// TestPreparePhaseDetection verifies Prepare runs the same dangerous
// structure check as Commit.
func TestPreparePhaseDetection(t *testing.T) {
	m := NewManager()

	t1 := m.Begin()
	t2 := m.Begin()

	if _, _, err := m.Read(t1, "t", "a"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Read(t2, "t", "b"); err != nil {
		t.Fatal(err)
	}
	if err := m.Write(t1, "t", "b", "1"); err != nil && !isSerialization(err) {
		t.Fatal(err)
	}
	if err := m.Write(t2, "t", "a", "2"); err != nil && !isSerialization(err) {
		t.Fatal(err)
	}

	var prepared, other *Txn
	if err := m.Prepare(t1); err == nil {
		prepared, other = t1, t2
	} else if isSerialization(err) {
		prepared, other = t2, t1
		if err := m.Prepare(t2); err != nil {
			t.Fatalf("t2 prepare: %v", err)
		}
	} else {
		t.Fatalf("prepare: %v", err)
	}

	if err := m.Commit(prepared); err != nil {
		t.Fatalf("prepared commit: %v", err)
	}
	err := m.Commit(other)
	if !isSerialization(err) {
		t.Fatalf("expected second txn abort, got %v", err)
	}
}

// TestSnapshotIsolationVisibility verifies snapshot reads ignore
// concurrent writes and observe committed values after re-beginning.
func TestSnapshotIsolationVisibility(t *testing.T) {
	m := NewManager()

	boot := m.Begin()
	if err := m.Write(boot, "t", "k", "v0"); err != nil {
		t.Fatal(err)
	}
	if err := m.Commit(boot); err != nil {
		t.Fatal(err)
	}

	reader := m.Begin()
	writer := m.Begin()

	val, ok, err := m.Read(reader, "t", "k")
	if err != nil || !ok || val != "v0" {
		t.Fatalf("initial read = %q,%v,%v", val, ok, err)
	}

	if err := m.Write(writer, "t", "k", "v1"); err != nil {
		t.Fatal(err)
	}
	val, ok, err = m.Read(reader, "t", "k")
	if err != nil || !ok || val != "v0" {
		t.Fatalf("snapshot read drifted = %q,%v,%v", val, ok, err)
	}

	if err := m.Commit(writer); err != nil {
		t.Fatal(err)
	}
	if err := m.Commit(reader); err != nil {
		t.Fatalf("reader commit: %v", err)
	}

	fresh := m.Begin()
	val, ok, err = m.Read(fresh, "t", "k")
	if err != nil || !ok || val != "v1" {
		t.Fatalf("fresh read = %q,%v,%v", val, ok, err)
	}
	if err := m.Commit(fresh); err != nil {
		t.Fatal(err)
	}

	pairs, err := m.RangeRead(m.Begin(), "t", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 1 || pairs[0].Value != "v1" {
		t.Fatalf("range read = %+v", pairs)
	}
}

// TestConcurrentSchedules exercises the manager from many goroutines to
// exercise the mutex under the race detector.
func TestConcurrentSchedules(t *testing.T) {
	m := NewManager(WithEscalationThreshold(4))

	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for round := 0; round < 20; round++ {
				txn := m.Begin()
				if i%2 == 0 {
					_ = m.Abort(txn)
					continue
				}
				keys := []string{"a", "b", "c", "d", "e"}
				for _, key := range keys {
					_, _, _ = m.Read(txn, "t", key)
				}
				if _, err := m.RangeRead(txn, "t", "a", "e"); err != nil {
					_ = m.Abort(txn)
					continue
				}
				if err := m.Write(txn, "t", keys[i%len(keys)], "v"); err != nil {
					_ = m.Abort(txn)
					continue
				}
				_ = m.Commit(txn)
			}
		}(i)
	}
	wg.Wait()

	if m.ActiveCount() != 0 {
		t.Fatalf("active count = %d", m.ActiveCount())
	}
}
