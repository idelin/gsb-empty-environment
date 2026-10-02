package roaring

import (
	"math/rand"
	"testing"
)

func TestAddSparse(t *testing.T) {
	bm := New()
	ids := []uint32{0, 1, 5, 100, 65535, 65536, 65537, 1 << 20, 1<<31 - 1, 1<<32 - 1}
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
	for _, id := range []uint32{2, 6, 65534, 65538, 1<<20 + 1, 1<<32 - 2} {
		if bm.Contains(id) {
			t.Fatalf("unexpected id %d", id)
		}
	}
	// Duplicates must not change cardinality.
	for _, id := range ids {
		bm.Add(id)
	}
	if got := bm.Cardinality(); got != uint64(len(ids)) {
		t.Fatalf("cardinality after re-add = %d, want %d", got, len(ids))
	}
}

func TestBoundaryValues(t *testing.T) {
	bm := New()
	for _, id := range []uint32{0, 65535, 65536, 131071, 1<<32 - 1} {
		bm.Add(id)
	}
	for _, id := range []uint32{0, 65535, 65536, 131071, 1<<32 - 1} {
		if !bm.Contains(id) {
			t.Fatalf("missing boundary id %d", id)
		}
	}
	if bm.Contains(65537) || bm.Contains(1<<32-2) {
		t.Fatal("unexpected boundary hit")
	}
}

func TestArrayToBitmapConversion(t *testing.T) {
	bm := New()
	// 4096 scattered values in one container: stays an array.
	for i := 0; i < arrayMaxCardinality; i++ {
		bm.Add(uint32(i * 16)) // stride 16, max 65520
	}
	if bm.entries[0].c.typ() != containerTypeArray {
		t.Fatalf("expected array container at %d values, got type %d", arrayMaxCardinality, bm.entries[0].c.typ())
	}
	// One more value crosses the threshold: promote to bitmap.
	bm.Add(uint32(1))
	if bm.entries[0].c.typ() != containerTypeBitmap {
		t.Fatalf("expected bitmap container at %d values, got type %d", arrayMaxCardinality+1, bm.entries[0].c.typ())
	}
	if got := bm.Cardinality(); got != uint64(arrayMaxCardinality+1) {
		t.Fatalf("cardinality = %d, want %d", got, arrayMaxCardinality+1)
	}
	for i := 0; i < arrayMaxCardinality; i++ {
		if !bm.Contains(uint32(i * 16)) {
			t.Fatalf("missing %d after promotion", i*16)
		}
	}
}

func TestAddRangeSelectsRunContainer(t *testing.T) {
	bm := New()
	bm.AddRange(1000, 2000)
	if bm.entries[0].c.typ() != containerTypeRun {
		t.Fatalf("expected run container for dense range, got type %d", bm.entries[0].c.typ())
	}
	if got := bm.Cardinality(); got != 1000 {
		t.Fatalf("cardinality = %d, want 1000", got)
	}
	for _, id := range []uint32{1000, 1500, 1999} {
		if !bm.Contains(id) {
			t.Fatalf("missing %d", id)
		}
	}
	if bm.Contains(999) || bm.Contains(2000) {
		t.Fatal("range bounds leaked")
	}
}

func TestAddRangeFullContainer(t *testing.T) {
	bm := New()
	bm.AddRange(0, 65536) // entire container 0
	if got := bm.Cardinality(); got != 65536 {
		t.Fatalf("cardinality = %d, want 65536", got)
	}
	if bm.entries[0].c.typ() != containerTypeRun {
		t.Fatalf("full container should be a single run, got type %d", bm.entries[0].c.typ())
	}
	if !bm.Contains(0) || !bm.Contains(65535) || bm.Contains(65536) {
		t.Fatal("full container bounds wrong")
	}
}

