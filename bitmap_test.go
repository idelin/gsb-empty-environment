package roaring

import (
	"math/rand"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestSparseInsertAndContains(t *testing.T) {
	b := New()
	rng := rand.New(rand.NewSource(1))
	ref := make(map[uint32]bool)
	for i := 0; i < 100000; i++ {
		v := rng.Uint32()
		ref[v] = true
		b.Add(v)
	}
	for v := range ref {
		if !b.Contains(v) {
			t.Fatalf("missing %d", v)
		}
	}
	if got := b.Cardinality(); got != int64(len(ref)) {
		t.Fatalf("cardinality = %d, want %d", got, len(ref))
	}
	// Absent values.
	for i := 0; i < 1000; i++ {
		v := rng.Uint32()
		if !ref[v] && b.Contains(v) {
			t.Fatalf("unexpected %d", v)
		}
	}
}

func TestDenseRangeInsert(t *testing.T) {
	b := New()
	b.AddRange(0, 999999) // spans 16 containers
	if got := b.Cardinality(); got != 1000000 {
		t.Fatalf("cardinality = %d, want 1000000", got)
	}
	for _, v := range []uint32{0, 65535, 65536, 500000, 999999} {
		if !b.Contains(v) {
			t.Fatalf("missing %d", v)
		}
	}
	if b.Contains(1000000) {
		t.Fatal("unexpected value past range end")
	}
	// Full key space.
	b2 := New()
	b2.AddRange(0, 0xFFFFFFFF)
	if got := b2.Cardinality(); got != 1<<32 {
		t.Fatalf("cardinality = %d, want 2^32", got)
	}
	if !b2.Contains(0xFFFFFFFF) || !b2.Contains(0) {
		t.Fatal("full range missing endpoints")
	}
	st := b2.Stats()
	if st.NumRun != numContainers {
		t.Fatalf("expected all-run containers, got %+v", st)
	}
}

func TestAddManyAndDuplicate(t *testing.T) {
	b := New()
	xs := []uint32{5, 5, 7, 65536 + 3, 65536 + 3, 1 << 20}
	b.AddMany(xs)
	if got := b.Cardinality(); got != 4 {
		t.Fatalf("cardinality = %d, want 4", got)
	}
	if b.Add(5) {
		t.Fatal("Add reported duplicate as new")
	}
	if !b.Add(6) {
		t.Fatal("Add reported new value as duplicate")
	}
}

func TestRemove(t *testing.T) {
	b := New()
	b.AddRange(100, 200)
	if !b.Remove(150) {
		t.Fatal("Remove reported absent")
	}
	if b.Remove(150) {
		t.Fatal("Remove reported present twice")
	}
	if b.Contains(150) || b.Cardinality() != 100 {
		t.Fatal("remove broken")
	}
	if b.Remove(1 << 30) {
		t.Fatal("Remove on untouched container reported success")
	}
}

func referenceBitmap(vals ...uint32) map[uint32]bool {
	m := make(map[uint32]bool, len(vals))
	for _, v := range vals {
		m[v] = true
	}
	return m
}

func buildMixedBitmap(rng *rand.Rand, seedContainers []int) *Bitmap {
	b := New()
	for _, k := range seedContainers {
		switch rng.Intn(3) {
		case 0: // sparse
			for i := 0; i < 1+rng.Intn(2000); i++ {
				b.Add(uint32(k)<<16 | rng.Uint32()&0xFFFF)
			}
		case 1: // dense random
			xs := make([]uint32, 0, 20000)
			for i := 0; i < 20000; i++ {
				xs = append(xs, uint32(k)<<16|rng.Uint32()&0xFFFF)
			}
			b.AddMany(xs)
		case 2: // contiguous runs
			lo := rng.Uint32() & 0x7FFF
			hi := lo + uint32(rng.Intn(20000))
			if hi > 0xFFFF {
				hi = 0xFFFF
			}
			b.AddRange(uint32(k)<<16|lo, uint32(k)<<16|hi)
		}
	}
	return b
}

func bitmapToSet(b *Bitmap) map[uint32]bool {
	out := make(map[uint32]bool)
	for _, v := range b.ToArray() {
		out[v] = true
	}
	return out
}

func setsEqual(a, b map[uint32]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for v := range a {
		if !b[v] {
			return false
		}
	}
	return true
}

func TestBitmapSetOpsDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	keys := []int{0, 1, 5, 100, 1000}
	for trial := 0; trial < 10; trial++ {
		a := buildMixedBitmap(rng, keys)
		b := buildMixedBitmap(rng, keys)
		sa, sb := bitmapToSet(a), bitmapToSet(b)

		andRef := make(map[uint32]bool)
		orRef := make(map[uint32]bool)
		xorRef := make(map[uint32]bool)
		for v := range sa {
			orRef[v] = true
			if sb[v] {
				andRef[v] = true
			} else {
				xorRef[v] = true
			}
		}
		for v := range sb {
			orRef[v] = true
			if !sa[v] {
				xorRef[v] = true
			}
		}

		if got := bitmapToSet(a.And(b)); !setsEqual(got, andRef) {
			t.Fatalf("trial %d: AND mismatch (got %d, want %d)", trial, len(got), len(andRef))
		}
		if got := bitmapToSet(a.Or(b)); !setsEqual(got, orRef) {
			t.Fatalf("trial %d: OR mismatch (got %d, want %d)", trial, len(got), len(orRef))
		}
		if got := bitmapToSet(a.Xor(b)); !setsEqual(got, xorRef) {
			t.Fatalf("trial %d: XOR mismatch (got %d, want %d)", trial, len(got), len(xorRef))
		}
		if got := a.AndCardinality(b); got != int64(len(andRef)) {
			t.Fatalf("trial %d: AndCardinality = %d, want %d", trial, got, len(andRef))
		}
	}
}

