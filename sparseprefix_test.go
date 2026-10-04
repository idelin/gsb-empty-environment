package sparseprefix

import (
	"math"
	"math/rand"
	"testing"
)

// naive is the per-element reference implementation.
type naive struct{ a []uint64 }

func newNaive(n int64) *naive { return &naive{a: make([]uint64, n)} }

func (n *naive) assign(l, r int64, v uint64) {
	for i := l; i < r; i++ {
		n.a[i] = v
	}
}

func (n *naive) total() uint64 {
	var t uint64
	for _, v := range n.a {
		t += v
	}
	return t
}

func (n *naive) find(amount uint64) (int64, bool) {
	if amount == 0 {
		return 0, len(n.a) > 0
	}
	var cum uint64
	for i, v := range n.a {
		cum += v
		if cum >= amount {
			return int64(i), true
		}
	}
	return 0, false
}

// checkInvariants verifies the segment-list representation rules.
func checkInvariants(t *testing.T, s *Sum) {
	t.Helper()
	for i, seg := range s.segs {
		if seg.lo < 0 || seg.hi > s.n || seg.lo >= seg.hi {
			t.Fatalf("segment %d out of bounds: %+v (n=%d)", i, seg, s.n)
		}
		if seg.val == 0 {
			t.Fatalf("zero-valued segment kept: %+v", seg)
		}
		if i > 0 {
			prev := s.segs[i-1]
			if prev.hi > seg.lo {
				t.Fatalf("overlapping segments: %+v then %+v", prev, seg)
			}
			if prev.hi == seg.lo && prev.val == seg.val {
				t.Fatalf("adjacent equal segments not merged: %+v then %+v", prev, seg)
			}
		}
	}
}

// checkAgainst cross-checks total and Find over a dense set of amounts.
func checkAgainst(t *testing.T, s *Sum, ref *naive) {
	t.Helper()
	checkInvariants(t, s)
	if got, want := s.Total(), ref.total(); got != want {
		t.Fatalf("Total() = %d, want %d", got, want)
	}
	total := ref.total()
	amounts := []uint64{0, 1, 2, total, total + 1, total + 2}
	if total > 0 {
		amounts = append(amounts, total-1, total/2)
	}
	for _, amt := range amounts {
		gi, gok := s.Find(amt)
		wi, wok := ref.find(amt)
		if gi != wi || gok != wok {
			t.Fatalf("Find(%d) = (%d,%v), want (%d,%v)", amt, gi, gok, wi, wok)
		}
	}
}

func TestRandomizedAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 200; trial++ {
		n := int64(1 + rng.Intn(40))
		s := New(n)
		ref := newNaive(n)
		ops := 50 + rng.Intn(150)
		for op := 0; op < ops; op++ {
			l := rng.Int63n(n)
			r := l + rng.Int63n(n-l+1) // may be empty
			v := uint64(rng.Intn(4))   // includes 0 to exercise clearing
			s.Assign(l, r, v)
			ref.assign(l, r, v)
			checkAgainst(t, s, ref)
		}
	}
}

func TestZeroSegments(t *testing.T) {
	s := New(10)
	checkAgainst(t, s, newNaive(10)) // all zero: no segments, Find(1) missing

	s.Assign(2, 5, 7)
	s.Assign(2, 5, 0) // clear again
	checkAgainst(t, s, newNaive(10))
	if len(s.segs) != 0 {
		t.Fatalf("expected no segments, got %v", s.segs)
	}
	if _, ok := s.Find(1); ok {
		t.Fatal("Find(1) on empty sum should report not-exists")
	}
	if idx, ok := s.Find(0); !ok || idx != 0 {
		t.Fatalf("Find(0) = (%d,%v), want (0,true)", idx, ok)
	}
}

func TestExactBoundaryAmounts(t *testing.T) {
	// values: [2,2,2, 0,0, 1,1,1, 0, 5]  prefix sums: 6 at 2, 9 at 7, 14 at 9
	s := New(10)
	s.Assign(0, 3, 2)
	s.Assign(5, 8, 1)
	s.Assign(9, 10, 5)
	cases := []struct {
		amount uint64
		idx    int64
		ok     bool
	}{
		{0, 0, true},
		{1, 0, true},
		{6, 2, true},   // exactly the first segment's cumulative sum
		{7, 5, true},   // skips the zero gap
		{9, 7, true},   // exactly the second segment's cumulative sum
		{10, 9, true},  // skips the zero gap
		{14, 9, true},  // exactly the total
		{15, 0, false}, // exceeds the total
	}
	for _, c := range cases {
		if idx, ok := s.Find(c.amount); idx != c.idx || ok != c.ok {
			t.Errorf("Find(%d) = (%d,%v), want (%d,%v)", c.amount, idx, ok, c.idx, c.ok)
		}
	}
}

