package roaring

import (
	"bytes"
	"math/bits"
	"math/rand"
	"testing"
)

func TestSparseIDsUseArrayContainer(t *testing.T) {
	bm := New()
	ids := []uint32{1, 100, 70000, 1 << 20, (1 << 20) + 5}
	for _, id := range ids {
		bm.Add(id)
	}
	if got := bm.Cardinality(); got != uint64(len(ids)) {
		t.Fatalf("cardinality = %d, want %d", got, len(ids))
	}
	for _, id := range ids {
		if !bm.Contains(id) {
			t.Fatalf("missing id %d", id)
		}
	}
	if bm.Contains(2) || bm.Contains(1<<20+7) {
		t.Fatal("unexpected membership")
	}
	st := bm.Stats()
	if st.ArrayContainers == 0 || st.BitmapContainers != 0 || st.RunContainers != 0 {
		t.Fatalf("expected only array containers, got %+v", st)
	}
}

func TestDenseRangeUsesRunContainer(t *testing.T) {
	bm := New()
	bm.AddRange(3, 1000, 60000)
	if got, want := bm.Cardinality(), uint64(59001); got != want {
		t.Fatalf("cardinality = %d, want %d", got, want)
	}
	st := bm.Stats()
	if st.RunContainers != 1 || st.ArrayContainers != 0 || st.BitmapContainers != 0 {
		t.Fatalf("expected exactly one run container, got %+v", st)
	}
	for _, id := range []uint32{3<<16 | 1000, 3<<16 | 60000, 3<<16 | 5000} {
		if !bm.Contains(id) {
			t.Fatalf("missing id %d", id)
		}
	}
	if bm.Contains(3<<16|999) || bm.Contains(3<<16|60001) {
		t.Fatal("unexpected membership outside range")
	}
}

func TestDenseRandomUsesBitmapContainer(t *testing.T) {
	bm := New()
	rnd := rand.New(rand.NewSource(42))
	for i := 0; i < 20000; i++ {
		bm.Add(uint32(rnd.Intn(65536)))
	}
	st := bm.Stats()
	if st.BitmapContainers != 1 {
		t.Fatalf("expected one bitmap container, got %+v", st)
	}
}

func TestContainerTransitions(t *testing.T) {
	// array -> run: contiguous growth past the array threshold.
	bm := New()
	for i := uint32(0); i < 5000; i++ {
		bm.Add(i)
	}
	if st := bm.Stats(); st.RunContainers != 1 {
		t.Fatalf("expected conversion to run container, got %+v", st)
	}
	// array -> bitmap: scattered growth past the array threshold.
	bm2 := New()
	rnd := rand.New(rand.NewSource(7))
	for i := 0; i < 5000; i++ {
		bm2.Add(uint32(rnd.Intn(65536)))
	}
	if st := bm2.Stats(); st.BitmapContainers != 1 {
		t.Fatalf("expected conversion to bitmap container, got %+v", st)
	}
	// run -> bitmap: fragmenting a run container with scattered adds.
	bm3 := New()
	bm3.AddRange(0, 0, 100)
	rnd3 := rand.New(rand.NewSource(11))
	for i := 0; i < 3000; i++ {
		bm3.Add(uint32(rnd3.Intn(65536)))
	}
	if st := bm3.Stats(); st.RunContainers != 0 {
		t.Fatalf("expected run container to convert away, got %+v", st)
	}
}

func TestFullRangeContainer(t *testing.T) {
	bm := New()
	bm.AddRange(9, 0, 65535)
	if got := bm.Cardinality(); got != 65536 {
		t.Fatalf("cardinality = %d, want 65536", got)
	}
	if !bm.Contains(9<<16) || !bm.Contains(9<<16|65535) {
		t.Fatal("missing boundary ids")
	}
	if st := bm.Stats(); st.RunContainers != 1 {
		t.Fatalf("expected run container for full range, got %+v", st)
	}
}

