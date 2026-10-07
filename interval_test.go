package interval

import (
	"math/big"
	"testing"
)

// R parses a rational literal like "3/2" or "-2".
func R(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("bad rational: " + s)
	}
	return r
}

// representative returns a point strictly inside a non-empty interval.
func representative(iv Interval) *big.Rat {
	lo, hi := iv.Lo, iv.Hi
	switch {
	case lo.Kind == Finite && hi.Kind == Finite:
		if lo.Value.Cmp(hi.Value) == 0 {
			return new(big.Rat).Set(lo.Value) // single point
		}
		m := new(big.Rat).Add(lo.Value, hi.Value)
		return m.Quo(m, big.NewRat(2, 1))
	case lo.Kind == Finite: // hi = +inf
		return new(big.Rat).Add(lo.Value, big.NewRat(1, 1))
	case hi.Kind == Finite: // lo = -inf
		return new(big.Rat).Sub(hi.Value, big.NewRat(1, 1))
	default: // (-inf, +inf)
		return big.NewRat(0, 1)
	}
}

// samplePoints collects finite key points (every finite endpoint and a
// small neighbourhood around it) plus one representative point per
// interval, covering unbounded intervals too.
func samplePoints(sets ...Set) []*big.Rat {
	seen := map[string]bool{}
	var pts []*big.Rat
	add := func(r *big.Rat) {
		if k := r.RatString(); !seen[k] {
			seen[k] = true
			pts = append(pts, r)
		}
	}
	eps := big.NewRat(1, 2)
	for _, s := range sets {
		for _, iv := range s.ivs {
			for _, e := range []Endpoint{iv.Lo, iv.Hi} {
				if e.Kind == Finite {
					add(new(big.Rat).Set(e.Value))
					add(new(big.Rat).Sub(e.Value, eps))
					add(new(big.Rat).Add(e.Value, eps))
				}
			}
			add(representative(iv))
		}
	}
	add(big.NewRat(0, 1))
	return pts
}

// rawContains evaluates membership against an unnormalized interval list.
func rawContains(ivs []Interval, x *big.Rat) bool {
	for _, iv := range ivs {
		if iv.Contains(x) {
			return true
		}
	}
	return false
}

// checkNormalized verifies the Set invariants: sorted, disjoint, and no
// two adjacent intervals mergeable without a gap.
func checkNormalized(t *testing.T, s Set) {
	t.Helper()
	for i, iv := range s.ivs {
		if iv.IsEmpty() {
			t.Fatalf("empty interval kept at %d: %v", i, iv)
		}
		if i == 0 {
			continue
		}
		prev := s.ivs[i-1]
		c := comparePos(prev.Hi, iv.Lo)
		if c > 0 {
			t.Fatalf("intervals %v and %v overlap", prev, iv)
		}
		if c == 0 && (!prev.Hi.Open || !iv.Lo.Open) {
			t.Fatalf("intervals %v and %v are gap-free adjacent, should be merged", prev, iv)
		}
	}
}

// verifyOps cross-checks Union/Intersect/Subtract membership against the
// brute-force predicates on the raw operand lists, using key points and
// representative points.
func verifyOps(t *testing.T, rawA, rawB []Interval) {
	t.Helper()
	a, b := NewSet(rawA...), NewSet(rawB...)
	u, in, diff := Union(a, b), Intersect(a, b), Subtract(a, b)
	checkNormalized(t, u)
	checkNormalized(t, in)
	checkNormalized(t, diff)
	for _, x := range samplePoints(a, b, u, in, diff) {
		inA, inB := rawContains(rawA, x), rawContains(rawB, x)
		if got, want := u.Contains(x), inA || inB; got != want {
			t.Errorf("union membership at %s: got %v, want %v", x, got, want)
		}
		if got, want := in.Contains(x), inA && inB; got != want {
			t.Errorf("intersect membership at %s: got %v, want %v", x, got, want)
		}
		if got, want := diff.Contains(x), inA && !inB; got != want {
			t.Errorf("subtract membership at %s: got %v, want %v", x, got, want)
		}
	}
}

func TestNormalizeMergesOnlyWithoutGap(t *testing.T) {
	cases := []struct {
		name string
		in   []Interval
		want int // number of resulting segments
	}{
		{"closed+closed merges", []Interval{
			New(ClosedEnd(R("1")), ClosedEnd(R("2"))),
			New(ClosedEnd(R("2")), ClosedEnd(R("3"))),
		}, 1},
		{"closed+open merges", []Interval{
			New(ClosedEnd(R("1")), ClosedEnd(R("2"))),
			New(OpenEnd(R("2")), ClosedEnd(R("3"))),
		}, 1},
		{"open+closed merges", []Interval{
			New(ClosedEnd(R("1")), OpenEnd(R("2"))),
			New(ClosedEnd(R("2")), ClosedEnd(R("3"))),
		}, 1},
		{"open+open has gap", []Interval{
			New(ClosedEnd(R("1")), OpenEnd(R("2"))),
			New(OpenEnd(R("2")), ClosedEnd(R("3"))),
		}, 2},
		{"distinct segments stay", []Interval{
			New(ClosedEnd(R("1")), ClosedEnd(R("2"))),
			New(ClosedEnd(R("4")), ClosedEnd(R("5"))),
		}, 2},
	}
	for _, tc := range cases {
		got := NewSet(tc.in...)
		checkNormalized(t, got)
		if len(got.ivs) != tc.want {
			t.Errorf("%s: got %d segments (%v), want %d", tc.name, len(got.ivs), got, tc.want)
		}
	}
}

