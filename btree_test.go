package bptree

import (
	"math/rand"
	"testing"
)

// buildTree inserts n records (key=i, value=i*10) into a tree backed by
// a deliberately tiny buffer pool, and asserts nothing stays pinned.
func buildTree(t *testing.T, n, poolCap int) (*BPlusTree, *BufferPool) {
	t.Helper()
	pool := NewBufferPool(poolCap)
	tree, err := NewBPlusTree(pool)
	if err != nil {
		t.Fatalf("NewBPlusTree: %v", err)
	}
	for i := 0; i < n; i++ {
		if err := tree.Insert(i, i*10); err != nil {
			t.Fatalf("Insert(%d): %v", i, err)
		}
	}
	if pins := pool.PinnedCount(); pins != 0 {
		t.Fatalf("after inserts: %d pinned pages, want 0", pins)
	}
	return tree, pool
}

// Acceptance test: 10000 records, Scan(2500, 7500) yields exactly the
// 5000 keys 2500..7499, in order, without duplicates, and leaves no
// pinned page behind — all with a 16-frame buffer pool.
func TestRangeScanAcceptance(t *testing.T) {
	tree, pool := buildTree(t, 10000, 16)

	it, err := tree.Scan(2500, 7500)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	count := 0
	prev := -1
	for {
		k, v, ok := it.Next()
		if !ok {
			break
		}
		if want := 2500 + count; k != want {
			t.Fatalf("record %d: key = %d, want %d", count, k, want)
		}
		if v != k*10 {
			t.Fatalf("key %d: value = %d, want %d", k, v, k*10)
		}
		if k <= prev {
			t.Fatalf("keys not strictly increasing: %d after %d", k, prev)
		}
		prev = k
		count++
	}
	if err := it.Err(); err != nil {
		t.Fatalf("iterator error: %v", err)
	}
	if count != 5000 {
		t.Fatalf("scanned %d records, want 5000", count)
	}
	if pins := pool.PinnedCount(); pins != 0 {
		t.Fatalf("page leak: %d pinned pages after scan", pins)
	}
	t.Logf("scanned %d records across %d leaf pages; pool: hits=%d misses=%d evictions=%d",
		count, pool.PagesOnDisk(), pool.Hits, pool.Misses, pool.Evictions)
}

// The scan must cross many physical leaf pages (leaf capacity is 32
// keys, so 5000 records span >150 pages) while the pool holds only 16.
func TestScanCrossesPageBoundaries(t *testing.T) {
	tree, pool := buildTree(t, 10000, 16)
	leaves := 0
	p, err := tree.findLeaf(0)
	if err != nil {
		t.Fatal(err)
	}
	for {
		leaves++
		next := p.node.next
		if err := pool.UnpinPage(p.ID(), false); err != nil {
			t.Fatal(err)
		}
		if next == InvalidPageID {
			break
		}
		if p, err = pool.FetchPage(next); err != nil {
			t.Fatal(err)
		}
	}
	if leaves < 100 {
		t.Fatalf("expected many leaf pages, got %d", leaves)
	}
	it, err := tree.Scan(2500, 7500)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for {
		if _, _, ok := it.Next(); !ok {
			break
		}
		n++
	}
	if err := it.Err(); err != nil {
		t.Fatal(err)
	}
	if n != 5000 {
		t.Fatalf("got %d records, want 5000", n)
	}
	if pins := pool.PinnedCount(); pins != 0 {
		t.Fatalf("page leak: %d pinned pages", pins)
	}
}

// Closing an iterator early must release its pinned page.
func TestIteratorEarlyClose(t *testing.T) {
	tree, pool := buildTree(t, 10000, 16)
	it, err := tree.Scan(100, 9900)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if _, _, ok := it.Next(); !ok {
			t.Fatal("iterator ended early")
		}
	}
	it.Close()
	it.Close() // idempotent
	if pins := pool.PinnedCount(); pins != 0 {
		t.Fatalf("page leak after early Close: %d pinned pages", pins)
	}
}

func TestScanEdgeRanges(t *testing.T) {
	tree, pool := buildTree(t, 10000, 16)
	cases := []struct {
		name       string
		start, end int
		want       int
	}{
		{"empty range", 5000, 5000, 0},
		{"single key", 42, 43, 1},
		{"before min", -100, 3, 3},
		{"past max", 9998, 20000, 2},
		{"full range", 0, 10000, 10000},
		{"beyond both ends", -1, 10001, 10000},
		{"outside tree", 20000, 30000, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			it, err := tree.Scan(tc.start, tc.end)
			if err != nil {
				t.Fatal(err)
			}
			n := 0
			prev := tc.start - 1
			for {
				k, _, ok := it.Next()
				if !ok {
					break
				}
				if k < tc.start || k >= tc.end {
					t.Fatalf("key %d outside [%d, %d)", k, tc.start, tc.end)
				}
				if k <= prev && n > 0 {
					t.Fatalf("duplicate/out-of-order key %d", k)
				}
				prev = k
				n++
			}
			if err := it.Err(); err != nil {
				t.Fatal(err)
			}
			if n != tc.want {
				t.Fatalf("got %d records, want %d", n, tc.want)
			}
		})
	}
	if pins := pool.PinnedCount(); pins != 0 {
		t.Fatalf("page leak: %d pinned pages", pins)
	}
}

// Shuffled inserts must still produce a correctly ordered scan.
func TestShuffledInsertOrderedScan(t *testing.T) {
	pool := NewBufferPool(16)
	tree, err := NewBPlusTree(pool)
	if err != nil {
		t.Fatal(err)
	}
	perm := rand.New(rand.NewSource(1)).Perm(5000)
	for _, i := range perm {
		if err := tree.Insert(i, i); err != nil {
			t.Fatal(err)
		}
	}
	it, err := tree.Scan(0, 5000)
	if err != nil {
		t.Fatal(err)
	}
	want := 0
	for {
		k, v, ok := it.Next()
		if !ok {
			break
		}
		if k != want || v != want {
			t.Fatalf("position %d: got (%d, %d)", want, k, v)
		}
		want++
	}
	if err := it.Err(); err != nil {
		t.Fatal(err)
	}
	if want != 5000 {
		t.Fatalf("got %d records, want 5000", want)
	}
	if pins := pool.PinnedCount(); pins != 0 {
		t.Fatalf("page leak: %d pinned pages", pins)
	}
}

// A pool with barely more frames than the tree's height must still
// serve inserts and a full scan, proving the iterator never holds more
// than one leaf page at a time.
func TestScanWithMinimalPool(t *testing.T) {
	tree, pool := buildTree(t, 10000, 8)
	it, err := tree.Scan(0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for {
		if _, _, ok := it.Next(); !ok {
			break
		}
		n++
	}
	if err := it.Err(); err != nil {
		t.Fatal(err)
	}
	if n != 10000 {
		t.Fatalf("got %d records, want 10000", n)
	}
	if pool.Evictions == 0 {
		t.Fatal("expected LRU evictions with an 8-frame pool")
	}
	if pins := pool.PinnedCount(); pins != 0 {
		t.Fatalf("page leak: %d pinned pages", pins)
	}
}