func TestAgainstReferenceModel(t *testing.T) {
	rnd := rand.New(rand.NewSource(99))
	bm := New()
	ref := make(map[uint32]struct{})
	for i := 0; i < 30000; i++ {
		switch rnd.Intn(4) {
		case 0, 1:
			id := uint32(rnd.Intn(1 << 22))
			bm.Add(id)
			ref[id] = struct{}{}
		case 2:
			key := uint16(rnd.Intn(64))
			lo := uint32(rnd.Intn(65536))
			hi := lo + uint32(rnd.Intn(3000))
			if hi > 65535 {
				hi = 65535
			}
			bm.AddRange(key, uint16(lo), uint16(hi))
			for v := lo; v <= hi; v++ {
				ref[uint32(key)<<16|v] = struct{}{}
			}
		case 3:
			id := uint32(rnd.Intn(1 << 22))
			_, inRef := ref[id]
			if bm.Contains(id) != inRef {
				t.Fatalf("membership mismatch for %d", id)
			}
		}
	}
	if got := bm.Cardinality(); got != uint64(len(ref)) {
		t.Fatalf("cardinality = %d, want %d", got, len(ref))
	}
	for id := range ref {
		if !bm.Contains(id) {
			t.Fatalf("missing id %d", id)
		}
	}
	checkContainerInvariants(t, bm)
}

func TestAndOrXorAgainstModel(t *testing.T) {
	rnd := rand.New(rand.NewSource(1234))
	build := func() (*Bitmap, map[uint32]bool) {
		bm := New()
		ref := map[uint32]bool{}
		for i := 0; i < 3000; i++ {
			switch rnd.Intn(3) {
			case 0: // sparse singles
				id := uint32(rnd.Intn(1 << 21))
				bm.Add(id)
				ref[id] = true
			case 1: // dense contiguous ranges
				key := uint16(rnd.Intn(32))
				lo := uint32(rnd.Intn(60000))
				hi := lo + uint32(rnd.Intn(5000))
				if hi > 65535 {
					hi = 65535
				}
				bm.AddRange(key, uint16(lo), uint16(hi))
				for v := lo; v <= hi; v++ {
					ref[uint32(key)<<16|v] = true
				}
			case 2: // dense random -> bitmap containers
				key := uint16(rnd.Intn(32))
				for j := 0; j < 200; j++ {
					id := uint32(key)<<16 | uint32(rnd.Intn(65536))
					bm.Add(id)
					ref[id] = true
				}
			}
		}
		return bm, ref
	}
	b1, r1 := build()
	b2, r2 := build()

	andRef := map[uint32]bool{}
	for id := range r1 {
		if r2[id] {
			andRef[id] = true
		}
	}
	orRef := map[uint32]bool{}
	for id := range r1 {
		orRef[id] = true
	}
	for id := range r2 {
		orRef[id] = true
	}
	xorRef := map[uint32]bool{}
	for id := range orRef {
		if r1[id] != r2[id] {
			xorRef[id] = true
		}
	}

	check := func(name string, got *Bitmap, want map[uint32]bool) {
		t.Helper()
		if got.Cardinality() != uint64(len(want)) {
			t.Fatalf("%s: cardinality = %d, want %d", name, got.Cardinality(), len(want))
		}
		for id := range want {
			if !got.Contains(id) {
				t.Fatalf("%s: missing id %d", name, id)
			}
		}
		checkContainerInvariants(t, got)
	}
	check("and", b1.And(b2), andRef)
	check("or", b1.Or(b2), orRef)
	check("xor", b1.Xor(b2), xorRef)

	// Operands must not be mutated by the operations.
	if b1.Cardinality() != uint64(len(r1)) || b2.Cardinality() != uint64(len(r2)) {
		t.Fatal("set operation mutated its operands")
	}
}

func TestSelfOperations(t *testing.T) {
	bm := New()
	bm.Add(1)
	bm.AddRange(2, 10, 500)
	if !bm.And(bm).Equals(bm) {
		t.Fatal("And(x, x) != x")
	}
	if !bm.Or(bm).Equals(bm) {
		t.Fatal("Or(x, x) != x")
	}
	if bm.Xor(bm).Cardinality() != 0 {
		t.Fatal("Xor(x, x) != empty")
	}
}

