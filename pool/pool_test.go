package pool

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// EntryPool
// ---------------------------------------------------------------------------

func TestEntryPoolReuseAndLeakFree(t *testing.T) {
	p := NewEntryPool(64)
	e1 := p.Acquire()
	e1.Slot = 7
	e1.Value = append(e1.Value, "hello"...)
	p.Release(e1)

	e2 := p.Acquire()
	if e2 != e1 {
		t.Fatal("expected entry memory to be reused")
	}
	if e2.Slot != 0 || e2.Noop || len(e2.Value) != 0 {
		t.Fatalf("acquired entry not reset: %+v", e2)
	}
	if cap(e2.Value) != 64 {
		t.Fatalf("value capacity lost: %d", cap(e2.Value))
	}
	p.Release(e2)
	if got := p.Outstanding(); got != 0 {
		t.Fatalf("leak: %d entries outstanding", got)
	}
	if got := p.Recycled(); got != 2 {
		t.Fatalf("recycled = %d, want 2", got)
	}
}

func TestEntryPoolConcurrentNoLeak(t *testing.T) {
	p := NewEntryPool(32)
	const workers = 64
	const iters = 5000
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				e := p.Acquire()
				e.Slot = uint64(i)
				e.Value = append(e.Value, byte(id), byte(i))
				p.Release(e)
			}
		}(w)
	}
	wg.Wait()
	if got := p.Outstanding(); got != 0 {
		t.Fatalf("leak under concurrency: %d outstanding", got)
	}
	if got, want := p.Recycled(), int64(workers*iters); got != want {
		t.Fatalf("recycled = %d, want %d", got, want)
	}
}

func TestBufferPool(t *testing.T) {
	b := GetBuffer(100)
	if cap(b.Buf) < 100 {
		t.Fatalf("capacity %d < hint 100", cap(b.Buf))
	}
	b.Buf = append(b.Buf, "data"...)
	PutBuffer(b)
	b2 := GetBuffer(10)
	if len(b2.Buf) != 0 {
		t.Fatal("buffer not reset on reuse")
	}
	PutBuffer(b2)
	PutBuffer(nil) // must not panic
}

// ---------------------------------------------------------------------------
// Cache boundaries
// ---------------------------------------------------------------------------

func TestCachePutGetDelete(t *testing.T) {
	c := NewCache(4, 16, false)
	if err := c.Put(1, []byte("alpha")); err != nil {
		t.Fatal(err)
	}
	dst := make([]byte, 16)
	n, ok := c.Get(1, dst)
	if !ok || string(dst[:n]) != "alpha" {
		t.Fatalf("get = %q ok=%v", dst[:n], ok)
	}
	// Overwrite in place.
	if err := c.Put(1, []byte("beta")); err != nil {
		t.Fatal(err)
	}
	n, _ = c.Get(1, dst)
	if string(dst[:n]) != "beta" {
		t.Fatalf("overwrite = %q", dst[:n])
	}
	if !c.Delete(1) {
		t.Fatal("delete existing key failed")
	}
	if _, ok := c.Get(1, dst); ok {
		t.Fatal("get after delete must miss")
	}
	if c.Delete(1) {
		t.Fatal("double delete must report false")
	}
	if c.Len() != 0 {
		t.Fatalf("len = %d, want 0", c.Len())
	}
}

func TestCacheValueTooLarge(t *testing.T) {
	c := NewCache(2, 4, false)
	if err := c.Put(1, []byte("12345")); err != ErrValueTooLarge {
		t.Fatalf("err = %v, want ErrValueTooLarge", err)
	}
	if err := c.Put(1, []byte("1234")); err != nil {
		t.Fatalf("exact-fit value rejected: %v", err)
	}
}

func TestCacheFullNoEvict(t *testing.T) {
	c := NewCache(2, 8, false)
	_ = c.Put(1, []byte("a"))
	_ = c.Put(2, []byte("b"))
	if err := c.Put(3, []byte("c")); err != ErrCacheFull {
		t.Fatalf("err = %v, want ErrCacheFull", err)
	}
	// Overwriting an existing key must still work when full.
	if err := c.Put(1, []byte("z")); err != nil {
		t.Fatalf("overwrite on full cache: %v", err)
	}
	// Deleting frees a slot.
	c.Delete(2)
	if err := c.Put(3, []byte("c")); err != nil {
		t.Fatalf("put after delete: %v", err)
	}
}

func TestCacheEviction(t *testing.T) {
	c := NewCache(3, 8, true)
	for k := uint64(1); k <= 5; k++ {
		if err := c.Put(k, []byte(fmt.Sprintf("v%d", k))); err != nil {
			t.Fatal(err)
		}
	}
	if c.Len() != 3 {
		t.Fatalf("len = %d, want 3", c.Len())
	}
	_, _, evicted := c.Stats()
	if evicted != 2 {
		t.Fatalf("evicted = %d, want 2", evicted)
	}
	// Keys 4 and 5 (most recent) must survive.
	dst := make([]byte, 8)
	for _, k := range []uint64{4, 5} {
		if _, ok := c.Get(k, dst); !ok {
			t.Fatalf("recent key %d evicted", k)
		}
	}
}

func TestCacheConcurrentSafety(t *testing.T) {
	c := NewCache(128, 32, true)
	const workers = 32
	const iters = 2000
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			dst := make([]byte, 32)
			key := uint64(id % 16)
			for i := 0; i < iters; i++ {
				_ = c.Put(key, []byte{byte(id), byte(i)})
				_, _ = c.Get(key, dst)
				if i%7 == 0 {
					c.Delete(uint64((id + i) % 16))
				}
			}
		}(w)
	}
	wg.Wait()
	if c.Len() > 128 {
		t.Fatalf("len %d exceeds capacity", c.Len())
	}
}

// TestCacheZeroAllocHotPath asserts the steady-state hot path performs zero
// heap allocations — the core GC-pause-elimination guarantee.
func TestCacheZeroAllocHotPath(t *testing.T) {
	c := NewCache(256, 64, true)
	dst := make([]byte, 64)
	val := []byte("hot-path-value")
	_ = c.Put(1, val)

	allocs := testing.AllocsPerRun(1000, func() {
		_ = c.Put(1, val)      // overwrite existing key
		_, _ = c.Get(1, dst)   // hit
		_, _ = c.Get(999, dst) // miss
	})
	if allocs != 0 {
		t.Fatalf("hot path allocated %v times per run, want 0", allocs)
	}
}

func TestCacheHitMissStats(t *testing.T) {
	c := NewCache(4, 8, false)
	_ = c.Put(1, []byte("x"))
	dst := make([]byte, 8)
	_, _ = c.Get(1, dst)
	_, _ = c.Get(2, dst)
	hits, misses, _ := c.Stats()
	if hits != 1 || misses != 1 {
		t.Fatalf("hits=%d misses=%d, want 1/1", hits, misses)
	}
}

func TestCacheValueIsolation(t *testing.T) {
	// Mutating the caller's slice after Put must not corrupt the cache, and
	// Get must copy out rather than alias internal storage.
	c := NewCache(2, 8, false)
	src := []byte("abc")
	_ = c.Put(1, src)
	src[0] = 'X'
	dst := make([]byte, 8)
	n, _ := c.Get(1, dst)
	if !bytes.Equal(dst[:n], []byte("abc")) {
		t.Fatalf("cache aliased caller memory: %q", dst[:n])
	}
}
