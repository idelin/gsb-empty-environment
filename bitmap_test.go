package cbitmap

import (
	"math/rand"
	"runtime"
	"sync"
	"testing"
	"time"
)

// waitGoroutines settles and returns the number of runnable goroutines.
func goroutineCount() int {
	time.Sleep(50 * time.Millisecond)
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	return runtime.NumGoroutine()
}

func TestArrayToBitmapBoundary(t *testing.T) {
	b := New()
	// Exactly arrayMaxSize sparse values in one 64K block: stays array.
	for i := 0; i < arrayMaxSize; i++ {
		b.Add(uint32(i * 16)) // spread out so RLE is not chosen
	}
	if got := b.Stats().Array; got != 1 {
		t.Fatalf("expected 1 array container, got %+v", b.Stats())
	}
	// One more in the same block crosses the threshold: converts to bitmap.
	b.Add(1)
	st := b.Stats()
	if st.Bitmap != 1 || st.Array != 0 {
		t.Fatalf("expected conversion to bitmap container, got %+v", st)
	}
	if b.Cardinality() != arrayMaxSize+1 {
		t.Fatalf("cardinality = %d, want %d", b.Cardinality(), arrayMaxSize+1)
	}
}

func TestDenseRangeUsesRunContainer(t *testing.T) {
	b := New()
	b.AddRange(1000, 9999)
	st := b.Stats()
	if st.Run != 1 {
		t.Fatalf("expected 1 run container, got %+v", st)
	}
	if b.Cardinality() != 9000 {
		t.Fatalf("cardinality = %d, want 9000", b.Cardinality())
	}
	for _, id := range []uint32{1000, 5000, 9999} {
		if !b.Contains(id) {
			t.Fatalf("expected %d to be present", id)
		}
	}
	for _, id := range []uint32{999, 10000} {
		if b.Contains(id) {
			t.Fatalf("expected %d to be absent", id)
		}
	}
}

func TestRunContainerMerging(t *testing.T) {
	b := New()
	b.Add(10)
	b.Add(20)
	b.Add(30)
	// Bridge the gaps: 11..19 and 21..29 merge everything into one run.
	b.AddRange(11, 29)
	st := b.Stats()
	if st.Run != 1 {
		t.Fatalf("expected merged single run container, got %+v", st)
	}
	if b.Cardinality() != 21 {
		t.Fatalf("cardinality = %d, want 21", b.Cardinality())
	}
	for i := uint32(10); i <= 30; i++ {
		if !b.Contains(i) {
			t.Fatalf("expected %d present after merge", i)
		}
	}
}

func TestSparseAndDenseMixed(t *testing.T) {
	b := New()
	// Sparse discrete IDs across many blocks.
	sparse := []uint32{0, 7, 65535, 65536, 1 << 20, 1<<31 - 1, 1 << 31}
	for _, id := range sparse {
		b.Add(id)
	}
	// Dense contiguous ranges.
	b.AddRange(3_000_000, 3_100_000)
	b.AddRange(9_000_000_000%(1<<32), 9_000_000_000%(1<<32)+50_000)
	for _, id := range sparse {
		if !b.Contains(id) {
			t.Fatalf("sparse id %d missing", id)
		}
	}
	if !b.Contains(3_050_000) || b.Contains(3_100_001) {
		t.Fatal("dense range boundaries wrong")
	}
	st := b.Stats()
	if st.Run == 0 {
		t.Fatalf("expected run containers for dense ranges, got %+v", st)
	}
	if st.Cardinality != b.Cardinality() {
		t.Fatalf("stats cardinality %d != %d", st.Cardinality, b.Cardinality())
	}
}

// referenceSet is the ground-truth model for set-op tests.
type referenceSet map[uint32]struct{}

func buildRandom(t *testing.T, rng *rand.Rand, n int, denseRanges int) (*Bitmap, referenceSet) {
	t.Helper()
	b := New()
	ref := referenceSet{}
	for i := 0; i < n; i++ {
		id := rng.Uint32() % 5_000_000
		b.Add(id)
		ref[id] = struct{}{}
	}
	for i := 0; i < denseRanges; i++ {
		start := rng.Uint32() % 5_000_000
		end := start + rng.Uint32()%20_000
		b.AddRange(start, end)
		for id := start; id <= end; id++ {
			ref[id] = struct{}{}
		}
	}
	return b, ref
}

func checkAgainstReference(t *testing.T, b *Bitmap, ref referenceSet, what string) {
	t.Helper()
	if b.Cardinality() != uint64(len(ref)) {
		t.Fatalf("%s: cardinality = %d, want %d", what, b.Cardinality(), len(ref))
	}
	for id := range ref {
		if !b.Contains(id) {
			t.Fatalf("%s: id %d missing", what, id)
		}
	}
}

