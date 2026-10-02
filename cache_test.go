package hexgrid

import (
	"sync"
	"testing"
)

func TestCachePutGet(t *testing.T) {
	var c Cache
	key := CacheKey(39.9042, 116.4074, 8)
	if _, hit := c.Get(key); hit {
		t.Fatal("unexpected hit on empty cache")
	}
	c.Put(key, 42)
	v, hit := c.Get(key)
	if !hit || v != 42 {
		t.Fatalf("got (%d, %v), want (42, true)", v, hit)
	}
	// Update in place.
	c.Put(key, 43)
	if v, _ := c.Get(key); v != 43 {
		t.Fatalf("got %d after update, want 43", v)
	}
	if c.Len() != 1 {
		t.Fatalf("len = %d, want 1", c.Len())
	}
}

func TestCacheKeyReservedZero(t *testing.T) {
	var c Cache
	if _, hit := c.Get(0); hit {
		t.Fatal("key 0 is reserved and must never hit")
	}
	c.Put(0, 99) // must be ignored
	if c.Len() != 0 {
		t.Fatal("put of key 0 must be ignored")
	}
	// CacheKey itself never produces 0 (even for the all-zero corner).
	if CacheKey(-90, -180, 0) == 0 {
		t.Fatal("CacheKey must never return 0")
	}
}

func TestCacheEvictionBounded(t *testing.T) {
	var c Cache
	// Insert far more entries than the capacity: the cache must stay
	// bounded and never grow (fixed memory footprint).
	for i := uint64(1); i <= 100000; i++ {
		c.Put(i, i*7)
	}
	if c.Len() > c.Capacity() {
		t.Fatalf("len %d exceeds capacity %d", c.Len(), c.Capacity())
	}
	if c.Capacity() != 512 {
		t.Fatalf("capacity = %d, want 512", c.Capacity())
	}
}

func TestCacheKeyQuantisation(t *testing.T) {
	// Coordinates within the same 1e-6 degree bucket share a key.
	a := CacheKey(39.9042001, 116.4074001, 8)
	b := CacheKey(39.9042002, 116.4074002, 8)
	if a != b {
		t.Fatal("sub-bucket coordinates must share a cache key")
	}
	// Different resolutions produce different keys.
	if CacheKey(39.9, 116.4, 7) == CacheKey(39.9, 116.4, 8) {
		t.Fatal("resolution must participate in the key")
	}
	// Clamping keeps out-of-range inputs safe.
	if CacheKey(91, 181, 8) != CacheKey(90, 180, 8) {
		t.Fatal("out-of-range coordinates must clamp")
	}
}

// TestCacheZeroAlloc: hits and puts on a warm cache must not allocate.
func TestCacheZeroAlloc(t *testing.T) {
	var c Cache
	key := CacheKey(39.9042, 116.4074, 8)
	c.Put(key, 123)
	if allocs := testing.AllocsPerRun(500, func() {
		if _, hit := c.Get(key); !hit {
			t.Fatal("expected hit")
		}
	}); allocs != 0 {
		t.Fatalf("Get allocated %v times per op, want 0", allocs)
	}
	if allocs := testing.AllocsPerRun(500, func() {
		c.Put(key, 124)
	}); allocs != 0 {
		t.Fatalf("Put allocated %v times per op, want 0", allocs)
	}
}

// TestCacheConcurrent: mixed readers/writers across goroutines; run with
// -race. The cache must stay consistent and bounded.
func TestCacheConcurrent(t *testing.T) {
	var c Cache
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(id uint64) {
			defer wg.Done()
			base := id*100000 + 1
			for i := uint64(0); i < 20000; i++ {
				k := base + i%512
				c.Put(k, k*3)
				if v, hit := c.Get(k); hit && v != k*3 {
					t.Errorf("inconsistent value for key %d: %d", k, v)
					return
				}
			}
		}(uint64(g))
	}
	wg.Wait()
	if c.Len() > c.Capacity() {
		t.Fatalf("len %d exceeds capacity %d", c.Len(), c.Capacity())
	}
}

// TestCacheEncodeMemoisation shows the intended use: memoising the
// quantised-coordinate -> cell mapping with zero allocations on hits.
func TestCacheEncodeMemoisation(t *testing.T) {
	var c Cache
	lat, lng := 39.9042, 116.4074
	key := CacheKey(lat, lng, 8)
	want := LatLngToCell(lat, lng, 8)
	c.Put(key, uint64(want))
	if allocs := testing.AllocsPerRun(500, func() {
		v, hit := c.Get(key)
		if !hit || Cell(v) != want {
			t.Fatal("memoised lookup failed")
		}
	}); allocs != 0 {
		t.Fatalf("memoised encode allocated %v times per op, want 0", allocs)
	}
}
