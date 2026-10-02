package roaring

import (
	"math/rand"
	"sort"
	"testing"
)

// buildMixed creates a bitmap mixing sparse points and dense ranges.
func buildMixed(r *rand.Rand, containers int) *Bitmap {
	bm := New()
	for k := 0; k < containers; k++ {
		hi := uint32(r.Intn(1 << 16))
		switch r.Intn(3) {
		case 0: // sparse points
			for i := 0; i < 1+r.Intn(200); i++ {
				bm.Add(hi<<16 | uint32(r.Intn(65536)))
			}
		case 1: // dense range
			start := uint32(r.Intn(40000))
			bm.AddRange(hi<<16|start, uint64(hi<<16|start)+uint64(1+r.Intn(20000)))
		case 2: // many random points -> bitmap container
			for i := 0; i < 4500; i++ {
				bm.Add(hi<<16 | uint32(r.Intn(65536)))
			}
		}
	}
	return bm
}

func toSet(bm *Bitmap) map[uint32]bool {
	out := make(map[uint32]bool)
	for _, id := range bm.ToArray() {
		out[id] = true
	}
	return out
}

func checkSet(t *testing.T, name string, got *Bitmap, want map[uint32]bool) {
	t.Helper()
	if got.Cardinality() != uint64(len(want)) {
		t.Fatalf("%s: cardinality = %d, want %d", name, got.Cardinality(), len(want))
	}
	arr := got.ToArray()
	if len(arr) != len(want) {
		t.Fatalf("%s: ToArray len = %d, want %d", name, len(arr), len(want))
	}
	for _, id := range arr {
		if !want[id] {
			t.Fatalf("%s: unexpected id %d", name, id)
		}
	}
}

func TestOpsAgainstReference(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for trial := 0; trial < 20; trial++ {
		a := buildMixed(r, 8)
		b := buildMixed(r, 8)
		sa, sb := toSet(a), toSet(b)

		andWant := make(map[uint32]bool)
		for id := range sa {
			if sb[id] {
				andWant[id] = true
			}
		}
		orWant := make(map[uint32]bool)
		for id := range sa {
			orWant[id] = true
		}
		for id := range sb {
			orWant[id] = true
		}
		xorWant := make(map[uint32]bool)
		for id := range orWant {
			if sa[id] != sb[id] {
				xorWant[id] = true
			}
		}

		checkSet(t, "and", a.And(b), andWant)
		checkSet(t, "or", a.Or(b), orWant)
		checkSet(t, "xor", a.Xor(b), xorWant)

		// Inputs must be unchanged by the operations.
		checkSet(t, "a-preserved", a, sa)
		checkSet(t, "b-preserved", b, sb)
	}
}

