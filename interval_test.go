package intervalround

import (
	"math/big"
	"testing"
)

func rat(a, b int64) *big.Rat { return big.NewRat(a, b) }

func checkBound(t *testing.T, b Bound, want *big.Rat, wantOpen bool, what string) {
	t.Helper()
	if b.Inf != 0 {
		t.Fatalf("%s: got infinite bound, want %v", what, want)
	}
	if b.Rat.Cmp(want) != 0 || b.Open != wantOpen {
		t.Fatalf("%s: got (%v, open=%v), want (%v, open=%v)", what, b.Rat, b.Open, want, wantOpen)
	}
}

func TestRoundNegativeContainment(t *testing.T) {
	m := Closed(rat(-1, 3), rat(1, 3)) // [-1/3, 1/3]
	r := m.Round(2)
	if len(r.Segs) != 1 {
		t.Fatalf("want 1 segment, got %d", len(r.Segs))
	}
	checkBound(t, r.Segs[0].Lo, rat(-34, 100), false, "lo rounds down away from zero")
	checkBound(t, r.Segs[0].Hi, rat(34, 100), false, "hi rounds up away from zero")
	for _, x := range []*big.Rat{rat(-1, 3), rat(1, 3), rat(0, 1), rat(-33, 100), rat(1, 7)} {
		if !r.Contains(x) {
			t.Fatalf("rounded interval must contain exact point %v", x)
		}
	}
	if r.Contains(rat(-35, 100)) || r.Contains(rat(35, 100)) {
		t.Fatal("rounded interval must not contain points outside its bounds")
	}
}

func TestRoundPreservesOpenFlags(t *testing.T) {
	// Endpoints already exact at 2 digits: openness must be preserved.
	m := Of(rat(-33, 100), rat(34, 100), true, true)
	r := m.Round(2)
	checkBound(t, r.Segs[0].Lo, rat(-33, 100), true, "exact lo keeps open")
	checkBound(t, r.Segs[0].Hi, rat(34, 100), true, "exact hi keeps open")
	if r.Contains(rat(-33, 100)) || r.Contains(rat(34, 100)) {
		t.Fatal("open exact endpoints must stay excluded after rounding")
	}
	// Endpoints off the 2-digit grid move outward; closed is then safe.
	m2 := Of(rat(-331, 1000), rat(341, 1000), true, true)
	r2 := m2.Round(2)
	checkBound(t, r2.Segs[0].Lo, rat(-34, 100), false, "moved lo becomes closed")
	checkBound(t, r2.Segs[0].Hi, rat(35, 100), false, "moved hi becomes closed")
	if !r2.Contains(rat(-331, 1000)) || !r2.Contains(rat(341, 1000)) {
		t.Fatal("outward-rounded enclosure must contain the open exact endpoints")
	}
}

func TestInfiniteEndpoints(t *testing.T) {
	m := All()
	if !m.Contains(rat(-1000000, 3)) || !m.Contains(rat(0, 1)) {
		t.Fatal("(-inf,+inf) contains every rational")
	}
	r := m.Round(5)
	if r.Segs[0].Lo.Inf != -1 || r.Segs[0].Hi.Inf != 1 {
		t.Fatal("infinite endpoints survive rounding")
	}
	// Half-line (-inf, 1/3] rounds only the finite end.
	half := Multi{Segs: []Interval{{Lo: NegInf(), Hi: Fin(rat(1, 3), false)}}}
	rh := half.Round(1)
	if rh.Segs[0].Lo.Inf != -1 {
		t.Fatal("lo stays -inf")
	}
	checkBound(t, rh.Segs[0].Hi, rat(4, 10), false, "hi rounds up")
	if !rh.Contains(rat(1, 3)) || rh.Contains(rat(1, 2)) {
		t.Fatal("half-line enclosure containment wrong")
	}
}

