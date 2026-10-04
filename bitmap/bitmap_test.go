package bitmap

import (
	"math"
	"math/rand"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestAddBoundaries(t *testing.T) {
	b := New()
	ids := []uint64{0, 1, 65535, 65536, 65537, math.MaxUint64, math.MaxUint64 - 1}
	for _, id := range ids {
		b.Add(id)
	}
	// duplicates must be idempotent
	for _, id := range ids {
		b.Add(id)
	}
	if got := b.Cardinality(); got != uint64(len(ids)) {
		t.Fatalf("cardinality = %d, want %d", got, len(ids))
	}
	for _, id := range ids {
		if !b.Contains(id) {
			t.Fatalf("missing id %d", id)
		}
	}
	if b.Contains(2) || b.Contains(math.MaxUint64-2) {
		t.Fatal("unexpected id present")
	}
	// ids sharing the low 48 bits must not collide (key is id>>16, 48 bits)
	c := New()
	c.Add(math.MaxUint64)
	if c.Contains(0x0000FFFFFFFFFFFF) || c.Contains(0xFFFFFFFF) {
		t.Fatal("high-key collision: ids truncated to 32 bits")
	}
	if !c.Contains(math.MaxUint64) {
		t.Fatal("MaxUint64 lost")
	}
}

func TestArrayToBitmapConversion(t *testing.T) {
	b := New()
	for i := 0; i < arrayMaxSize; i++ {
		b.Add(uint64(i * 3)) // sparse, stays array
	}
	if s := b.Stats(); s.ArrayContainers != 1 || s.BitmapContainers != 0 {
		t.Fatalf("expected 1 array container, got %+v", s)
	}
	b.Add(uint64(arrayMaxSize * 3)) // crosses the 4096 threshold
	if s := b.Stats(); s.BitmapContainers != 1 {
		t.Fatalf("expected conversion to bitmap container, got %+v", s)
	}
	if got := b.Cardinality(); got != arrayMaxSize+1 {
		t.Fatalf("cardinality = %d", got)
	}
}

func TestDenseRangeBecomesRun(t *testing.T) {
	b := New()
	b.AddRange(100000, 200000)
	s := b.Stats()
	if s.RunContainers == 0 {
		t.Fatalf("expected run containers, got %+v", s)
	}
	if s.BitmapContainers != 0 || s.ArrayContainers != 0 {
		t.Fatalf("expected only run containers, got %+v", s)
	}
	if got := b.Cardinality(); got != 100000 {
		t.Fatalf("cardinality = %d, want 100000", got)
	}
	for _, id := range []uint64{100000, 150000, 199999} {
		if !b.Contains(id) {
			t.Fatalf("missing %d", id)
		}
	}
	if b.Contains(99999) || b.Contains(200000) {
		t.Fatal("range boundary leak")
	}
}

func TestOptimizeChoosesRun(t *testing.T) {
	b := New()
	// contiguous 5000 ids inserted one by one -> bitmap container (>4096)
	for i := 0; i < 5000; i++ {
		b.Add(uint64(i))
	}
	if s := b.Stats(); s.BitmapContainers != 1 {
		t.Fatalf("expected bitmap container, got %+v", s)
	}
	b.Optimize()
	if s := b.Stats(); s.RunContainers != 1 {
		t.Fatalf("expected run container after Optimize, got %+v", s)
	}
	if got := b.Cardinality(); got != 5000 {
		t.Fatalf("cardinality = %d", got)
	}
}

func TestAddRangeEdges(t *testing.T) {
	b := New()
	b.AddRange(0, 0)         // empty
	b.AddRange(100, 100)     // empty
	b.AddRange(65535, 65537) // straddles a container boundary
	b.AddRange(0, 1<<16)     // exactly one full container
	b.AddRange(3<<16, 5<<16) // two full containers
	if got, want := b.Cardinality(), uint64(65536+1+2*65536); got != want {
		t.Fatalf("cardinality = %d, want %d", got, want)
	}
	if !b.Contains(65535) || !b.Contains(65536) || b.Contains(65537) {
		t.Fatal("boundary ids wrong")
	}
	// full-container ranges should be single runs
	if s := b.Stats(); s.RunContainers != s.Containers {
		t.Fatalf("expected all-run storage, got %+v", s)
	}
}

// referenceSet is the ground-truth implementation.
type referenceSet map[uint64]bool

func buildShapes(seed int64) map[string]*Bitmap {
	r := rand.New(rand.NewSource(seed))
	shapes := map[string]*Bitmap{}

	sparse := New() // array containers
	for i := 0; i < 2000; i++ {
		sparse.Add(uint64(r.Int63n(1 << 24)))
	}
	shapes["sparse"] = sparse

	dense := New() // run containers
	for i := 0; i < 8; i++ {
		lo := uint64(r.Int63n(1 << 20))
		dense.AddRange(lo, lo+uint64(1+r.Int63n(1<<16)))
	}
	shapes["dense"] = dense

	scattered := New() // bitmap containers
	for i := 0; i < 30000; i++ {
		scattered.Add(uint64(r.Int63n(1 << 17)))
	}
	shapes["scattered"] = scattered

	mixed := New() // everything at once
	mixed.AddRange(0, 50000)
	for i := 0; i < 3000; i++ {
		mixed.Add(uint64(r.Int63n(1 << 22)))
	}
	mixed.AddRange(1<<21, (1<<21)+70000)
	mixed.Optimize()
	shapes["mixed"] = mixed

	shapes["empty"] = New()
	return shapes
}

func toRef(b *Bitmap) referenceSet {
	ref := referenceSet{}
	b.ForEach(func(id uint64) { ref[id] = true })
	return ref
}

func fromRef(ref referenceSet) *Bitmap {
	b := New()
	for id := range ref {
		b.Add(id)
	}
	return b
}

func TestSetOpsAgainstReference(t *testing.T) {
	shapes := buildShapes(7)
	refs := map[string]referenceSet{}
	for name, b := range shapes {
		refs[name] = toRef(b)
		if uint64(len(refs[name])) != b.Cardinality() {
			t.Fatalf("%s: ForEach/cardinality mismatch", name)
		}
	}
	for n1, b1 := range shapes {
		for n2, b2 := range shapes {
			r1, r2 := refs[n1], refs[n2]

			andRef := referenceSet{}
			for id := range r1 {
				if r2[id] {
					andRef[id] = true
				}
			}
			if got := b1.And(b2); !got.Equal(fromRef(andRef)) {
				t.Fatalf("AND %s x %s mismatch: got %d want %d",
					n1, n2, got.Cardinality(), len(andRef))
			}

			orRef := referenceSet{}
			for id := range r1 {
				orRef[id] = true
			}
			for id := range r2 {
				orRef[id] = true
			}
			if got := b1.Or(b2); !got.Equal(fromRef(orRef)) {
				t.Fatalf("OR %s x %s mismatch", n1, n2)
			}

			xorRef := referenceSet{}
			for id := range r1 {
				if !r2[id] {
					xorRef[id] = true
				}
			}
			for id := range r2 {
				if !r1[id] {
					xorRef[id] = true
				}
			}
			if got := b1.Xor(b2); !got.Equal(fromRef(xorRef)) {
				t.Fatalf("XOR %s x %s mismatch", n1, n2)
			}
		}
	}
}

func TestSetOpsDoNotMutateInputs(t *testing.T) {
	shapes := buildShapes(11)
	for _, b := range shapes {
		before := b.Checksum()
		other := buildShapes(13)["mixed"]
		b.And(other)
		b.Or(other)
		b.Xor(other)
		if b.Checksum() != before {
			t.Fatal("set op mutated its receiver")
		}
	}
}

func TestBatchOps(t *testing.T) {
	shapes := buildShapes(17)
	all := []*Bitmap{shapes["sparse"], shapes["dense"], shapes["scattered"], shapes["mixed"]}

	andSeq := all[0].And(all[1]).And(all[2]).And(all[3])
	if !AndMany(all...).Equal(andSeq) {
		t.Fatal("AndMany mismatch")
	}
	orSeq := all[0].Or(all[1]).Or(all[2]).Or(all[3])
	if !OrMany(all...).Equal(orSeq) {
		t.Fatal("OrMany mismatch")
	}
	xorSeq := all[0].Xor(all[1]).Xor(all[2]).Xor(all[3])
	if !XorMany(all...).Equal(xorSeq) {
		t.Fatal("XorMany mismatch")
	}
	if !AndMany().Equal(New()) {
		t.Fatal("AndMany() should be empty")
	}
}

func TestCloneEqualChecksum(t *testing.T) {
	shapes := buildShapes(23)
	for name, b := range shapes {
		c := b.Clone()
		if !b.Equal(c) || b.Checksum() != c.Checksum() {
			t.Fatalf("%s: clone not equal", name)
		}
		c.Add(123456789)
		if b.Contains(123456789) {
			t.Fatalf("%s: clone shares state with original", name)
		}
	}
}

func TestConcurrentReadOps(t *testing.T) {
	shapes := buildShapes(29)
	base := shapes["mixed"]
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_ = base.And(shapes["dense"])
				_ = base.Or(shapes["sparse"])
				_ = base.Xor(shapes["scattered"])
				_ = base.Contains(uint64(i * 7))
				_ = base.Cardinality()
			}
		}(g)
	}
	wg.Wait()
}

