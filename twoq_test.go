package paircache

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func fixedLoader(size int) Loader {
	return func(string) ([]byte, error) { return make([]byte, size), nil }
}

func acq(t *testing.T, c *TwoQ, key string) *Lease {
	t.Helper()
	l, err := c.Acquire(key)
	if err != nil {
		t.Fatalf("Acquire(%q): %v", key, err)
	}
	return l
}

func touch(t *testing.T, c *TwoQ, key string) {
	t.Helper()
	acq(t, c, key).Release()
}

// Same key, touched again after eviction from A1in, must be promoted into Am
// via the ghost queue, and must then survive a long sequential scan.
func TestSameKeyRepeatHitPromotion(t *testing.T) {
	c := NewTwoQ(Options{CapacityBytes: 512, Loader: fixedLoader(64)}) // recentCap = 128
	touch(t, c, "h")
	for i := 0; i < 4; i++ { // 4*64 = 256 > recentCap: pushes h out of A1in
		touch(t, c, fmt.Sprintf("s%d", i))
	}
	if _, ok := c.ghostIx["h"]; !ok {
		t.Fatal("h should have left a ghost record in A1out")
	}
	touch(t, c, "h") // second touch: ghost hit -> promote to Am
	if s := c.Stats(); s.GhostHits != 1 {
		t.Fatalf("GhostHits = %d, want 1", s.GhostHits)
	}
	if _, ok := c.freqIx["h"]; !ok {
		t.Fatal("h should be promoted into Am after repeat access")
	}
	for i := 0; i < 30; i++ { // scan churns A1in only
		touch(t, c, fmt.Sprintf("x%d", i))
	}
	if _, ok := c.freqIx["h"]; !ok {
		t.Fatal("sequential scan evicted hot key from Am")
	}
	before := c.Stats().Hits
	touch(t, c, "h")
	if got := c.Stats().Hits - before; got != 1 {
		t.Fatalf("hot key hit after scan: got %d new hits, want 1", got)
	}
}

// A sequential scan must not flush the hot set; plain LRU loses it.
func TestScanResistanceVsLRU(t *testing.T) {
	const capBytes = 4096 // 32 entries of 128B
	loader := fixedLoader(128)
	run := func(newCache func() interface {
		Acquire(string) (*Lease, error)
		Stats() Stats
	}) int64 {
		c := newCache()
		touchN := func(prefix string, n int) {
			for i := 0; i < n; i++ {
				l, err := c.Acquire(fmt.Sprintf("%s%d", prefix, i))
				if err != nil {
					t.Fatalf("Acquire: %v", err)
				}
				l.Release()
			}
		}
		touchN("h", 8)
		touchN("w", 40)
		touchN("h", 8)
		touchN("s", 64)
		before := c.Stats().Hits
		touchN("h", 8)
		return c.Stats().Hits - before
	}

	hits2Q := run(func() interface {
		Acquire(string) (*Lease, error)
		Stats() Stats
	} {
		return NewTwoQ(Options{CapacityBytes: capBytes, Loader: loader})
	})
	hitsLRU := run(func() interface {
		Acquire(string) (*Lease, error)
		Stats() Stats
	} {
		return NewLRU(Options{CapacityBytes: capBytes, Loader: loader})
	})
	if hits2Q != 8 {
		t.Fatalf("2Q kept %d/8 hot keys after scan, want 8", hits2Q)
	}
	if hitsLRU != 0 {
		t.Fatalf("LRU kept %d/8 hot keys after scan, want 0 (baseline)", hitsLRU)
	}
}

// Capacity holding exactly one entry: inserts and promotions still work and
// resident bytes never exceed capacity.
func TestCapacityExactlyOneEntry(t *testing.T) {
	c := NewTwoQ(Options{CapacityBytes: 100, Loader: fixedLoader(100)})
	touch(t, c, "a")
	touch(t, c, "a") // ghost hit -> Am
	if got := c.Stats().ResidentBytes; got != 100 {
		t.Fatalf("ResidentBytes = %d, want 100", got)
	}
	touch(t, c, "b")
	touch(t, c, "b") // b promoted, a evicted from Am
	if _, ok := c.freqIx["a"]; ok {
		t.Fatal("a should have been evicted from Am")
	}
	if _, ok := c.freqIx["b"]; !ok {
		t.Fatal("b should be resident in Am")
	}
	if got := c.Stats().ResidentBytes; got != 100 {
		t.Fatalf("ResidentBytes = %d, want 100", got)
	}
}

// Byte-capacity 1: degenerate but must terminate and stay within capacity.
func TestCapacityOneByte(t *testing.T) {
	for _, newC := range []func() interface {
		Acquire(string) (*Lease, error)
		Stats() Stats
	}{
		func() interface {
			Acquire(string) (*Lease, error)
			Stats() Stats
		} {
			return NewTwoQ(Options{CapacityBytes: 1, Loader: fixedLoader(1)})
		},
		func() interface {
			Acquire(string) (*Lease, error)
			Stats() Stats
		} {
			return NewLRU(Options{CapacityBytes: 1, Loader: fixedLoader(1)})
		},
	} {
		c := newC()
		for i := 0; i < 50; i++ {
			l, err := c.Acquire(fmt.Sprintf("k%d", i%3))
			if err != nil {
				t.Fatalf("Acquire: %v", err)
			}
			l.Release()
			if got := c.Stats().ResidentBytes; got > 1 {
				t.Fatalf("ResidentBytes = %d exceeds capacity 1", got)
			}
		}
	}
}

