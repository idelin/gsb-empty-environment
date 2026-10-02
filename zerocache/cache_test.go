package zerocache

import (
	"sync"
	"testing"
)

func newTestCache(perShard, bufCap int) (*Cache, *SlicePool) {
	pool := NewSlicePool(shardCount*perShard, bufCap)
	return NewCache(pool, perShard), pool
}

func valueOf(key, n uint64) []uint64 {
	v := make([]uint64, n)
	for i := range v {
		v[i] = key*1000 + uint64(i)
	}
	return v
}

func TestCachePutGet(t *testing.T) {
	c, _ := newTestCache(4, 16)
	val := valueOf(42, 5)
	if !c.Put(42, val) {
		t.Fatal("Put failed")
	}
	dst := make([]uint64, 16)
	n := c.Get(42, dst)
	if n != 5 {
		t.Fatalf("Get returned %d elements, want 5", n)
	}
	for i := 0; i < 5; i++ {
		if dst[i] != val[i] {
			t.Fatalf("dst[%d] = %d, want %d", i, dst[i], val[i])
		}
	}
	// Mutating the source after Put must not affect the cached copy.
	val[0] = 999
	c.Get(42, dst)
	if dst[0] == 999 {
		t.Fatal("cache did not copy on Put")
	}
}

func TestCacheMiss(t *testing.T) {
	c, _ := newTestCache(4, 16)
	dst := make([]uint64, 16)
	if n := c.Get(12345, dst); n != -1 {
		t.Fatalf("miss returned %d, want -1", n)
	}
	if l := c.Len(12345); l != -1 {
		t.Fatalf("Len on miss = %d, want -1", l)
	}
}

func TestCacheGetBufferTooSmall(t *testing.T) {
	c, _ := newTestCache(4, 16)
	c.Put(7, valueOf(7, 10))
	if n := c.Get(7, make([]uint64, 4)); n != -1 {
		t.Fatalf("Get with small dst = %d, want -1", n)
	}
	if l := c.Len(7); l != 10 {
		t.Fatalf("Len = %d, want 10", l)
	}
}

func TestCacheOverwrite(t *testing.T) {
	c, pool := newTestCache(4, 16)
	c.Put(9, valueOf(9, 3))
	c.Put(9, valueOf(100, 6))
	dst := make([]uint64, 16)
	n := c.Get(9, dst)
	if n != 6 || dst[0] != 100000 {
		t.Fatalf("overwrite failed: n=%d dst[0]=%d", n, dst[0])
	}
	// Overwrite must not consume an extra pool buffer.
	if pool.InUse() != 1 {
		t.Fatalf("InUse = %d after overwrite, want 1", pool.InUse())
	}
}

func TestCacheEvictionReturnsBuffers(t *testing.T) {
	perShard := 2
	c, pool := newTestCache(perShard, 16)
	// Insert far more keys than the cache holds; every eviction must
	// recycle its buffer into the pool.
	const total = 64 * 5
	for i := 0; i < total; i++ {
		if !c.Put(uint64(i)*0x100, valueOf(uint64(i), 4)) {
			t.Fatalf("Put %d failed", i)
		}
	}
	if inUse := pool.InUse(); inUse > int64(shardCount*perShard) {
		t.Fatalf("InUse = %d exceeds capacity", inUse)
	}
	c.Close()
	if pool.InUse() != 0 {
		t.Fatalf("InUse = %d after Close, want 0 (leak)", pool.InUse())
	}
	gets, puts, discarded := pool.Stats()
	if gets != puts+discarded {
		t.Fatalf("pool leak: gets=%d puts=%d discarded=%d", gets, puts, discarded)
	}
}

func TestCacheDelete(t *testing.T) {
	c, pool := newTestCache(4, 16)
	c.Put(1, valueOf(1, 4))
	c.Delete(1)
	if c.Len(1) != -1 {
		t.Fatal("Delete did not remove entry")
	}
	if pool.InUse() != 0 {
		t.Fatalf("Delete did not return buffer, InUse = %d", pool.InUse())
	}
	c.Delete(1) // deleting a missing key must be a no-op
}

func TestCacheZeroAllocHit(t *testing.T) {
	c, _ := newTestCache(4, 64)
	c.Put(42, valueOf(42, 37))
	dst := make([]uint64, 64)
	c.Get(42, dst) // warm up
	allocs := testing.AllocsPerRun(500, func() {
		if c.Get(42, dst) != 37 {
			t.Fatal("miss")
		}
	})
	if allocs != 0 {
		t.Fatalf("cache hit allocated %v times per run, want 0", allocs)
	}
}

func TestCacheConcurrentNoLeak(t *testing.T) {
	perShard := 8
	c, pool := newTestCache(perShard, 37)
	const workers = 16
	const iterations = 2000
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed uint64) {
			defer wg.Done()
			dst := make([]uint64, 37)
			for i := 0; i < iterations; i++ {
				key := (seed*7919 + uint64(i)*104729) % 500
				switch i % 4 {
				case 0:
					c.Put(key, valueOf(key, 37))
				case 1:
					c.Delete(key)
				default:
					c.Get(key, dst)
				}
			}
		}(uint64(w))
	}
	wg.Wait()
	c.Close()
	// Strict leak assertion after draining.
	if pool.InUse() != 0 {
		t.Fatalf("InUse = %d after Close, want 0 (leak)", pool.InUse())
	}
	gets, puts, discarded := pool.Stats()
	if gets != puts+discarded {
		t.Fatalf("pool leak: gets=%d puts=%d discarded=%d", gets, puts, discarded)
	}
	if pool.Available() != shardCount*perShard {
		t.Fatalf("Available = %d, want %d (buffers lost)",
			pool.Available(), shardCount*perShard)
	}
}
