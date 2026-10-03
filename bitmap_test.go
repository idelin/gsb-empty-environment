package bitmapckpt

import (
	"math"
	"math/rand"
	"sync"
	"testing"
)

type refSet map[uint64]struct{}

func (s refSet) add(v uint64) { s[v] = struct{}{} }

func (s refSet) addRange(lo, hi uint64) {
	for v := lo; v < hi; v++ {
		s[v] = struct{}{}
	}
}

func checkAgainstRef(t *testing.T, bm *Bitmap, ref refSet) {
	t.Helper()
	if bm.Cardinality() != uint64(len(ref)) {
		t.Fatalf("cardinality = %d, want %d", bm.Cardinality(), len(ref))
	}
	for v := range ref {
		if !bm.Contains(v) {
			t.Fatalf("missing value %d", v)
		}
	}
	// spot-check some absent values
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 10000; i++ {
		v := uint64(r.Uint32())
		if _, ok := ref[v]; !ok && bm.Contains(v) {
			t.Fatalf("unexpected value %d", v)
		}
	}
}

func TestSparseAndDenseInsert(t *testing.T) {
	bm := New()
	ref := refSet{}
	// sparse discrete IDs scattered across the whole uint32 space
	sparse := []uint64{0, 3, 7, 65535, 65536, 65537, 1 << 20, 1 << 31, math.MaxUint32 - 1, math.MaxUint32, 1 << 40, 1 << 62, math.MaxUint64 - 1, math.MaxUint64}
	for _, v := range sparse {
		bm.Add(v)
		ref.add(v)
	}
	// dense contiguous ranges
	ranges := [][2]uint64{
		{100000, 200000},             // inside one container
		{1 << 16, 3 << 16},           // two full containers
		{5000000, 5000000 + 37},      // tiny range
		{1 << 31, (1 << 31) + 70000}, // crosses a container boundary
	}
	for _, rg := range ranges {
		bm.AddRange(rg[0], rg[1])
		ref.addRange(rg[0], rg[1])
	}
	checkAgainstRef(t, bm, ref)
}

func TestContainerAutoSelection(t *testing.T) {
	bm := New()
	// sparse: array container
	for i := 0; i < 100; i++ {
		bm.Add(uint64(i * 613 % 65536))
	}
	// dense but not contiguous: bitmap container (>4096 scattered values)
	r := rand.New(rand.NewSource(3))
	for i := 0; i < 20000; i++ {
		bm.Add(1<<16 | uint64(r.Intn(65536)))
	}
	// contiguous full range: run container
	bm.AddRange(2<<16, 3<<16)
	// contiguous partial range: run container
	bm.AddRange(7<<16, 7<<16+5000)

	bm.mu.RLock()
	defer bm.mu.RUnlock()
	if bm.cs[0].typeID() != typeArray {
		t.Fatalf("key 0: expected array, got %d", bm.cs[0].typeID())
	}
	if bm.cs[1].typeID() != typeBitmap {
		t.Fatalf("key 1: expected bitmap, got %d", bm.cs[1].typeID())
	}
	if bm.cs[2].typeID() != typeRun {
		t.Fatalf("key 2: expected run, got %d", bm.cs[2].typeID())
	}
	if bm.cs[7].typeID() != typeRun {
		t.Fatalf("key 7: expected run, got %d", bm.cs[7].typeID())
	}
}

func TestOptimizePicksBestContainer(t *testing.T) {
	bm := New()
	// 65536 consecutive single adds: array -> bitmap along the way
	for i := 0; i < 65536; i++ {
		bm.Add(uint64(i))
	}
	bm.Optimize()
	bm.mu.RLock()
	c := bm.cs[0]
	bm.mu.RUnlock()
	if c.typeID() != typeRun {
		t.Fatalf("expected run after Optimize on dense data, got %d", c.typeID())
	}
	if bm.Cardinality() != 65536 {
		t.Fatalf("cardinality = %d, want 65536", bm.Cardinality())
	}
}

