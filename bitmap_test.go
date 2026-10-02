package roaring

import (
	"math"
	"math/rand"
	"testing"
)

func TestAddContainsBoundaries(t *testing.T) {
	b := New()
	ids := []uint64{0, 1, 65535, 65536, 65537, math.MaxUint64 - 1, math.MaxUint64}
	for _, id := range ids {
		b.Add(id)
	}
	for _, id := range ids {
		if !b.Contains(id) {
			t.Fatalf("missing id %d", id)
		}
	}
	for _, id := range []uint64{2, 65534, 65538, math.MaxUint64 - 2} {
		if b.Contains(id) {
			t.Fatalf("unexpected id %d", id)
		}
	}
	if got := b.Cardinality(); got != uint64(len(ids)) {
		t.Fatalf("cardinality = %d, want %d", got, len(ids))
	}
	// Duplicate adds are idempotent.
	for _, id := range ids {
		b.Add(id)
	}
	if got := b.Cardinality(); got != uint64(len(ids)) {
		t.Fatalf("cardinality after duplicates = %d, want %d", got, len(ids))
	}
}

func TestAddRangeBoundaries(t *testing.T) {
	b := New()
	b.AddRange(10, 10) // empty range: no-op
	if b.Cardinality() != 0 {
		t.Fatal("empty range should not add anything")
	}
	b.AddRange(0, 1)                           // single element at minimum
	b.AddRange(math.MaxUint64, math.MaxUint64) // empty at max
	if !b.Contains(0) || b.Cardinality() != 1 {
		t.Fatal("single-element range failed")
	}
	b.AddRange(65530, 65540) // crosses a container boundary
	for v := uint64(65530); v < 65540; v++ {
		if !b.Contains(v) {
			t.Fatalf("missing %d in cross-boundary range", v)
		}
	}
	if b.Contains(65529) || b.Contains(65540) {
		t.Fatal("range leaked past its edges")
	}
}

func TestBitmapOpsAgainstReference(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	for iter := 0; iter < 30; iter++ {
		bitmaps := []*Bitmap{New(), New()}
		refs := []map[uint64]bool{{}, {}}
		for k := 0; k < 2; k++ {
			// sparse random IDs
			for i := 0; i < 500; i++ {
				v := uint64(rng.Intn(1 << 24))
				bitmaps[k].Add(v)
				refs[k][v] = true
			}
			// dense contiguous ranges
			for i := 0; i < 3; i++ {
				start := uint64(rng.Intn(1 << 20))
				end := start + uint64(rng.Intn(3000))
				bitmaps[k].AddRange(start, end)
				for v := start; v < end; v++ {
					refs[k][v] = true
				}
			}
		}
		and, or, xor := map[uint64]bool{}, map[uint64]bool{}, map[uint64]bool{}
		for v := range refs[0] {
			or[v] = true
			if refs[1][v] {
				and[v] = true
			} else {
				xor[v] = true
			}
		}
		for v := range refs[1] {
			or[v] = true
			if !refs[0][v] {
				xor[v] = true
			}
		}
		check := func(bm *Bitmap, want map[uint64]bool, op string) {
			t.Helper()
			if got := bm.Cardinality(); got != uint64(len(want)) {
				t.Fatalf("iter %d %s: cardinality = %d, want %d", iter, op, got, len(want))
			}
			for v := range want {
				if !bm.Contains(v) {
					t.Fatalf("iter %d %s: missing %d", iter, op, v)
				}
			}
		}
		check(bitmaps[0].And(bitmaps[1]), and, "AND")
		check(bitmaps[0].Or(bitmaps[1]), or, "OR")
		check(bitmaps[0].Xor(bitmaps[1]), xor, "XOR")
	}
}

