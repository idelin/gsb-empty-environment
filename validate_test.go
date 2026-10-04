package bptree

import (
	"strings"
	"testing"
)

// The validator accepts trees built by random workloads.
func TestValidatorAcceptsValidTree(t *testing.T) {
	tr := mustTree(t, 3)
	fill(t, tr, 0, 120)
	if err := tr.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	tx := tr.Begin()
	for k := int64(0); k < 120; k += 3 {
		if err := tx.Delete(k); err != nil {
			t.Fatalf("Delete: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := tr.Validate(); err != nil {
		t.Fatalf("Validate after deletes: %v", err)
	}
}

// writeNode is a test helper that hand-builds a page, bypassing the tree.
func writeNode(t *testing.T, tr *Tree, n *node) PageID {
	if n.id == 0 {
		n.id = tr.Store().Alloc()
	}
	if err := tr.Store().Write(n.id, n.encode(tr.Store().PageSize())); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return n.id
}

// A wrong separator key must be caught.
func TestValidatorDetectsBadSeparator(t *testing.T) {
	tr := mustTree(t, 4)
	left := writeNode(t, tr, &node{leaf: true, keys: []int64{1, 2}, vals: [][]byte{[]byte("a"), []byte("b")}})
	right := writeNode(t, tr, &node{leaf: true, keys: []int64{5, 6}, vals: [][]byte{[]byte("c"), []byte("d")}})
	// Separator says 4 but the right subtree's minimum is 5.
	root := writeNode(t, tr, &node{keys: []int64{4}, children: []PageID{left, right}})

	err := Validate(tr.Store(), root, 4)
	if err == nil || !strings.Contains(err.Error(), "separator") {
		t.Fatalf("Validate err = %v, want separator mismatch", err)
	}
}

// An out-of-order key sequence must be caught.
func TestValidatorDetectsUnsortedKeys(t *testing.T) {
	tr := mustTree(t, 4)
	root := writeNode(t, tr, &node{leaf: true, keys: []int64{3, 1}, vals: [][]byte{[]byte("a"), []byte("b")}})
	err := Validate(tr.Store(), root, 4)
	if err == nil || !strings.Contains(err.Error(), "increasing") {
		t.Fatalf("Validate err = %v, want unsorted keys error", err)
	}
}

// Leaves at different depths must be caught.
func TestValidatorDetectsUnevenLeaves(t *testing.T) {
	tr := mustTree(t, 4)
	leaf := func(k0, k1 int64) PageID {
		return writeNode(t, tr, &node{leaf: true, keys: []int64{k0, k1},
			vals: [][]byte{[]byte("a"), []byte("b")}})
	}
	mid := writeNode(t, tr, &node{keys: []int64{2, 4}, children: []PageID{
		leaf(0, 1), leaf(2, 3), leaf(4, 5),
	}})
	shallowLeaf := leaf(9, 10)
	root := writeNode(t, tr, &node{keys: []int64{9}, children: []PageID{mid, shallowLeaf}})

	err := Validate(tr.Store(), root, 4)
	if err == nil || !strings.Contains(err.Error(), "depth") {
		t.Fatalf("Validate err = %v, want leaf depth error", err)
	}
}

// Over-capacity and underfull nodes must be caught.
func TestValidatorDetectsCapacityViolations(t *testing.T) {
	tr := mustTree(t, 4)
	fat := writeNode(t, tr, &node{leaf: true,
		keys: []int64{1, 2, 3, 4, 5},
		vals: [][]byte{[]byte("a"), []byte("b"), []byte("c"), []byte("d"), []byte("e")}})
	if err := Validate(tr.Store(), fat, 4); err == nil || !strings.Contains(err.Error(), "capacity") {
		t.Fatalf("Validate err = %v, want capacity error", err)
	}

	thin := writeNode(t, tr, &node{leaf: true, keys: []int64{1}, vals: [][]byte{[]byte("a")}})
	other := writeNode(t, tr, &node{leaf: true, keys: []int64{5, 6}, vals: [][]byte{[]byte("b"), []byte("c")}})
	root := writeNode(t, tr, &node{keys: []int64{5}, children: []PageID{thin, other}})
	if err := Validate(tr.Store(), root, 4); err == nil || !strings.Contains(err.Error(), "underfull") {
		t.Fatalf("Validate err = %v, want underfull error", err)
	}
}

// Dangling and duplicate page references must be caught.
func TestValidatorDetectsBadPageReferences(t *testing.T) {
	tr := mustTree(t, 4)
	leaf := writeNode(t, tr, &node{leaf: true, keys: []int64{1, 2}, vals: [][]byte{[]byte("a"), []byte("b")}})

	// Dangling reference to a page that was never allocated.
	root := writeNode(t, tr, &node{keys: []int64{3}, children: []PageID{leaf, 9999}})
	if err := Validate(tr.Store(), root, 4); err == nil {
		t.Fatalf("Validate should reject dangling child reference")
	}

	// Same page referenced twice.
	leafB := writeNode(t, tr, &node{leaf: true, keys: []int64{3, 4}, vals: [][]byte{[]byte("c"), []byte("d")}})
	root2 := writeNode(t, tr, &node{keys: []int64{3, 5}, children: []PageID{leaf, leafB, leaf}})
	if err := Validate(tr.Store(), root2, 4); err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("Validate err = %v, want duplicate reference error", err)
	}
}

// The validator works on historical roots, not just the current one.
func TestValidatorAuditsHistoricalRoot(t *testing.T) {
	tr := mustTree(t, 4)
	fill(t, tr, 0, 50)
	oldRoot := tr.Root()
	fill(t, tr, 50, 120)
	if tr.Root() == oldRoot {
		t.Fatalf("expected root to change")
	}
	if err := Validate(tr.Store(), oldRoot, 4); err != nil {
		t.Fatalf("Validate historical root: %v", err)
	}
	if err := Validate(tr.Store(), tr.Root(), 4); err != nil {
		t.Fatalf("Validate current root: %v", err)
	}
}
