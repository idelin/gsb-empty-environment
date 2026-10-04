package cbitmap

import (
	"math/rand"
	"sort"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func buildContainer(kind string, vals []uint16) container {
	sorted := append([]uint16(nil), vals...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	switch kind {
	case "array":
		return &arrayContainer{data: sorted}
	case "bitmap":
		bc := &bitmapContainer{}
		for _, v := range sorted {
			bc.set(v)
		}
		return bc
	case "run":
		ac := &arrayContainer{data: sorted}
		return ac.toRun()
	}
	panic("bad kind")
}

func refSet(vals []uint16) map[uint16]bool {
	m := make(map[uint16]bool, len(vals))
	for _, v := range vals {
		m[v] = true
	}
	return m
}

func refOp(op string, a, b map[uint16]bool) []uint16 {
	out := []uint16{}
	for v := range a {
		inB := b[v]
		switch op {
		case "and":
			if inB {
				out = append(out, v)
			}
		case "or":
			out = append(out, v)
		case "xor":
			if !inB {
				out = append(out, v)
			}
		}
	}
	if op != "and" {
		for v := range b {
			if !a[v] {
				out = append(out, v)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func containerVals(c container) []uint16 {
	u32 := c.appendTo(0, nil)
	out := make([]uint16, len(u32))
	for i, v := range u32 {
		out[i] = uint16(v)
	}
	return out
}

func equalU16(a, b []uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalU32(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// container operation matrix: all kind combinations x {and, or, xor}
// ---------------------------------------------------------------------------

func TestContainerOpMatrix(t *testing.T) {
	sparse := []uint16{1, 100, 500, 4000, 65535}
	dense := make([]uint16, 0, 5000)
	for v := 0; v < 10000; v += 2 {
		dense = append(dense, uint16(v))
	}
	ranged := make([]uint16, 0, 2001)
	for v := 1000; v <= 3000; v++ {
		ranged = append(ranged, uint16(v))
	}
	edge := []uint16{0, 1, 2, 65533, 65534, 65535}

	sets := map[string][]uint16{
		"array":  sparse,
		"bitmap": dense,
		"run":    ranged,
	}
	extra := map[string][]uint16{"array": edge, "bitmap": edge, "run": edge}

	ops := map[string]func(a, b container) container{
		"and": containerAnd,
		"or":  containerOr,
		"xor": containerXor,
	}

	for ka, va := range sets {
		for kb, vb := range sets {
			for _, pair := range [][2][]uint16{{va, vb}, {extra[ka], extra[kb]}, {va, extra[kb]}, {extra[ka], vb}} {
				for opName, op := range ops {
					ca := buildContainer(ka, pair[0])
					cb := buildContainer(kb, pair[1])
					got := containerVals(op(ca, cb))
					want := refOp(opName, refSet(pair[0]), refSet(pair[1]))
					if !equalU16(got, want) {
						t.Fatalf("%s %s(%s,%s): got %d vals, want %d", opName, opName, ka, kb, len(got), len(want))
					}
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// container auto-conversion boundaries
// ---------------------------------------------------------------------------

func TestArrayToBitmapBoundary(t *testing.T) {
	m := NewMetrics()
	b := New(WithMetrics(m))
	// 4096 sparse values in one container -> array
	for i := 0; i < arrayMaxSize; i++ {
		b.Add(uint32(i * 2))
	}
	if got := b.ContainerKinds()["array"]; got != 1 {
		t.Fatalf("expected 1 array container, got %v", b.ContainerKinds())
	}
	// 4097th value -> bitmap
	b.Add(1)
	if got := b.ContainerKinds()["bitmap"]; got != 1 {
		t.Fatalf("expected 1 bitmap container, got %v", b.ContainerKinds())
	}
	if n := b.Cardinality(); n != arrayMaxSize+1 {
		t.Fatalf("cardinality = %d, want %d", n, arrayMaxSize+1)
	}
	// remove back down to 4096 -> array again
	if !b.Remove(1) {
		t.Fatal("Remove(1) should report true")
	}
	if got := b.ContainerKinds()["array"]; got != 1 {
		t.Fatalf("expected conversion back to array, got %v", b.ContainerKinds())
	}
	if b.Remove(1) {
		t.Fatal("Remove(1) should report false the second time")
	}
	snap := m.Snapshot()
	if snap.Conversions["to_bitmap"] != 1 || snap.Conversions["to_array"] != 1 {
		t.Fatalf("unexpected conversion counters: %v", snap.Conversions)
	}
}

func TestDenseRangeBecomesRun(t *testing.T) {
	b := New()
	if err := b.AddRange(100000, 500000); err != nil {
		t.Fatal(err)
	}
	kinds := b.ContainerKinds()
	if kinds["run"] == 0 {
		t.Fatalf("expected run containers for dense range, got %v", kinds)
	}
	if n := b.Cardinality(); n != 400000 {
		t.Fatalf("cardinality = %d, want 400000", n)
	}
	for _, x := range []uint32{100000, 250000, 499999} {
		if !b.Contains(x) {
			t.Fatalf("expected %d to be present", x)
		}
	}
	for _, x := range []uint32{99999, 500000} {
		if b.Contains(x) {
			t.Fatalf("expected %d to be absent", x)
		}
	}
}

func TestNonCompressibleStaysBitmap(t *testing.T) {
	b := New()
	// 5000 isolated even values: not compressible by runs, too big for array.
	for v := 0; v < 10000; v += 2 {
		b.Add(uint32(v))
	}
	kinds := b.ContainerKinds()
	if kinds["bitmap"] != 1 {
		t.Fatalf("expected a bitmap container, got %v", kinds)
	}
	b.Optimize()
	if kinds = b.ContainerKinds(); kinds["bitmap"] != 1 {
		t.Fatalf("expected bitmap to survive Optimize, got %v", kinds)
	}
}

func TestRunContainerPointOps(t *testing.T) {
	b := New()
	if err := b.AddRange(10, 20); err != nil {
		t.Fatal(err)
	}
	b.Add(25)
	b.Add(20)                          // bridges the gap: [10,25]
	if n := b.Cardinality(); n != 12 { // [10,20) + {20, 25}
		t.Fatalf("cardinality = %d, want 12", n)
	}
	b.Add(5) // extends left
	b.Add(0) // new run before
	if !b.Contains(5) || !b.Contains(0) || b.Contains(6) {
		t.Fatal("run container point ops misbehaved")
	}
	// split a run by removal
	if !b.Remove(12) {
		t.Fatal("Remove(12) should succeed")
	}
	if b.Contains(12) || !b.Contains(11) || !b.Contains(13) {
		t.Fatal("run split by removal misbehaved")
	}
	// remove run edges
	b.Remove(10)
	b.Remove(25)
	if b.Contains(10) || b.Contains(25) {
		t.Fatal("run edge removal misbehaved")
	}
}

// ---------------------------------------------------------------------------
// bitmap-level fuzz against a map reference
// ---------------------------------------------------------------------------

func TestBitmapOpsFuzz(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 30; trial++ {
		a, b := New(), New()
		ra, rb := map[uint32]bool{}, map[uint32]bool{}
		fill := func(bm *Bitmap, ref map[uint32]bool) {
			// sparse random points
			for i := 0; i < 200; i++ {
				v := uint32(rng.Intn(1 << 22))
				bm.Add(v)
				ref[v] = true
			}
			// dense ranges
			for i := 0; i < 5; i++ {
				lo := uint64(rng.Intn(1 << 20))
				hi := lo + uint64(rng.Intn(200000))
				if err := bm.AddRange(lo, hi); err != nil {
					t.Fatal(err)
				}
				for v := lo; v < hi; v++ {
					ref[uint32(v)] = true
				}
			}
		}
		fill(a, ra)
		fill(b, rb)

		check := func(name string, got *Bitmap, want map[uint32]bool) {
			t.Helper()
			arr := got.ToArray()
			if uint64(len(want)) != got.Cardinality() || len(arr) != len(want) {
				t.Fatalf("trial %d %s: got %d values, want %d", trial, name, got.Cardinality(), len(want))
			}
			for _, v := range arr {
				if !want[v] {
					t.Fatalf("trial %d %s: unexpected value %d", trial, name, v)
				}
			}
		}

		andRef := map[uint32]bool{}
		for v := range ra {
			if rb[v] {
				andRef[v] = true
			}
		}
		check("and", a.And(b), andRef)

		orRef := map[uint32]bool{}
		for v := range ra {
			orRef[v] = true
		}
		for v := range rb {
			orRef[v] = true
		}
		check("or", a.Or(b), orRef)

		xorRef := map[uint32]bool{}
		for v := range ra {
			if !rb[v] {
				xorRef[v] = true
			}
		}
		for v := range rb {
			if !ra[v] {
				xorRef[v] = true
			}
		}
		check("xor", a.Xor(b), xorRef)
	}
}

// ---------------------------------------------------------------------------
// batch operations
// ---------------------------------------------------------------------------

func TestBatchOps(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	mk := func() *Bitmap {
		b := New()
		for i := 0; i < 300; i++ {
			b.Add(uint32(rng.Intn(1 << 20)))
		}
		lo := uint64(rng.Intn(1 << 18))
		if err := b.AddRange(lo, lo+50000); err != nil {
			t.Fatal(err)
		}
		return b
	}
	base := mk()
	others := []*Bitmap{mk(), mk(), mk(), mk()}

	andSeq := base
	orSeq := base
	xorSeq := base
	for _, o := range others {
		andSeq = andSeq.And(o)
		orSeq = orSeq.Or(o)
		xorSeq = xorSeq.Xor(o)
	}
	if got := base.AndMany(others...); !got.Equals(andSeq) {
		t.Fatal("AndMany mismatch with sequential And")
	}
	if got := base.OrMany(others...); !got.Equals(orSeq) {
		t.Fatal("OrMany mismatch with sequential Or")
	}
	if got := base.XorMany(others...); !got.Equals(xorSeq) {
		t.Fatal("XorMany mismatch with sequential Xor")
	}
	// empty batch is the identity
	if got := base.AndMany(); !got.Equals(base) {
		t.Fatal("AndMany() with no args should clone")
	}
}

// ---------------------------------------------------------------------------
// error paths
// ---------------------------------------------------------------------------

func TestErrorPaths(t *testing.T) {
	m := NewMetrics()
	b := New(WithMetrics(m))

	if err := b.AddRange(10, 5); err == nil {
		t.Fatal("expected error for lo > hi")
	} else if e, ok := err.(*Error); !ok || e.Code != ErrInvalidRange {
		t.Fatalf("expected ErrInvalidRange, got %v", err)
	}
	if err := b.AddRange(0, 1<<32+1); err == nil {
		t.Fatal("expected error for hi > 2^32")
	}
	if err := b.AddRange(5, 5); err != nil {
		t.Fatalf("empty range should be a no-op, got %v", err)
	}
	if got := b.And(nil); got.Cardinality() != 0 {
		t.Fatal("And(nil) should be empty")
	}
	b.Add(1)
	if got := b.Or(nil); !got.Contains(1) {
		t.Fatal("Or(nil) should clone")
	}

	snap := m.Snapshot()
	if snap.Errors[ErrInvalidRange.String()] != 2 {
		t.Fatalf("expected 2 invalid_range errors, got %v", snap.Errors)
	}
	if snap.Errors[ErrNilBitmap.String()] != 2 {
		t.Fatalf("expected 2 nil_bitmap errors, got %v", snap.Errors)
	}
}

// ---------------------------------------------------------------------------
// large-scale simulation: ~10^10 values across shards, microsecond ops
// ---------------------------------------------------------------------------

func TestLargeScaleSimulation(t *testing.T) {
	const shards = 4
	const perShard = uint64(2500000000) // 4 x 2.5e9 = 1e10 values
	type pair struct{ a, b *Bitmap }
	pairs := make([]pair, shards)
	for s := 0; s < shards; s++ {
		a, b := New(), New()
		if err := a.AddRange(0, perShard); err != nil {
			t.Fatal(err)
		}
		if err := b.AddRange(perShard/2, perShard/2+perShard); err != nil {
			t.Fatal(err)
		}
		pairs[s] = pair{a, b}
	}

	var totalAnd, totalOr uint64
	start := time.Now()
	var worst time.Duration
	for _, p := range pairs {
		t0 := time.Now()
		inter := p.a.And(p.b)
		if d := time.Since(t0); d > worst {
			worst = d
		}
		totalAnd += inter.Cardinality()

		t0 = time.Now()
		union := p.a.Or(p.b)
		if d := time.Since(t0); d > worst {
			worst = d
		}
		totalOr += union.Cardinality()
	}
	elapsed := time.Since(start)

	wantAnd := uint64(shards) * (perShard / 2)
	wantOr := uint64(shards) * (perShard + perShard/2)
	if totalAnd != wantAnd {
		t.Fatalf("AND cardinality = %d, want %d", totalAnd, wantAnd)
	}
	if totalOr != wantOr {
		t.Fatalf("OR cardinality = %d, want %d", totalOr, wantOr)
	}
	t.Logf("simulated %d values across %d shards: AND+OR of ~%.1e-value bitmaps in %v total, worst single op %v",
		uint64(shards)*perShard, shards, float64(perShard), elapsed, worst)
	if worst > 2*time.Second {
		t.Fatalf("single op took %v, expected microsecond-to-millisecond scale", worst)
	}
}

// ---------------------------------------------------------------------------
// misc invariants
// ---------------------------------------------------------------------------

func TestCloneAndEquals(t *testing.T) {
	a := New()
	for i := 0; i < 1000; i++ {
		a.Add(uint32(i * 3))
	}
	if err := a.AddRange(1<<20, 1<<20+5000); err != nil {
		t.Fatal(err)
	}
	c := a.Clone()
	if !a.Equals(c) {
		t.Fatal("clone should equal original")
	}
	c.Add(7)
	if a.Equals(c) {
		t.Fatal("clone must be a deep copy")
	}
	if a.Equals(nil) {
		t.Fatal("Equals(nil) should be false")
	}
	if !a.And(a).Equals(a) {
		t.Fatal("And(self) should equal self")
	}
	if a.Xor(a).Cardinality() != 0 {
		t.Fatal("Xor(self) should be empty")
	}
}

func TestToArraySorted(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	b := New()
	ref := map[uint32]bool{}
	for i := 0; i < 5000; i++ {
		v := uint32(rng.Intn(1 << 24))
		b.Add(v)
		ref[v] = true
	}
	arr := b.ToArray()
	if len(arr) != len(ref) {
		t.Fatalf("ToArray len = %d, want %d", len(arr), len(ref))
	}
	for i := 1; i < len(arr); i++ {
		if arr[i] <= arr[i-1] {
			t.Fatal("ToArray must be strictly increasing")
		}
	}
}
