package sparseprefix

import (
	"math/big"
	"math/rand"
	"testing"
)

// reference is a dense per-element model used to cross-check the sparse
// implementation on small arrays.
type reference struct {
	a []uint64
}

func newReference(n int) *reference { return &reference{a: make([]uint64, n)} }

func (r *reference) assign(lo, hi int64, v uint64) {
	for i := lo; i <= hi; i++ {
		r.a[i] = v
	}
}

func (r *reference) total() uint64 {
	var t uint64
	for _, v := range r.a {
		t += v
	}
	return t
}

// findIndex mirrors the documented semantics: smallest i with prefix >= cum;
// cum == 0 matches index 0; cum above the total does not exist.
func (r *reference) findIndex(cum uint64) (int64, bool) {
	var sum uint64
	for i, v := range r.a {
		sum += v
		if sum >= cum {
			return int64(i), true
		}
	}
	if cum == 0 {
		return 0, true
	}
	return 0, false
}

func checkInvariants(t *testing.T, s *SparsePrefixSum) {
	t.Helper()
	segs := s.Segments()
	for i, g := range segs {
		if g.Val == 0 {
			t.Fatalf("segment %d has zero value: %+v", i, g)
		}
		if g.Lo < 0 || g.Hi < g.Lo {
			t.Fatalf("segment %d has bad bounds: %+v", i, g)
		}
		if i > 0 {
			prev := segs[i-1]
			if prev.Hi >= g.Lo {
				t.Fatalf("segments %d and %d overlap or are unordered: %+v %+v", i-1, i, prev, g)
			}
			if prev.Hi+1 == g.Lo && prev.Val == g.Val {
				t.Fatalf("adjacent equal segments %d and %d not merged: %+v %+v", i-1, i, prev, g)
			}
		}
	}
}

// checkAgainst verifies the sparse structure against the dense model:
// every element, the total, and FindIndex for boundary cumulative amounts.
func checkAgainst(t *testing.T, s *SparsePrefixSum, ref *reference) {
	t.Helper()
	checkInvariants(t, s)
	for i := range ref.a {
		if got, want := s.ValueAt(int64(i)), ref.a[i]; got != want {
			t.Fatalf("ValueAt(%d) = %d, want %d", i, got, want)
		}
	}
	total := ref.total()
	if got := s.Total(); !got.IsUint64() || got.Uint64() != total {
		t.Fatalf("Total() = %v, want %d", got, total)
	}
	// Candidate cumulative amounts: 0, 1, total, total+1, and the exact
	// prefix sum at every index plus its neighbours (boundary hits).
	cums := map[uint64]bool{0: true, 1: true, total: true, total + 1: true}
	var sum uint64
	for _, v := range ref.a {
		sum += v
		cums[sum] = true
		cums[sum+1] = true
		if sum > 0 {
			cums[sum-1] = true
		}
	}
	for cum := range cums {
		gotIdx, gotOK := s.FindIndex(cum)
		wantIdx, wantOK := ref.findIndex(cum)
		if gotOK != wantOK || (gotOK && gotIdx != wantIdx) {
			t.Fatalf("FindIndex(%d) = (%d, %v), want (%d, %v)", cum, gotIdx, gotOK, wantIdx, wantOK)
		}
	}
}

func TestRandomizedCrossCheck(t *testing.T) {
	const n = 128
	for _, seed := range []int64{20261004, 1, 42, 987654321} {
		rng := rand.New(rand.NewSource(seed))
		s := new(SparsePrefixSum)
		ref := newReference(n)
		for op := 0; op < 3000; op++ {
			lo := int64(rng.Intn(n))
			hi := lo + int64(rng.Intn(n-int(lo)))
			val := uint64(rng.Intn(4)) // 0 clears; small set forces merges
			if err := s.Assign(lo, hi, val); err != nil {
				t.Fatalf("seed %d op %d: Assign(%d, %d, %d) error: %v", seed, op, lo, hi, val, err)
			}
			ref.assign(lo, hi, val)
			checkAgainst(t, s, ref)
		}
	}
}

func TestZeroSegmentsDropped(t *testing.T) {
	s := new(SparsePrefixSum)
	mustAssign(t, s, 0, 99, 7)
	mustAssign(t, s, 20, 39, 0) // punch a hole
	if got := s.NumSegments(); got != 2 {
		t.Fatalf("NumSegments = %d, want 2", got)
	}
	mustAssign(t, s, 0, 99, 0) // clear everything
	if got := s.NumSegments(); got != 0 {
		t.Fatalf("NumSegments = %d, want 0", got)
	}
	if got := s.Total().Sign(); got != 0 {
		t.Fatalf("Total sign = %d, want 0", got)
	}
	if _, ok := s.FindIndex(1); ok {
		t.Fatal("FindIndex(1) on empty structure should not exist")
	}
}

func TestBoundaryLookup(t *testing.T) {
	s := new(SparsePrefixSum)
	// Segments: [10,19]->2 (sum 20), gap, [30,34]->3 (sum 15). Total 35.
	mustAssign(t, s, 10, 19, 2)
	mustAssign(t, s, 30, 34, 3)
	cases := []struct {
		cum  uint64
		want int64
		ok   bool
	}{
		{1, 10, true},   // first unit lands on first index of segment
		{2, 10, true},   // still index 10 (value 2)
		{3, 11, true},   // spills to next index
		{20, 19, true},  // exactly the first segment's cumulative end
		{21, 30, true},  // one past: jumps across the zero gap
		{35, 34, true},  // exactly the total: last non-zero index
		{36, 0, false},  // beyond the total: does not exist
		{100, 0, false}, // far beyond
	}
	for _, c := range cases {
		got, ok := s.FindIndex(c.cum)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("FindIndex(%d) = (%d, %v), want (%d, %v)", c.cum, got, ok, c.want, c.ok)
		}
	}
}