func TestAddRangeSpanningContainers(t *testing.T) {
	bm := New()
	bm.AddRange(65530, 65536*3+10) // tail of c0, all of c1, c2, head of c3
	want := uint64(65536*3 + 10 - 65530)
	if got := bm.Cardinality(); got != want {
		t.Fatalf("cardinality = %d, want %d", got, want)
	}
	if len(bm.entries) != 4 {
		t.Fatalf("containers = %d, want 4", len(bm.entries))
	}
	for _, id := range []uint32{65530, 65535, 65536, 65536 * 2, 65536*3 + 9} {
		if !bm.Contains(id) {
			t.Fatalf("missing %d", id)
		}
	}
	if bm.Contains(65529) || bm.Contains(65536*3+10) {
		t.Fatal("range bounds leaked")
	}
}

func TestAddRangeToMaxUint32(t *testing.T) {
	bm := New()
	bm.AddRange(1<<32-10, 1<<32) // last 10 values of the key space
	if got := bm.Cardinality(); got != 10 {
		t.Fatalf("cardinality = %d, want 10", got)
	}
	if !bm.Contains(1<<32 - 1) {
		t.Fatal("missing max uint32")
	}
}

func TestAddRangeMergesWithExisting(t *testing.T) {
	bm := New()
	bm.AddRange(100, 200)
	bm.AddRange(150, 300) // overlapping
	bm.AddRange(400, 500) // disjoint
	if got := bm.Cardinality(); got != 300 {
		t.Fatalf("cardinality = %d, want 300", got)
	}
	if !bm.Contains(100) || !bm.Contains(299) || bm.Contains(300) || !bm.Contains(499) {
		t.Fatal("merged range contents wrong")
	}
}

func TestCloneIndependence(t *testing.T) {
	bm := New()
	bm.AddRange(0, 5000)
	bm.Add(1 << 20)
	clone := bm.Clone()
	clone.Add(6000)
	clone.AddRange(7000, 8000)
	if bm.Contains(6000) || bm.Contains(7000) {
		t.Fatal("mutation of clone leaked into original")
	}
	if !clone.Contains(6000) || !clone.Contains(7000) {
		t.Fatal("clone lost its own additions")
	}
	if bm.Cardinality() != 5001 {
		t.Fatalf("original cardinality = %d, want 5001", bm.Cardinality())
	}
}

func TestToArraySorted(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	bm := New()
	want := make(map[uint32]bool)
	for i := 0; i < 5000; i++ {
		id := r.Uint32()
		bm.Add(id)
		want[id] = true
	}
	bm.AddRange(100, 50000)
	for id := uint32(100); id < 50000; id++ {
		want[id] = true
	}
	got := bm.ToArray()
	if len(got) != len(want) {
		t.Fatalf("ToArray len = %d, want %d", len(got), len(want))
	}
	for i := 1; i < len(got); i++ {
		if got[i] <= got[i-1] {
			t.Fatal("ToArray not strictly increasing")
		}
	}
	for _, id := range got {
		if !want[id] {
			t.Fatalf("ToArray contains unexpected %d", id)
		}
	}
}

func TestStats(t *testing.T) {
	bm := New()
	bm.AddRange(0, 65536)      // run container
	for i := 0; i < 100; i++ { // array container
		bm.Add(uint32(2<<16 + i*100))
	}
	for i := 0; i < 5000; i++ { // bitmap container
		bm.Add(uint32(3<<16 + i*13))
	}
	s := bm.Stats()
	if s.Containers != 3 {
		t.Fatalf("containers = %d, want 3", s.Containers)
	}
	if s.RunContainers != 1 || s.ArrayContainers != 1 || s.BitmapContainers != 1 {
		t.Fatalf("container mix = %d/%d/%d, want 1/1/1",
			s.ArrayContainers, s.BitmapContainers, s.RunContainers)
	}
	if s.Cardinality != 65536+100+5000 {
		t.Fatalf("cardinality = %d", s.Cardinality)
	}
	if s.SerializedBytes != bm.SerializedSize() {
		t.Fatalf("serialized bytes mismatch")
	}
}

func TestEmptyBitmap(t *testing.T) {
	bm := New()
	if !bm.IsEmpty() {
		t.Fatal("new bitmap not empty")
	}
	if bm.Cardinality() != 0 || bm.Contains(0) {
		t.Fatal("empty bitmap misbehaving")
	}
	if len(bm.ToArray()) != 0 {
		t.Fatal("empty ToArray not empty")
	}
}
