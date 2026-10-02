package bplustree

import (
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// check validates the structural invariants of the whole tree and
// returns the keys found by scanning the leaf level.
func check(t *testing.T, tree *Tree) []int {
	t.Helper()
	var walk func(n *node, depth int, isRoot bool) (min, max int, leafDepth int)
	leafDepths := map[int]bool{}
	walk = func(n *node, depth int, isRoot bool) (min, max int, leafDepth int) {
		if !sort.IntsAreSorted(n.keys) {
			t.Fatalf("keys not sorted in node %v", n.keys)
		}
		if !isRoot && len(n.keys) < tree.minKeys() {
			t.Fatalf("node %v underflows min=%d", n.keys, tree.minKeys())
		}
		if len(n.keys) > tree.order {
			t.Fatalf("node %v overflows order=%d", n.keys, tree.order)
		}
		if n.leaf {
			leafDepths[depth] = true
			return n.keys[0], n.keys[len(n.keys)-1], depth
		}
		if len(n.children) != len(n.keys)+1 {
			t.Fatalf("internal node keys=%v children=%d", n.keys, len(n.children))
		}
		lo, hi := 0, 0
		for i, c := range n.children {
			if c.parent != n {
				t.Fatalf("bad parent pointer")
			}
			cmin, cmax, d := walk(c, depth+1, false)
			if i == 0 {
				lo = cmin
			}
			if i > 0 && n.keys[i-1] != cmin {
				t.Fatalf("separator %d != right-child min %d", n.keys[i-1], cmin)
			}
			if i > 0 && cmin <= n.children[i-1].keys[len(n.children[i-1].keys)-1] && n.children[i-1].leaf {
				t.Fatalf("leaf ranges overlap")
			}
			hi = cmax
			leafDepth = d
		}
		return lo, hi, leafDepth
	}
	if tree.root.leaf && len(tree.root.keys) == 0 {
		return nil
	}
	walk(tree.root, 0, true)
	if len(leafDepths) != 1 {
		t.Fatalf("leaves at different depths: %v", leafDepths)
	}
	got := tree.Keys()
	if !sort.IntsAreSorted(got) {
		t.Fatalf("leaf chain not sorted: %v", got)
	}
	if len(got) != tree.Size() {
		t.Fatalf("size=%d but %d keys in leaves", tree.Size(), len(got))
	}
	return got
}

func buildTree(keys []int, order int) *Tree {
	tree := New(order)
	for _, k := range keys {
		tree.Insert(k, k*10)
	}
	return tree
}

// Acceptance scenario: deleting a key underflows its leaf; the leaf
// borrows the maximum key from its left sibling, the parent separator
// is corrected exactly, and the tree height stays the same.
func TestDeleteBorrowFromLeftSibling(t *testing.T) {
	// Insert 1..9, then delete 2: the first leaf merges into
	// [1 3 4], leaving leaves [1 3 4] [5 6] [7 8 9] under root [5 7].
	tree := buildTree([]int{1, 2, 3, 4, 5, 6, 7, 8, 9}, 4)
	if !tree.Delete(2) {
		t.Fatal("setup Delete(2) failed")
	}
	check(t, tree)
	if got := tree.leftmost().next.keys; !reflect.DeepEqual(got, []int{5, 6}) {
		t.Fatalf("fixture broken: second leaf = %v, want [5 6]", got)
	}
	heightBefore := tree.Height()

	// Delete 5: leaf [5 6] drops to [6] and underflows (min is 4/2=2).
	// The left sibling [1 3 4] has a surplus, so its maximum record (4)
	// must be stolen; the right sibling is never touched.
	leaf := tree.findLeaf(6)
	left := tree.leftmost()
	parent := leaf.parent
	if !tree.Delete(5) {
		t.Fatal("Delete(5) reported missing")
	}

	// Tree height unchanged.
	if got := tree.Height(); got != heightBefore {
		t.Fatalf("height changed: %d -> %d", heightBefore, got)
	}
	// The borrowed record is exactly the left sibling's old maximum,
	// now the leaf's new minimum.
	if !reflect.DeepEqual(leaf.keys, []int{4, 6}) {
		t.Fatalf("leaf keys = %v, want [4 6] (borrowed 4 from left sibling)", leaf.keys)
	}
	// Left sibling lost exactly one record (its maximum).
	if !reflect.DeepEqual(left.keys, []int{1, 3}) {
		t.Fatalf("left sibling keys = %v, want [1 3]", left.keys)
	}
	// Parent separator is precisely the leaf's new minimum.
	if parent.keys[0] != 4 {
		t.Fatalf("parent separator = %d, want 4", parent.keys[0])
	}
	// The whole tree is still valid and contains every remaining key.
	want := []int{1, 3, 4, 6, 7, 8, 9}
	if got := check(t, tree); !reflect.DeepEqual(got, want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	for _, k := range want {
		if v, ok := tree.Get(k); !ok || v != k*10 {
			t.Fatalf("Get(%d) = %d,%v", k, v, ok)
		}
	}
}

func TestDeleteBorrowFromRightSibling(t *testing.T) {
	// Leaves: [1 2] [3 4] [5 6 7]; deleting 1 and 2 underflows the first
	// leaf, which must then borrow 3 (the right sibling's minimum).
	tree := buildTree([]int{1, 2, 3, 4, 5, 6, 7}, 4)
	check(t, tree)
	h := tree.Height()
	tree.Delete(1)
	check(t, tree)
	tree.Delete(2)
	got := check(t, tree)
	if tree.Height() != h {
		t.Fatalf("height changed after borrow")
	}
	want := []int{3, 4, 5, 6, 7}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	// First leaf must now hold the borrowed minimum of its right sibling.
	first := tree.leftmost()
	if first.keys[0] != 3 {
		t.Fatalf("first leaf = %v, want it to start with borrowed key 3", first.keys)
	}
}

func TestDeleteMergeWhenNoSurplus(t *testing.T) {
	// Every leaf at minimum fill: deletion must merge and shrink the root.
	tree := buildTree([]int{1, 2, 3, 4, 5, 6, 7, 8}, 4)
	check(t, tree)
	for _, k := range []int{1, 3, 5, 7, 2, 4, 6} {
		if !tree.Delete(k) {
			t.Fatalf("Delete(%d) failed", k)
		}
		check(t, tree)
	}
	if got := tree.Keys(); !reflect.DeepEqual(got, []int{8}) {
		t.Fatalf("keys = %v, want [8]", got)
	}
	if tree.Height() != 1 {
		t.Fatalf("height = %d, want 1 after merges collapse the root", tree.Height())
	}
}

func TestDeleteMissingKey(t *testing.T) {
	tree := buildTree([]int{1, 2, 3}, 4)
	if tree.Delete(99) {
		t.Fatal("Delete(99) should report false")
	}
	if tree.Size() != 3 {
		t.Fatalf("size = %d, want 3", tree.Size())
	}
}

func TestInsertDeleteRandomized(t *testing.T) {
	rand.Seed(42)
	tree := New(5)
	ref := map[int]bool{}
	var want []int
	for i := 0; i < 2000; i++ {
		k := rand.Intn(500)
		if !ref[k] {
			tree.Insert(k, k)
			ref[k] = true
		}
	}
	check(t, tree)
	for k := range ref {
		if rand.Intn(2) == 0 {
			if !tree.Delete(k) {
				t.Fatalf("Delete(%d) failed", k)
			}
			delete(ref, k)
		}
	}
	for k := range ref {
		want = append(want, k)
	}
	sort.Ints(want)
	if got := check(t, tree); !reflect.DeepEqual(got, want) {
		t.Fatalf("final keys mismatch: got %d want %d", len(got), len(want))
	}
	// Delete everything: the tree must drain cleanly back to an empty root.
	for k := range ref {
		tree.Delete(k)
	}
	check(t, tree)
	if tree.Size() != 0 || tree.Height() != 1 {
		t.Fatalf("after draining: size=%d height=%d", tree.Size(), tree.Height())
	}
}