func randomTestBitmap(r *rand.Rand, keys int) (*Bitmap, refSet) {
	bm := New()
	ref := refSet{}
	for k := 0; k < keys; k++ {
		key := uint64(r.Intn(8))
		switch r.Intn(3) {
		case 0: // sparse points
			for i := 0; i < 1+r.Intn(50); i++ {
				v := key<<16 | uint64(r.Intn(65536))
				bm.Add(v)
				ref.add(v)
			}
		case 1: // medium dense range
			lo := uint64(key)<<16 + uint64(r.Intn(30000))
			hi := lo + uint64(r.Intn(20000))
			bm.AddRange(lo, hi)
			ref.addRange(lo, hi)
		case 2: // nearly full container
			lo := uint64(key) << 16
			hi := lo + 60000 + uint64(r.Intn(5536))
			bm.AddRange(lo, hi)
			ref.addRange(lo, hi)
		}
	}
	return bm, ref
}

func bitmapToRef(bm *Bitmap) refSet {
	ref := refSet{}
	bm.ForEach(func(v uint64) bool {
		ref.add(v)
		return true
	})
	return ref
}

func refsEqual(a, b refSet) bool {
	if len(a) != len(b) {
		return false
	}
	for v := range a {
		if _, ok := b[v]; !ok {
			return false
		}
	}
	return true
}

func TestSetOpsAgainstReference(t *testing.T) {
	r := rand.New(rand.NewSource(11))
	for trial := 0; trial < 10; trial++ {
		bmA, refA := randomTestBitmap(r, 6)
		bmB, refB := randomTestBitmap(r, 6)

		andRef := refSet{}
		for v := range refA {
			if _, ok := refB[v]; ok {
				andRef.add(v)
			}
		}
		orRef := refSet{}
		for v := range refA {
			orRef.add(v)
		}
		for v := range refB {
			orRef.add(v)
		}
		xorRef := refSet{}
		for v := range refA {
			if _, ok := refB[v]; !ok {
				xorRef.add(v)
			}
		}
		for v := range refB {
			if _, ok := refA[v]; !ok {
				xorRef.add(v)
			}
		}

		if got := bitmapToRef(bmA.And(bmB)); !refsEqual(got, andRef) {
			t.Fatalf("trial %d: AND mismatch: got %d, want %d", trial, len(got), len(andRef))
		}
		if got := bitmapToRef(bmA.Or(bmB)); !refsEqual(got, orRef) {
			t.Fatalf("trial %d: OR mismatch: got %d, want %d", trial, len(got), len(orRef))
		}
		if got := bitmapToRef(bmA.Xor(bmB)); !refsEqual(got, xorRef) {
			t.Fatalf("trial %d: XOR mismatch: got %d, want %d", trial, len(got), len(xorRef))
		}
	}
}

func TestBatchOps(t *testing.T) {
	r := rand.New(rand.NewSource(13))
	bms := make([]*Bitmap, 4)
	refs := make([]refSet, 4)
	for i := range bms {
		bms[i], refs[i] = randomTestBitmap(r, 5)
	}
	andAll := And(bms...)
	wantAnd := refSet{}
	for v := range refs[0] {
		inAll := true
		for _, ref := range refs[1:] {
			if _, ok := ref[v]; !ok {
				inAll = false
				break
			}
		}
		if inAll {
			wantAnd.add(v)
		}
	}
	if got := bitmapToRef(andAll); !refsEqual(got, wantAnd) {
		t.Fatalf("batch AND mismatch: got %d, want %d", len(got), len(wantAnd))
	}
	orAll := Or(bms...)
	wantOr := refSet{}
	for _, ref := range refs {
		for v := range ref {
			wantOr.add(v)
		}
	}
	if got := bitmapToRef(orAll); !refsEqual(got, wantOr) {
		t.Fatalf("batch OR mismatch: got %d, want %d", len(got), len(wantOr))
	}
	xorAll := Xor(bms...)
	wantXor := refSet{}
	for _, ref := range refs {
		for v := range ref {
			if _, ok := wantXor[v]; ok {
				delete(wantXor, v)
			} else {
				wantXor.add(v)
			}
		}
	}
	if got := bitmapToRef(xorAll); !refsEqual(got, wantXor) {
		t.Fatalf("batch XOR mismatch: got %d, want %d", len(got), len(wantXor))
	}
	// degenerate cases
	if And().Cardinality() != 0 || Or().Cardinality() != 0 || Xor().Cardinality() != 0 {
		t.Fatal("empty batch ops must be empty")
	}
	if !And(bms[0]).Equals(bms[0]) || !Or(bms[0]).Equals(bms[0]) {
		t.Fatal("single-element batch ops must equal the input")
	}
}

