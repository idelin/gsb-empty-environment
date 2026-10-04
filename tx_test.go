package bptree

import "testing"

// A failed commit must not publish a new root: the tree keeps serving the
// old version and stays valid and writable.
func TestFailedCommitDoesNotPublishRoot(t *testing.T) {
	tr := mustTree(t, 4)
	fill(t, tr, 0, 40)
	rootBefore := tr.Root()
	versionBefore := tr.Version()

	tr.Store().SetWriteFailures(1)
	tx := tr.Begin()
	if err := tx.Put(1000, []byte("boom")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := tx.Commit(); err != ErrInjected {
		t.Fatalf("Commit err = %v, want ErrInjected", err)
	}

	if tr.Root() != rootBefore {
		t.Fatalf("root changed after failed commit")
	}
	if tr.Version() != versionBefore {
		t.Fatalf("version changed after failed commit")
	}
	if _, err := tr.Get(1000); err != ErrNotFound {
		t.Fatalf("Get(1000) err = %v, want ErrNotFound", err)
	}
	if err := tr.Validate(); err != nil {
		t.Fatalf("Validate after failed commit: %v", err)
	}

	// The tree is still fully usable.
	tx = tr.Begin()
	if err := tx.Put(1000, []byte("ok")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit after failure: %v", err)
	}
	if v, err := tr.Get(1000); err != nil || string(v) != "ok" {
		t.Fatalf("Get(1000) = %q, %v", v, err)
	}

	// Garbage pages left by the failed commit are reclaimable.
	before := tr.Store().AllocatedCount()
	freed, err := tr.Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if freed == 0 {
		t.Fatalf("Collect freed nothing; failed-commit garbage leaked")
	}
	if after := tr.Store().AllocatedCount(); after >= before {
		t.Fatalf("allocated pages: before=%d after=%d", before, after)
	}
}

// Rollback publishes nothing.
func TestRollbackPublishesNothing(t *testing.T) {
	tr := mustTree(t, 4)
	fill(t, tr, 0, 20)
	rootBefore := tr.Root()

	tx := tr.Begin()
	if err := tx.Put(999, []byte("x")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	tx.Rollback()

	if tr.Root() != rootBefore {
		t.Fatalf("root changed after rollback")
	}
	if _, err := tr.Get(999); err != ErrNotFound {
		t.Fatalf("Get(999) err = %v, want ErrNotFound", err)
	}
	if err := tx.Put(1, []byte("y")); err != ErrTxDone {
		t.Fatalf("Put after rollback err = %v, want ErrTxDone", err)
	}
}

// A transaction reads its own writes.
func TestReadYourWrites(t *testing.T) {
	tr := mustTree(t, 4)
	fill(t, tr, 0, 20)
	tx := tr.Begin()
	if err := tx.Put(7, []byte("mine")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := tx.Delete(3); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if v, err := tx.Get(7); err != nil || string(v) != "mine" {
		t.Fatalf("tx Get(7) = %q, %v", v, err)
	}
	if _, err := tx.Get(3); err != ErrNotFound {
		t.Fatalf("tx Get(3) err = %v, want ErrNotFound", err)
	}
	tx.Rollback()
}

// Oversized values are rejected.
func TestValueTooLarge(t *testing.T) {
	tr := mustTree(t, 4)
	tx := tr.Begin()
	if err := tx.Put(1, make([]byte, 1024)); err != ErrValueTooLarge {
		t.Fatalf("Put err = %v, want ErrValueTooLarge", err)
	}
	tx.Rollback()
}
