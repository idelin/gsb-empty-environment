package bitmap

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestConcurrentDisjointAdds: many nodes add disjoint ID ranges; the final
// cardinality must exactly match the number of successful Adds, and every
// successful Add must be immediately visible (linearizability).
func TestConcurrentDisjointAdds(t *testing.T) {
	bm := New()
	const nodes = 16
	const perNode = 5000
	var successes int64
	var wg sync.WaitGroup
	for n := 0; n < nodes; n++ {
		wg.Add(1)
		go func(node int) {
			defer wg.Done()
			nd := bm.Node(node)
			base := uint64(node) * perNode
			for i := 0; i < perNode; i++ {
				id := base + uint64(i)
				if nd.Add(id) {
					atomic.AddInt64(&successes, 1)
					// Linearizability: a committed Add is visible now.
					if !bm.Contains(id) {
						t.Errorf("Add(%d) committed but not visible", id)
						return
					}
				}
			}
		}(n)
	}
	wg.Wait()
	if got := bm.Cardinality(); got != uint64(successes) {
		t.Fatalf("cardinality=%d, successful adds=%d", got, successes)
	}
	if successes != nodes*perNode {
		t.Fatalf("successes=%d want %d", successes, nodes*perNode)
	}
}

// TestConcurrentOverlappingAdds: all nodes race to insert the SAME ids into
// the SAME containers; CAS arbitration must admit exactly one winner per id.
func TestConcurrentOverlappingAdds(t *testing.T) {
	bm := New()
	const nodes = 32
	const ids = 2000
	var successes int64
	var wg sync.WaitGroup
	for n := 0; n < nodes; n++ {
		wg.Add(1)
		go func(node int) {
			defer wg.Done()
			nd := bm.Node(node)
			for i := 0; i < ids; i++ {
				if nd.Add(uint64(i)) {
					atomic.AddInt64(&successes, 1)
				}
			}
		}(n)
	}
	wg.Wait()
	if successes != ids {
		t.Fatalf("successful adds=%d want exactly %d (CAS arbitration admitted duplicates)", successes, ids)
	}
	if got := bm.Cardinality(); got != ids {
		t.Fatalf("cardinality=%d want %d", got, ids)
	}
	for i := 0; i < ids; i++ {
		if !bm.Contains(uint64(i)) {
			t.Fatalf("missing id %d", i)
		}
	}
}

// TestConcurrentSameContainerStorm: heavy contention on a single container
// slot, mixing adds and removes, verified against exact arithmetic.
func TestConcurrentSameContainerStorm(t *testing.T) {
	bm := New()
	const nodes = 16
	const rounds = 500
	var wg sync.WaitGroup
	for n := 0; n < nodes; n++ {
		wg.Add(1)
		go func(node int) {
			defer wg.Done()
			nd := bm.Node(node)
			// Each node owns a disjoint stripe of one container's key space.
			for r := 0; r < rounds; r++ {
				id := uint64(node*rounds + r) // all < 65536: one container
				nd.Add(id)
			}
		}(n)
	}
	wg.Wait()
	if got := bm.Cardinality(); got != nodes*rounds {
		t.Fatalf("cardinality=%d want %d", got, nodes*rounds)
	}
	st := bm.Stats()
	if st.Containers != 1 {
		t.Fatalf("containers=%d want 1 (%+v)", st.Containers, st)
	}
	// Concurrent removes of exactly half the ids.
	for n := 0; n < nodes; n++ {
		wg.Add(1)
		go func(node int) {
			defer wg.Done()
			nd := bm.Node(node)
			for r := 0; r < rounds; r += 2 {
				nd.Remove(uint64(node*rounds + r))
			}
		}(n)
	}
	wg.Wait()
	if got := bm.Cardinality(); got != nodes*rounds/2 {
		t.Fatalf("after removes cardinality=%d want %d", got, nodes*rounds/2)
	}
}

// TestBatchAtomicityUnderContention: writers commit 4-id batches; readers
// must never observe a partially committed batch (0 or 4, never 1..3).
func TestBatchAtomicityUnderContention(t *testing.T) {
	bm := New()
	const writers = 8
	const batches = 300
	const batchSize = 4
	var stop int32
	var violations int64
	var wg sync.WaitGroup

	// Batch k of writer w: ids spread across DIFFERENT shards so the commit
	// must hold multiple shard locks to be atomic.
	batchIDs := func(w, k int) []uint64 {
		ids := make([]uint64, batchSize)
		for j := 0; j < batchSize; j++ {
			// Distinct container keys in distinct shards per j.
			ids[j] = (uint64(w*batches+k)*batchSize+uint64(j))*65536 + 7
		}
		return ids
	}

	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for atomic.LoadInt32(&stop) == 0 {
				for w := 0; w < writers; w++ {
					for k := 0; k < batches; k += 7 { // sample
						n := bm.CountPresent(batchIDs(w, k)...)
						if n != 0 && n != batchSize {
							atomic.AddInt64(&violations, 1)
						}
					}
				}
			}
		}()
	}
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			nd := bm.Node(w)
			for k := 0; k < batches; k++ {
				b := bm.Batch()
				for _, id := range batchIDs(w, k) {
					b.Add(id)
				}
				nd.Commit(b)
			}
		}(w)
	}
	// Poll until all batches are in.
	want := uint64(writers * batches * batchSize)
	for bm.Cardinality() != want {
		runtime.Gosched()
	}
	atomic.StoreInt32(&stop, 1)
	wg.Wait()
	if v := atomic.LoadInt64(&violations); v != 0 {
		t.Fatalf("observed %d partially committed batches (atomicity violation)", v)
	}
	if got := bm.Cardinality(); got != want {
		t.Fatalf("cardinality=%d want %d", got, want)
	}
}

