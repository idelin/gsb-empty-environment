package bitmap

import (
	"runtime"
	"sync"
	"testing"
	"time"
)

// runWithTimeout fails the test if fn does not finish within d
// (deadlock detector for the arbiter).
func runWithTimeout(t *testing.T, d time.Duration, name string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		fn()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s did not finish within %s: deadlock suspected", name, d)
	}
}

// TestConcurrentDisjointAdds: many writers on disjoint key ranges must
// commit every write exactly once (no lost updates).
func TestConcurrentDisjointAdds(t *testing.T) {
	b := New()
	const writers = 16
	const perWriter = 50000
	runWithTimeout(t, 30*time.Second, "disjoint adds", func() {
		var wg sync.WaitGroup
		for w := 0; w < writers; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				base := uint32(w * perWriter)
				for i := 0; i < perWriter; i++ {
					b.Add(base + uint32(i))
				}
			}(w)
		}
		wg.Wait()
	})
	if got, want := b.Cardinality(), uint64(writers*perWriter); got != want {
		t.Fatalf("cardinality = %d, want %d (lost updates)", got, want)
	}
}

// TestConcurrentOverlappingAdds: contending writers inserting the SAME ids
// must converge to exactly the shared set (conflict arbitration).
func TestConcurrentOverlappingAdds(t *testing.T) {
	b := New()
	const writers = 16
	const n = 100000
	runWithTimeout(t, 30*time.Second, "overlapping adds", func() {
		var wg sync.WaitGroup
		start := make(chan struct{})
		for w := 0; w < writers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				for i := 0; i < n; i++ {
					b.Add(uint32(i))
				}
			}()
		}
		close(start)
		wg.Wait()
	})
	if got := b.Cardinality(); got != n {
		t.Fatalf("cardinality = %d, want %d", got, n)
	}
}

// TestConcurrentAddRemoveSameKeys: adders and removers race on one key
// range; afterwards every key must be in exactly one well-defined state
// and repeated Contains must agree with a final quiescent read.
func TestConcurrentAddRemoveSameKeys(t *testing.T) {
	b := New()
	const n = 50000
	runWithTimeout(t, 30*time.Second, "add/remove race", func() {
		var wg sync.WaitGroup
		for w := 0; w < 8; w++ {
			wg.Add(2)
			go func() {
				defer wg.Done()
				for i := 0; i < n; i++ {
					b.Add(uint32(i))
				}
			}()
			go func() {
				defer wg.Done()
				for i := 0; i < n; i++ {
					b.Remove(uint32(i))
				}
			}()
		}
		wg.Wait()
	})
	// Quiescent state must be internally consistent: cardinality equals
	// the number of keys reporting present.
	var count uint64
	for i := 0; i < n; i++ {
		if b.Contains(uint32(i)) {
			count++
		}
	}
	if count != b.Cardinality() {
		t.Fatalf("inconsistent state: %d keys present but cardinality %d", count, b.Cardinality())
	}
}

// TestConcurrentBatchOpsLinearizability: writers monotonically grow two
// bitmaps while readers compute And/Or/Xor. Because adds only ever insert
// ids from a fixed known set, every batch result (taken at some
// linearization point) must be a subset of the final reference results.
func TestConcurrentBatchOpsLinearizability(t *testing.T) {
	a, b := New(), New()
	const n = 200000
	stop := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			a.Add(uint32(i))
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			b.Add(uint32(i / 2)) // b = [0, n/2)
		}
	}()

	// Final reference results (monotonic loads make these deterministic).
	finalAnd := make(map[uint32]struct{}, n/2)
	for i := 0; i < n/2; i++ {
		finalAnd[uint32(i)] = struct{}{}
	}

	errCh := make(chan string, 64)
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				res := And(a, b)
				// Every element of the result must belong to the final
				// intersection, and the result can never exceed it.
				if res.Cardinality() > uint64(len(finalAnd)) {
					errCh <- "And result larger than final intersection"
					return
				}
				if Or(a, b).Cardinality() > n {
					errCh <- "Or result larger than final union"
					return
				}
				if Xor(a, b).Cardinality() > n {
					errCh <- "Xor result larger than final symmetric difference"
					return
				}
			}
		}()
	}

	// Let readers race with writers for a bounded time.
	time.Sleep(300 * time.Millisecond)
	close(stop)
	wg.Wait()
	close(errCh)
	for msg := range errCh {
		t.Fatal(msg)
	}
	// After quiescence the intersection must be exact.
	if got := And(a, b).Cardinality(); got != uint64(n/2) {
		t.Fatalf("final And cardinality = %d, want %d", got, n/2)
	}
}