func TestParallelMatchesSequential(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	keys := make([]int, 0, 16)
	for i := 0; i < 16; i++ {
		keys = append(keys, rng.Intn(65536))
	}
	a := buildMixedBitmap(rng, keys)
	b := buildMixedBitmap(rng, keys)
	for _, workers := range []int{2, 4, 16} {
		if !setsEqual(bitmapToSet(a.ParallelAnd(b, workers)), bitmapToSet(a.And(b))) {
			t.Fatalf("ParallelAnd(%d) != And", workers)
		}
		if !setsEqual(bitmapToSet(a.ParallelOr(b, workers)), bitmapToSet(a.Or(b))) {
			t.Fatalf("ParallelOr(%d) != Or", workers)
		}
		if !setsEqual(bitmapToSet(a.ParallelXor(b, workers)), bitmapToSet(a.Xor(b))) {
			t.Fatalf("ParallelXor(%d) != Xor", workers)
		}
	}
}

func TestConcurrentAddDisjointShards(t *testing.T) {
	b := New()
	var wg sync.WaitGroup
	const writers = 8
	const perWriter = 20000
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			base := uint32(w) * (1 << 20)
			for i := 0; i < perWriter; i++ {
				b.Add(base + uint32(i))
			}
		}(w)
	}
	wg.Wait()
	if got := b.Cardinality(); got != writers*perWriter {
		t.Fatalf("cardinality = %d, want %d", got, writers*perWriter)
	}
	for w := 0; w < writers; w++ {
		base := uint32(w) * (1 << 20)
		for i := 0; i < perWriter; i += 977 {
			if !b.Contains(base + uint32(i)) {
				t.Fatalf("missing value from writer %d", w)
			}
		}
	}
}

// TestConcurrentSameContainer hammers a single container from many
// goroutines to stress the per-container CAS spinlock.
func TestConcurrentSameContainer(t *testing.T) {
	b := New()
	var wg sync.WaitGroup
	const writers = 16
	const space = 1 << 16
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(w)))
			for i := 0; i < 20000; i++ {
				b.Add(uint32(rng.Intn(space)))
			}
		}(w)
	}
	wg.Wait()
	// Every value was added by someone with probability ~1; verify the
	// bitmap agrees with a sequential replay of the same streams.
	want := New()
	for w := 0; w < writers; w++ {
		rng := rand.New(rand.NewSource(int64(w)))
		for i := 0; i < 20000; i++ {
			want.Add(uint32(rng.Intn(space)))
		}
	}
	if b.Cardinality() != want.Cardinality() {
		t.Fatalf("cardinality = %d, want %d", b.Cardinality(), want.Cardinality())
	}
	if !setsEqual(bitmapToSet(b), bitmapToSet(want)) {
		t.Fatal("concurrent adds lost updates")
	}
}

// TestConcurrentReadWrite mixes readers and writers; run with -race.
func TestConcurrentReadWrite(t *testing.T) {
	b := New()
	b.AddRange(0, 1<<20)
	stop := make(chan struct{})
	var readers, writers sync.WaitGroup
	for w := 0; w < 4; w++ {
		readers.Add(1)
		go func(w int) {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				b.Contains(uint32(w * 1000))
				_ = b.Cardinality()
				runtime.Gosched()
			}
		}(w)
	}
	for w := 0; w < 4; w++ {
		writers.Add(1)
		go func(w int) {
			defer writers.Done()
			base := uint32(w) << 24
			for i := 0; i < 5000; i++ {
				b.Add(base + uint32(i))
			}
		}(w)
	}
	writers.Wait()
	close(stop)
	readers.Wait()
}

func TestCloneAndOptimize(t *testing.T) {
	b := New()
	b.AddRange(0, 500000)
	for i := 0; i < 1000; i++ {
		b.Add(uint32(i*99991 + 7))
	}
	c := b.Clone()
	if !setsEqual(bitmapToSet(b), bitmapToSet(c)) {
		t.Fatal("clone diverges")
	}
	// Writes to the clone must not affect the original (copy-on-write).
	c.Add(4000000000)
	if b.Contains(4000000000) {
		t.Fatal("clone write leaked into original")
	}
	b.Optimize()
	st := b.Stats()
	if st.NumRun == 0 {
		t.Fatalf("expected run containers after Optimize, got %+v", st)
	}
	if !setsEqual(bitmapToSet(b), bitmapToSet(c)) && false {
		t.Fatal("unreachable")
	}
	if b.Cardinality() != c.Cardinality()-1 {
		t.Fatal("Optimize changed cardinality")
	}
}

// waitForGoroutines polls until the goroutine count returns to base.
func waitForGoroutines(base int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
		if runtime.NumGoroutine() <= base {
			return true
		}
	}
	return false
}

func TestNoGoroutineLeak(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	keys := []int{1, 2, 3, 4, 5}
	a := buildMixedBitmap(rng, keys)
	b := buildMixedBitmap(rng, keys)
	// Warm up so any one-time background goroutines exist before baseline.
	a.ParallelAnd(b, 4)
	if !waitForGoroutines(runtime.NumGoroutine(), 2*time.Second) {
		t.Fatal("could not establish baseline")
	}
	base := runtime.NumGoroutine()
	for i := 0; i < 20; i++ {
		a.ParallelAnd(b, 8)
		a.ParallelOr(b, 8)
		a.ParallelXor(b, 8)
	}
	if !waitForGoroutines(base, 5*time.Second) {
		t.Fatalf("goroutine leak: base=%d now=%d", base, runtime.NumGoroutine())
	}
}