func TestSetOpsAgainstReference(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	a, refA := buildRandom(t, rng, 30_000, 15)
	b, refB := buildRandom(t, rng, 30_000, 15)

	andRef, orRef, xorRef := referenceSet{}, referenceSet{}, referenceSet{}
	for id := range refA {
		orRef[id] = struct{}{}
		if _, ok := refB[id]; ok {
			andRef[id] = struct{}{}
		} else {
			xorRef[id] = struct{}{}
		}
	}
	for id := range refB {
		orRef[id] = struct{}{}
		if _, ok := refA[id]; !ok {
			xorRef[id] = struct{}{}
		}
	}

	checkAgainstReference(t, a.And(b), andRef, "AND")
	checkAgainstReference(t, a.Or(b), orRef, "OR")
	checkAgainstReference(t, a.Xor(b), xorRef, "XOR")
}

func TestSetOpsEmptyAndSelf(t *testing.T) {
	a := New()
	a.AddRange(0, 100_000)
	empty := New()

	if got := a.And(empty).Cardinality(); got != 0 {
		t.Fatalf("A AND empty = %d, want 0", got)
	}
	if got := a.Or(empty).Cardinality(); got != 100_001 {
		t.Fatalf("A OR empty = %d, want 100001", got)
	}
	if got := a.Xor(a).Cardinality(); got != 0 {
		t.Fatalf("A XOR A = %d, want 0", got)
	}
	if got := a.And(a).Cardinality(); got != 100_001 {
		t.Fatalf("A AND A = %d, want 100001", got)
	}
	if !empty.IsEmpty() {
		t.Fatal("empty bitmap should be empty")
	}
}

func TestConcurrentAdd(t *testing.T) {
	b := New()
	const workers = 16
	const perWorker = 20_000
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < perWorker; i++ {
				// Overlapping ranges across goroutines to force CAS retries.
				b.Add(rng.Uint32() % 200_000)
			}
		}(int64(w))
	}
	// Concurrent dense-range writers and readers at the same time.
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(base uint32) {
			defer wg.Done()
			b.AddRange(base, base+50_000)
		}(uint32(w * 10_000))
	}
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50_000; i++ {
				_ = b.Contains(uint32(i))
			}
			_ = b.Cardinality()
		}()
	}
	wg.Wait()

	// Every dense-range id must be present.
	for w := 0; w < 4; w++ {
		for id := uint32(w * 10_000); id <= uint32(w*10_000)+50_000; id++ {
			if !b.Contains(id) {
				t.Fatalf("id %d lost under concurrency", id)
			}
		}
	}
}

func TestNoGoroutineLeak(t *testing.T) {
	before := goroutineCount()
	for i := 0; i < 20; i++ {
		a := New()
		b := New()
		a.AddRange(0, 1_000_000)
		b.AddRange(500_000, 1_500_000)
		_ = a.And(b)
		_ = a.Or(b)
		_ = a.Xor(b)
	}
	after := goroutineCount()
	if after > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}

func TestContainerTypeNames(t *testing.T) {
	b := New()
	b.Add(1)
	if b.Stats().Array != 1 {
		t.Fatal("single value should use array container")
	}
	c := New()
	c.AddRange(0, 65535) // full block: run container with one run
	if c.Stats().Run != 1 {
		t.Fatalf("full dense block should use run container, got %+v", c.Stats())
	}
	d := New()
	for i := 0; i < 65536; i += 2 { // dense but not RLE-friendly
		d.Add(uint32(i))
	}
	if d.Stats().Bitmap != 1 {
		t.Fatalf("dense scattered block should use bitmap container, got %+v", d.Stats())
	}
}

// ---------------------------------------------------------------------------
// Benchmarks: simulate massive-scale intersection/union. Each full 64K
// block is one bitmap container; 1024 containers ~= 67M values per bitmap.
// ---------------------------------------------------------------------------

func denseBitmap(blocks int) *Bitmap {
	b := New()
	for hi := 0; hi < blocks; hi++ {
		b.AddRange(uint32(hi)<<16, uint32(hi)<<16|0xffff)
	}
	return b
}

func BenchmarkAndDense(b *testing.B) {
	x := denseBitmap(1024)
	y := denseBitmap(1024)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = x.And(y)
	}
}

func BenchmarkOrDense(b *testing.B) {
	x := denseBitmap(1024)
	y := denseBitmap(1024)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = x.Or(y)
	}
}

func BenchmarkXorDense(b *testing.B) {
	x := denseBitmap(1024)
	y := denseBitmap(1024)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = x.Xor(y)
	}
}

func BenchmarkConcurrentAdd(b *testing.B) {
	bm := New()
	b.RunParallel(func(pb *testing.PB) {
		rng := rand.New(rand.NewSource(1))
		for pb.Next() {
			bm.Add(rng.Uint32() % 1_000_000)
		}
	})
}