func TestSplitAndMerge(t *testing.T) {
	s := New(100)
	s.Assign(10, 20, 3)
	s.Assign(30, 40, 3) // disjoint, same value: must NOT merge (gap of zeros)
	if len(s.segs) != 2 {
		t.Fatalf("expected 2 segments, got %v", s.segs)
	}
	s.Assign(20, 30, 3) // bridges the gap: all three merge into one
	if len(s.segs) != 1 || s.segs[0] != (segment{10, 40, 3}) {
		t.Fatalf("expected merged [10,40)=3, got %v", s.segs)
	}
	s.Assign(15, 35, 0) // punch a hole: splits into two
	if len(s.segs) != 2 || s.segs[0] != (segment{10, 15, 3}) || s.segs[1] != (segment{35, 40, 3}) {
		t.Fatalf("expected split segments, got %v", s.segs)
	}
	s.Assign(0, 100, 3) // overwrite everything: single segment again
	if len(s.segs) != 1 || s.segs[0] != (segment{0, 100, 3}) {
		t.Fatalf("expected [0,100)=3, got %v", s.segs)
	}
	s.Assign(0, 100, 0)
	if len(s.segs) != 0 || s.Total() != 0 {
		t.Fatalf("expected empty, got %v", s.segs)
	}
}

func TestRepeatedOverwriteSameValue(t *testing.T) {
	s := New(50)
	for i := int64(0); i < 50; i++ {
		s.Assign(i, i+1, 2) // each point write merges with the previous run
	}
	if len(s.segs) != 1 || s.segs[0] != (segment{0, 50, 2}) {
		t.Fatalf("expected single merged segment, got %v", s.segs)
	}
	if s.Total() != 100 {
		t.Fatalf("Total() = %d, want 100", s.Total())
	}
}

func TestNoOverflow(t *testing.T) {
	// Segment total 4 * 2^62 = 2^64 overflows uint64; Total must saturate.
	s := New(1 << 40)
	big := uint64(1) << 62
	s.Assign(0, 4, big)
	if got := s.Total(); got != math.MaxUint64 {
		t.Fatalf("Total() = %d, want saturated %d", got, uint64(math.MaxUint64))
	}
	// True total is 2^64 > MaxUint64, so Find(MaxUint64) must still resolve:
	// ceil((2^64-1)/2^62)-1 = 3.
	if idx, ok := s.Find(math.MaxUint64); !ok || idx != 3 {
		t.Fatalf("Find(MaxUint64) = (%d,%v), want (3,true)", idx, ok)
	}
	if idx, ok := s.Find(big + 1); !ok || idx != 1 {
		t.Fatalf("Find(2^62+1) = (%d,%v), want (1,true)", idx, ok)
	}

	// Accumulated saturation across several large segments.
	s2 := New(1 << 40)
	s2.Assign(0, 1, math.MaxUint64)
	s2.Assign(10, 11, math.MaxUint64) // pushes the running sum past 2^64
	if got := s2.Total(); got != math.MaxUint64 {
		t.Fatalf("Total() = %d, want saturated", got)
	}
	if idx, ok := s2.Find(math.MaxUint64); !ok || idx != 0 {
		t.Fatalf("Find(MaxUint64) = (%d,%v), want (0,true)", idx, ok)
	}
}

func TestEmptyArray(t *testing.T) {
	s := New(0)
	if _, ok := s.Find(0); ok {
		t.Fatal("Find on empty array should report not-exists")
	}
	if s.Total() != 0 {
		t.Fatal("Total of empty array should be 0")
	}
}

func TestInvalidRangePanics(t *testing.T) {
	s := New(10)
	for _, r := range [][2]int64{{-1, 5}, {0, 11}, {7, 3}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("Assign(%d,%d) should panic", r[0], r[1])
				}
			}()
			s.Assign(r[0], r[1], 1)
		}()
	}
}