// An evicted entry with an outstanding lease must not be freed until the
// lease is released; physical bytes exceed logical capacity meanwhile.
func TestLeasePinsEvictedMemory(t *testing.T) {
	var freed []string
	c := NewTwoQ(Options{
		CapacityBytes: 256, // recentCap = 64
		Loader:        fixedLoader(64),
		OnFree:        func(k string, _ int64) { freed = append(freed, k) },
	})
	lease := acq(t, c, "a") // held, not released
	touch(t, c, "b")        // 128 > recentCap: evicts a from A1in
	if _, ok := c.recentIx["a"]; ok {
		t.Fatal("a should be evicted from A1in")
	}
	if len(freed) != 0 {
		t.Fatalf("leased entry freed early: %v", freed)
	}
	if got := c.Stats().PhysicalBytes; got != 128 {
		t.Fatalf("PhysicalBytes = %d, want 128 (evicted a still leased)", got)
	}
	if len(lease.Value()) != 64 {
		t.Fatal("leased value must remain readable after eviction")
	}
	lease.Release()
	if len(freed) != 1 || freed[0] != "a" {
		t.Fatalf("after release freed = %v, want [a]", freed)
	}
	if got := c.Stats().PhysicalBytes; got != 64 {
		t.Fatalf("PhysicalBytes = %d, want 64 after release", got)
	}
}

// Deleted keys leave ghost records, but the ghost queue is bounded and
// reclaimed FIFO: deletes cannot grow metadata without limit.
func TestDeleteGhostRecordsBounded(t *testing.T) {
	c := NewTwoQ(Options{CapacityBytes: 1 << 20, Loader: fixedLoader(64), GhostLimit: 4})
	for i := 0; i < 100; i++ {
		k := fmt.Sprintf("k%d", i)
		touch(t, c, k)
		c.Delete(k)
	}
	if n := c.Stats().GhostEntries; n != 4 {
		t.Fatalf("GhostEntries = %d, want bounded to 4", n)
	}
	if _, ok := c.ghostIx["k0"]; ok {
		t.Fatal("oldest ghost record should have been reclaimed")
	}
	if _, ok := c.ghostIx["k99"]; !ok {
		t.Fatal("newest ghost record should be retained")
	}
	if got := c.Stats().ResidentEntries; got != 0 {
		t.Fatalf("ResidentEntries = %d, want 0", got)
	}
	if got := c.Stats().PhysicalBytes; got != 0 {
		t.Fatalf("PhysicalBytes = %d, want 0", got)
	}
}

// Loader failures propagate, are counted, and do not pollute the cache.
func TestLoaderFailureInjection(t *testing.T) {
	boom := errors.New("backend down")
	c := NewTwoQ(Options{CapacityBytes: 1024, Loader: func(k string) ([]byte, error) {
		if k == "bad" {
			return nil, boom
		}
		return make([]byte, 64), nil
	}})
	if _, err := c.Acquire("bad"); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	s := c.Stats()
	if s.LoadErrors != 1 || s.Misses != 1 || s.ResidentEntries != 0 || s.PhysicalBytes != 0 {
		t.Fatalf("after failure: %+v", s)
	}
	touch(t, c, "good")
	touch(t, c, "good")
	if got := c.Stats().Hits; got != 1 {
		t.Fatalf("Hits = %d, want 1 (cache still works after failure)", got)
	}
}

// The clock is injectable and recorded timestamps land in the snapshot.
func TestClockInjection(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	c := NewTwoQ(Options{
		CapacityBytes: 1024,
		Loader:        fixedLoader(64),
		Now:           func() time.Time { return now },
	})
	touch(t, c, "a")
	t1 := now
	now = now.Add(time.Hour)
	touch(t, c, "b")
	t2 := now
	data, err := c.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreTwoQ(data, Options{Loader: fixedLoader(64)})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]time.Time{}
	for el := restored.recent.Front(); el != nil; el = el.Next() {
		e := el.Value.(*entry)
		got[e.key] = e.at
	}
	if !got["a"].Equal(t1) || !got["b"].Equal(t2) {
		t.Fatalf("restored timestamps = %v, want a=%v b=%v", got, t1, t2)
	}
}

// Every allocated byte is freed exactly once after eviction + lease release.
func TestResourceRelease(t *testing.T) {
	freed := map[string]int{}
	c := NewTwoQ(Options{
		CapacityBytes: 512,
		Loader:        fixedLoader(64),
		OnFree:        func(k string, _ int64) { freed[k]++ },
	})
	var leases []*Lease
	for i := 0; i < 50; i++ {
		leases = append(leases, acq(t, c, fmt.Sprintf("k%d", i)))
	}
	for _, l := range leases {
		l.Release()
	}
	for i := 0; i < 50; i++ { // evict whatever is still resident
		c.Delete(fmt.Sprintf("k%d", i))
	}
	if got := c.Stats().PhysicalBytes; got != 0 {
		t.Fatalf("PhysicalBytes = %d, want 0 after full release", got)
	}
	for i := 0; i < 50; i++ {
		k := fmt.Sprintf("k%d", i)
		if freed[k] != 1 {
			t.Fatalf("%s freed %d times, want exactly 1", k, freed[k])
		}
	}
}

// Concurrent acquire/release must stay race-free and leak nothing.
func TestConcurrentAcquireRelease(t *testing.T) {
	c := NewTwoQ(Options{CapacityBytes: 4096, Loader: fixedLoader(64)})
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				l, err := c.Acquire(fmt.Sprintf("k%d", (g*7+i)%100))
				if err == nil {
					_ = l.Value()
					l.Release()
				}
			}
		}(g)
	}
	wg.Wait()
	s := c.Stats()
	if s.PhysicalBytes != s.ResidentBytes {
		t.Fatalf("PhysicalBytes %d != ResidentBytes %d after all releases", s.PhysicalBytes, s.ResidentBytes)
	}
}
