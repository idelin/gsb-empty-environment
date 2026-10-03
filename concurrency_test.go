package hexgrid

import (
	"runtime"
	"sync"
	"testing"
	"time"
)

// workload deterministically exercises encode, k-ring and hierarchy
// from many goroutines; the folded checksum must match the serial
// reference exactly, proving no shared mutable state.
func workloadChecksum(worker, iters int) uint64 {
	var sum uint64
	seed := uint64(worker)*1099087573 + 7
	for it := 0; it < iters; it++ {
		seed = seed*6364136223846793005 + 1442695040888963407
		lat := float64(seed%180000000)/1e6 - 90
		seed = seed*6364136223846793005 + 1442695040888963407
		lng := float64(seed%360000000)/1e6 - 180
		seed = seed*6364136223846793005 + 1442695040888963407
		res := int(seed % 9)
		c, err := LatLngToCell(lat, lng, res)
		if err != nil {
			continue
		}
		sum ^= uint64(c)
		for _, rc := range KRing(c, 2) {
			sum = sum*1099511628211 ^ uint64(rc)
		}
		if p, ok := c.Parent(); ok {
			sum ^= uint64(p)
			children, _ := p.Children()
			for _, ch := range children {
				sum = sum*1099511628211 ^ uint64(ch)
			}
		}
	}
	return sum
}

func TestConcurrentSafety(t *testing.T) {
	const workers = 16
	const iters = 300
	want := make([]uint64, workers)
	for w := 0; w < workers; w++ {
		want[w] = workloadChecksum(w, iters)
	}
	got := make([]uint64, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			got[w] = workloadChecksum(w, iters)
		}(w)
	}
	wg.Wait()
	for w := 0; w < workers; w++ {
		if got[w] != want[w] {
			t.Fatalf("worker %d: concurrent checksum %d != serial %d", w, got[w], want[w])
		}
	}
}

// TestNoGoroutineLeak asserts the package spawns no background
// goroutines and leaves none behind after heavy concurrent use.
func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	var wg sync.WaitGroup
	for w := 0; w < 32; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			workloadChecksum(w, 50)
		}(w)
	}
	wg.Wait()
	// Allow the scheduler a brief moment to retire worker goroutines.
	var after int
	for i := 0; i < 100; i++ {
		after = runtime.NumGoroutine()
		if after <= before {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if after > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}

// TestConcurrentResultIndependence hammers one cell from many
// goroutines and requires every returned ring to be identical and
// independently owned (no shared backing arrays).
func TestConcurrentResultIndependence(t *testing.T) {
	c, _ := LatLngToCell(39.9042, 116.4074, 8)
	ref := KRing(c, 3)
	const workers = 32
	errs := make(chan string, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := 0; it < 200; it++ {
				ring := KRing(c, 3)
				if len(ring) != len(ref) {
					errs <- "ring size mismatch"
					return
				}
				for i := range ring {
					if ring[i] != ref[i] {
						errs <- "ring content mismatch"
						return
					}
				}
				ring[0] = 0 // must not corrupt other results
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	again := KRing(c, 3)
	if again[0] != ref[0] {
		t.Fatal("shared state corrupted by mutated result")
	}
}
