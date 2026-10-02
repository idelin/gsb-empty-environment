package cache

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
)

func newTestCache() *Cache {
	return NewCache(Options{Capacity: 128, MaxValueSize: 64, Shards: 4})
}

func TestPutGetRoundtrip(t *testing.T) {
	c := newTestCache()
	if !c.Put(42, []byte("hello")) {
		t.Fatal("Put failed")
	}
	got, ok := c.Get(42, nil)
	if !ok || string(got) != "hello" {
		t.Fatalf("Get = %q, %v; want %q, true", got, ok, "hello")
	}
}

func TestGetMissing(t *testing.T) {
	c := newTestCache()
	got, ok := c.Get(999, nil)
	if ok {
		t.Fatalf("Get on missing key: ok=true, got=%q", got)
	}
	if len(got) != 0 {
		t.Fatalf("missing key should return empty slice, got %q", got)
	}
	if st := c.Stats(); st.Misses != 1 {
		t.Fatalf("misses=%d, want 1", st.Misses)
	}
}

func TestOverwriteSameKey(t *testing.T) {
	c := newTestCache()
	c.Put(7, []byte("a"))
	c.Put(7, []byte("bb"))
	c.Put(7, []byte("ccc"))
	got, ok := c.Get(7, nil)
	if !ok || string(got) != "ccc" {
		t.Fatalf("Get = %q, %v; want %q, true", got, ok, "ccc")
	}
	if c.Len() != 1 {
		t.Fatalf("Len=%d, want 1 (overwrite must not grow the cache)", c.Len())
	}
}

func TestValueSizeBoundaries(t *testing.T) {
	c := NewCache(Options{Capacity: 8, MaxValueSize: 16, Shards: 1})
	if !c.Put(1, make([]byte, 16)) {
		t.Fatal("value of exactly MaxValueSize must be accepted")
	}
	if c.Put(2, make([]byte, 17)) {
		t.Fatal("value larger than MaxValueSize must be rejected")
	}
	if _, ok := c.Get(2, nil); ok {
		t.Fatal("rejected value must not be stored")
	}
	// Empty value is legal and distinguishable from a miss via ok.
	if !c.Put(3, []byte{}) {
		t.Fatal("empty value must be accepted")
	}
	got, ok := c.Get(3, nil)
	if !ok || len(got) != 0 {
		t.Fatalf("empty value: got=%q ok=%v", got, ok)
	}
}

func TestKeyBoundaries(t *testing.T) {
	c := newTestCache()
	keys := []uint64{0, 1, ^uint64(0), 1 << 63}
	for _, k := range keys {
		if !c.Put(k, []byte(fmt.Sprintf("v-%d", k))) {
			t.Fatalf("Put key %d failed", k)
		}
	}
	for _, k := range keys {
		got, ok := c.Get(k, nil)
		if !ok || string(got) != fmt.Sprintf("v-%d", k) {
			t.Fatalf("key %d: got=%q ok=%v", k, got, ok)
		}
	}
}

func TestDelete(t *testing.T) {
	c := newTestCache()
	if c.Delete(5) {
		t.Fatal("Delete on missing key must return false")
	}
	c.Put(5, []byte("x"))
	if !c.Delete(5) {
		t.Fatal("Delete on existing key must return true")
	}
	if _, ok := c.Get(5, nil); ok {
		t.Fatal("deleted key must miss")
	}
	if c.Len() != 0 {
		t.Fatalf("Len=%d after delete, want 0", c.Len())
	}
	// The freed slot is reusable.
	c.Put(5, []byte("y"))
	got, ok := c.Get(5, nil)
	if !ok || string(got) != "y" {
		t.Fatalf("reuse after delete: got=%q ok=%v", got, ok)
	}
}

func TestEvictionBoundedCapacity(t *testing.T) {
	const cap = 16
	c := NewCache(Options{Capacity: cap, MaxValueSize: 8, Shards: 1})
	for i := uint64(0); i < 4*cap; i++ {
		c.Put(i, []byte{byte(i)})
	}
	if got := c.Len(); got > cap {
		t.Fatalf("Len=%d exceeds capacity %d: memory leak", got, cap)
	}
	if got := c.Capacity(); got != cap {
		t.Fatalf("Capacity=%d, want %d", got, cap)
	}
	st := c.Stats()
	if st.Evictions == 0 {
		t.Fatal("expected evictions under capacity pressure")
	}
	// Most recent key must still be present.
	if _, ok := c.Get(4*cap-1, nil); !ok {
		t.Fatal("most recently inserted key was evicted")
	}
}

