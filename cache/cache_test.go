package cache

import (
	"bytes"
	"fmt"
	"sync"
	"testing"

	"protowire/pool"
)

func TestPutGetRoundTrip(t *testing.T) {
	p := pool.NewBufferPool()
	c := New(p, 8)
	defer c.Close()

	c.Put(1, []byte("alpha"))
	c.Put(2, []byte("beta"))

	got, ok := c.Get(1, nil)
	if !ok || string(got) != "alpha" {
		t.Fatalf("get 1: %q ok=%v", got, ok)
	}
	if _, ok := c.Get(999, nil); ok {
		t.Fatal("unexpected hit for missing key")
	}
	got, ok = c.Get(2, got[:0]) // reuse dst
	if !ok || string(got) != "beta" {
		t.Fatalf("get 2: %q ok=%v", got, ok)
	}
}

func TestOverwrite(t *testing.T) {
	p := pool.NewBufferPool()
	c := New(p, 4)
	defer c.Close()

	c.Put(7, []byte("first"))
	c.Put(7, []byte("second-longer"))
	got, ok := c.Get(7, nil)
	if !ok || string(got) != "second-longer" {
		t.Fatalf("overwrite: %q ok=%v", got, ok)
	}
	// Overwrite with a shorter value into the same lease.
	c.Put(7, []byte("x"))
	got, _ = c.Get(7, nil)
	if string(got) != "x" {
		t.Fatalf("shrink overwrite: %q", got)
	}
	if s := c.Stats(); s.Entries != 1 {
		t.Fatalf("entries after overwrite: %d, want 1", s.Entries)
	}
}

func TestEviction(t *testing.T) {
	p := pool.NewBufferPool()
	const maxPerShard = 4
	c := New(p, maxPerShard)

	// Insert far more keys than total capacity: every shard must fill to
	// exactly maxPerShard and never exceed it.
	const capacity = numShards * maxPerShard
	const inserts = capacity * 4
	for i := 0; i < inserts; i++ {
		c.Put(uint64(i), []byte(fmt.Sprintf("v%d", i)))
	}
	s := c.Stats()
	if s.Entries != capacity {
		t.Fatalf("entries: %d, want %d (capacity must hold)", s.Entries, capacity)
	}
	if s.Evictions != inserts-capacity {
		t.Fatalf("evictions: %d, want %d", s.Evictions, inserts-capacity)
	}
	// Inserting still more must keep the entry count pinned at capacity.
	for i := inserts; i < inserts+numShards; i++ {
		c.Put(uint64(i), []byte("overflow"))
	}
	if s = c.Stats(); s.Entries != capacity {
		t.Fatalf("entries after overflow: %d, want %d", s.Entries, capacity)
	}
	c.Close()
	if ps := p.Stats(); ps.InUse() != 0 {
		t.Fatalf("buffer leak after Close: %+v", ps)
	}
}

func TestDelete(t *testing.T) {
	p := pool.NewBufferPool()
	c := New(p, 4)
	c.Put(1, []byte("a"))
	c.Put(2, []byte("b"))
	c.Delete(1)
	if _, ok := c.Get(1, nil); ok {
		t.Fatal("deleted key still present")
	}
	if _, ok := c.Get(2, nil); !ok {
		t.Fatal("unrelated key lost after delete")
	}
	c.Delete(1) // deleting a missing key is a no-op
	c.Close()
	if ps := p.Stats(); ps.InUse() != 0 {
		t.Fatalf("buffer leak: %+v", ps)
	}
}

func TestValueIsolation(t *testing.T) {
	// Mutating the source slice after Put must not corrupt the cache,
	// and the returned copy must not alias cached storage.
	p := pool.NewBufferPool()
	c := New(p, 4)
	defer c.Close()

	src := []byte("mutable")
	c.Put(1, src)
	src[0] = 'X'
	got, _ := c.Get(1, nil)
	if string(got) != "mutable" {
		t.Fatalf("cache aliases caller memory: %q", got)
	}
}

func TestZeroAllocGet(t *testing.T) {
	p := pool.NewBufferPool()
	c := New(p, 4)
	defer c.Close()
	c.Put(42, bytes.Repeat([]byte("z"), 100))

	dst := make([]byte, 0, 128)
	allocs := testing.AllocsPerRun(1000, func() {
		dst, _ = c.Get(42, dst[:0])
	})
	if allocs != 0 {
		t.Fatalf("Get allocated %v times per run, want 0", allocs)
	}
	if len(dst) != 100 {
		t.Fatalf("dst len: %d", len(dst))
	}
}

func TestConcurrent(t *testing.T) {
	p := pool.NewBufferPool()
	c := New(p, 64)

	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			dst := make([]byte, 0, 64)
			for i := 0; i < 2000; i++ {
				key := uint64((g*131 + i) % 500)
				c.Put(key, []byte(fmt.Sprintf("g%d-i%d", g, i)))
				if v, ok := c.Get(key, dst[:0]); ok && len(v) == 0 {
					t.Errorf("empty value for present key %d", key)
				}
				if i%7 == 0 {
					c.Delete(key)
				}
			}
		}(g)
	}
	wg.Wait()
	c.Close()
	if ps := p.Stats(); ps.InUse() != 0 {
		t.Fatalf("buffer leak after concurrent run: %+v", ps)
	}
}

func TestStatsCounters(t *testing.T) {
	p := pool.NewBufferPool()
	c := New(p, 4)
	defer c.Close()

	c.Put(1, []byte("a"))
	_, _ = c.Get(1, nil) // hit
	_, _ = c.Get(2, nil) // miss
	s := c.Stats()
	if s.Gets != 2 || s.Hits != 1 {
		t.Fatalf("stats: %+v, want Gets=2 Hits=1", s)
	}
}
