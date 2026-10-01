package crset

import (
	"math/rand"
	"sort"
	"testing"
)

func sortedKeys(m map[uint32]bool) []uint32 {
	out := make([]uint32, 0, len(m))
	for v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func elementsOf(s *Set) map[uint32]bool {
	m := map[uint32]bool{}
	for _, v := range s.Elements() {
		m[v] = true
	}
	return m
}

func checkOp(t *testing.T, name string, got *Set, want map[uint32]bool) {
	t.Helper()
	els := got.Elements()
	if len(els) != len(want) {
		t.Fatalf("%s: %d elements, want %d", name, len(els), len(want))
	}
	for _, v := range els {
		if !want[v] {
			t.Fatalf("%s: unexpected element %d", name, v)
		}
	}
	if st := got.Stats(); st.Elements != len(want) {
		t.Fatalf("%s: stats elements = %d, want %d", name, st.Elements, len(want))
	}
}

func mapUnion(a, b map[uint32]bool) map[uint32]bool {
	out := map[uint32]bool{}
	for v := range a {
		out[v] = true
	}
	for v := range b {
		out[v] = true
	}
	return out
}

func mapInter(a, b map[uint32]bool) map[uint32]bool {
	out := map[uint32]bool{}
	for v := range a {
		if b[v] {
			out[v] = true
		}
	}
	return out
}

func mapDiff(a, b map[uint32]bool) map[uint32]bool {
	out := map[uint32]bool{}
	for v := range a {
		if !b[v] {
			out[v] = true
		}
	}
	return out
}

func mapSymDiff(a, b map[uint32]bool) map[uint32]bool {
	return mapUnion(mapDiff(a, b), mapDiff(b, a))
}

// TestSetOpsCrossRepresentation builds one set per physical encoding and
// checks all four set operations on every pair against map references.
func TestSetOpsCrossRepresentation(t *testing.T) {
	// run-encoded: contiguous ranges in two buckets.
	runSet := New()
	for v := uint32(0); v < 5000; v++ {
		runSet.Add(v)
	}
	for v := uint32(70000); v < 80000; v++ {
		runSet.Add(v)
	}
	// bitmap-encoded: stride 3, 17000 elements; 17000 runs make RLE
	// (68000 B) more expensive than the 65536 B bitmap, so it stays bitmap.
	bitSet := New()
	for i := 0; i < 17000; i++ {
		bitSet.Add(uint32(3 * i))
	}
	// sparse-encoded: few random values per bucket.
	rng := rand.New(rand.NewSource(3))
	sparseSet := New()
	for sparseSet.Cardinality() < 300 {
		sparseSet.Add(uint32(rng.Intn(300000)))
	}
	if got := repOf(t, runSet, 0); got != repRun {
		t.Fatalf("runSet bucket 0 rep = %s, want run", got)
	}
	if got := repOf(t, bitSet, 0); got != repBitmap {
		t.Fatalf("bitSet bucket 0 rep = %s, want bitmap", got)
	}
	for i := range sparseSet.buckets {
		if sparseSet.buckets[i].c.rep != repSparse {
			t.Fatalf("sparseSet bucket %d rep = %s, want sparse", i, sparseSet.buckets[i].c.rep)
		}
	}
	sets := map[string]*Set{"run": runSet, "bitmap": bitSet, "sparse": sparseSet}
	refs := map[string]map[uint32]bool{}
	for name, s := range sets {
		refs[name] = elementsOf(s)
	}
	for an, a := range sets {
		for bn, b := range sets {
			if an >= bn {
				continue
			}
			pair := an + "x" + bn
			checkOp(t, pair+" union", a.Union(b), mapUnion(refs[an], refs[bn]))
			checkOp(t, pair+" inter", a.Intersect(b), mapInter(refs[an], refs[bn]))
			checkOp(t, pair+" diff", a.Difference(b), mapDiff(refs[an], refs[bn]))
			checkOp(t, pair+" symdiff", a.SymDifference(b), mapSymDiff(refs[an], refs[bn]))
		}
	}
}

// TestOpsAcrossBucketBoundary uses values hugging the 65535/65536 split.
func TestOpsAcrossBucketBoundary(t *testing.T) {
	a := New()
	for _, v := range []uint32{65534, 65535, 65536, 65537, 0xFFFFFFFF} {
		a.Add(v)
	}
	b := New()
	for _, v := range []uint32{65535, 65536, 131072} {
		b.Add(v)
	}
	cases := []struct {
		name string
		got  *Set
		want []uint32
	}{
		{"union", a.Union(b), []uint32{65534, 65535, 65536, 65537, 131072, 0xFFFFFFFF}},
		{"inter", a.Intersect(b), []uint32{65535, 65536}},
		{"diff", a.Difference(b), []uint32{65534, 65537, 0xFFFFFFFF}},
		{"symdiff", a.SymDifference(b), []uint32{65534, 65537, 131072, 0xFFFFFFFF}},
	}
	for _, tc := range cases {
		if got := tc.got.Elements(); !equalUint32s(got, tc.want) {
			t.Fatalf("%s = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestOpsResultRenormalizes: union of two overlapping RLE ranges must come
// out as a single merged run (9 bytes total).
func TestOpsResultRenormalizes(t *testing.T) {
	a := New()
	for v := uint32(0); v < 5000; v++ {
		a.Add(v)
	}
	b := New()
	for v := uint32(2500); v < 10000; v++ {
		b.Add(v)
	}
	u := a.Union(b)
	if got := repOf(t, u, 0); got != repRun {
		t.Fatalf("union rep = %s, want run", got)
	}
	if runs := u.buckets[0].c.runs; len(runs) != 1 {
		t.Fatalf("union runs = %d, want 1", len(runs))
	}
	if st := u.Stats(); st.TotalBytes != 9 {
		t.Fatalf("union TotalBytes = %d, want 9", st.TotalBytes)
	}
	if got := u.Cardinality(); got != 10000 {
		t.Fatalf("union card = %d, want 10000", got)
	}
}

// TestOpsFuzz cross-checks all four operations on random sets.
func TestOpsFuzz(t *testing.T) {
	rng := rand.New(rand.NewSource(9))
	build := func(n int) (*Set, map[uint32]bool) {
		s := New()
		ref := map[uint32]bool{}
		for i := 0; i < n; i++ {
			v := uint32(rng.Intn(200000))
			s.Add(v)
			ref[v] = true
		}
		return s, ref
	}
	a, ra := build(30000)
	b, rb := build(30000)
	checkOp(t, "fuzz union", a.Union(b), mapUnion(ra, rb))
	checkOp(t, "fuzz inter", a.Intersect(b), mapInter(ra, rb))
	checkOp(t, "fuzz diff", a.Difference(b), mapDiff(ra, rb))
	checkOp(t, "fuzz symdiff", a.SymDifference(b), mapSymDiff(ra, rb))
}

// TestOpsEmptyAndDisjoint covers degenerate inputs.
func TestOpsEmptyAndDisjoint(t *testing.T) {
	empty := New()
	a := New()
	for v := uint32(0); v < 100; v++ {
		a.Add(v)
	}
	if got := a.Union(empty).Cardinality(); got != 100 {
		t.Fatalf("union with empty = %d", got)
	}
	if got := a.Intersect(empty).Cardinality(); got != 0 {
		t.Fatalf("intersect with empty = %d", got)
	}
	if got := a.Difference(empty).Cardinality(); got != 100 {
		t.Fatalf("difference with empty = %d", got)
	}
	if got := a.SymDifference(a).Cardinality(); got != 0 {
		t.Fatalf("symdiff with self = %d", got)
	}
	lo, hi := New(), New()
	lo.Add(1)
	hi.Add(1 << 20)
	if got := lo.Union(hi).Elements(); !equalUint32s(got, []uint32{1, 1 << 20}) {
		t.Fatalf("disjoint union = %v", got)
	}
	if got := lo.Intersect(hi).Cardinality(); got != 0 {
		t.Fatalf("disjoint intersect = %d", got)
	}
	_ = sortedKeys
}