func TestBatchOps(t *testing.T) {
	rng := rand.New(rand.NewSource(13))
	var bs []*Bitmap
	var refs []map[uint64]bool
	for i := 0; i < 5; i++ {
		b := New()
		ref := map[uint64]bool{}
		for j := 0; j < 2000; j++ {
			v := uint64(rng.Intn(100000))
			b.Add(v)
			ref[v] = true
		}
		bs = append(bs, b)
		refs = append(refs, ref)
	}
	and := AndBatch(bs...)
	for v := range refs[0] {
		inAll := true
		for _, ref := range refs[1:] {
			if !ref[v] {
				inAll = false
				break
			}
		}
		if inAll != and.Contains(v) {
			t.Fatalf("AndBatch mismatch at %d", v)
		}
	}
	or := OrBatch(bs...)
	wantOr := 0
	seen := map[uint64]bool{}
	for _, ref := range refs {
		for v := range ref {
			if !seen[v] {
				seen[v] = true
				wantOr++
			}
		}
	}
	if got := or.Cardinality(); got != uint64(wantOr) {
		t.Fatalf("OrBatch cardinality = %d, want %d", got, wantOr)
	}
	xor := XorBatch(bs...)
	count := map[uint64]int{}
	for _, ref := range refs {
		for v := range ref {
			count[v]++
		}
	}
	for v, c := range count {
		if (c%2 == 1) != xor.Contains(v) {
			t.Fatalf("XorBatch mismatch at %d", v)
		}
	}
	// Empty batch and single-element batch.
	if AndBatch().Cardinality() != 0 || OrBatch().Cardinality() != 0 || XorBatch().Cardinality() != 0 {
		t.Fatal("empty batch should be empty")
	}
	if got := AndBatch(bs[0]); !got.Equal(bs[0]) {
		t.Fatal("single-element AndBatch should equal its input")
	}
}

func TestEmptyAndSelfOps(t *testing.T) {
	a, e := New(), New()
	a.AddRange(100, 200)
	a.Add(7)
	if got := a.And(e); got.Cardinality() != 0 {
		t.Fatal("AND with empty must be empty")
	}
	if got := a.Or(e); !got.Equal(a) {
		t.Fatal("OR with empty must be identity")
	}
	if got := a.Xor(e); !got.Equal(a) {
		t.Fatal("XOR with empty must be identity")
	}
	if got := a.And(a); !got.Equal(a) {
		t.Fatal("AND with self must be identity")
	}
	if got := a.Xor(a); got.Cardinality() != 0 {
		t.Fatal("XOR with self must be empty")
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	b := New()
	rng := rand.New(rand.NewSource(17))
	for i := 0; i < 5000; i++ {
		b.Add(uint64(rng.Intn(1 << 30)))
	}
	b.AddRange(1<<20, 1<<20+100000) // dense run
	b.Add(0)
	b.Add(math.MaxUint64)
	b.Optimize()
	back, err := Unmarshal(b.Marshal())
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !back.Equal(b) {
		t.Fatal("round trip mismatch")
	}
	if got, want := back.Stats(), b.Stats(); got.Containers != want.Containers {
		t.Fatalf("container count = %d, want %d", got.Containers, want.Containers)
	}
}

func TestUnmarshalRejectsCorruptData(t *testing.T) {
	b := New()
	b.AddRange(0, 100000)
	for i := 0; i < 100; i++ {
		b.Add(uint64(i * 7919))
	}
	good := b.Marshal()
	cases := [][]byte{
		nil,
		{},
		good[:4],           // truncated header
		good[:len(good)/2], // truncated body
		good[:len(good)-1], // truncated CRC
		append(good, 0),    // trailing garbage
	}
	for i, c := range cases {
		if _, err := Unmarshal(c); err != ErrCorrupt {
			t.Fatalf("case %d: expected ErrCorrupt, got %v", i, err)
		}
	}
	// Bit flips anywhere must be caught by the CRC.
	for _, pos := range []int{0, 8, len(good) / 2, len(good) - 2} {
		bad := append([]byte(nil), good...)
		bad[pos] ^= 0xFF
		if _, err := Unmarshal(bad); err != ErrCorrupt {
			t.Fatalf("bit flip at %d: expected ErrCorrupt, got %v", pos, err)
		}
	}
}
