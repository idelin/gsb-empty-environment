package bptree

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

func mustTree(t *testing.T, capacity int) *Tree {
	tr, err := New(Options{Capacity: capacity, MaxValueSize: 64})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return tr
}

func putBatch(t *testing.T, tr *Tree, keys ...int64) {
	tx := tr.Begin()
	for _, k := range keys {
		if err := tx.Put(k, []byte(fmt.Sprintf("v%d", k))); err != nil {
			t.Fatalf("Put(%d): %v", k, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
}

func treeDepth(t *testing.T, tr *Tree) int {
	id := tr.Root()
	depth := 0
	for {
		buf, err := tr.Store().Read(id)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		n, err := decodeNode(buf)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if n.leaf {
			return depth
		}
		id = n.children[0]
		depth++
	}
}

func TestPutGetAndDuplicateOverwrite(t *testing.T) {
	tr := mustTree(t, 4)
	putBatch(t, tr, 1, 2, 3, 4, 5)

	v, err := tr.Get(3)
	if err != nil || string(v) != "v3" {
		t.Fatalf("Get(3) = %q, %v", v, err)
	}
	if _, err := tr.Get(99); err != ErrNotFound {
		t.Fatalf("Get(99) err = %v, want ErrNotFound", err)
	}

	// Duplicate key overwrites the value.
	tx := tr.Begin()
	if err := tx.Put(3, []byte("replaced")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	v, err = tr.Get(3)
	if err != nil || string(v) != "replaced" {
		t.Fatalf("Get(3) after overwrite = %q, %v", v, err)
	}
	if err := tr.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestEmptyRangeReturnsEmpty(t *testing.T) {
	tr := mustTree(t, 4)
	putBatch(t, tr, 1, 2, 3)
	snap := tr.Snapshot()
	defer snap.Close()
	for _, r := range [][2]int64{{5, 5}, {10, 2}, {0, 0}} {
		it := snap.Iterator(r[0], r[1])
		if it.Next() {
			t.Fatalf("range [%d,%d) should be empty, got key %d", r[0], r[1], it.Key())
		}
		if it.Err() != nil {
			t.Fatalf("range [%d,%d) err = %v", r[0], r[1], it.Err())
		}
	}
}

func TestRangeScanMatchesModel(t *testing.T) {
	tr := mustTree(t, 5)
	rnd := rand.New(rand.NewSource(7))
	model := map[int64][]byte{}
	for i := 0; i < 400; i++ {
		tx := tr.Begin()
		for j := 0; j < 5; j++ {
			k := int64(rnd.Intn(500))
			v := []byte(fmt.Sprintf("val-%d-%d", k, rnd.Int()))
			if err := tx.Put(k, v); err != nil {
				t.Fatalf("Put: %v", err)
			}
			model[k] = v
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("Commit: %v", err)
		}
	}
	if err := tr.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	var keys []int64
	for k := range model {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

	snap := tr.Snapshot()
	defer snap.Close()
	it := snap.Iterator(-1, 501)
	got := 0
	for it.Next() {
		if got >= len(keys) {
			t.Fatalf("iterator yielded extra key %d", it.Key())
		}
		if it.Key() != keys[got] {
			t.Fatalf("position %d: got key %d, want %d", got, it.Key(), keys[got])
		}
		if string(it.Value()) != string(model[it.Key()]) {
			t.Fatalf("key %d: got value %q, want %q", it.Key(), it.Value(), model[it.Key()])
		}
		got++
	}
	if it.Err() != nil {
		t.Fatalf("iterator err: %v", it.Err())
	}
	if got != len(keys) {
		t.Fatalf("iterator yielded %d keys, want %d", got, len(keys))
	}

	// Sub-range, left-closed right-open.
	lo, hi := keys[len(keys)/4], keys[3*len(keys)/4]
	it = snap.Iterator(lo, hi)
	for it.Next() {
		if it.Key() < lo || it.Key() >= hi {
			t.Fatalf("key %d outside [%d,%d)", it.Key(), lo, hi)
		}
	}
	if it.Err() != nil {
		t.Fatalf("sub-range err: %v", it.Err())
	}
}

func TestDeleteBorrowMergeRootShrink(t *testing.T) {
	tr := mustTree(t, 4)
	const n = 300
	tx := tr.Begin()
	for i := 0; i < n; i++ {
		if err := tx.Put(int64(i), []byte(fmt.Sprintf("v%d", i))); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := tr.Validate(); err != nil {
		t.Fatalf("Validate after bulk insert: %v", err)
	}
	depthBefore := treeDepth(t, tr)
	if depthBefore < 2 {
		t.Fatalf("expected a tree of depth >= 2, got %d", depthBefore)
	}

	// Delete everything in key order; forces borrows, merges and root shrinks.
	for i := 0; i < n; i++ {
		tx := tr.Begin()
		if err := tx.Delete(int64(i)); err != nil {
			t.Fatalf("Delete(%d): %v", i, err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("Commit: %v", err)
		}
		if err := tr.Validate(); err != nil {
			t.Fatalf("Validate after Delete(%d): %v", i, err)
		}
	}
	if d := treeDepth(t, tr); d != 0 {
		t.Fatalf("root did not shrink to a leaf, depth %d", d)
	}
	if _, err := tr.Get(0); err != ErrNotFound {
		t.Fatalf("Get on emptied tree err = %v, want ErrNotFound", err)
	}

	// Random-order deletes against a model.
	tr2 := mustTree(t, 3)
	rnd := rand.New(rand.NewSource(11))
	model := map[int64]bool{}
	tx = tr2.Begin()
	for i := 0; i < 200; i++ {
		k := int64(rnd.Intn(400))
		if err := tx.Put(k, []byte("x")); err != nil {
			t.Fatalf("Put: %v", err)
		}
		model[k] = true
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	for k := range model {
		tx := tr2.Begin()
		if err := tx.Delete(k); err != nil {
			t.Fatalf("Delete(%d): %v", k, err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("Commit: %v", err)
		}
		delete(model, k)
		if err := tr2.Validate(); err != nil {
			t.Fatalf("Validate after Delete(%d): %v", k, err)
		}
	}
	if len(model) != 0 {
		t.Fatalf("model not drained")
	}
}

func TestDeleteMissingKey(t *testing.T) {
	tr := mustTree(t, 4)
	putBatch(t, tr, 1, 2, 3)
	tx := tr.Begin()
	if err := tx.Delete(42); err != ErrNotFound {
		t.Fatalf("Delete(42) err = %v, want ErrNotFound", err)
	}
	tx.Rollback()
}
