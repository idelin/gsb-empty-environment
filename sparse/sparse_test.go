package sparse

import (
	"math/rand"
	"testing"
)

// refArray is the naive dense reference implementation.
type refArray struct {
	vals     []int64
	explicit []bool
	def      int64
}

func newRef(n int) *refArray {
	return &refArray{vals: make([]int64, n), explicit: make([]bool, n)}
}

func (r *refArray) set(p uint64, v int64) { r.vals[p] = v; r.explicit[p] = true }

func (r *refArray) rangeAdd(lo, hi uint64, d int64) {
	for i := lo; i <= hi; i++ {
		r.vals[i] += d
	}
}

func (r *refArray) setDefault(nd int64, follow bool) {
	old := r.def
	for i := range r.vals {
		if !r.explicit[i] {
			r.vals[i] = nd
			continue
		}
		if follow && r.vals[i] == old {
			r.explicit[i] = false
			r.vals[i] = nd
			continue
		}
		if r.vals[i] == nd {
			r.explicit[i] = false
		}
	}
	r.def = nd
}

func (r *refArray) sum(lo, hi uint64) int64 {
	var s int64
	for i := lo; i <= hi; i++ {
		s += r.vals[i]
	}
	return s
}

// TestRandomAgainstReference drives the sparse array and a dense reference
// through the same random op mix (set / rangeAdd / setDefault with both
// follow modes / snapshot) and cross-checks every query.
func TestRandomAgainstReference(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	type savedSnap struct {
		snap *Snapshot
		vals []int64
	}
	for trial := 0; trial < 60; trial++ {
		n := uint64(1 + rng.Intn(200))
		a := New(n)
		ref := newRef(int(n))
		var snaps []savedSnap
		for op := 0; op < 400; op++ {
			switch rng.Intn(7) {
			case 0, 1: // point set (includes explicit zeros and negatives)
				p := uint64(rng.Intn(int(n)))
				v := int64(rng.Intn(21) - 10)
				a.Set(p, v)
				ref.set(p, v)
			case 2: // lazy range add
				lo := uint64(rng.Intn(int(n)))
				hi := lo + uint64(rng.Intn(int(n-lo)))
				d := int64(rng.Intn(11) - 5)
				a.RangeAdd(lo, hi, d)
				ref.rangeAdd(lo, hi, d)
			case 3: // default change, both follow modes
				nd := int64(rng.Intn(7) - 3)
				follow := rng.Intn(2) == 0
				a.SetDefault(nd, follow)
				ref.setDefault(nd, follow)
			case 4: // point query
				p := uint64(rng.Intn(int(n)))
				if got, want := a.Get(p), ref.vals[p]; got != want {
					t.Fatalf("trial %d op %d: Get(%d)=%d, want %d", trial, op, p, got, want)
				}
			case 5: // range sum query
				lo := uint64(rng.Intn(int(n)))
				hi := lo + uint64(rng.Intn(int(n-lo)))
				if got, want := a.RangeSum(lo, hi), ref.sum(lo, hi); got != want {
					t.Fatalf("trial %d op %d: RangeSum(%d,%d)=%d, want %d", trial, op, lo, hi, got, want)
				}
			case 6: // snapshot
				cp := make([]int64, len(ref.vals))
				copy(cp, ref.vals)
				snaps = append(snaps, savedSnap{a.Snapshot(), cp})
			}
		}
		// Full final comparison.
		for p := uint64(0); p < n; p++ {
			if got := a.Get(p); got != ref.vals[p] {
				t.Fatalf("trial %d: final Get(%d)=%d, want %d", trial, p, got, ref.vals[p])
			}
		}
		if got, want := a.Total(), ref.sum(0, n-1); got != want {
			t.Fatalf("trial %d: Total()=%d, want %d", trial, got, want)
		}
		// Snapshots must be frozen at their own point in time.
		for si, s := range snaps {
			for p := uint64(0); p < n; p++ {
				if got := s.snap.Get(p); got != s.vals[p] {
					t.Fatalf("trial %d snap %d: Get(%d)=%d, want %d", trial, si, p, got, s.vals[p])
				}
			}
		}
	}
}

