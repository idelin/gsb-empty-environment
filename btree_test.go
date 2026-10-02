package bptree

import (
	"math/rand"
	"testing"
)

const (
	totalRecords = 10000
	scanLo       = 2500
	scanHi       = 7500
)

// buildTree inserts keys [0, total) with val = key*10. If shuffle is set
// the insertion order is a deterministic permutation, which forces leaf
// and internal splits across the whole key space.
func buildTree(t *testing.T, poolCapacity int, shuffle bool) (*BPlusTree, *BufferPool) {
	t.Helper()
	pool := NewBufferPool(poolCapacity)
	tree, err := NewBPlusTree(pool)
	if err != nil {
		t.Fatalf("NewBPlusTree: %v", err)
	}
	keys := make([]int64, totalRecords)
	for i := range keys {
		keys[i] = int64(i)
	}
	if shuffle {
		rand.New(rand.NewSource(42)).Shuffle(len(keys), func(i, j int) {
			keys[i], keys[j] = keys[j], keys[i]
		})
	}
	for _, k := range keys {
		if err := tree.Insert(k, k*10); err != nil {
			t.Fatalf("Insert(%d): %v", k, err)
		}
	}
	if got := pool.TotalPins(); got != 0 {
		t.Fatalf("pin leak after inserts: TotalPins = %d, want 0", got)
	}
	return tree, pool
}

// TestRangeScanAcceptance is the acceptance case: 10000 consecutive
// records, Scan(2500, 7500) must yield exactly 5000 ordered, unique
// records, with no pin leak and no pool exhaustion (capacity is only 8
// frames, far smaller than the tree).
func TestRangeScanAcceptance(t *testing.T) {
	for _, shuffle := range []bool{false, true} {
		name := "sequential-insert"
		if shuffle {
			name = "shuffled-insert"
		}
		t.Run(name, func(t *testing.T) {
			tree, pool := buildTree(t, 8, shuffle)

			it, err := tree.Scan(scanLo, scanHi)
			if err != nil {
				t.Fatalf("Scan: %v", err)
			}
			count := 0
			prev := int64(-1)
			seen := make(map[int64]bool)
			for k, v, ok := it.Next(); ok; k, v, ok = it.Next() {
				if k < scanLo || k >= scanHi {
					t.Fatalf("key %d out of range [%d, %d)", k, scanLo, scanHi)
				}
				if k <= prev {
					t.Fatalf("keys not strictly increasing: prev=%d got=%d", prev, k)
				}
				if seen[k] {
					t.Fatalf("duplicate key %d", k)
				}
				if v != k*10 {
					t.Fatalf("key %d has value %d, want %d", k, v, k*10)
				}
				seen[k] = true
				prev = k
				count++
			}
			if err := it.Err(); err != nil {
				t.Fatalf("iterator error: %v", err)
			}
			if count != scanHi-scanLo {
				t.Fatalf("scanned %d records, want %d", count, scanHi-scanLo)
			}
			for k := int64(scanLo); k < scanHi; k++ {
				if !seen[k] {
					t.Fatalf("missing key %d", k)
				}
			}
			if got := pool.TotalPins(); got != 0 {
				t.Fatalf("pin leak after scan: TotalPins = %d, want 0", got)
			}
			if pool.Evictions == 0 {
				t.Fatalf("expected evictions with an 8-frame pool, got 0")
			}
			t.Logf("records=%d pool: hits=%d misses=%d evictions=%d pins=%d",
				count, pool.Hits, pool.Misses, pool.Evictions, pool.TotalPins())
		})
	}
}

// TestScanCrossesManyLeaves verifies the iterator walks the sibling chain
// across every leaf page in the tree.
func TestScanCrossesManyLeaves(t *testing.T) {
	tree, pool := buildTree(t, 8, true)
	it, err := tree.Scan(0, totalRecords)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	count := 0
	prev := int64(-1)
	for k, _, ok := it.Next(); ok; k, _, ok = it.Next() {
		if k != prev+1 {
			t.Fatalf("gap in full scan: prev=%d got=%d", prev, k)
		}
		prev = k
		count++
	}
	if count != totalRecords {
		t.Fatalf("full scan yielded %d records, want %d", count, totalRecords)
	}
	if got := pool.TotalPins(); got != 0 {
		t.Fatalf("pin leak after full scan: TotalPins = %d", got)
	}
}

// TestScanEmptyAndEdgeRanges covers empty ranges and boundaries outside
// the key space.
func TestScanEmptyAndEdgeRanges(t *testing.T) {
	tree, pool := buildTree(t, 8, false)
	cases := []struct {
		lo, hi int64
		want   int
	}{
		{5000, 5000, 0},  // empty interval
		{6000, 5000, 0},  // inverted interval
		{-100, 3, 3},     // lo below min key
		{9997, 20000, 3}, // hi above max key
		{0, 1, 1},        // first record only
		{9999, 10000, 1}, // last record only
		{64, 128, 64},    // exactly one full leaf (order 64)
		{63, 65, 2},      // straddles a leaf boundary
	}
	for _, c := range cases {
		it, err := tree.Scan(c.lo, c.hi)
		if err != nil {
			t.Fatalf("Scan(%d, %d): %v", c.lo, c.hi, err)
		}
		count := 0
		for _, _, ok := it.Next(); ok; _, _, ok = it.Next() {
			count++
		}
		it.Close()
		if count != c.want {
			t.Errorf("Scan(%d, %d) yielded %d records, want %d", c.lo, c.hi, count, c.want)
		}
	}
	if got := pool.TotalPins(); got != 0 {
		t.Fatalf("pin leak after edge scans: TotalPins = %d", got)
	}
}

// TestIteratorEarlyClose ensures abandoning a scan mid-page releases the
// pinned leaf.
func TestIteratorEarlyClose(t *testing.T) {
	tree, pool := buildTree(t, 8, false)
	it, err := tree.Scan(scanLo, scanHi)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	for i := 0; i < 10; i++ {
		if _, _, ok := it.Next(); !ok {
			t.Fatal("iterator exhausted too early")
		}
	}
	it.Close()
	it.Close() // idempotent
	if got := pool.TotalPins(); got != 0 {
		t.Fatalf("pin leak after early close: TotalPins = %d", got)
	}
}

// TestPoolExhaustionGuard proves the pool refuses to grow beyond its
// capacity when every frame is pinned (memory-overflow guard).
func TestPoolExhaustionGuard(t *testing.T) {
	pool := NewBufferPool(2)
	p1, _ := pool.NewPage()
	p2, _ := pool.NewPage()
	if _, err := pool.NewPage(); err != ErrPoolExhausted {
		t.Fatalf("want ErrPoolExhausted, got %v", err)
	}
	pool.Unpin(p1.ID)
	pool.Unpin(p2.ID)
	if _, err := pool.NewPage(); err != nil {
		t.Fatalf("after unpin, NewPage should evict and succeed: %v", err)
	}
}
