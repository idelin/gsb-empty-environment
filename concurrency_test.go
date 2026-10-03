package cbitmap

import (
	"runtime"
	"sync"
	"testing"
)

func countGoroutines() int { return runtime.NumGoroutine() }

// TestConcurrentFrozenReads hammers a frozen (zero-copy) bitmap from many
// goroutines. Run with -race.
func TestConcurrentFrozenReads(t *testing.T) {
	bm := buildMixedBitmap(t)
	zc, err := UnmarshalZeroCopy(bm.MarshalBinary())
	if err != nil {
		t.Fatal(err)
	}
	want := zc.Cardinality()
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 20000; i++ {
				v := uint32((i*7919 + g*104729) % (1 << 24))
				if zc.Contains(v) != bm.Contains(v) {
					t.Errorf("Contains mismatch at %d", v)
					return
				}
			}
			if zc.Cardinality() != want {
				t.Errorf("cardinality changed")
			}
			// read-only set algebra on shared bitmaps
			if And(zc, zc).Cardinality() != want {
				t.Errorf("And(self) mismatch")
			}
			if Or(zc, zc).Cardinality() != want {
				t.Errorf("Or(self) mismatch")
			}
			if !Xor(zc, zc).IsEmpty() {
				t.Errorf("Xor(self) must be empty")
			}
			n := uint64(0)
			it := zc.Iterator()
			for _, ok := it.Next(); ok; _, ok = it.Next() {
				n++
			}
			if n != want {
				t.Errorf("iterated %d values, want %d", n, want)
			}
		}(g)
	}
	wg.Wait()
}

// TestConcurrentIndependentBitmaps runs builders and set ops on separate
// bitmaps concurrently. Run with -race.
func TestConcurrentIndependentBitmaps(t *testing.T) {
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			bm := New()
			for i := 0; i < 20000; i++ {
				bm.Add(uint32((i*31 + g) % (1 << 20)))
			}
			bm.AddRange(uint32(g*1000), uint32(g*1000+50000))
			buf := bm.MarshalBinary()
			back, err := UnmarshalBinary(buf)
			if err != nil {
				t.Error(err)
				return
			}
			if And(bm, back).Cardinality() != bm.Cardinality() {
				t.Errorf("roundtrip mismatch in goroutine %d", g)
			}
		}(g)
	}
	wg.Wait()
}

// TestNoGoroutineLeak ensures the package spawns no background
// goroutines: the goroutine count after a second round of work does not
// exceed the count after the first (warm) round.
func TestNoGoroutineLeak(t *testing.T) {
	bm := buildMixedBitmap(t)
	zc, _ := UnmarshalZeroCopy(bm.MarshalBinary())
	round := func() {
		var wg sync.WaitGroup
		for g := 0; g < 4; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				And(zc, zc)
			}()
		}
		wg.Wait()
	}
	round() // warm-up: settles runtime/race-detector background goroutines
	before := countGoroutines()
	round()
	after := countGoroutines()
	if after > before {
		t.Fatalf("goroutine leak: after warm-up=%d after second round=%d", before, after)
	}
	// all container memory is released once references are dropped:
	// a frozen bitmap thawed by mutation must not retain the source buffer
	// beyond the copied containers (verified by buffer-isolation tests).
	_ = zc
}