func TestRepeatedSplitMerge(t *testing.T) {
	s := new(SparsePrefixSum)
	mustAssign(t, s, 0, 9, 1)
	mustAssign(t, s, 10, 19, 1) // adjacent equal -> single [0,19]
	wantSegs := []Segment{{0, 19, 1}}
	assertSegments(t, s, wantSegs)

	mustAssign(t, s, 5, 14, 2) // split into three
	assertSegments(t, s, []Segment{{0, 4, 1}, {5, 14, 2}, {15, 19, 1}})

	mustAssign(t, s, 5, 14, 1) // restore -> merges back into one
	assertSegments(t, s, wantSegs)

	mustAssign(t, s, 5, 14, 0) // zero the middle -> two disjoint runs
	assertSegments(t, s, []Segment{{0, 4, 1}, {15, 19, 1}})

	mustAssign(t, s, 5, 14, 1) // fill the gap with equal value -> one run
	assertSegments(t, s, wantSegs)

	// Partial overlap at both edges with a different value.
	mustAssign(t, s, 3, 16, 9)
	assertSegments(t, s, []Segment{{0, 2, 1}, {3, 16, 9}, {17, 19, 1}})
	mustAssign(t, s, 3, 16, 1)
	assertSegments(t, s, wantSegs)
}

func TestNoOverflow(t *testing.T) {
	s := new(SparsePrefixSum)
	// Total = MaxUint64 * (2^62 + 1), far beyond 64 bits.
	const hi = int64(1) << 62
	mustAssign(t, s, 0, hi, ^uint64(0))
	total := s.Total()
	if total.BitLen() <= 64 {
		t.Fatalf("Total bit length = %d, want > 64", total.BitLen())
	}
	want := new(big.Int).SetUint64(^uint64(0))
	want.Mul(want, new(big.Int).SetUint64(uint64(hi)+1))
	if total.Cmp(want) != 0 {
		t.Fatalf("Total = %v, want %v", total, want)
	}
	// PrefixSum at the last index equals the total.
	if got := s.PrefixSum(hi); got.Cmp(total) != 0 {
		t.Fatalf("PrefixSum(hi) = %v, want %v", got, total)
	}
	// A cumulative amount of MaxUint64 is covered by the very first element.
	if idx, ok := s.FindIndex(^uint64(0)); !ok || idx != 0 {
		t.Fatalf("FindIndex(MaxUint64) = (%d, %v), want (0, true)", idx, ok)
	}
	// Exactly the total maps to the last index; total+1 does not exist.
	if idx, ok := s.FindIndexBig(total); !ok || idx != hi {
		t.Fatalf("FindIndexBig(total) = (%d, %v), want (%d, true)", idx, ok, hi)
	}
	over := new(big.Int).Add(total, big.NewInt(1))
	if _, ok := s.FindIndexBig(over); ok {
		t.Fatal("FindIndexBig(total+1) should not exist")
	}
	// Huge query far beyond the total does not exist either.
	huge := new(big.Int).Lsh(big.NewInt(1), 200)
	if _, ok := s.FindIndexBig(huge); ok {
		t.Fatal("FindIndexBig(2^200) should not exist")
	}
	// Negative cumulative amount never matches.
	if _, ok := s.FindIndexBig(big.NewInt(-1)); ok {
		t.Fatal("FindIndexBig(-1) should not exist")
	}
}

func TestEmptyAndZeroCum(t *testing.T) {
	s := new(SparsePrefixSum)
	if idx, ok := s.FindIndex(0); !ok || idx != 0 {
		t.Fatalf("FindIndex(0) = (%d, %v), want (0, true)", idx, ok)
	}
	if _, ok := s.FindIndex(1); ok {
		t.Fatal("FindIndex(1) on empty structure should not exist")
	}
	if got := s.Total().Sign(); got != 0 {
		t.Fatalf("Total sign = %d, want 0", got)
	}
	if got := s.PrefixSum(-5).Sign(); got != 0 {
		t.Fatalf("PrefixSum(-5) sign = %d, want 0", got)
	}
	if got := s.ValueAt(-1); got != 0 {
		t.Fatalf("ValueAt(-1) = %d, want 0", got)
	}
}

func TestInvalidRange(t *testing.T) {
	s := new(SparsePrefixSum)
	if err := s.Assign(5, 4, 1); err != ErrInvalidRange {
		t.Fatalf("Assign(5,4,1) err = %v, want ErrInvalidRange", err)
	}
	if err := s.Assign(-1, 4, 1); err != ErrInvalidRange {
		t.Fatalf("Assign(-1,4,1) err = %v, want ErrInvalidRange", err)
	}
	if got := s.NumSegments(); got != 0 {
		t.Fatalf("NumSegments = %d after failed assigns, want 0", got)
	}
}

func mustAssign(t *testing.T, s *SparsePrefixSum, lo, hi int64, val uint64) {
	t.Helper()
	if err := s.Assign(lo, hi, val); err != nil {
		t.Fatalf("Assign(%d, %d, %d): %v", lo, hi, val, err)
	}
}

func assertSegments(t *testing.T, s *SparsePrefixSum, want []Segment) {
	t.Helper()
	got := s.Segments()
	if len(got) != len(want) {
		t.Fatalf("Segments = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Segments = %+v, want %+v", got, want)
		}
	}
}
