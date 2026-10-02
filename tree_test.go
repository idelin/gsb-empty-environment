package bplustree

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// checkInvariants verifies B+ tree structural rules: sorted keys, routing
// keys equal to subtree minimums, fill-factor bounds, and leaf alignment.
func checkInvariants(t *testing.T, tr *Tree) {
	t.Helper()
	depth := -1
	var walk func(n *node, d int, lo, hi *int)
	walk = func(n *node, d int, lo, hi *int) {
		if !sort.IntsAreSorted(n.keys) {
			t.Errorf("unsorted keys %v", n.keys)
		}
		if n != tr.root && len(n.keys) < tr.min {
			t.Errorf("node %v below min fill %d", n.keys, tr.min)
		}
		if len(n.keys) > tr.order {
			t.Errorf("node %v above order %d", n.keys, tr.order)
		}
		if lo != nil && len(n.keys) > 0 && n.keys[0] < *lo {
			t.Errorf("node %v below lower bound %d", n.keys, *lo)
		}
		if hi != nil && len(n.keys) > 0 && n.keys[len(n.keys)-1] >= *hi {
			t.Errorf("node %v above upper bound %d", n.keys, *hi)
		}
		if n.leaf {
			if depth == -1 {
				depth = d
			} else if depth != d {
				t.Errorf("leaves at different depths %d and %d", depth, d)
			}
			return
		}
		if len(n.children) != len(n.keys)+1 {
			t.Errorf("internal %v has %d children", n.keys, len(n.children))
		}
		for i, c := range n.children {
			if c.parent != n {
				t.Errorf("broken parent pointer of %v", c.keys)
			}
			var lo2, hi2 *int
			if i > 0 {
				v := n.keys[i-1]
				lo2 = &v
			}
			if i < len(n.keys) {
				v := n.keys[i]
				hi2 = &v
			}
			walk(c, d+1, lo2, hi2)
			// Routing key must equal the minimum key of the right subtree.
			if i > 0 {
				m := c
				for !m.leaf {
					m = m.children[0]
				}
				if n.keys[i-1] != m.keys[0] {
					t.Errorf("separator %d != subtree min %d", n.keys[i-1], m.keys[0])
				}
			}
		}
	}
	walk(tr.root, 0, nil, nil)
	// Leaf chain must contain exactly the sorted key set.
	keys := tr.Keys()
	if !sort.IntsAreSorted(keys) {
		t.Errorf("leaf chain not sorted: %v", keys)
	}
	if len(keys) != tr.Size() {
		t.Errorf("leaf chain has %d keys, size is %d", len(keys), tr.Size())
	}
}

// TestDeleteBorrowFromLeftSibling is the acceptance scenario: deleting a
// key underflows a leaf, the algorithm steals the maximum key from the
// left sibling, the tree height stays the same, and the parent separator
// key is corrected precisely.
func TestDeleteBorrowFromLeftSibling(t *testing.T) {
	tr := New(4) // max 4 keys/leaf, min fill = 2
	for k := 1; k <= 14; k++ {
		tr.Insert(k)
	}
	checkInvariants(t, tr)

	heightBefore := tr.Height()
	if heightBefore != 2 {
		t.Fatalf("setup: height = %d, want 2", heightBefore)
	}
	if got, want := tr.root.keys, []int{4, 7, 10, 13}; !reflect.DeepEqual(got, want) {
		t.Fatalf("setup: root keys = %v, want %v", got, want)
	}
	// Leaves: [1 2 3] [4 5 6] [7 8 9] [10 11 12] [13 14]
	// Deleting 13 leaves [14] (1 key < min 2): underflow.
	// Left sibling [10 11 12] has a surplus, so its max key 12 is borrowed.

	if !tr.Delete(13) {
		t.Fatal("Delete(13) = false, want true")
	}

	if got := tr.Height(); got != heightBefore {
		t.Errorf("height changed: %d -> %d, want unchanged", heightBefore, got)
	}
	// Parent separator between the two leaves: 13 -> 12 (the borrowed key).
	if got, want := tr.root.keys, []int{4, 7, 10, 12}; !reflect.DeepEqual(got, want) {
		t.Errorf("root separator keys = %v, want %v", got, want)
	}
	// Borrowed key moved: left sibling lost its max, underflowed leaf gained it.
	if tr.Get(13) {
		t.Error("13 still present after delete")
	}
	for _, k := range []int{10, 11, 12, 14} {
		if !tr.Get(k) {
			t.Errorf("key %d missing after rebalance", k)
		}
	}
	leaf := tr.findLeaf(14)
	if got, want := leaf.keys, []int{12, 14}; !reflect.DeepEqual(got, want) {
		t.Errorf("rebalanced leaf = %v, want %v", got, want)
	}
	if got, want := leaf.parent.children[3].keys, []int{10, 11}; !reflect.DeepEqual(got, want) {
		t.Errorf("left sibling = %v, want %v", got, want)
	}
	checkInvariants(t, tr)
}

