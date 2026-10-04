package bptree

import (
	"fmt"
	"testing"
)

func fill(t *testing.T, tr *Tree, lo, hi int64) {
	tx := tr.Begin()
	for k := lo; k < hi; k++ {
		if err := tx.Put(k, []byte(fmt.Sprintf("v%03d", k))); err != nil {
			t.Fatalf("Put(%d): %v", k, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
}

func drain(t *testing.T, it *Iterator) ([]int64, [][]byte) {
	var keys []int64
	var vals [][]byte
	for it.Next() {
		keys = append(keys, it.Key())
		vals = append(vals, it.Value())
	}
	if err := it.Err(); err != nil {
		t.Fatalf("iterator err: %v", err)
	}
	return keys, vals
}

// A snapshot must keep serving the exact version it pinned while heavy
// writes (leaf splits, merges, overwrites) commit on the tree.
func TestSnapshotIsolationUnderHeavyWrites(t *testing.T) {
	tr := mustTree(t, 4)
	fill(t, tr, 0, 100)

	snap := tr.Snapshot()
	defer snap.Close()

	// Overwrite half, delete a quarter, insert new keys: splits + merges.
	tx := tr.Begin()
	for k := int64(0); k < 100; k += 2 {
		if err := tx.Put(k, []byte(fmt.Sprintf("NEW%03d", k))); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	for k := int64(1); k < 100; k += 4 {
		if err := tx.Delete(k); err != nil {
			t.Fatalf("Delete: %v", err)
		}
	}
	for k := int64(200); k < 260; k++ {
		if err := tx.Put(k, []byte("extra")); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// The snapshot still sees all 100 original keys with original values.
	keys, vals := drain(t, snap.Iterator(-1, 1000))
	if len(keys) != 100 {
		t.Fatalf("snapshot scan yielded %d keys, want 100", len(keys))
	}
	for i, k := range keys {
		if k != int64(i) {
			t.Fatalf("snapshot key %d = %d, want %d", i, k, i)
		}
		if string(vals[i]) != fmt.Sprintf("v%03d", k) {
			t.Fatalf("snapshot value for %d = %q, want old value", k, vals[i])
		}
	}
	v, err := snap.Get(0)
	if err != nil || string(v) != "v000" {
		t.Fatalf("snapshot Get(0) = %q, %v; want old value", v, err)
	}
	if _, err := snap.Get(1); err != nil {
		t.Fatalf("snapshot Get(1) = %v, want old value present", err)
	}

	// The live tree sees the new version.
	if v, _ := tr.Get(0); string(v) != "NEW000" {
		t.Fatalf("live Get(0) = %q, want NEW000", v)
	}
	if _, err := tr.Get(1); err != ErrNotFound {
		t.Fatalf("live Get(1) err = %v, want ErrNotFound", err)
	}
}

// An iterator crossing leaf boundaries must stay in its pinned version even
// while commits split and merge the very leaves it is about to visit.
func TestIteratorNeverJumpsVersions(t *testing.T) {
	tr := mustTree(t, 4)
	fill(t, tr, 0, 64)

	snap := tr.Snapshot()
	defer snap.Close()
	it := snap.Iterator(-100, 100)

	// Read a few entries, then churn the tree between iterator steps.
	for k := int64(0); k < 64; k++ {
		tx := tr.Begin()
		// Split leaves ahead of the iterator and delete already-visited keys.
		for j := int64(0); j < 8; j++ {
			if err := tx.Put(1000+k*8+j, []byte("churn")); err != nil {
				t.Fatalf("Put: %v", err)
			}
		}
		if err := tx.Delete(k); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("Commit: %v", err)
		}

		if !it.Next() {
			t.Fatalf("iterator exhausted at step %d, err=%v", k, it.Err())
		}
		if it.Key() != k {
			t.Fatalf("iterator key = %d, want %d (version jump?)", it.Key(), k)
		}
		if string(it.Value()) != fmt.Sprintf("v%03d", k) {
			t.Fatalf("iterator value for %d = %q, want pinned old value", k, it.Value())
		}
	}
	if it.Next() {
		t.Fatalf("iterator should be exhausted, got key %d", it.Key())
	}
	if it.Err() != nil {
		t.Fatalf("iterator err: %v", it.Err())
	}
}

// After the snapshot is closed, its iterators must fail with
// ErrSnapshotClosed instead of reading reclaimed pages.
func TestIteratorFailsAfterSnapshotClose(t *testing.T) {
	tr := mustTree(t, 4)
	fill(t, tr, 0, 50)

	snap := tr.Snapshot()
	it := snap.Iterator(0, 50)
	if !it.Next() {
		t.Fatalf("iterator should yield first key")
	}
	snap.Close()

	if it.Next() {
		t.Fatalf("iterator advanced after snapshot close")
	}
	if it.Err() != ErrSnapshotClosed {
		t.Fatalf("iterator err = %v, want ErrSnapshotClosed", it.Err())
	}
	if _, err := snap.Get(1); err != ErrSnapshotClosed {
		t.Fatalf("snapshot Get err = %v, want ErrSnapshotClosed", err)
	}
	it2 := snap.Iterator(0, 50)
	if it2.Next() || it2.Err() != ErrSnapshotClosed {
		t.Fatalf("iterator on closed snapshot: next=%v err=%v", it2.Next(), it2.Err())
	}

	// Reclaim and reuse pages; the closed iterator must still refuse.
	if _, err := tr.Collect(); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	fill(t, tr, 500, 560)
	if it.Next() {
		t.Fatalf("closed-snapshot iterator advanced after GC")
	}
	if it.Err() != ErrSnapshotClosed {
		t.Fatalf("iterator err = %v, want ErrSnapshotClosed", it.Err())
	}
}

// Empty ranges produce empty iterators.
func TestEmptyRangeIterator(t *testing.T) {
	tr := mustTree(t, 4)
	fill(t, tr, 0, 10)
	snap := tr.Snapshot()
	defer snap.Close()
	for _, r := range [][2]int64{{5, 5}, {9, 3}, {0, 0}} {
		it := snap.Iterator(r[0], r[1])
		if it.Next() {
			t.Fatalf("range [%d,%d) should be empty", r[0], r[1])
		}
		if it.Err() != nil {
			t.Fatalf("range [%d,%d) err = %v", r[0], r[1], it.Err())
		}
	}
}