// TestConcurrentReadWriteMix: readers hammer Contains/Cardinality/Stats/
// ForEach while writers mutate; run under -race to prove no data races.
func TestConcurrentReadWriteMix(t *testing.T) {
	bm := New()
	bm.AddRange(0, 9999)
	var stop int32
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			nd := bm.Node(w)
			base := uint64(10000 + w*10000)
			for i := 0; i < 2000; i++ {
				nd.Add(base + uint64(i))
				if i%3 == 0 {
					nd.Remove(base + uint64(i/2))
				}
			}
		}(w)
	}
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for atomic.LoadInt32(&stop) == 0 {
				bm.Contains(5000)
				bm.ContainsAll(1, 2, 3)
				_ = bm.Cardinality()
				_ = bm.Stats()
				bm.ForEach(func(uint64) {})
			}
		}()
	}
	time.Sleep(200 * time.Millisecond)
	atomic.StoreInt32(&stop, 1)
	wg.Wait()
}

// TestConcurrentSetOpsDuringWrites: AND/OR/XOR run concurrently with
// writers; results must be consistent with set invariants.
func TestConcurrentSetOpsDuringWrites(t *testing.T) {
	a, b := New(), New()
	a.AddRange(0, 49999)
	b.AddRange(25000, 74999)
	var stop int32
	var wg sync.WaitGroup
	var writerDone int32
	wg.Add(1)
	go func() {
		defer wg.Done()
		nd := a.Node(1)
		for i := 0; i < 2000; i++ {
			nd.Add(uint64(100000 + i))
		}
		atomic.StoreInt32(&writerDone, 1)
	}()
	for r := 0; r < 3; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for atomic.LoadInt32(&stop) == 0 {
				// Each op is internally consistent (per-shard linearizable);
				// facts independent of the racing writer must always hold:
				// the intersection is exactly [25000,49999] (writer only
				// adds ids >= 100000, outside b), and the union always
				// covers b's full range.
				if ic := a.And(b).Cardinality(); ic != 25000 {
					t.Errorf("intersection=%d want exactly 25000", ic)
					return
				}
				if uc := a.Or(b).Cardinality(); uc < 75000 {
					t.Errorf("union=%d want >= 75000", uc)
					return
				}
				if dc := a.Xor(b).Cardinality(); dc < 50000 {
					t.Errorf("xor=%d want >= 50000", dc)
					return
				}
			}
		}()
	}
	for atomic.LoadInt32(&writerDone) == 0 {
		runtime.Gosched()
	}
	time.Sleep(100 * time.Millisecond)
	atomic.StoreInt32(&stop, 1)
	wg.Wait()
	// Quiescent: the full set identity |A∪B| = |A∩B| + |A△B| must hold.
	ic := a.And(b).Cardinality()
	uc := a.Or(b).Cardinality()
	dc := a.Xor(b).Cardinality()
	if uc != ic+dc {
		t.Fatalf("set identity violated: |union|=%d |inter|=%d |xor|=%d", uc, ic, dc)
	}
	if a.Cardinality() != 52000 {
		t.Fatalf("a cardinality=%d want 52000", a.Cardinality())
	}
}

// TestConcurrentCommitsDisjoint: concurrent Commits to overlapping shard
// sets must serialize without deadlock and lose no updates.
func TestConcurrentCommitsDisjoint(t *testing.T) {
	bm := New()
	const nodes = 12
	const batches = 100
	var wg sync.WaitGroup
	for n := 0; n < nodes; n++ {
		wg.Add(1)
		go func(node int) {
			defer wg.Done()
			nd := bm.Node(node)
			for k := 0; k < batches; k++ {
				b := bm.Batch()
				// Each batch touches several shards.
				for j := 0; j < 4; j++ {
					id := uint64(node*batches*4+k*4+j)*65536 + 1
					b.Add(id)
				}
				nd.Commit(b)
			}
		}(n)
	}
	wg.Wait()
	if got := bm.Cardinality(); got != nodes*batches*4 {
		t.Fatalf("cardinality=%d want %d", got, nodes*batches*4)
	}
}

// TestNoGoroutineLeak: the bitmap spawns no background goroutines; after
// all work finishes the goroutine count must return to baseline.
func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	for round := 0; round < 3; round++ {
		bm := New()
		var wg sync.WaitGroup
		for n := 0; n < 8; n++ {
			wg.Add(1)
			go func(node int) {
				defer wg.Done()
				nd := bm.Node(node)
				for i := 0; i < 500; i++ {
					nd.Add(uint64(node*1000 + i))
				}
			}(n)
		}
		wg.Wait()
		_ = bm.Cardinality()
	}
	// Let any stragglers exit, then assert strictly no growth.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: before=%d after=%d", before, runtime.NumGoroutine())
}