func TestEmptySet(t *testing.T) {
	if !Empty().IsEmpty() {
		t.Fatal("Empty() is empty")
	}
	if Empty().Contains(rat(0, 1)) {
		t.Fatal("empty set contains nothing")
	}
	// Inverted and open-point intervals normalize to empty.
	if !Of(rat(1, 1), rat(0, 1), false, false).IsEmpty() {
		t.Fatal("lo > hi is empty")
	}
	if !Of(rat(1, 1), rat(1, 1), true, false).IsEmpty() {
		t.Fatal("open single point is empty")
	}
	// Disjoint intersection is empty and stays empty after rounding.
	d := Intersect(Closed(rat(0, 1), rat(1, 1)), Closed(rat(2, 1), rat(3, 1)))
	if !d.IsEmpty() || !d.Round(2).IsEmpty() {
		t.Fatal("disjoint intersection is empty, rounding keeps it empty")
	}
	// Open/closed touch at a point excluded by one side: empty.
	e := Intersect(Of(rat(0, 1), rat(1, 1), false, true), Closed(rat(1, 1), rat(2, 1)))
	if !e.IsEmpty() {
		t.Fatal("[0,1) ∩ [1,2] is empty")
	}
}

func TestSinglePointNotEmpty(t *testing.T) {
	p := Point(rat(3, 7))
	if p.IsEmpty() {
		t.Fatal("single point is not empty")
	}
	if !p.IsPoint() {
		t.Fatal("IsPoint detects {3/7}")
	}
	if !p.Contains(rat(3, 7)) || p.Contains(rat(3, 7)) == p.Contains(rat(4, 7)) && p.Contains(rat(4, 7)) {
		t.Fatal("point contains only itself")
	}
	// Intersection of two closed segments touching at 1 is the point {1}.
	q := Intersect(Closed(rat(0, 1), rat(1, 1)), Closed(rat(1, 1), rat(2, 1)))
	if !q.IsPoint() || !q.Contains(rat(1, 1)) {
		t.Fatal("[0,1] ∩ [1,2] = {1}")
	}
	// Rounding a point yields a small closed enclosure, never empty.
	r := p.Round(2)
	if r.IsEmpty() || !r.Contains(rat(3, 7)) {
		t.Fatal("rounded point still contains 3/7")
	}
	checkBound(t, r.Segs[0].Lo, rat(42, 100), false, "point lo")
	checkBound(t, r.Segs[0].Hi, rat(43, 100), false, "point hi")
}

func TestUnionMerging(t *testing.T) {
	// Overlap and closed touch merge into one segment.
	u := Union(Closed(rat(0, 1), rat(1, 1)), Closed(rat(1, 1), rat(2, 1)), 10)
	if len(u.Segs) != 1 {
		t.Fatalf("[0,1] ∪ [1,2] merges, got %d segments", len(u.Segs))
	}
	// Both open at the shared point: cannot merge.
	v := Union(Of(rat(0, 1), rat(1, 1), false, true), Of(rat(1, 1), rat(2, 1), true, false), 10)
	if len(v.Segs) != 2 || v.Contains(rat(1, 1)) {
		t.Fatal("(0,1) ∪ (1,2) stays two segments, 1 excluded")
	}
	// One side closed at the touch point: merges and includes it.
	w := Union(Of(rat(0, 1), rat(1, 1), false, true), Closed(rat(1, 1), rat(2, 1)), 10)
	if len(w.Segs) != 1 || !w.Contains(rat(1, 1)) {
		t.Fatal("(0,1) ∪ [1,2] merges and contains 1")
	}
}

func TestDivAcrossZero(t *testing.T) {
	// [1,2] / [-1,3] = (-inf,-1] ∪ [1/3,+inf)
	d := Div(Closed(rat(1, 1), rat(2, 1)), Closed(rat(-1, 1), rat(3, 1)), 10)
	if len(d.Segs) != 2 {
		t.Fatalf("division across zero yields 2 segments, got %d", len(d.Segs))
	}
	if d.Segs[0].Lo.Inf != -1 {
		t.Fatal("left segment starts at -inf")
	}
	checkBound(t, d.Segs[0].Hi, rat(-1, 1), false, "left segment ends at -1 closed")
	checkBound(t, d.Segs[1].Lo, rat(1, 3), false, "right segment starts at 1/3 closed")
	if d.Segs[1].Hi.Inf != 1 {
		t.Fatal("right segment ends at +inf")
	}
	for _, x := range []*big.Rat{rat(-1, 1), rat(-5, 1), rat(1, 3), rat(2, 1), rat(100, 1)} {
		if !d.Contains(x) {
			t.Fatalf("quotient set must contain %v", x)
		}
	}
	for _, x := range []*big.Rat{rat(0, 1), rat(-1, 2), rat(1, 4)} {
		if d.Contains(x) {
			t.Fatalf("quotient set must exclude gap point %v", x)
		}
	}
	// Division by the single point {0} is empty.
	z := Div(Closed(rat(1, 1), rat(2, 1)), Point(rat(0, 1)), 10)
	if !z.IsEmpty() {
		t.Fatal("division by {0} is empty")
	}
	// Numerator containing zero over [0,2]: result [0,+inf).
	g := Div(Closed(rat(0, 1), rat(1, 1)), Closed(rat(0, 1), rat(2, 1)), 10)
	if len(g.Segs) != 1 || !g.Contains(rat(0, 1)) || g.Segs[0].Hi.Inf != 1 {
		t.Fatal("[0,1]/[0,2] = [0,+inf)")
	}
}