func TestBoundaryValues(t *testing.T) {
	bm := New()
	bm.Add(0)
	bm.Add(math.MaxUint64)
	bm.AddRange(0, 1)
	bm.AddRange(math.MaxUint64, 1<<64-1) // empty after hi<=lo check? hi>lo here
	if !bm.Contains(0) || !bm.Contains(math.MaxUint64) {
		t.Fatal("boundary values missing")
	}
	if bm.Cardinality() != 2 {
		t.Fatalf("cardinality = %d, want 2", bm.Cardinality())
	}
	// 2^32-value range (larger than the whole old uint32 space)
	full := New()
	full.AddRange(0, 1<<32)
	if full.Cardinality() != 1<<32 {
		t.Fatalf("full space cardinality = %d, want 2^32", full.Cardinality())
	}
	if !full.Contains(0) || !full.Contains(math.MaxUint32) || !full.Contains(123456789) {
		t.Fatal("full space bitmap missing values")
	}
	// high key space (beyond 2^32)
	high := New()
	high.AddRange(1<<40, (1<<40)+100000)
	if high.Cardinality() != 100000 || !high.Contains((1<<40)+99999) {
		t.Fatal("high key space range broken")
	}
	// empty range is a no-op
	empty := New()
	empty.AddRange(100, 100)
	empty.AddRange(200, 100)
	if empty.Cardinality() != 0 {
		t.Fatal("empty ranges must be no-ops")
	}
}

func TestCloneIsCopyOnWrite(t *testing.T) {
	bm := New()
	bm.AddRange(0, 100000)
	clone := bm.Clone()
	bm.Add(1 << 20)
	bm.AddRange(0, 50) // touches an existing, shared container
	if clone.Contains(1 << 20) {
		t.Fatal("clone leaked mutation from original")
	}
	if clone.Cardinality() != 100000 {
		t.Fatalf("clone cardinality = %d, want 100000", clone.Cardinality())
	}
	if !bm.Contains(1 << 20) {
		t.Fatal("original missing new value")
	}
}

func TestConcurrentAdd(t *testing.T) {
	bm := New()
	const workers = 8
	const perWorker = 5000
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			base := uint64(id) * perWorker * 2
			for i := 0; i < perWorker; i++ {
				bm.Add(base + uint64(i)*2)
			}
			rangeBase := uint64(1)<<20 + uint64(id)*2000
			bm.AddRange(rangeBase, rangeBase+1000)
		}(w)
	}
	// concurrent readers
	stop := make(chan struct{})
	var rwg sync.WaitGroup
	for i := 0; i < 2; i++ {
		rwg.Add(1)
		go func() {
			defer rwg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					bm.Cardinality()
					bm.Contains(42)
				}
			}
		}()
	}
	wg.Wait()
	close(stop)
	rwg.Wait()
	want := uint64(perWorker*workers + 1000*workers)
	if bm.Cardinality() != want {
		t.Fatalf("cardinality = %d, want %d", bm.Cardinality(), want)
	}
}

func TestEqualsAndSelfOps(t *testing.T) {
	bm := New()
	bm.AddRange(0, 50000)
	if !bm.Equals(bm.Clone()) {
		t.Fatal("clone must equal original")
	}
	if !bm.And(bm).Equals(bm) {
		t.Fatal("self AND must be identity")
	}
	if !bm.Or(bm).Equals(bm) {
		t.Fatal("self OR must be identity")
	}
	if bm.Xor(bm).Cardinality() != 0 {
		t.Fatal("self XOR must be empty")
	}
	other := New()
	other.AddRange(25000, 75000)
	if bm.Equals(other) {
		t.Fatal("different bitmaps must not be equal")
	}
}