func TestClockEvictionPrefersUnreferenced(t *testing.T) {
	c := NewCache(Options{Capacity: 4, MaxValueSize: 8, Shards: 1})
	c.Put(1, []byte("1"))
	c.Put(2, []byte("2"))
	c.Put(3, []byte("3"))
	c.Put(4, []byte("4"))
	// First eviction: every slot has its reference bit set from insertion,
	// so the CLOCK sweep clears them all and evicts the oldest slot (key 1).
	c.Put(5, []byte("5"))
	if _, ok := c.Get(1, nil); ok {
		t.Fatal("key 1 should have been the first CLOCK victim")
	}
	// Re-reference keys 2 and 3; key 4 stays unreferenced.
	if _, ok := c.Get(2, nil); !ok {
		t.Fatal("key 2 missing")
	}
	if _, ok := c.Get(3, nil); !ok {
		t.Fatal("key 3 missing")
	}
	// Second eviction must skip the referenced keys and evict key 4.
	c.Put(6, []byte("6"))
	if _, ok := c.Get(4, nil); ok {
		t.Fatal("unreferenced key 4 should have been evicted")
	}
	for _, k := range []uint64{2, 3, 5, 6} {
		if _, ok := c.Get(k, nil); !ok {
			t.Fatalf("referenced key %d should have survived CLOCK eviction", k)
		}
	}
	if c.Len() != 4 {
		t.Fatalf("Len=%d, want 4", c.Len())
	}
}

func TestStatsAccounting(t *testing.T) {
	c := newTestCache()
	c.Put(1, []byte("a"))
	c.Get(1, nil)
	c.Get(1, nil)
	c.Get(2, nil)
	st := c.Stats()
	if st.Hits != 2 || st.Misses != 1 {
		t.Fatalf("stats = %+v, want Hits=2 Misses=1", st)
	}
}

// TestZeroAllocHotPath proves the GC-stall elimination claim: after
// warm-up, Get and overwrite-Put perform zero heap allocations.
func TestZeroAllocHotPath(t *testing.T) {
	c := NewCache(Options{Capacity: 256, MaxValueSize: 64, Shards: 8})
	dst := make([]byte, 64)
	val := []byte("hot-value")
	c.Put(1, val) // warm up
	allocs := testing.AllocsPerRun(1000, func() {
		c.Put(1, val)
		if _, ok := c.Get(1, dst); !ok {
			t.Fatal("hot key missing")
		}
	})
	if allocs != 0 {
		t.Fatalf("hot path allocated %v times per op, want 0", allocs)
	}
}

// TestConcurrentMixed verifies race-safety and per-key value integrity
// under parallel load. Each goroutine owns a disjoint key range, so every
// Get after a Put must observe exactly the value that goroutine wrote.
func TestConcurrentMixed(t *testing.T) {
	c := NewCache(Options{Capacity: 1 << 16, MaxValueSize: 32, Shards: 64})
	const workers = 8
	const keysPerWorker = 500
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			base := uint64(w * keysPerWorker)
			dst := make([]byte, 32)
			for i := 0; i < keysPerWorker; i++ {
				k := base + uint64(i)
				val := []byte(fmt.Sprintf("w%d-k%d", w, i))
				if !c.Put(k, val) {
					t.Errorf("Put key %d failed", k)
					return
				}
				got, ok := c.Get(k, dst)
				if !ok {
					t.Errorf("key %d missing right after Put", k)
					return
				}
				if !bytes.Equal(got, val) {
					t.Errorf("key %d: got %q, want %q", k, got, val)
					return
				}
				if i%3 == 0 {
					c.Delete(k)
					if _, ok := c.Get(k, dst); ok {
						t.Errorf("key %d present after Delete", k)
						return
					}
				}
			}
		}(w)
	}
	wg.Wait()
	st := c.Stats()
	if st.Hits+st.Misses == 0 {
		t.Fatal("no cache traffic recorded")
	}
}

// TestConcurrentSameKey hammers a single key from many goroutines; the
// cache must stay consistent (value is always one of the written values).
func TestConcurrentSameKey(t *testing.T) {
	c := newTestCache()
	const workers = 16
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			val := []byte(fmt.Sprintf("writer-%02d", w))
			dst := make([]byte, 64)
			for i := 0; i < 2000; i++ {
				c.Put(1, val)
				got, ok := c.Get(1, dst)
				if ok && !bytes.HasPrefix(got, []byte("writer-")) {
					t.Errorf("corrupt value %q", got)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	if _, ok := c.Get(1, nil); !ok {
		t.Fatal("key vanished after concurrent writes")
	}
}
