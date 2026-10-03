package roaring

import (
	"math/rand"
	"testing"
)

func TestBitmapSparseAndDense(t *testing.T) {
	bm := New()
	// Sparse, scattered IDs.
	sparse := []uint32{0, 1, 7, 65535, 65536, 1 << 20, 1<<20 + 3, uint32(MaxValue - 1)}
	for _, v := range sparse {
		bm.Add(v)
	}
	// Dense contiguous ranges.
	bm.AddRange(1_000_000, 1_100_000)
	bm.AddRange(3_000_000_000, 3_000_050_000)
	for _, v := range sparse {
		if !bm.Contains(v) {
			t.Fatalf("missing sparse id %d", v)
		}
	}
	for _, v := range []uint32{1_000_000, 1_050_000, 1_099_999, 3_000_000_000, 3_000_049_999} {
		if !bm.Contains(v) {
			t.Fatalf("missing dense id %d", v)
		}
	}
	if bm.Contains(999_999) || bm.Contains(1_100_000) || bm.Contains(3_000_050_000) {
		t.Fatal("range end must be exclusive")
	}
	// Two sparse IDs (1<<20 and 1<<20+3) fall inside the first dense range.
	want := uint64(len(sparse)-2) + 100_000 + 50_000
	if bm.Cardinality() != want {
		t.Fatalf("cardinality = %d, want %d", bm.Cardinality(), want)
	}
	if err := bm.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestAutoContainerSelection(t *testing.T) {
	// Dense contiguous range -> run container.
	dense := New()
	dense.AddRange(0, 100_000)
	if dense.conts[0].ctype() != typeRun {
		t.Fatalf("dense range stored as %s, want run", dense.conts[0].ctype())
	}
	if err := dense.checkOptimal(); err != nil {
		t.Fatal(err)
	}
	// Few scattered IDs -> array container.
	sparse := New()
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 100; i++ {
		sparse.Add(uint32(rng.Intn(65536)))
	}
	if sparse.conts[0].ctype() != typeArray {
		t.Fatalf("sparse set stored as %s, want array", sparse.conts[0].ctype())
	}
	// Many scattered IDs in one container -> bitmap container.
	many := New()
	for i := 0; i < 20_000; i++ {
		many.Add(uint32(rng.Intn(65536)))
	}
	if many.conts[0].ctype() != typeBitmap {
		t.Fatalf("dense random set stored as %s, want bitmap", many.conts[0].ctype())
	}
	// Intersection that shrinks a bitmap container -> array container.
	small := New()
	small.AddRange(0, 10)
	got := many.And(small)
	if got.Cardinality() > 10 {
		t.Fatalf("intersection too large: %d", got.Cardinality())
	}
	if got.Containers() > 0 && got.conts[0].ctype() != typeArray {
		t.Fatalf("small intersection stored as %s, want array", got.conts[0].ctype())
	}
	if err := got.checkOptimal(); err != nil {
		t.Fatal(err)
	}
}

func TestAddRangeBoundaries(t *testing.T) {
	bm := New()
	bm.AddRange(0, 0) // empty
	if !bm.IsEmpty() {
		t.Fatal("empty range must not add anything")
	}
	bm.AddRange(65535, 65536+65535) // crosses a container boundary
	if bm.Cardinality() != 65536 {
		t.Fatalf("cardinality = %d, want 65536", bm.Cardinality())
	}
	if !bm.Contains(65535) || !bm.Contains(65536) || !bm.Contains(65536+65534) {
		t.Fatal("boundary values missing")
	}
	// Full key space.
	full := New()
	full.AddRange(0, MaxValue)
	if full.Cardinality() != MaxValue {
		t.Fatalf("cardinality = %d, want %d", full.Cardinality(), MaxValue)
	}
	if !full.Contains(0) || !full.Contains(uint32(MaxValue-1)) {
		t.Fatal("full range missing extremes")
	}
	if err := full.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := full.checkOptimal(); err != nil {
		t.Fatal(err)
	}
	// End beyond the key space is clamped.
	clamped := New()
	clamped.AddRange(MaxValue-10, MaxValue+1000)
	if clamped.Cardinality() != 10 {
		t.Fatalf("cardinality = %d, want 10", clamped.Cardinality())
	}
}

func TestRemoveRange(t *testing.T) {
	bm := New()
	bm.AddRange(0, 1_000_000)
	bm.RemoveRange(500_000, 600_000)
	if bm.Cardinality() != 900_000 {
		t.Fatalf("cardinality = %d, want 900000", bm.Cardinality())
	}
	if bm.Contains(550_000) {
		t.Fatal("removed value still present")
	}
	bm.RemoveRange(0, MaxValue)
	if !bm.IsEmpty() {
		t.Fatal("bitmap should be empty after full remove")
	}
	if len(bm.keys) != 0 || len(bm.conts) != 0 {
		t.Fatal("containers leaked after full remove")
	}
}

func TestBitmapRandomOpsVsModel(t *testing.T) {
	rng := rand.New(rand.NewSource(2024))
	bm := New()
	model := map[uint32]bool{}
	const keySpace = 1 << 20
	for i := 0; i < 15_000; i++ {
		switch rng.Intn(10) {
		case 0, 1, 2, 3:
			v := uint32(rng.Intn(keySpace))
			bm.Add(v)
			model[v] = true
		case 4, 5:
			start := uint32(rng.Intn(keySpace))
			length := uint32(rng.Intn(3000))
			end := uint64(start) + uint64(length)
			bm.AddRange(uint64(start), end)
			for v := start; uint64(v) < end; v++ {
				model[v] = true
			}
		case 6, 7:
			v := uint32(rng.Intn(keySpace))
			bm.Remove(v)
			delete(model, v)
		case 8:
			start := uint32(rng.Intn(keySpace))
			length := uint32(rng.Intn(3000))
			end := uint64(start) + uint64(length)
			bm.RemoveRange(uint64(start), end)
			for v := start; uint64(v) < end; v++ {
				delete(model, v)
			}
		case 9:
			if bm.Cardinality() != uint64(len(model)) {
				t.Fatalf("step %d: cardinality = %d, want %d", i, bm.Cardinality(), len(model))
			}
		}
	}
	if bm.Cardinality() != uint64(len(model)) {
		t.Fatalf("final cardinality = %d, want %d", bm.Cardinality(), len(model))
	}
	for v := range model {
		if !bm.Contains(v) {
			t.Fatalf("model value %d missing from bitmap", v)
		}
	}
	bm.ForEach(func(v uint32) {
		if !model[v] {
			t.Fatalf("bitmap value %d missing from model", v)
		}
	})
	if err := bm.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestBitmapSetOpsVsModel(t *testing.T) {
	rng := rand.New(rand.NewSource(555))
	const keySpace = 1 << 18
	build := func() (*Bitmap, map[uint32]bool) {
		bm := New()
		model := map[uint32]bool{}
		for i := 0; i < 200; i++ {
			if rng.Intn(2) == 0 {
				v := uint32(rng.Intn(keySpace))
				bm.Add(v)
				model[v] = true
			} else {
				start := uint32(rng.Intn(keySpace))
				length := uint32(1 + rng.Intn(5000))
				end := uint64(start) + uint64(length)
				bm.AddRange(uint64(start), end)
				for v := start; uint64(v) < end; v++ {
					model[v] = true
				}
			}
		}
		return bm, model
	}
	a, ma := build()
	b, mb := build()
	andModel := map[uint32]bool{}
	for v := range ma {
		if mb[v] {
			andModel[v] = true
		}
	}
	orModel := map[uint32]bool{}
	for v := range ma {
		orModel[v] = true
	}
	for v := range mb {
		orModel[v] = true
	}
	xorModel := map[uint32]bool{}
	for v := range ma {
		if !mb[v] {
			xorModel[v] = true
		}
	}
	for v := range mb {
		if !ma[v] {
			xorModel[v] = true
		}
	}
	cases := []struct {
		name  string
		got   *Bitmap
		model map[uint32]bool
	}{
		{"and", a.And(b), andModel},
		{"or", a.Or(b), orModel},
		{"xor", a.Xor(b), xorModel},
	}
	for _, tc := range cases {
		if tc.got.Cardinality() != uint64(len(tc.model)) {
			t.Fatalf("%s: cardinality = %d, want %d", tc.name, tc.got.Cardinality(), len(tc.model))
		}
		for v := range tc.model {
			if !tc.got.Contains(v) {
				t.Fatalf("%s: missing %d", tc.name, v)
			}
		}
		if err := tc.got.Validate(); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if err := tc.got.checkOptimal(); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
	}
}

func TestBatchOps(t *testing.T) {
	rng := rand.New(rand.NewSource(31))
	const n = 5
	bms := make([]*Bitmap, n)
	models := make([]map[uint32]bool, n)
	for i := 0; i < n; i++ {
		bms[i] = New()
		models[i] = map[uint32]bool{}
		for j := 0; j < 500; j++ {
			v := uint32(rng.Intn(100_000))
			bms[i].Add(v)
			models[i][v] = true
		}
	}
	and := AndAll(bms...)
	andWant := map[uint32]bool{}
	for v := range models[0] {
		ok := true
		for i := 1; i < n; i++ {
			if !models[i][v] {
				ok = false
				break
			}
		}
		if ok {
			andWant[v] = true
		}
	}
	if and.Cardinality() != uint64(len(andWant)) {
		t.Fatalf("AndAll cardinality = %d, want %d", and.Cardinality(), len(andWant))
	}
	or := OrAll(bms...)
	orWant := map[uint32]bool{}
	for _, m := range models {
		for v := range m {
			orWant[v] = true
		}
	}
	if or.Cardinality() != uint64(len(orWant)) {
		t.Fatalf("OrAll cardinality = %d, want %d", or.Cardinality(), len(orWant))
	}
	xor := XorAll(bms...)
	xorWant := map[uint32]bool{}
	for _, m := range models {
		for v := range m {
			if xorWant[v] {
				delete(xorWant, v)
			} else {
				xorWant[v] = true
			}
		}
	}
	if xor.Cardinality() != uint64(len(xorWant)) {
		t.Fatalf("XorAll cardinality = %d, want %d", xor.Cardinality(), len(xorWant))
	}
	if AndAll().Cardinality() != 0 {
		t.Fatal("AndAll of no bitmaps must be empty")
	}
}

func TestCloneEquals(t *testing.T) {
	bm := New()
	bm.AddRange(10, 20_000)
	bm.Add(5)
	clone := bm.Clone()
	if !bm.Equals(clone) {
		t.Fatal("clone must equal original")
	}
	clone.Remove(15)
	if bm.Equals(clone) {
		t.Fatal("mutating clone must not affect original")
	}
	if !bm.Contains(15) {
		t.Fatal("original lost a value after clone mutation")
	}
}

// TestNoContainerLeak verifies that containers are released as soon as
// they become empty, across many add/remove cycles.
func TestNoContainerLeak(t *testing.T) {
	bm := New()
	rng := rand.New(rand.NewSource(8))
	for cycle := 0; cycle < 100; cycle++ {
		var added []uint32
		for i := 0; i < 500; i++ {
			v := uint32(rng.Intn(1 << 20))
			bm.Add(v)
			added = append(added, v)
		}
		bm.AddRange(5_000_000, 5_010_000)
		for _, v := range added {
			bm.Remove(v)
		}
		bm.RemoveRange(5_000_000, 5_010_000)
		if !bm.IsEmpty() {
			t.Fatalf("cycle %d: bitmap not empty after removing everything", cycle)
		}
		if len(bm.keys) != 0 || len(bm.conts) != 0 {
			t.Fatalf("cycle %d: %d containers leaked", cycle, len(bm.conts))
		}
	}
}
