package ssi

import (
	"errors"
	"testing"
)

// mustGet reads key and fails the test on error.
func mustGet(t *testing.T, tx *Txn, key string) int64 {
	t.Helper()
	v, found, err := tx.Get(key)
	if err != nil {
		t.Fatalf("Get(%s): %v", key, err)
	}
	if !found {
		t.Fatalf("Get(%s): key not found", key)
	}
	return v
}

// TestWriteSkewPrevented is the acceptance scenario:
//
//	A = 100, B = 100, invariant A+B >= 0.
//	T1 reads A,B and withdraws 80 from A.
//	T2 concurrently reads A,B and withdraws 80 from B.
//
// Plain snapshot isolation would commit both (A=-? no: A=20, B=20 is fine,
// but with 80+80 from 100+100 the sum would be 200-160=40 >= 0... to make
// the invariant break we withdraw 150 each? No: per the spec, each checks
// A+B >= 0 before withdrawing; under SI both see 200 and both commit,
// leaving A+B = 40 which is still >= 0. To make the anomaly violate the
// invariant we use withdrawals of 150? The spec says 80. With 80 each the
// sum stays positive, so we assert the SSI-specific behaviour instead:
// T2 must be aborted because it forms the dangerous structure
// T1 ->rw-> T2 ->rw-> T1, and the final state must equal the serial
// execution T1 then nothing (A=20, B=100).
func TestWriteSkewPrevented(t *testing.T) {
	s := NewStore()
	s.Set("A", 100)
	s.Set("B", 100)

	t1 := s.Begin()
	t2 := s.Begin()

	// T1: read both accounts, check invariant, withdraw 80 from A.
	a1 := mustGet(t, t1, "A")
	b1 := mustGet(t, t1, "B")
	if a1+b1-80 < 0 {
		t.Fatal("T1: invariant would break, should not withdraw")
	}
	if err := t1.Put("A", a1-80); err != nil {
		t.Fatalf("T1 Put: %v", err)
	}

	// T2: read both accounts (still sees A=100,B=100 under SI), withdraw 80 from B.
	a2 := mustGet(t, t2, "A")
	b2 := mustGet(t, t2, "B")
	if a2 != 100 || b2 != 100 {
		t.Fatalf("T2 should read the snapshot A=100,B=100, got A=%d,B=%d", a2, b2)
	}
	if a2+b2-80 < 0 {
		t.Fatal("T2: invariant would break, should not withdraw")
	}
	if err := t2.Put("B", b2-80); err != nil {
		t.Fatalf("T2 Put: %v", err)
	}

	// T1 commits first: it is T3 of the dangerous structure
	// T1 ->rw-> T2 ->rw-> T1, so the pivot T2 is aborted.
	if err := t1.Commit(); err != nil {
		t.Fatalf("T1 should commit, got %v", err)
	}
	if err := t2.Commit(); !errors.Is(err, ErrSerializationFailure) {
		t.Fatalf("T2 must abort with ErrSerializationFailure, got %v", err)
	}

	// Final state must equal the serial execution of T1 alone.
	a, _ := s.Snapshot("A")
	b, _ := s.Snapshot("B")
	if a != 20 || b != 100 {
		t.Fatalf("final state A=%d B=%d, want A=20 B=100", a, b)
	}
	if a+b < 0 {
		t.Fatalf("invariant violated: A+B=%d", a+b)
	}
}

// TestWriteSkewReverseCommitOrder: whichever transaction commits first
// survives; the second one is aborted.
func TestWriteSkewReverseCommitOrder(t *testing.T) {
	s := NewStore()
	s.Set("A", 100)
	s.Set("B", 100)

	t1 := s.Begin()
	t2 := s.Begin()

	a1 := mustGet(t, t1, "A")
	b1 := mustGet(t, t1, "B")
	_ = t1.Put("A", a1-80)

	a2 := mustGet(t, t2, "A")
	b2 := mustGet(t, t2, "B")
	_ = t2.Put("B", b2-80)
	_, _ = a1, a2
	_ = b1

	if err := t2.Commit(); err != nil {
		t.Fatalf("T2 (first committer) should commit, got %v", err)
	}
	if err := t1.Commit(); !errors.Is(err, ErrSerializationFailure) {
		t.Fatalf("T1 must abort, got %v", err)
	}

	a, _ := s.Snapshot("A")
	b, _ := s.Snapshot("B")
	if a != 100 || b != 20 {
		t.Fatalf("final state A=%d B=%d, want A=100 B=20", a, b)
	}
}

