package bptree

import (
	"sort"
	"testing"
)

// checkInvariants validates structural B+ tree invariants and page accounting.
func checkInvariants(t *testing.T, tr *Tree) {
	t.Helper()

	st := tr.Stats()
	if st.LivePages+st.FreePages != st.Created {
		t.Fatalf("page leak: live=%d free=%d created=%d", st.LivePages, st.FreePages, st.Created)
	}

	root := tr.p.load(tr.root)
	leafDepth, count := -1, 0

	var walk func(n *page, depth int, isRoot bool) (min, max int)
	walk = func(n *page, depth int, isRoot bool) (int, int) {
		if !sort.IntsAreSorted(n.keys) {
			t.Fatalf("page %d: keys not sorted: %v", n.id, n.keys)
		}
		if len(n.keys) > MaxKeys {
			t.Fatalf("page %d: overflow (%d keys)", n.id, len(n.keys))
		}
		if !isRoot && len(n.keys) < MinKeys {
			t.Fatalf("page %d: underflow (%d keys)", n.id, len(n.keys))
		}
		if n.leaf {
			if leafDepth == -1 {
				leafDepth = depth
			}
			if depth != leafDepth {
				t.Fatalf("leaves at different depths: %d vs %d", depth, leafDepth)
			}
			count += len(n.keys)
			if len(n.keys) == 0 {
				t.Fatalf("page %d: empty non-root leaf", n.id)
			}
			return n.keys[0], n.keys[len(n.keys)-1]
		}
		if isRoot && len(n.keys) == 0 {
			t.Fatalf("internal root with no keys")
		}
		if len(n.children) != len(n.keys)+1 {
			t.Fatalf("page %d: %d keys but %d children", n.id, len(n.keys), len(n.children))
		}
		min, max := 0, 0
		for i, cid := range n.children {
			c := tr.p.load(cid)
			if c == nil {
				t.Fatalf("page %d: dangling child page %d", n.id, cid)
			}
			cmin, cmax := walk(c, depth+1, false)
			if i == 0 {
				min = cmin
			} else if n.keys[i-1] > cmin {
				// Separators are lazy: deleting a leaf key does not update
				// its copy in ancestors, so a separator may be smaller than
				// the current minimum of its right subtree, never larger.
				t.Fatalf("page %d: separator %d > min of right subtree %d", n.id, n.keys[i-1], cmin)
			}
			if i < len(n.keys) && cmax >= n.keys[i] {
				t.Fatalf("page %d: subtree max %d >= separator %d", n.id, cmax, n.keys[i])
			}
			max = cmax
		}
		return min, max
	}

	if tr.size == 0 {
		if !root.leaf || len(root.keys) != 0 {
			t.Fatalf("empty tree must have an empty leaf root")
		}
	} else {
		walk(root, 1, true)
	}
	if count != tr.size {
		t.Fatalf("leaf key count %d != size %d", count, tr.size)
	}
	if got := tr.Scan(); !sort.IntsAreSorted(got) || len(got) != tr.size {
		t.Fatalf("leaf chain scan broken: len=%d sorted=%v", len(got), sort.IntsAreSorted(got))
	}
}