func TestExplicitZeroIsNotMissing(t *testing.T) {
	a := New(8)
	a.Set(2, 0) // explicit zero
	if !a.IsException(2) {
		t.Fatal("explicit 0 must be stored as an exception")
	}
	if a.IsException(3) {
		t.Fatal("position 3 was never set and must not be an exception")
	}
	a.SetDefault(2, false)
	if got := a.Get(2); got != 0 {
		t.Fatalf("explicit 0 must survive default 0->2, got %d", got)
	}
	if got := a.Get(3); got != 2 {
		t.Fatalf("unassigned position must follow default 0->2, got %d", got)
	}
	if !a.IsException(2) {
		t.Fatal("explicit 0 must keep its identity across default change")
	}
	// Reclaim: exception equal to the new default is recycled.
	a.SetDefault(0, false)
	if a.IsException(2) {
		t.Fatal("exception equal to new default should be reclaimed")
	}
	if got := a.Get(2); got != 0 {
		t.Fatalf("reclaimed position must read as default 0, got %d", got)
	}
}

func TestSetDefaultFollowOld(t *testing.T) {
	// followOld=true: exceptions equal to the old default follow.
	a := New(4)
	a.Set(0, 0) // explicit, equal to old default
	a.Set(1, 5)
	a.SetDefault(3, true)
	if got := a.Get(0); got != 3 {
		t.Fatalf("followOld: explicit old-default position must follow, got %d", got)
	}
	if got := a.Get(1); got != 5 {
		t.Fatalf("unrelated exception must keep its value, got %d", got)
	}
	// followOld=false: position identity is preserved.
	b := New(4)
	b.Set(0, 0)
	b.SetDefault(3, false)
	if got := b.Get(0); got != 0 {
		t.Fatalf("followOld=false: explicit position must keep 0, got %d", got)
	}
}

func TestRangeSumIncludesDefaultContribution(t *testing.T) {
	a := NewWithDefault(1000, 3)
	if got := a.RangeSum(0, 999); got != 3000 {
		t.Fatalf("pure default range sum = %d, want 3000", got)
	}
	a.Set(10, 100)
	if got := a.RangeSum(0, 999); got != 3000-3+100 {
		t.Fatalf("range sum with one exception = %d, want %d", got, 3000-3+100)
	}
	a.RangeAdd(0, 999, 2) // lazy, covers defaults and the exception
	want := int64(3000-3+100) + 2*1000
	if got := a.RangeSum(0, 999); got != want {
		t.Fatalf("range sum after full-range add = %d, want %d", got, want)
	}
}

func TestRangeAddIsLazy(t *testing.T) {
	n := uint64(1) << 40
	a := New(n)
	a.Set(7, 10)
	before := a.Stats().Exceptions
	for i := 0; i < 1000; i++ {
		a.RangeAdd(0, n-1, 1) // touches the whole huge array, lazily
	}
	st := a.Stats()
	if st.Exceptions != before {
		t.Fatalf("range add must not materialize exceptions: %d -> %d", before, st.Exceptions)
	}
	if got := a.Get(7); got != 1010 {
		t.Fatalf("Get after 1000 full-range adds = %d, want 1010", got)
	}
	if got := a.Get(123456789); got != 1000 {
		t.Fatalf("default position after adds = %d, want 1000", got)
	}
}

func TestLocate(t *testing.T) {
	a := New(10)
	for p, v := range map[uint64]int64{1: 2, 3: 3, 4: 1, 7: 4} {
		a.Set(p, v)
	}
	// values: 0 2 0 3 1 0 0 4 0 0 ; prefix: 0 2 2 5 6 6 6 10 10 10
	cases := []struct {
		k    int64
		want uint64
		ok   bool
	}{
		{1, 1, true}, {2, 1, true}, {3, 3, true}, {5, 3, true},
		{6, 4, true}, {7, 7, true}, {10, 7, true},
		{11, 0, false}, {0, 0, false}, {-1, 0, false},
	}
	for _, c := range cases {
		got, ok := a.Locate(c.k)
		if ok != c.ok || (ok && got != c.want) {
			t.Fatalf("Locate(%d) = (%d,%v), want (%d,%v)", c.k, got, ok, c.want, c.ok)
		}
	}
	// Negative values close Locate.
	a.Set(2, -1)
	if _, ok := a.Locate(1); ok {
		t.Fatal("Locate must be closed once the array is not known non-negative")
	}
	// A snapshot taken while non-negative keeps Locate open.
	b := New(10)
	b.Set(5, 4)
	snap := b.Snapshot()
	b.Set(6, -2)
	if _, ok := b.Locate(1); ok {
		t.Fatal("Locate must be closed on the mutated array")
	}
	if p, ok := snap.Locate(1); !ok || p != 5 {
		t.Fatalf("non-negative snapshot must keep Locate open, got (%d,%v)", p, ok)
	}
}

