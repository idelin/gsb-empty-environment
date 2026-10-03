package buffer

import (
	"sync"
	"testing"

	"aries/metrics"
)

func newTestPool(cap int) (*Pool, *metrics.Counter, *metrics.Counter) {
	hits, misses := &metrics.Counter{}, &metrics.Counter{}
	return NewPool(cap, hits, misses), hits, misses
}

func TestFetchHitMissAndRate(t *testing.T) {
	p, hits, misses := newTestPool(4)
	p.Fetch(1) // miss
	p.Fetch(1) // hit
	p.Fetch(1) // hit
	if hits.Value() != 2 || misses.Value() != 1 {
		t.Fatalf("hits/misses = %d/%d, want 2/1", hits.Value(), misses.Value())
	}
	if got := p.HitRate(); got != 2.0/3.0 {
		t.Fatalf("hit rate = %v, want 2/3", got)
	}
}

func TestHitRateZeroAccesses(t *testing.T) {
	p, _, _ := newTestPool(4)
	if p.HitRate() != 0 {
		t.Fatalf("hit rate with no accesses = %v, want 0", p.HitRate())
	}
}

func TestEvictionWritesBackDirty(t *testing.T) {
	p, _, _ := newTestPool(2)
	pg := p.Fetch(1)
	pg.Data = []byte("dirty")
	pg.Dirty = true
	p.Fetch(2)
	p.Fetch(3) // evicts page 1
	d, ok := p.DiskPage(1)
	if !ok || string(d.Data) != "dirty" {
		t.Fatalf("evicted dirty page not written back: %v %q", ok, d.Data)
	}
}

func TestCrashLosesUnflushed(t *testing.T) {
	p, _, _ := newTestPool(4)
	pg := p.Fetch(1)
	pg.Data = []byte("volatile")
	pg.Dirty = true
	p.Crash()
	if _, ok := p.DiskPage(1); ok {
		t.Fatal("unflushed page survived crash")
	}
}

func TestConcurrentFetch(t *testing.T) {
	p, hits, misses := newTestPool(8)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(id uint64) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				p.Fetch(id % 4)
			}
		}(uint64(i))
	}
	wg.Wait()
	p.FlushAll()
	if hits.Value()+misses.Value() != 16*100 {
		t.Fatalf("accounting lost updates: %d + %d", hits.Value(), misses.Value())
	}
}