// TestDisjointWritesCommit: no false positives when transactions do not
// overlap through rw-antidependencies.
func TestDisjointWritesCommit(t *testing.T) {
	s := NewStore()
	s.Set("A", 100)
	s.Set("B", 100)

	t1 := s.Begin()
	t2 := s.Begin()

	a := mustGet(t, t1, "A")
	if err := t1.Put("A", a-10); err != nil {
		t.Fatal(err)
	}
	b := mustGet(t, t2, "B")
	if err := t2.Put("B", b-10); err != nil {
		t.Fatal(err)
	}

	if err := t1.Commit(); err != nil {
		t.Fatalf("T1 should commit: %v", err)
	}
	if err := t2.Commit(); err != nil {
		t.Fatalf("T2 should commit: %v", err)
	}
	if a, _ = s.Snapshot("A"); a != 90 {
		t.Fatalf("A=%d, want 90", a)
	}
	if b, _ = s.Snapshot("B"); b != 90 {
		t.Fatalf("B=%d, want 90", b)
	}
}

// TestWriteWriteConflict: first committer wins.
func TestWriteWriteConflict(t *testing.T) {
	s := NewStore()
	s.Set("A", 100)

	t1 := s.Begin()
	t2 := s.Begin()

	a1 := mustGet(t, t1, "A")
	a2 := mustGet(t, t2, "A")
	if err := t1.Put("A", a1-10); err != nil {
		t.Fatal(err)
	}
	if err := t2.Put("A", a2-20); err != nil {
		t.Fatal(err)
	}
	if err := t1.Commit(); err != nil {
		t.Fatalf("T1 should commit: %v", err)
	}
	if err := t2.Commit(); !errors.Is(err, ErrWriteConflict) {
		t.Fatalf("T2 must fail with ErrWriteConflict, got %v", err)
	}
	if a, _ := s.Snapshot("A"); a != 90 {
		t.Fatalf("A=%d, want 90", a)
	}
}

// TestAbortedTxnCannotContinue: once aborted, all operations fail.
func TestAbortedTxnCannotContinue(t *testing.T) {
	s := NewStore()
	s.Set("A", 100)
	s.Set("B", 100)

	t1 := s.Begin()
	t2 := s.Begin()

	a1 := mustGet(t, t1, "A")
	mustGet(t, t1, "B")
	_ = t1.Put("A", a1-80)
	mustGet(t, t2, "A")
	b2 := mustGet(t, t2, "B")
	_ = t2.Put("B", b2-80)

	if err := t1.Commit(); err != nil {
		t.Fatalf("T1 should commit: %v", err)
	}
	if _, _, err := t2.Get("A"); !errors.Is(err, ErrSerializationFailure) {
		t.Fatalf("Get on aborted T2 should fail, got %v", err)
	}
	if err := t2.Put("A", 0); !errors.Is(err, ErrSerializationFailure) {
		t.Fatalf("Put on aborted T2 should fail, got %v", err)
	}
}

// TestReadOnlyDoesNotBlock: a read-only transaction overlapping a writer
// commits fine (single rw-antidependency is not a dangerous structure).
func TestReadOnlyDoesNotBlock(t *testing.T) {
	s := NewStore()
	s.Set("A", 100)

	t1 := s.Begin()
	t2 := s.Begin()

	mustGet(t, t1, "A") // T1 reads A
	a2 := mustGet(t, t2, "A")
	if err := t2.Put("A", a2+1); err != nil {
		t.Fatal(err)
	}
	if err := t2.Commit(); err != nil {
		t.Fatalf("writer T2 should commit: %v", err)
	}
	if err := t1.Commit(); err != nil {
		t.Fatalf("read-only T1 should commit: %v", err)
	}
}

// TestLongerChainAborted: T1 ->rw-> T2 ->rw-> T3 across three keys.
func TestLongerChainAborted(t *testing.T) {
	s := NewStore()
	s.Set("X", 1)
	s.Set("Y", 1)
	s.Set("Z", 1)

	t1 := s.Begin()
	t2 := s.Begin()
	t3 := s.Begin()

	// T1 reads X; T2 writes X (edge T1->T2) and reads Y; T3 writes Y (edge T2->T3).
	mustGet(t, t1, "X")
	if err := t2.Put("X", 2); err != nil {
		t.Fatal(err)
	}
	mustGet(t, t2, "Y")
	if err := t3.Put("Y", 2); err != nil {
		t.Fatal(err)
	}

	// T3 commits first: dangerous structure T1->T2->T3, pivot T2 aborts.
	if err := t3.Commit(); err != nil {
		t.Fatalf("T3 should commit: %v", err)
	}
	if err := t2.Commit(); !errors.Is(err, ErrSerializationFailure) {
		t.Fatalf("pivot T2 must abort, got %v", err)
	}
	if err := t1.Commit(); err != nil {
		t.Fatalf("T1 (read-only, not on a remaining cycle) should commit: %v", err)
	}
}