func TestSnapshotIsolation(t *testing.T) {
	a := New(16)
	a.Set(3, 10)
	a.RangeAdd(2, 6, 4)
	snap := a.Snapshot()
	a.Set(3, 99)
	a.SetDefault(7, false)
	a.RangeAdd(0, 15, 1)
	if got := snap.Get(3); got != 14 {
		t.Fatalf("snapshot Get(3) = %d, want 14", got)
	}
	if got := snap.Get(4); got != 4 {
		t.Fatalf("snapshot Get(4) = %d, want 4", got)
	}
	if got := snap.Default(); got != 0 {
		t.Fatalf("snapshot default = %d, want 0", got)
	}
	if got := a.Get(3); got != 100 {
		t.Fatalf("mutated array Get(3) = %d, want 100", got)
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	a := New(1 << 20)
	a.SetDefault(2, false)
	for i := 0; i < 500; i++ {
		a.Set(uint64(rng.Intn(1<<20)), int64(rng.Intn(2000)-1000))
	}
	for i := 0; i < 20; i++ {
		lo := uint64(rng.Intn(1 << 20))
		hi := lo + uint64(rng.Intn(int((1<<20)-lo)))
		a.RangeAdd(lo, hi, int64(rng.Intn(20)-10))
	}
	b, err := Decode(a.Encode())
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	for i := 0; i < 2000; i++ {
		p := uint64(rng.Intn(1 << 20))
		if got, want := b.Get(p), a.Get(p); got != want {
			t.Fatalf("round trip Get(%d) = %d, want %d", p, got, want)
		}
	}
	if got, want := b.Total(), a.Total(); got != want {
		t.Fatalf("round trip Total = %d, want %d", got, want)
	}
	if got, want := b.NonNegative(), a.NonNegative(); got != want {
		t.Fatalf("round trip NonNegative = %v, want %v", got, want)
	}
}

// TestLargeLogicalLength is the physical-storage evidence: a 2^40-long
// logical array (8.8 TB dense) with 10k exceptions and 100 lazy layers
// occupies a few hundred KB physically, and queries stay fast.
func TestLargeLogicalLength(t *testing.T) {
	n := uint64(1) << 40
	a := New(n)
	a.SetDefault(2, false)
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 10000; i++ {
		a.Set(uint64(rng.Int63n(int64(n))), int64(rng.Intn(100)))
	}
	for i := 0; i < 100; i++ {
		lo := uint64(rng.Int63n(int64(n / 2)))
		a.RangeAdd(lo, lo+n/3, int64(rng.Intn(10)))
	}
	st := a.Stats()
	t.Logf("logical=%d dense=%d bytes, exceptions=%d layers=%d physical=%d bytes inmem=%d bytes",
		st.LogicalLen, st.DenseBytes, st.Exceptions, st.Layers, st.PhysicalBytes, st.InMemoryBytes)
	if st.PhysicalBytes > 1<<20 {
		t.Fatalf("physical size %d exceeds 1 MiB for 10k exceptions", st.PhysicalBytes)
	}
	// Analytic check: default 2, one full-range +5 add.
	b := New(n)
	b.SetDefault(2, false)
	b.RangeAdd(0, n-1, 5)
	b.Set(0, 100) // point set after the range add overrides it at position 0
	want := int64(100) + int64(n-1)*7
	if got := b.Total(); got != want {
		t.Fatalf("Total over 2^40 positions = %d, want %d", got, want)
	}
}

func benchSetup() *Array {
	n := uint64(1) << 40
	a := New(n)
	a.SetDefault(2, false)
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 10000; i++ {
		a.Set(uint64(rng.Int63n(int64(n))), int64(rng.Intn(100)))
	}
	for i := 0; i < 50; i++ {
		lo := uint64(rng.Int63n(int64(n / 2)))
		a.RangeAdd(lo, lo+n/3, int64(rng.Intn(10)))
	}
	return a
}

func BenchmarkRangeSum(b *testing.B) {
	a := benchSetup()
	rng := rand.New(rand.NewSource(1))
	n := a.Len()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lo := uint64(rng.Int63n(int64(n / 2)))
		a.RangeSum(lo, lo+n/4)
	}
}

func BenchmarkSet(b *testing.B) {
	a := benchSetup()
	rng := rand.New(rand.NewSource(1))
	n := a.Len()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.Set(uint64(rng.Int63n(int64(n))), int64(rng.Intn(100)))
	}
}

func BenchmarkRangeAdd(b *testing.B) {
	a := benchSetup()
	rng := rand.New(rand.NewSource(1))
	n := a.Len()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lo := uint64(rng.Int63n(int64(n / 2)))
		a.RangeAdd(lo, lo+n/4, 1)
	}
}

func BenchmarkLocate(b *testing.B) {
	a := benchSetup()
	total := a.Total()
	rng := rand.New(rand.NewSource(1))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.Locate(rng.Int63n(total) + 1)
	}
}