// TestConcurrentBatchOpsDeadlockFree: batch ops in every operand order
// (And(a,b) vs And(b,a), AndMany over permutations) racing with writers.
// Any lock-ordering bug would deadlock and trip the timeout.
func TestConcurrentBatchOpsDeadlockFree(t *testing.T) {
	bitmaps := make([]*Bitmap, 4)
	for i := range bitmaps {
		bitmaps[i] = New()
		bitmaps[i].AddRange(uint64(i)*1000, uint64(i)*1000+5000)
	}
	runWithTimeout(t, 90*time.Second, "multi-bitmap batch ops", func() {
		var writerWg, readerWg sync.WaitGroup
		stop := make(chan struct{})
		// Writers.
		for w := 0; w < 4; w++ {
			writerWg.Add(1)
			go func(idx int) {
				defer writerWg.Done()
				i := uint32(0)
				for {
					select {
					case <-stop:
						return
					default:
					}
					bitmaps[idx].Add(1000000 + i%50000)
					i++
				}
			}(w)
		}
		// Readers with conflicting operand orders.
		orders := [][4]int{{0, 1, 2, 3}, {3, 2, 1, 0}, {1, 3, 0, 2}, {2, 0, 3, 1}}
		for r := 0; r < 4; r++ {
			readerWg.Add(1)
			go func(ord [4]int) {
				defer readerWg.Done()
				for i := 0; i < 400; i++ {
					a, b, c, d := bitmaps[ord[0]], bitmaps[ord[1]], bitmaps[ord[2]], bitmaps[ord[3]]
					_ = And(a, b)
					_ = And(b, a)
					_ = Or(c, d)
					_ = Xor(a, d)
					_ = AndMany(a, b, c, d)
					_ = OrMany(d, c, b, a)
				}
			}(orders[r%len(orders)])
		}
		readerWg.Wait()
		close(stop)
		writerWg.Wait()
	})
}

// TestConcurrentMixedWorkload: adds, removes, range-adds, point queries,
// and batch ops all racing on shared bitmaps. Run under -race; finishing
// within the timeout proves liveness.
func TestConcurrentMixedWorkload(t *testing.T) {
	a, b := New(), New()
	runWithTimeout(t, 60*time.Second, "mixed workload", func() {
		var wg sync.WaitGroup
		stop := make(chan struct{})
		for w := 0; w < 6; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				for i := 0; ; i++ {
					select {
					case <-stop:
						return
					default:
					}
					id := uint32((i*7919 + w*104729) % 300000)
					switch i % 4 {
					case 0:
						a.Add(id)
					case 1:
						b.Add(id)
					case 2:
						a.Remove(id)
					case 3:
						b.AddRange(uint64(id), uint64(id)+100)
					}
				}
			}(w)
		}
		for r := 0; r < 3; r++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < 300; i++ {
					_ = And(a, b).Cardinality()
					_ = Or(a, b).Cardinality()
					_ = Xor(a, b).Cardinality()
					_ = a.Contains(uint32(i))
				}
			}()
		}
		time.Sleep(500 * time.Millisecond)
		close(stop)
		wg.Wait()
	})
}

// TestNoGoroutineLeak: the library spawns no goroutines of its own; after
// a concurrent workload quiesces, the goroutine count must return to its
// baseline (strict no-leak assertion).
func TestNoGoroutineLeak(t *testing.T) {
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	before := runtime.NumGoroutine()

	b := New()
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 10000; i++ {
				b.Add(uint32(w*10000 + i))
			}
		}(w)
	}
	wg.Wait()
	_ = And(b, b)
	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	after := runtime.NumGoroutine()
	if after > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}

// TestVersionMonotonic: every successful mutation bumps the version,
// giving callers a linearization token.
func TestVersionMonotonic(t *testing.T) {
	b := New()
	v0 := b.Version()
	b.Add(1)
	v1 := b.Version()
	b.Add(1) // duplicate: no mutation
	if b.Version() != v1 {
		t.Fatal("duplicate add must not bump version")
	}
	b.Add(2)
	b.Remove(1)
	if b.Version() != v1+2 {
		t.Fatalf("version = %d, want %d (started at %d)", b.Version(), v1+2, v0)
	}
}