func TestSerializationRoundTrip(t *testing.T) {
	bm := New()
	for _, id := range []uint32{1, 2, 3, 1 << 20} {
		bm.Add(id)
	}
	bm.AddRange(5, 100, 40000)
	rnd := rand.New(rand.NewSource(8))
	for i := 0; i < 10000; i++ {
		bm.Add(uint32(7)<<16 | uint32(rnd.Intn(65536)))
	}
	data := bm.Marshal()
	back := New()
	if err := back.Unmarshal(data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !bm.Equals(back) {
		t.Fatal("round trip mismatch")
	}
	if !bytes.Equal(bm.Marshal(), back.Marshal()) {
		t.Fatal("marshal is not deterministic")
	}
}

func TestUnmarshalRejectsCorruptData(t *testing.T) {
	bm := New()
	bm.Add(1)
	bm.AddRange(2, 10, 100)
	rnd := rand.New(rand.NewSource(6))
	for i := 0; i < 10000; i++ {
		bm.Add(uint32(9)<<16 | uint32(rnd.Intn(65536)))
	}
	data := bm.Marshal()

	cases := map[string][]byte{
		"empty":         nil,
		"short magic":   data[:3],
		"truncated":     data[:len(data)-1],
		"header only":   data[:8],
		"trailing junk": append(append([]byte(nil), data...), 0x00),
	}
	for name, tc := range cases {
		if err := New().Unmarshal(tc); err == nil {
			t.Fatalf("%s: expected error, got nil", name)
		}
	}
	// Bit flip inside the bitmap container's words: cardinality checksum
	// must catch it.
	corrupt := append([]byte(nil), data...)
	corrupt[len(corrupt)-100] ^= 0xff
	if err := New().Unmarshal(corrupt); err == nil {
		t.Fatal("expected error for corrupted bitmap words")
	}
}

func checkContainerInvariants(t *testing.T, bm *Bitmap) {
	t.Helper()
	bm.mu.RLock()
	defer bm.mu.RUnlock()
	for key, c := range bm.containers {
		switch cc := c.(type) {
		case *arrayContainer:
			if len(cc.vals) > arrayMaxSize {
				t.Errorf("key %d: oversized array container (%d)", key, len(cc.vals))
			}
			for i := 1; i < len(cc.vals); i++ {
				if cc.vals[i] <= cc.vals[i-1] {
					t.Errorf("key %d: array container not strictly sorted", key)
				}
			}
		case *runContainer:
			if len(cc.runs) >= runMaxCount {
				t.Errorf("key %d: oversized run container (%d runs)", key, len(cc.runs))
			}
			for i := 1; i < len(cc.runs); i++ {
				if uint32(cc.runs[i].start) <= uint32(cc.runs[i-1].last)+1 {
					t.Errorf("key %d: overlapping or unsorted runs", key)
				}
			}
		case *bitmapContainer:
			sum := 0
			for _, w := range cc.words {
				sum += bits.OnesCount64(w)
			}
			if sum != cc.card {
				t.Errorf("key %d: bitmap cardinality %d != popcount %d", key, cc.card, sum)
			}
		}
	}
}

// Regression: bitmapContainer.runs() used to drop the run containing value
// 0, losing it when a bitmap container was optimized into a run container.
func TestOptimizeBitmapKeepsZeroValue(t *testing.T) {
	bm1 := New()
	rnd := rand.New(rand.NewSource(17))
	for i := 0; i < 10000; i++ {
		bm1.Add(uint32(rnd.Intn(65536))) // dense -> bitmap container
	}
	bm2 := New()
	bm2.Add(0) // value 0 lives in an array container
	if st := bm1.Stats(); st.BitmapContainers != 1 {
		t.Fatalf("setup: expected bitmap container, got %+v", st)
	}
	or := bm1.Or(bm2)
	if !or.Contains(0) {
		t.Fatal("value 0 lost during bitmap->run optimization")
	}
	// Same path through contiguous values starting at 0.
	bm3 := New()
	bm3.AddRange(0, 0, 5000)
	bm4 := New()
	rnd2 := rand.New(rand.NewSource(18))
	for i := 0; i < 10000; i++ {
		bm4.Add(uint32(rnd2.Intn(65536)))
	}
	or2 := bm3.Or(bm4)
	for v := uint32(0); v <= 5000; v++ {
		if !or2.Contains(v) {
			t.Fatalf("value %d lost during bitmap->run optimization", v)
		}
	}
}
