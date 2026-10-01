package mvcc

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// TestAcceptanceScenario covers the required acceptance boundary:
// T1(10) writes K1 uncommitted; T2(11) reads K1 and sees the old value;
// T2 writing K1 hits a write-write conflict and is aborted; after T1 commits,
// a new T3(12) reads T1's latest value.
func TestAcceptanceScenario(t *testing.T) {
	store := NewStore(9) // seed txn takes 9, so T1=10, T2=11, T3=12

	// Seed K1 with an old committed value.
	seed := store.Begin()
	if err := seed.Put("K1", "old"); err != nil {
		t.Fatalf("seed put: %v", err)
	}
	if err := seed.Commit(); err != nil {
		t.Fatalf("seed commit: %v", err)
	}

	// T1 (ID=10) modifies K1 but does not commit.
	t1 := store.Begin()
	if t1.ID() != 10 {
		t.Fatalf("T1 id = %d, want 10", t1.ID())
	}
	if err := t1.Put("K1", "new"); err != nil {
		t.Fatalf("T1 put: %v", err)
	}

	// T2 (ID=11) reads K1: must see the old committed version only.
	t2 := store.Begin()
	if t2.ID() != 11 {
		t.Fatalf("T2 id = %d, want 11", t2.ID())
	}
	got, err := t2.Get("K1")
	if err != nil {
		t.Fatalf("T2 get: %v", err)
	}
	if got != "old" {
		t.Fatalf("T2 read K1 = %q, want %q (snapshot must hide T1's uncommitted write)", got, "old")
	}

	// T2 writes K1: write-write conflict with T1's pending write -> aborted.
	err = t2.Put("K1", "t2-value")
	if !errors.Is(err, ErrWriteConflict) {
		t.Fatalf("T2 put err = %v, want ErrWriteConflict", err)
	}
	if _, err := t2.Get("K1"); !errors.Is(err, ErrTxnNotActive) {
		t.Fatalf("T2 after abort get err = %v, want ErrTxnNotActive", err)
	}

	// T1 commits; a new T3 (ID=12) sees T1's latest value.
	if err := t1.Commit(); err != nil {
		t.Fatalf("T1 commit: %v", err)
	}
	t3 := store.Begin()
	if t3.ID() != 12 {
		t.Fatalf("T3 id = %d, want 12", t3.ID())
	}
	got, err = t3.Get("K1")
	if err != nil {
		t.Fatalf("T3 get: %v", err)
	}
	if got != "new" {
		t.Fatalf("T3 read K1 = %q, want %q (T1's committed value)", got, "new")
	}
}

// TestMonotonicTxnIDs verifies IDs increase monotonically.
func TestMonotonicTxnIDs(t *testing.T) {
	store := NewStore(1)
	prev := uint64(0)
	for i := 0; i < 100; i++ {
		txn := store.Begin()
		if txn.ID() <= prev {
			t.Fatalf("txn id %d not greater than previous %d", txn.ID(), prev)
		}
		prev = txn.ID()
		txn.Abort()
	}
}

// TestSnapshotIsStable verifies a transaction keeps reading its snapshot even
// after newer commits happen.
func TestSnapshotIsStable(t *testing.T) {
	store := NewStore(1)

	seed := store.Begin()
	_ = seed.Put("K", "v1")
	_ = seed.Commit()

	reader := store.Begin()

	writer := store.Begin()
	_ = writer.Put("K", "v2")
	_ = writer.Commit()

	got, err := reader.Get("K")
	if err != nil || got != "v1" {
		t.Fatalf("reader got %q, %v; want stable snapshot value v1", got, err)
	}

	fresh := store.Begin()
	got, err = fresh.Get("K")
	if err != nil || got != "v2" {
		t.Fatalf("fresh txn got %q, %v; want v2", got, err)
	}
}

// TestFirstCommitterWinsAtCommit: two txns write different keys fine, but two
// txns writing the same key where one commits first -> the other aborts at
// commit time.
func TestFirstCommitterWinsAtCommit(t *testing.T) {
	store := NewStore(1)

	t1 := store.Begin()
	t2 := store.Begin()

	// Disjoint writes: both commit.
	if err := t1.Put("A", "1"); err != nil {
		t.Fatalf("t1 put A: %v", err)
	}
	if err := t2.Put("B", "2"); err != nil {
		t.Fatalf("t2 put B: %v", err)
	}
	if err := t1.Commit(); err != nil {
		t.Fatalf("t1 commit: %v", err)
	}
	if err := t2.Commit(); err != nil {
		t.Fatalf("t2 commit: %v", err)
	}

	// Same key: t3 stages first, t4 is aborted at Put (pending-write conflict).
	t3 := store.Begin()
	t4 := store.Begin()
	if err := t3.Put("C", "3"); err != nil {
		t.Fatalf("t3 put C: %v", err)
	}
	if err := t4.Put("C", "4"); !errors.Is(err, ErrWriteConflict) {
		t.Fatalf("t4 put C err = %v, want ErrWriteConflict", err)
	}
	if err := t3.Commit(); err != nil {
		t.Fatalf("t3 commit: %v", err)
	}
}

// TestReadOwnWrites verifies a transaction reads its own pending writes.
func TestReadOwnWrites(t *testing.T) {
	store := NewStore(1)
	txn := store.Begin()
	if err := txn.Put("K", "mine"); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := txn.Get("K")
	if err != nil || got != "mine" {
		t.Fatalf("got %q, %v; want own pending write", got, err)
	}
	txn.Abort()
	if _, err := txn.Get("K"); !errors.Is(err, ErrTxnNotActive) {
		t.Fatalf("after abort err = %v, want ErrTxnNotActive", err)
	}
}

// TestAbortDiscardsWrites verifies aborted transactions leave no trace.
func TestAbortDiscardsWrites(t *testing.T) {
	store := NewStore(1)
	txn := store.Begin()
	_ = txn.Put("K", "temp")
	txn.Abort()

	fresh := store.Begin()
	if _, err := fresh.Get("K"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("get err = %v, want ErrKeyNotFound after abort", err)
	}
}

// TestConcurrentTransactions runs many goroutines doing disjoint writes to
// exercise the store under -race.
func TestConcurrentTransactions(t *testing.T) {
	store := NewStore(1)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("key-%d", i)
			txn := store.Begin()
			if err := txn.Put(key, fmt.Sprintf("val-%d", i)); err != nil {
				t.Errorf("put %s: %v", key, err)
				return
			}
			if err := txn.Commit(); err != nil {
				t.Errorf("commit %s: %v", key, err)
				return
			}
			reader := store.Begin()
			if got, err := reader.Get(key); err != nil || got != fmt.Sprintf("val-%d", i) {
				t.Errorf("read %s = %q, %v", key, got, err)
			}
		}(i)
	}
	wg.Wait()
}