func TestSafeBitmapConcurrent(t *testing.T) {
	sb := NewSafe()
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			base := uint64(g) << 20
			for i := 0; i < 500; i++ {
				sb.Add(base + uint64(i))
				sb.AddRange(base+100000, base+100100)
				_ = sb.Contains(base + uint64(i))
				_ = sb.Snapshot()
			}
		}(g)
	}
	wg.Wait()
	if got := sb.Cardinality(); got != 4*(500+100) {
		t.Fatalf("cardinality = %d, want %d", got, 4*600)
	}
}

func TestNoGoroutineLeak(t *testing.T) {
	workload := func() {
		var wg sync.WaitGroup
		for g := 0; g < 8; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				b := New()
				b.AddRange(0, 100000)
				_ = b.And(b)
			}()
		}
		wg.Wait()
	}
	workload() // warm up lazy runtime/race goroutines
	before := runtime.NumGoroutine()
	for round := 0; round < 3; round++ {
		workload()
	}
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, got)
	}
}

func TestNoMemoryLeak(t *testing.T) {
	var m1, m2 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m1)
	for i := 0; i < 200; i++ {
		b := New()
		b.AddRange(0, 1<<20)
		for j := 0; j < 5000; j++ {
			b.Add(uint64(j * 13))
		}
		_ = b.Or(b).And(b).Xor(b)
	}
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&m2)
	growth := int64(m2.HeapAlloc) - int64(m1.HeapAlloc)
	if growth > 1<<20 {
		t.Fatalf("heap grew by %d bytes after releasing bitmaps", growth)
	}
}