// TestDeleteBorrowFromRightSibling covers the mirror case: the left
// sibling is at the minimum, so the minimum key of the right sibling is
// borrowed and the separator updated.
func TestDeleteBorrowFromRightSibling(t *testing.T) {
	tr := New(4)
	for k := 1; k <= 16; k++ {
		tr.Insert(k)
	}
	// Leaves: [1 2 3] [4 5 6] [7 8 9] [10 11 12] [13 14 15 16]
	// Shrink the first leaf to the minimum so it cannot lend.
	if !tr.Delete(1) {
		t.Fatal("Delete(1) = false")
	}
	// Leaves: [2 3] [4 5 6] ... ; deleting 4 leaves [5 6] (ok), deleting 5
	// leaves [6]: underflow, left sibling [2 3] is at min, so borrow 7
	// from right sibling [7 8 9].
	if !tr.Delete(4) || !tr.Delete(5) {
		t.Fatal("setup deletes failed")
	}
	leaf := tr.findLeaf(6)
	if got, want := leaf.keys, []int{6, 7}; !reflect.DeepEqual(got, want) {
		t.Errorf("rebalanced leaf = %v, want %v", got, want)
	}
	if got, want := tr.root.keys, []int{6, 8, 10, 13}; !reflect.DeepEqual(got, want) {
		t.Errorf("root separator keys = %v, want %v", got, want)
	}
	if got := tr.Height(); got != 2 {
		t.Errorf("height = %d, want 2", got)
	}
	checkInvariants(t, tr)
}

// TestDeleteMergeFallback ensures that when no sibling can lend, leaves
// merge and the tree still rebalances correctly (height may shrink).
func TestDeleteMergeFallback(t *testing.T) {
	tr := New(4)
	for k := 1; k <= 16; k++ {
		tr.Insert(k)
	}
	// Drain keys so leaves must merge; eventually the root collapses.
	for _, k := range []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12} {
		if !tr.Delete(k) {
			t.Fatalf("Delete(%d) = false", k)
		}
		checkInvariants(t, tr)
	}
	// Both remaining leaves sit at the minimum; one more delete forces a
	// merge and collapses the root.
	if got := tr.Height(); got != 2 {
		t.Errorf("height = %d, want 2 before final merge", got)
	}
	if !tr.Delete(13) {
		t.Fatal("Delete(13) = false")
	}
	checkInvariants(t, tr)
	if got := tr.Height(); got != 1 {
		t.Errorf("height = %d, want 1 after merges", got)
	}
	if got, want := tr.Keys(), []int{14, 15, 16}; !reflect.DeepEqual(got, want) {
		t.Errorf("remaining keys = %v, want %v", got, want)
	}
}

// TestDeleteMissingKey verifies deleting absent keys is a no-op.
func TestDeleteMissingKey(t *testing.T) {
	tr := New(4)
	for k := 1; k <= 10; k++ {
		tr.Insert(k)
	}
	if tr.Delete(99) {
		t.Error("Delete(99) = true, want false")
	}
	if tr.Size() != 10 {
		t.Errorf("size = %d, want 10", tr.Size())
	}
	checkInvariants(t, tr)
}

// TestRandomized cross-checks the tree against a map under random
// insert/delete workloads at several orders.
func TestRandomized(t *testing.T) {
	for _, order := range []int{3, 4, 5, 8} {
		t.Run(fmt.Sprintf("order=%d", order), func(t *testing.T) {
			rng := rand.New(rand.NewSource(int64(order)))
			tr := New(order)
			ref := map[int]bool{}
			for step := 0; step < 5000; step++ {
				k := rng.Intn(200)
				if rng.Intn(2) == 0 {
					tr.Insert(k)
					ref[k] = true
				} else {
					if got := tr.Delete(k); got != ref[k] {
						t.Fatalf("step %d: Delete(%d) = %v, want %v", step, k, got, ref[k])
					}
					delete(ref, k)
				}
				for key := range ref {
					if !tr.Get(key) {
						t.Fatalf("step %d: Get(%d) = false", step, key)
					}
				}
				checkInvariants(t, tr)
			}
		})
	}
}