// makeContainerBitmap builds a bitmap with a single container (key 0) of the
// requested encoding, holding the given values.
func makeContainerBitmap(typ byte, vals ...uint16) *Bitmap {
	bm := New()
	var c container
	switch typ {
	case containerTypeArray:
		sorted := append([]uint16(nil), vals...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		c = &arrayContainer{vals: sorted}
	case containerTypeBitmap:
		bc := &bitmapContainer{words: make([]uint64, bitmapWords)}
		for _, v := range vals {
			bc.words[v>>6] |= 1 << (v & 63)
			bc.card++
		}
		c = bc
	case containerTypeRun:
		rc := &runContainer{}
		for _, v := range vals {
			rc = unionRuns(rc, &runContainer{iv: []runInterval{{start: v, length: 0}}})
		}
		c = rc
	}
	bm.entries = []bitmapEntry{{key: 0, c: repair(c)}}
	if bm.entries[0].c.typ() != typ {
		// repair may legitimately re-encode; force the requested encoding for
		// the matrix test by bypassing repair.
		bm.entries[0].c = c
	}
	return bm
}

func TestContainerOpMatrix(t *testing.T) {
	types := []byte{containerTypeArray, containerTypeBitmap, containerTypeRun}
	// Two value sets with partial overlap.
	aVals := []uint16{1, 2, 3, 100, 200, 300, 1000, 5000, 6000, 6001, 6002, 6003, 40000, 65535}
	bVals := []uint16{3, 4, 100, 201, 300, 1000, 1001, 6000, 6003, 6004, 40000, 40001, 65535}
	inA := make(map[uint32]bool)
	inB := make(map[uint32]bool)
	for _, v := range aVals {
		inA[uint32(v)] = true
	}
	for _, v := range bVals {
		inB[uint32(v)] = true
	}
	for _, ta := range types {
		for _, tb := range types {
			a := makeContainerBitmap(ta, aVals...)
			b := makeContainerBitmap(tb, bVals...)
			andWant, orWant, xorWant := make(map[uint32]bool), make(map[uint32]bool), make(map[uint32]bool)
			for id := range inA {
				if inB[id] {
					andWant[id] = true
				}
				orWant[id] = true
			}
			for id := range inB {
				orWant[id] = true
			}
			for id := range orWant {
				if inA[id] != inB[id] {
					xorWant[id] = true
				}
			}
			checkSet(t, "matrix-and", a.And(b), andWant)
			checkSet(t, "matrix-or", a.Or(b), orWant)
			checkSet(t, "matrix-xor", a.Xor(b), xorWant)
		}
	}
}

func TestOpResultContainerSelection(t *testing.T) {
	// Dense AND dense range -> run container result.
	a := New()
	a.AddRange(0, 60000)
	b := New()
	b.AddRange(100, 50000)
	res := a.And(b)
	if res.entries[0].c.typ() != containerTypeRun {
		t.Fatalf("dense AND dense should yield run container, got %d", res.entries[0].c.typ())
	}
	if res.Cardinality() != 50000-100 {
		t.Fatalf("cardinality = %d", res.Cardinality())
	}

	// Dense AND sparse -> array container result.
	sparse := New()
	for i := 0; i < 50; i++ {
		sparse.Add(uint32(i * 1000))
	}
	res = a.And(sparse)
	if res.entries[0].c.typ() != containerTypeArray {
		t.Fatalf("dense AND sparse should yield array container, got %d", res.entries[0].c.typ())
	}
	if res.Cardinality() != 50 {
		t.Fatalf("cardinality = %d", res.Cardinality())
	}

	// Union of two scattered arrays beyond threshold -> bitmap container.
	x, y := New(), New()
	for i := 0; i < 3000; i++ {
		x.Add(uint32(i * 21)) // stride 21: not run-compressible
		y.Add(uint32(i*21 + 1))
	}
	res = x.Or(y)
	if res.entries[0].c.typ() != containerTypeBitmap {
		t.Fatalf("large scattered union should yield bitmap container, got %d", res.entries[0].c.typ())
	}
	if res.Cardinality() != 6000 {
		t.Fatalf("cardinality = %d", res.Cardinality())
	}
}

func TestBatchOps(t *testing.T) {
	r := rand.New(rand.NewSource(99))
	var bms []*Bitmap
	var sets []map[uint32]bool
	for i := 0; i < 5; i++ {
		bm := buildMixed(r, 5)
		bms = append(bms, bm)
		sets = append(sets, toSet(bm))
	}
	andWant := make(map[uint32]bool)
	for id := range sets[0] {
		ok := true
		for _, s := range sets[1:] {
			if !s[id] {
				ok = false
				break
			}
		}
		if ok {
			andWant[id] = true
		}
	}
	orWant := make(map[uint32]bool)
	for _, s := range sets {
		for id := range s {
			orWant[id] = true
		}
	}
	xorWant := make(map[uint32]bool)
	for id := range orWant {
		n := 0
		for _, s := range sets {
			if s[id] {
				n++
			}
		}
		if n%2 == 1 {
			xorWant[id] = true
		}
	}
	checkSet(t, "andAll", AndAll(bms...), andWant)
	checkSet(t, "orAll", OrAll(bms...), orWant)
	checkSet(t, "xorAll", XorAll(bms...), xorWant)

	if !AndAll().IsEmpty() || !OrAll().IsEmpty() || !XorAll().IsEmpty() {
		t.Fatal("empty batch ops must yield empty bitmap")
	}
}

func TestOpsWithEmpty(t *testing.T) {
	a := New()
	a.AddRange(0, 1000)
	empty := New()
	if !a.And(empty).IsEmpty() {
		t.Fatal("And with empty must be empty")
	}
	if got := a.Or(empty); !got.Equals(a) {
		t.Fatal("Or with empty must equal original")
	}
	if got := a.Xor(empty); !got.Equals(a) {
		t.Fatal("Xor with empty must equal original")
	}
	if got := empty.Or(a); !got.Equals(a) {
		t.Fatal("empty.Or(a) must equal a")
	}
}

func TestEquals(t *testing.T) {
	a := New()
	a.AddRange(0, 1000)
	a.Add(1 << 20)
	b := New()
	b.AddRange(0, 1000)
	b.Add(1 << 20)
	if !a.Equals(b) || !b.Equals(a) {
		t.Fatal("equal bitmaps not equal")
	}
	b.Add(5000)
	if a.Equals(b) || b.Equals(a) {
		t.Fatal("different bitmaps reported equal")
	}
	c := New()
	c.Add(1 << 20)
	if a.Equals(c) {
		t.Fatal("different key sets reported equal")
	}
}

func TestDisjointKeysOps(t *testing.T) {
	a := New()
	a.AddRange(0, 100) // container key 0
	b := New()
	b.AddRange(1<<20, 1<<20+100) // container key 16
	if !a.And(b).IsEmpty() {
		t.Fatal("disjoint And not empty")
	}
	if got := a.Or(b).Cardinality(); got != 200 {
		t.Fatalf("Or cardinality = %d, want 200", got)
	}
	if got := a.Xor(b).Cardinality(); got != 200 {
		t.Fatalf("Xor cardinality = %d, want 200", got)
	}
}

// TestRepairBitmapToRun is a regression test: a bitmap container holding a
// contiguous range starting at bit 0 must repair into a single run.
func TestRepairBitmapToRun(t *testing.T) {
	a := New()
	for i := 0; i < 5000; i++ {
		a.Add(uint32(3<<16 + i)) // contiguous, promoted array -> bitmap
	}
	if a.entries[0].c.typ() != containerTypeBitmap {
		t.Fatalf("expected bitmap container, got %d", a.entries[0].c.typ())
	}
	b := New()
	b.AddRange(3<<16, 3<<16+65536) // full container, run-encoded
	res := a.And(b)
	if got := res.Cardinality(); got != 5000 {
		t.Fatalf("cardinality = %d, want 5000", got)
	}
	if res.entries[0].c.typ() != containerTypeRun {
		t.Fatalf("contiguous intersection should be a run, got %d", res.entries[0].c.typ())
	}
	for i := 0; i < 5000; i++ {
		if !res.Contains(uint32(3<<16 + i)) {
			t.Fatalf("missing %d", i)
		}
	}
	// Round-trip through serialization must not see empty containers.
	roundTripBuf := res.Serialize()
	if _, err := Deserialize(roundTripBuf); err != nil {
		t.Fatalf("Deserialize: %v", err)
	}
}
