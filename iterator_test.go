package bptree

import (
	"errors"
	"fmt"
	"testing"
)

func fill(t *testing.T, db *DB, n int64, prefix string) {
	t.Helper()
	if err := db.Update(func(tx *Tx) error {
		for i := int64(0); i < n; i++ {
			if err := tx.Put(i, val(fmt.Sprintf("%s%d", prefix, i))); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("fill: %v", err)
	}
}

func collect(t *testing.T, it *Iterator) ([]int64, []string) {
	t.Helper()
	var keys []int64
	var vals []string
	for it.Next() {
		keys = append(keys, it.Key())
		vals = append(vals, string(it.Value()))
	}
	if err := it.Err(); err != nil {
		t.Fatalf("iterator error: %v", err)
	}
	return keys, vals
}

func TestRangeBoundsAndEmpty(t *testing.T) {
	db := openTemp(t, 4)
	fill(t, db, 40, "v")
	snap := db.Snapshot()
	defer snap.Close()

	keys, _ := collect(t, snap.Scan(10, 20))
	if len(keys) != 10 || keys[0] != 10 || keys[9] != 19 {
		t.Fatalf("[10,20) returned %v", keys)
	}
	// Empty ranges return nothing.
	for _, r := range [][2]int64{{5, 5}, {20, 10}, {100, 100}} {
		keys, _ = collect(t, snap.Scan(r[0], r[1]))
		if len(keys) != 0 {
			t.Fatalf("range %v returned %v", r, keys)
		}
	}
	// Range beyond the data.
	keys, _ = collect(t, snap.Scan(35, 1000))
	if len(keys) != 5 || keys[0] != 35 {
		t.Fatalf("[35,1000) returned %v", keys)
	}
	keys, _ = collect(t, snap.Scan(-100, 2))
	if len(keys) != 2 || keys[0] != 0 {
		t.Fatalf("[-100,2) returned %v", keys)
	}
}

// TestSnapshotIsolationDuringSplits is the core isolation test: an old
// snapshot keeps scanning its own version while concurrent commits split
// and merge the very leaves being scanned. The iterator must never follow
// a newer version's link into the new tree.
func TestSnapshotIsolationDuringSplits(t *testing.T) {
	db := openTemp(t, 4)
	const n = 60
	fill(t, db, n, "old")

	snap := db.Snapshot()
	defer snap.Close()

	// Hammer the tree: overwrite every value, insert new keys that split
	// leaves, and delete keys to force merges.
	for round := 0; round < 5; round++ {
		if err := db.Update(func(tx *Tx) error {
			for i := int64(0); i < n; i++ {
				if err := tx.Put(i, val(fmt.Sprintf("new%d-%d", round, i))); err != nil {
					return err
				}
			}
			for i := int64(0); i < 30; i++ {
				if err := tx.Put(1000+int64(round)*30+i, val("extra")); err != nil {
					return err
				}
			}
			for i := int64(0); i < 10; i++ {
				if err := tx.Delete(int64(round*10) + i); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatalf("Update round %d: %v", round, err)
		}
		if err := db.Validate(db.Root()); err != nil {
			t.Fatalf("Validate round %d: %v", round, err)
		}
	}

	// The snapshot must still see exactly the original 60 keys with the
	// original values: no duplicates, no omissions, no new-version data.
	keys, vals := collect(t, snap.Scan(-1<<62, 1<<62))
	if len(keys) != n {
		t.Fatalf("snapshot saw %d keys, want %d", len(keys), n)
	}
	for i, k := range keys {
		if k != int64(i) {
			t.Fatalf("snapshot key[%d] = %d, want %d", i, k, i)
		}
		want := fmt.Sprintf("old%d", i)
		if vals[i] != want {
			t.Fatalf("snapshot val[%d] = %q, want %q (old value must be stable)", i, vals[i], want)
		}
	}
	// The current version sees the new data.
	v, err := db.Get(5)
	if err != nil {
		t.Fatalf("Get(5): %v", err)
	}
	if string(v) != "new4-5" {
		t.Fatalf("current Get(5) = %q, want new4-5", v)
	}
	if _, err := snap.Get(1000); !errors.Is(err, ErrNotFound) {
		t.Fatalf("snapshot Get(1000) err = %v, want ErrNotFound", err)
	}
}

func TestIteratorAfterSnapshotClose(t *testing.T) {
	db := openTemp(t, 4)
	fill(t, db, 30, "v")
	snap := db.Snapshot()
	it := snap.Scan(0, 100)
	if !it.Next() {
		t.Fatalf("first Next failed: %v", it.Err())
	}
	if err := snap.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if it.Next() {
		t.Fatalf("Next after close succeeded")
	}
	if !errors.Is(it.Err(), ErrSnapshotClosed) {
		t.Fatalf("Err = %v, want ErrSnapshotClosed", it.Err())
	}
	if _, err := snap.Get(1); !errors.Is(err, ErrSnapshotClosed) {
		t.Fatalf("Get err = %v, want ErrSnapshotClosed", err)
	}
	// Double close is fine.
	if err := snap.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// TestIteratorSlowScanInterleaved walks a range slowly while write
// transactions commit between steps.
func TestIteratorSlowScanInterleaved(t *testing.T) {
	db := openTemp(t, 3)
	const n = 80
	fill(t, db, n, "s")
	snap := db.Snapshot()
	defer snap.Close()
	it := snap.Scan(0, n)
	seen := int64(0)
	for it.Next() {
		if it.Key() != seen {
			t.Fatalf("key = %d, want %d", it.Key(), seen)
		}
		if string(it.Value()) != fmt.Sprintf("s%d", seen) {
			t.Fatalf("value for %d changed under snapshot", seen)
		}
		seen++
		// Interleave a write transaction every few keys.
		if seen%5 == 0 {
			if err := db.Update(func(tx *Tx) error {
				if err := tx.Put(seen, val("MUTATED")); err != nil {
					return err
				}
				if err := tx.Put(5000+seen, val("x")); err != nil {
					return err
				}
				return tx.Delete(seen + 1)
			}); err != nil {
				t.Fatalf("interleaved Update: %v", err)
			}
		}
	}
	if err := it.Err(); err != nil {
		t.Fatalf("iterator: %v", err)
	}
	if seen != n {
		t.Fatalf("saw %d keys, want %d", seen, n)
	}
}
