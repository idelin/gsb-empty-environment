package bptree

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

func openTemp(t *testing.T, capacity int) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"), capacity)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func val(s string) []byte { return []byte(s) }

func mustGet(t *testing.T, db *DB, key int64, want string) {
	t.Helper()
	got, err := db.Get(key)
	if err != nil {
		t.Fatalf("Get(%d): %v", key, err)
	}
	if string(got) != want {
		t.Fatalf("Get(%d) = %q, want %q", key, got, want)
	}
}

func TestBasicPutGetOverwrite(t *testing.T) {
	db := openTemp(t, 4)
	if err := db.Update(func(tx *Tx) error {
		for i := int64(0); i < 50; i++ {
			if err := tx.Put(i, val(fmt.Sprintf("v%d", i))); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	for i := int64(0); i < 50; i++ {
		mustGet(t, db, i, fmt.Sprintf("v%d", i))
	}
	// Duplicate keys overwrite.
	if err := db.Update(func(tx *Tx) error {
		return tx.Put(7, val("new"))
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	mustGet(t, db, 7, "new")
	if _, err := db.Get(999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(999) err = %v, want ErrNotFound", err)
	}
	if err := db.Validate(db.Root()); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestDeleteBorrowMergeRootShrink(t *testing.T) {
	db := openTemp(t, 3) // tiny capacity forces splits and merges
	n := int64(200)
	if err := db.Update(func(tx *Tx) error {
		for i := int64(0); i < n; i++ {
			if err := tx.Put(i, val(fmt.Sprintf("v%d", i))); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := db.Validate(db.Root()); err != nil {
		t.Fatalf("Validate after insert: %v", err)
	}
	// Delete almost everything, one key per transaction to exercise
	// borrow/merge paths and root shrink heavily.
	for i := int64(0); i < n-1; i++ {
		if err := db.Update(func(tx *Tx) error { return tx.Delete(i) }); err != nil {
			t.Fatalf("Delete(%d): %v", i, err)
		}
		if err := db.Validate(db.Root()); err != nil {
			t.Fatalf("Validate after deleting %d: %v", i, err)
		}
	}
	if _, err := db.Get(0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(0) err = %v, want ErrNotFound", err)
	}
	mustGet(t, db, n-1, fmt.Sprintf("v%d", n-1))
	// The tree must have shrunk back to a single leaf root.
	root, err := db.p.readNode(db.Root())
	if err != nil {
		t.Fatalf("readNode: %v", err)
	}
	if !root.leaf {
		t.Fatalf("root did not shrink to a leaf")
	}
	// Delete the last key: tree stays valid and empty.
	if err := db.Update(func(tx *Tx) error { return tx.Delete(n - 1) }); err != nil {
		t.Fatalf("Delete last: %v", err)
	}
	if err := db.Validate(db.Root()); err != nil {
		t.Fatalf("Validate empty: %v", err)
	}
	// Deleting a missing key is a no-op.
	if err := db.Update(func(tx *Tx) error { return tx.Delete(12345) }); err != nil {
		t.Fatalf("Delete missing: %v", err)
	}
}

func TestFailedTxnPublishesNoRoot(t *testing.T) {
	db := openTemp(t, 4)
	if err := db.Update(func(tx *Tx) error { return tx.Put(1, val("one")) }); err != nil {
		t.Fatalf("Update: %v", err)
	}
	rootBefore, seqBefore := db.Root(), db.Seq()

	// Case 1: the transaction function fails.
	wantErr := errors.New("boom")
	err := db.Update(func(tx *Tx) error {
		if err := tx.Put(2, val("two")); err != nil {
			t.Fatal(err)
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Update err = %v, want %v", err, wantErr)
	}
	if db.Root() != rootBefore || db.Seq() != seqBefore {
		t.Fatalf("root/seq changed after failed txn")
	}
	if _, err := db.Get(2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(2) err = %v, want ErrNotFound", err)
	}

	// Case 2: a page write fails during commit.
	db.mu.Lock()
	db.p.failWrite = true
	db.mu.Unlock()
	err = db.Update(func(tx *Tx) error { return tx.Put(3, val("three")) })
	if err == nil {
		t.Fatalf("Update with injected write failure succeeded")
	}
	db.mu.Lock()
	db.p.failWrite = false
	db.mu.Unlock()
	if db.Root() != rootBefore || db.Seq() != seqBefore {
		t.Fatalf("root/seq changed after failed commit")
	}
	mustGet(t, db, 1, "one")
	if _, err := db.Get(3); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(3) err = %v, want ErrNotFound", err)
	}
	// The DB keeps working afterwards.
	if err := db.Update(func(tx *Tx) error { return tx.Put(3, val("three")) }); err != nil {
		t.Fatalf("Update after failure: %v", err)
	}
	mustGet(t, db, 3, "three")
	if err := db.Validate(db.Root()); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValueTooLarge(t *testing.T) {
	db := openTemp(t, 4)
	err := db.Update(func(tx *Tx) error { return tx.Put(1, make([]byte, MaxValueSize+1)) })
	if !errors.Is(err, ErrValueTooLarge) {
		t.Fatalf("err = %v, want ErrValueTooLarge", err)
	}
}

func TestReopenKeepsData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reopen.db")
	db, err := Open(path, 4)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.Update(func(tx *Tx) error {
		for i := int64(0); i < 100; i++ {
			if err := tx.Put(i, val(fmt.Sprintf("v%d", i))); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	db2, err := Open(path, 0) // capacity from meta
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db2.Close()
	for i := int64(0); i < 100; i++ {
		mustGet(t, db2, i, fmt.Sprintf("v%d", i))
	}
	if err := db2.CheckIntegrity(); err != nil {
		t.Fatalf("CheckIntegrity: %v", err)
	}
	if err := db2.Validate(db2.Root()); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}