func TestPointInterval(t *testing.T) {
	p := Point(R("2"))
	if p.IsEmpty() {
		t.Fatal("[2,2] must be valid")
	}
	if !p.Contains(R("2")) || p.Contains(R("3/2")) || p.Contains(R("5/2")) {
		t.Fatal("point interval membership wrong")
	}
	// Half-open degenerate intervals are empty and dropped.
	for _, iv := range []Interval{
		New(OpenEnd(R("2")), ClosedEnd(R("2"))),
		New(ClosedEnd(R("2")), OpenEnd(R("2"))),
		New(OpenEnd(R("2")), OpenEnd(R("2"))),
		New(ClosedEnd(R("3")), ClosedEnd(R("1"))), // lo > hi
	} {
		if !iv.IsEmpty() {
			t.Errorf("%v should be empty", iv)
		}
	}
	s := NewSet(p, New(OpenEnd(R("2")), OpenEnd(R("2"))))
	if len(s.ivs) != 1 || !s.Contains(R("2")) {
		t.Fatalf("empty intervals not dropped or point lost: %v", s)
	}
	// A point plugs the gap between two open neighbours.
	merged := NewSet(
		New(ClosedEnd(R("1")), OpenEnd(R("2"))),
		Point(R("2")),
		New(OpenEnd(R("2")), ClosedEnd(R("3"))),
	)
	if len(merged.ivs) != 1 {
		t.Fatalf("point should bridge the gap, got %v", merged)
	}
}

func TestInfiniteEndpoints(t *testing.T) {
	left := NewSet(New(NegInfEnd(), ClosedEnd(R("0"))))  // (-inf, 0]
	right := NewSet(New(ClosedEnd(R("0")), PosInfEnd())) // [0, +inf)
	u := Union(left, right)
	if len(u.ivs) != 1 || u.ivs[0] != All() {
		t.Fatalf("union should be the whole line, got %v", u)
	}
	for _, x := range []string{"-1000000", "0", "1000000", "1/3"} {
		if !u.Contains(R(x)) {
			t.Errorf("whole line should contain %s", x)
		}
	}
	// Intersection of the two is exactly {0}.
	in := Intersect(left, right)
	if len(in.ivs) != 1 || !in.Contains(R("0")) || in.Contains(R("1")) || in.Contains(R("-1")) {
		t.Fatalf("intersection should be {0}, got %v", in)
	}
	// (-inf, 0] \ {0} = (-inf, 0)
	diff := Subtract(left, NewSet(Point(R("0"))))
	if len(diff.ivs) != 1 || diff.Contains(R("0")) || !diff.Contains(R("-1/2")) {
		t.Fatalf("difference wrong: %v", diff)
	}
	// All() \ [1,2] = (-inf,1) ∪ (2,+inf)
	rest := Subtract(NewSet(All()), NewSet(New(ClosedEnd(R("1")), ClosedEnd(R("2")))))
	if len(rest.ivs) != 2 || rest.Contains(R("3/2")) || !rest.Contains(R("0")) || !rest.Contains(R("3")) {
		t.Fatalf("complement of [1,2] wrong: %v", rest)
	}
}

func TestOpsAgainstBruteForce(t *testing.T) {
	rawA := []Interval{
		New(NegInfEnd(), OpenEnd(R("-1"))),        // (-inf, -1)
		New(ClosedEnd(R("0")), ClosedEnd(R("2"))), // [0, 2]
		Point(R("3")),                           // {3}
		New(OpenEnd(R("7/2")), PosInfEnd()),     // (7/2, +inf)
		New(OpenEnd(R("5")), ClosedEnd(R("5"))), // empty, dropped
	}
	rawB := []Interval{
		New(ClosedEnd(R("-3/2")), ClosedEnd(R("-1"))), // [-3/2, -1]
		New(OpenEnd(R("1")), OpenEnd(R("7/2"))),       // (1, 7/2)
		New(ClosedEnd(R("4")), ClosedEnd(R("4"))),     // {4}
	}
	verifyOps(t, rawA, rawB)
	verifyOps(t, rawB, rawA) // operands swapped
	verifyOps(t, rawA, rawA) // identical operands
	verifyOps(t, rawA, nil)  // empty operand
}

func TestSubtractSplitsAndTrims(t *testing.T) {
	// [0,10] \ (3,5) = [0,3] ∪ [5,10]
	s := Subtract(
		NewSet(New(ClosedEnd(R("0")), ClosedEnd(R("10")))),
		NewSet(New(OpenEnd(R("3")), OpenEnd(R("5")))),
	)
	if len(s.ivs) != 2 ||
		!s.Contains(R("3")) || !s.Contains(R("5")) ||
		s.Contains(R("4")) || s.Contains(R("7/2")) {
		t.Fatalf("split wrong: %v", s)
	}
	// Subtracting a superset removes everything.
	if got := Subtract(NewSet(Point(R("1"))), NewSet(All())); !got.IsEmpty() {
		t.Fatalf("expected empty, got %v", got)
	}
}
