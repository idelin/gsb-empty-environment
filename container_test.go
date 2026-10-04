package bitmap

import "testing"

// Container representation transitions.

func TestArrayToBitmapConversion(t *testing.T) {
	b := New()
	for i := 0; i < arrayMaxSize; i++ {
		b.Add(uint32(i*3 + 1)) // sparse-ish, stays array
	}
	if a, bm, r := b.Stats(); a != 1 || bm != 0 || r != 0 {
		t.Fatalf("want 1 array container, got arrays=%d bitmaps=%d runs=%d", a, bm, r)
	}
	b.Add(uint32(arrayMaxSize*3 + 1)) // 4097th element
	if a, bm, r := b.Stats(); a != 0 || bm != 1 || r != 0 {
		t.Fatalf("want conversion to bitmap, got arrays=%d bitmaps=%d runs=%d", a, bm, r)
	}
	// Remove back down to arrayMaxSize -> converts back to array.
	if !b.Remove(uint32(arrayMaxSize*3 + 1)) {
		t.Fatal("remove should succeed")
	}
	if a, bm, r := b.Stats(); a != 1 || bm != 0 || r != 0 {
		t.Fatalf("want conversion back to array, got arrays=%d bitmaps=%d runs=%d", a, bm, r)
	}
	if got := b.Cardinality(); got != uint64(arrayMaxSize) {
		t.Fatalf("cardinality = %d, want %d", got, arrayMaxSize)
	}
}

func TestRunContainerPreferredForDenseRange(t *testing.T) {
	b := New()
	b.AddRange(0, 100000)
	a, bm, r := b.Stats()
	if bm != 0 || a != 0 {
		t.Fatalf("dense range should use run containers, got arrays=%d bitmaps=%d runs=%d", a, bm, r)
	}
	if r != 2 { // 100000 spans two 65536-containers
		t.Fatalf("want 2 run containers, got %d", r)
	}
	if got := b.Cardinality(); got != 100000 {
		t.Fatalf("cardinality = %d, want 100000", got)
	}
}

func TestFullContainerBecomesSingleRun(t *testing.T) {
	b := New()
	b.AddRange(0, 65536)
	if a, bm, r := b.Stats(); a != 0 || bm != 0 || r != 1 {
		t.Fatalf("full container should be one run, got arrays=%d bitmaps=%d runs=%d", a, bm, r)
	}
	if got := b.Cardinality(); got != 65536 {
		t.Fatalf("cardinality = %d, want 65536", got)
	}
}

func TestSparseStaysArray(t *testing.T) {
	b := New()
	for i := 0; i < 1000; i++ {
		b.Add(uint32(i * 1000003)) // scattered across many containers
	}
	a, bm, r := b.Stats()
	if bm != 0 || r != 0 || a == 0 {
		t.Fatalf("sparse ids should use array containers, got arrays=%d bitmaps=%d runs=%d", a, bm, r)
	}
}

func TestRunContainerAddRemoveBoundaries(t *testing.T) {
	r := &runContainer{}
	c, _ := r.add(5)
	r = c.(*runContainer)
	c, _ = r.add(6)
	r = c.(*runContainer)
	c, _ = r.add(4)
	r = c.(*runContainer)
	if len(r.runs) != 1 || r.runs[0] != (interval{4, 6}) {
		t.Fatalf("adjacent adds should merge, got %+v", r.runs)
	}
	c, _ = r.add(8)
	r = c.(*runContainer)
	if len(r.runs) != 2 {
		t.Fatalf("non-adjacent add should create second run, got %+v", r.runs)
	}
	c, _ = r.add(7) // bridges the two runs
	r = c.(*runContainer)
	if len(r.runs) != 1 || r.runs[0] != (interval{4, 8}) {
		t.Fatalf("bridging add should merge runs, got %+v", r.runs)
	}
	// Remove from middle splits the run.
	c, _ = r.remove(6)
	r = c.(*runContainer)
	if len(r.runs) != 2 || r.runs[0] != (interval{4, 5}) || r.runs[1] != (interval{7, 8}) {
		t.Fatalf("middle remove should split run, got %+v", r.runs)
	}
	// Remove edges.
	c, _ = r.remove(4)
	r = c.(*runContainer)
	c, _ = r.remove(8)
	r = c.(*runContainer)
	if len(r.runs) != 2 || r.runs[0] != (interval{5, 5}) || r.runs[1] != (interval{7, 7}) {
		t.Fatalf("edge removes wrong, got %+v", r.runs)
	}
}

func TestUint16BoundaryValues(t *testing.T) {
	b := New()
	b.Add(0)
	b.Add(65535)             // max low-16
	b.Add(65536)             // first of next container
	b.Add(0xFFFFFFFF)        // max uint32
	b.AddRange(65534, 65538) // crosses container boundary
	for _, id := range []uint32{0, 65534, 65535, 65536, 65537, 0xFFFFFFFF} {
		if !b.Contains(id) {
			t.Fatalf("missing %d", id)
		}
	}
	if b.Contains(1) || b.Contains(65533) || b.Contains(65538) {
		t.Fatal("unexpected membership")
	}
	if got := b.Cardinality(); got != 6 {
		t.Fatalf("cardinality = %d, want 6", got)
	}
}

func TestAddRangeBoundaries(t *testing.T) {
	cases := []struct{ lo, hi, want uint64 }{
		{0, 0, 0},                 // empty
		{0, 1, 1},                 // single
		{0, 65536, 65536},         // exactly one container
		{65535, 65537, 2},         // straddles boundary
		{0, 1 << 32, 1 << 32},     // entire space
		{1<<32 - 10, 1 << 32, 10}, // tail of space
		{100, 100, 0},             // empty range
	}
	for _, c := range cases {
		b := New()
		b.AddRange(c.lo, c.hi)
		if got := b.Cardinality(); got != c.want {
			t.Fatalf("AddRange(%d,%d) cardinality = %d, want %d", c.lo, c.hi, got, c.want)
		}
	}
	// Clamping beyond 2^32.
	b := New()
	b.AddRange(1<<32-5, 1<<33)
	if got := b.Cardinality(); got != 5 {
		t.Fatalf("clamped AddRange cardinality = %d, want 5", got)
	}
}

func TestDuplicateAddAndRemoveMissing(t *testing.T) {
	b := New()
	if !b.Add(42) {
		t.Fatal("first add should report insertion")
	}
	if b.Add(42) {
		t.Fatal("duplicate add should report no-op")
	}
	if b.Remove(43) {
		t.Fatal("removing missing id should report false")
	}
	if !b.Remove(42) || b.Remove(42) {
		t.Fatal("remove semantics broken")
	}
	if !b.IsEmpty() {
		t.Fatal("bitmap should be empty")
	}
}
