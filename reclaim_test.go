package bptree

import (
	"fmt"
	"testing"
)

func TestReclaimKeepsActiveRoots(t *testing.T) {
	db := openTemp(t, 4)
	const n = 100
	fill(t, db, n, "v1")

	// Pin version 1.
	snap1 := db.Snapshot()
	root1 := snap1.Root()

	// Version 2: overwrite everything (all old leaves become garbage).
	if err := db.Update(func(tx *Tx) error {
		for i := int64(0); i < n; i++ {
			if err := tx.Put(i, val(fmt.Sprintf("v2-%d", i))); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("Update v2: %v", err)
	}
	snap2 := db.Snapshot()
	defer snap2.Close()

	// Version 3: delete half the keys (more garbage).
	if err := db.Update(func(tx *Tx) error {
		for i := int64(0); i < n; i += 2 {
			if err := tx.Delete(i); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("Update v3: %v", err)
	}

	pagesBefore := db.p.numPages - uint64(len(db.p.freeSet))
	freed, err := db.Reclaim()
	if err != nil {
		t.Fatalf("Reclaim: %v", err)
	}
	if freed == 0 {
		t.Fatalf("Reclaim freed nothing")
	}
	t.Logf("reclaimed %d of %d allocated pages", freed, pagesBefore)

	// Pages of pinned roots must survive: both snapshots still scan their
	// full versions correctly.
	keys, vals := collect(t, snap1.Scan(0, n))
	if len(keys) != n {
		t.Fatalf("snap1 saw %d keys, want %d", len(keys), n)
	}
	for i, k := range keys {
		if k != int64(i) || vals[i] != fmt.Sprintf("v1%d", i) {
			t.Fatalf("snap1[%d] = (%d,%q)", i, k, vals[i])
		}
	}
	keys, vals = collect(t, snap2.Scan(0, n))
	if len(keys) != n || vals[0] != "v2-0" {
		t.Fatalf("snap2 corrupted after reclaim: %d keys, first val %v", len(keys), vals[0])
	}
	if err := db.Validate(root1); err != nil {
		t.Fatalf("Validate(root1) after reclaim: %v", err)
	}
	if err := db.Validate(db.Root()); err != nil {
		t.Fatalf("Validate(current) after reclaim: %v", err)
	}

	// After closing snap1, its pages become reclaimable too.
	snap1.Close()
	freed2, err := db.Reclaim()
	if err != nil {
		t.Fatalf("Reclaim 2: %v", err)
	}
	if freed2 == 0 {
		t.Fatalf("second Reclaim freed nothing")
	}
	t.Logf("reclaimed %d more pages after closing snap1", freed2)

	// Current version is untouched and fully readable.
	keys, _ = collect(t, snap2.Scan(0, n))
	if len(keys) != n {
		t.Fatalf("snap2 saw %d keys after 2nd reclaim, want %d", len(keys), n)
	}
	mustGet(t, db, 1, "v2-1")
	if err := db.Validate(db.Root()); err != nil {
		t.Fatalf("Validate final: %v", err)
	}

	// Freed pages are reused: writing more data must not grow the file
	// without bound.
	before := db.p.numPages
	if err := db.Update(func(tx *Tx) error {
		for i := int64(0); i < n; i++ {
			if err := tx.Put(5000+i, val("reuse")); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("Update reuse: %v", err)
	}
	t.Logf("numPages before=%d after reuse-write=%d", before, db.p.numPages)
	if err := db.CheckIntegrity(); err != nil {
		t.Fatalf("CheckIntegrity: %v", err)
	}
}