func TestSegmentLimitConservativeMerge(t *testing.T) {
	m := Union(
		Union(Closed(rat(0, 1), rat(1, 1)), Closed(rat(3, 1), rat(4, 1)), 10),
		Closed(rat(20, 1), rat(21, 1)), 10)
	if len(m.Segs) != 3 {
		t.Fatalf("setup: want 3 segments, got %d", len(m.Segs))
	}
	// Limit 2: the closest pair [0,1],[3,4] hull-merges into [0,4].
	lim := limitSegs(m.Segs, 2)
	if len(lim) != 2 {
		t.Fatalf("want 2 segments, got %d", len(lim))
	}
	lm := Multi{Segs: lim}
	for _, x := range []*big.Rat{rat(0, 1), rat(1, 1), rat(3, 1), rat(4, 1), rat(20, 1), rat(21, 1)} {
		if !lm.Contains(x) {
			t.Fatalf("conservative merge must retain original point %v", x)
		}
	}
	if !lm.Contains(rat(2, 1)) {
		t.Fatal("hull merge fills the small gap conservatively")
	}
	if lm.Contains(rat(10, 1)) {
		t.Fatal("the large gap must not be filled when merging the closest pair")
	}
	// Limit 1: full convex hull.
	one := Multi{Segs: limitSegs(m.Segs, 1)}
	if len(one.Segs) != 1 || !one.Contains(rat(10, 1)) || one.Contains(rat(-1, 1)) {
		t.Fatal("limit 1 yields the convex hull [0,21]")
	}
	// Union with limit applies the same rule.
	u := Union(Closed(rat(0, 1), rat(1, 1)), Closed(rat(3, 1), rat(4, 1)), 1)
	if len(u.Segs) != 1 || !u.Contains(rat(2, 1)) {
		t.Fatal("Union with maxSegs=1 hull-merges")
	}
}

func TestRoundEnclosureProperty(t *testing.T) {
	// For a table of exact sets and sample points, the rounded enclosure
	// must contain every exact point, and endpoints must be decimals with
	// the requested denominator.
	cases := []Multi{
		Closed(rat(-7, 3), rat(-1, 9)),
		Of(rat(1, 7), rat(9, 4), true, false),
		Point(rat(-2, 11)),
		Div(Closed(rat(-3, 1), rat(5, 1)), Closed(rat(-2, 1), rat(7, 1)), 10),
		Union(Closed(rat(-5, 2), rat(-1, 5)), Closed(rat(1, 5), rat(5, 2)), 10),
	}
	for i, m := range cases {
		r := m.Round(3)
		if m.IsEmpty() != r.IsEmpty() {
			t.Fatalf("case %d: rounding changed emptiness", i)
		}
		// Sample rational points across each segment, endpoints included.
		for _, s := range m.Segs {
			lo, hi := rat(-100, 1), rat(100, 1)
			if s.Lo.Inf == 0 {
				lo = s.Lo.Rat
			}
			if s.Hi.Inf == 0 {
				hi = s.Hi.Rat
			}
			for k := int64(0); k <= 8; k++ {
				x := new(big.Rat).Mul(hi, rat(k, 8))
				x.Add(x, new(big.Rat).Mul(lo, rat(8-k, 8)))
				if s.contains(x) && !r.Contains(x) {
					t.Fatalf("case %d: enclosure lost exact point %v", i, x)
				}
			}
		}
		// Every finite rounded endpoint must sit on the 10^-3 grid.
		for _, s := range r.Segs {
			for _, b := range []Bound{s.Lo, s.Hi} {
				if b.Inf != 0 {
					continue
				}
				scaled := new(big.Rat).Mul(b.Rat, big.NewRat(1000, 1))
				if !scaled.IsInt() {
					t.Fatalf("case %d: endpoint %v not on 10^-3 grid", i, b.Rat)
				}
			}
		}
	}
}