// TestCascadingMergeShrinksTree is the acceptance test: build a full tree of
// 500 records (height 4), delete down to 3 records, and verify the height
// shrinks level by level to 1 while emptied pages are reclaimed to the free
// page list.
func TestCascadingMergeShrinksTree(t *testing.T) {
	tr := New()
	for i := 1; i <= 500; i++ {
		if !tr.Insert(i) {
			t.Fatalf("insert %d failed", i)
		}
	}
	if h := tr.Height(); h != 4 {
		t.Fatalf("full tree of 500 records: want height 4, got %d", h)
	}
	checkInvariants(t, tr)

	peak := tr.Stats()
	t.Logf("full tree: height=%d livePages=%d", tr.Height(), peak.LivePages)

	prev := tr.Height()
	seen := map[int]bool{prev: true}
	for i := 1; i <= 497; i++ {
		if !tr.Delete(i) {
			t.Fatalf("delete %d failed", i)
		}
		if h := tr.Height(); h > prev {
			t.Fatalf("height grew during deletion: %d -> %d", prev, h)
		}
		if h := tr.Height(); h < prev {
			t.Logf("after %3d deletions: height %d -> %d, stats=%+v", i, prev, h, tr.Stats())
			prev = h
		}
		seen[tr.Height()] = true
		checkInvariants(t, tr)
	}

	for h := 1; h <= 4; h++ {
		if !seen[h] {
			t.Fatalf("tree never reached height %d during shrink", h)
		}
	}
	if h := tr.Height(); h != 1 {
		t.Fatalf("want final height 1, got %d", h)
	}
	if got, want := tr.Scan(), []int{498, 499, 500}; !equalInts(got, want) {
		t.Fatalf("remaining keys: want %v, got %v", want, got)
	}

	st := tr.Stats()
	if st.LivePages != 1 {
		t.Fatalf("want exactly 1 live page (leaf root), got %d", st.LivePages)
	}
	if st.FreePages == 0 {
		t.Fatalf("free page list is empty; merged pages were not reclaimed")
	}
	if st.FreePages != st.Created-1 {
		t.Fatalf("all but the root page should be free: free=%d created=%d", st.FreePages, st.Created)
	}
	t.Logf("final: height=1 livePages=%d freePages=%d created=%d", st.LivePages, st.FreePages, st.Created)
}

// TestFreeListReuse verifies pages on the free list are recycled by later inserts.
func TestFreeListReuse(t *testing.T) {
	tr := New()
	for i := 1; i <= 200; i++ {
		tr.Insert(i)
	}
	for i := 1; i <= 150; i++ {
		tr.Delete(i)
	}
	before := tr.Stats()
	if before.FreePages == 0 {
		t.Fatalf("expected free pages after deletions")
	}
	for i := 1000; i < 1100; i++ {
		tr.Insert(i)
	}
	after := tr.Stats()
	if after.FreePages >= before.FreePages {
		t.Fatalf("free list was not tapped: before=%d after=%d", before.FreePages, after.FreePages)
	}
	if grown := after.Created - before.Created; grown >= before.FreePages {
		t.Fatalf("inserts allocated %d fresh pages despite %d free pages", grown, before.FreePages)
	}
	checkInvariants(t, tr)
	for i := 151; i <= 200; i++ {
		if !tr.Contains(i) {
			t.Fatalf("missing key %d", i)
		}
	}
	for i := 1000; i < 1100; i++ {
		if !tr.Contains(i) {
			t.Fatalf("missing key %d", i)
		}
	}
}

// TestRandomDeleteOrder stresses merges with a random deletion order.
func TestRandomDeleteOrder(t *testing.T) {
	tr := New()
	const n = 1000
	for i := 0; i < n; i++ {
		tr.Insert(i)
	}
	checkInvariants(t, tr)
	// Deterministic pseudo-random permutation.
	perm := make([]int, n)
	for i := range perm {
		perm[i] = i
	}
	seed := int64(42)
	for i := n - 1; i > 0; i-- {
		seed = seed*6364136223846793005 + 1442695040888963407
		j := int(uint64(seed>>33) % uint64(i+1))
		perm[i], perm[j] = perm[j], perm[i]
	}
	for k, key := range perm {
		if !tr.Delete(key) {
			t.Fatalf("delete %d failed", key)
		}
		if k%97 == 0 {
			checkInvariants(t, tr)
		}
	}
	checkInvariants(t, tr)
	if tr.Size() != 0 || tr.Height() != 1 {
		t.Fatalf("empty tree: size=%d height=%d", tr.Size(), tr.Height())
	}
	st := tr.Stats()
	if st.LivePages != 1 || st.FreePages != st.Created-1 {
		t.Fatalf("all pages but root should be reclaimed: %+v", st)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
