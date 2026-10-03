package roaring

import (
	"sync"
	"testing"
)

// TestConcurrentBitmap hammers a ConcurrentBitmap from many goroutines.
// Each writer owns a disjoint key range, so the final state is exact.
// Run with -race to catch data races.
func TestConcurrentBitmap(t *testing.T) {
	cb := NewConcurrent()
	const workers = 8
	const perWorker = 20_000
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			base := uint32(w * perWorker)
			for i := 0; i < perWorker; i++ {
				v := base + uint32(i)
				cb.Add(v)
				if i%3 == 0 {
					cb.Contains(v)
				}
				if i%5 == 0 {
					cb.Cardinality()
				}
			}
		}(w)
	}
	wg.Wait()
	if got := cb.Cardinality(); got != uint64(workers*perWorker) {
		t.Fatalf("cardinality = %d, want %d", got, workers*perWorker)
	}
	for w := 0; w < workers; w++ {
		base := uint32(w * perWorker)
		for i := 0; i < perWorker; i += 97 {
			if !cb.Contains(base + uint32(i)) {
				t.Fatalf("missing %d", base+uint32(i))
			}
		}
	}
}

// TestConcurrentSnapshotOps runs set operations on snapshots while other
// goroutines keep mutating the source bitmaps.
func TestConcurrentSnapshotOps(t *testing.T) {
	a := NewConcurrent()
	b := NewConcurrent()
	stop := make(chan struct{})
	var writers sync.WaitGroup
	for w := 0; w < 4; w++ {
		writers.Add(1)
		go func(w int) {
			defer writers.Done()
			base := uint32(w * 100_000)
			for i := 0; i < 50_000; i++ {
				a.Add(base + uint32(i))
				b.Add(base/2 + uint32(i))
			}
		}(w)
	}
	checkerDone := make(chan struct{})
	go func() {
		defer close(checkerDone)
		for {
			select {
			case <-stop:
				return
			default:
			}
			// All three results must come from the same pair of
			// snapshots for the set identities to hold.
			sa := a.Snapshot()
			sb := b.Snapshot()
			inter := sa.And(sb)
			if err := inter.Validate(); err != nil {
				t.Errorf("intersection invalid: %v", err)
				return
			}
			union := sa.Or(sb)
			if union.Cardinality() < inter.Cardinality() {
				t.Error("union smaller than intersection")
				return
			}
			x := sa.Xor(sb)
			if x.Cardinality()+inter.Cardinality() != union.Cardinality() {
				t.Error("xor + and != or")
				return
			}
		}
	}()
	writers.Wait()
	close(stop)
	<-checkerDone
}
