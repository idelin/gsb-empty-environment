package interval

import (
	"math/big"
	"math/rand"
	"sort"
	"testing"
)

func rat(v int64) *big.Rat { return big.NewRat(v, 1) }

func half(v int64) *big.Rat { return big.NewRat(v, 2) }

// representative returns one point strictly inside a non-empty interval,
// covering infinite endpoints.
func representative(iv Interval) *big.Rat {
	switch {
	case iv.Lo.Inf == -1 && iv.Hi.Inf == 1:
		return rat(0)
	case iv.Lo.Inf == -1:
		return new(big.Rat).Sub(iv.Hi.Val, rat(1))
	case iv.Hi.Inf == 1:
		return new(big.Rat).Add(iv.Lo.Val, rat(1))
	default:
		return new(big.Rat).Quo(new(big.Rat).Add(iv.Lo.Val, iv.Hi.Val), rat(2))
	}
}

// keyPoints builds a finite sample that pins down membership of the given
// sets: every finite endpoint value, one representative per open cell
// between consecutive endpoint values, points beyond the extreme values
// (which exercises infinite endpoints), and one representative point per
// interval.
func keyPoints(sets ...Set) []*big.Rat {
	seen := map[string]bool{}
	var vals []*big.Rat
	add := func(v *big.Rat) {
		k := v.RatString()
		if !seen[k] {
			seen[k] = true
			vals = append(vals, new(big.Rat).Set(v))
		}
	}
	var pts []*big.Rat
	for _, s := range sets {
		for _, iv := range s.ivs {
			if iv.Lo.Inf == 0 {
				add(iv.Lo.Val)
			}
			if iv.Hi.Inf == 0 {
				add(iv.Hi.Val)
			}
			pts = append(pts, representative(iv))
		}
	}
	if len(vals) == 0 {
		return append(pts, rat(-1), rat(0), rat(1))
	}
	sort.Slice(vals, func(i, j int) bool { return vals[i].Cmp(vals[j]) < 0 })
	pts = append(pts, new(big.Rat).Sub(vals[0], rat(1)))
	for i, v := range vals {
		pts = append(pts, v)
		if i+1 < len(vals) {
			mid := new(big.Rat).Quo(new(big.Rat).Add(v, vals[i+1]), rat(2))
			pts = append(pts, mid)
		}
	}
	pts = append(pts, new(big.Rat).Add(vals[len(vals)-1], rat(1)))
	return pts
}

// checkNormalized verifies the output invariants: no empty intervals,
// sorted, pairwise disjoint, and no adjacent pair mergeable without a gap.
func checkNormalized(t *testing.T, s Set) {
	t.Helper()
	for i, iv := range s.ivs {
		if iv.IsEmpty() {
			t.Fatalf("set %v contains empty interval %v", s, iv)
		}
		if i == 0 {
			continue
		}
		prev := s.ivs[i-1]
		if cmpPosition(prev.Hi, iv.Lo) > 0 {
			t.Fatalf("set %v not sorted/disjoint at %v and %v", s, prev, iv)
		}
		if mergeable(prev.Hi, iv.Lo) {
			t.Fatalf("set %v: %v and %v should have been merged", s, prev, iv)
		}
	}
}

func TestContains(t *testing.T) {
	cases := []struct {
		iv  Interval
		in  []*big.Rat
		out []*big.Rat
	}{
		{New(OpenAt(rat(1)), ClosedAt(rat(3))),
			[]*big.Rat{rat(2), rat(3), half(3)},
			[]*big.Rat{rat(1), rat(4), rat(0)}},
		{New(ClosedAt(rat(2)), ClosedAt(rat(2))),
			[]*big.Rat{rat(2)},
			[]*big.Rat{rat(1), rat(3), half(3), half(5)}},
		{New(NegInf(), OpenAt(rat(0))),
			[]*big.Rat{rat(-100), rat(-1), half(-1)},
			[]*big.Rat{rat(0), rat(1)}},
		{New(ClosedAt(rat(5)), PosInf()),
			[]*big.Rat{rat(5), rat(1 << 40)},
			[]*big.Rat{rat(4), rat(-7)}},
		{New(NegInf(), PosInf()),
			[]*big.Rat{rat(0), rat(-1 << 40), rat(1 << 40)},
			nil},
	}
	for _, c := range cases {
		for _, p := range c.in {
			if !c.iv.Contains(p) {
				t.Errorf("%v should contain %v", c.iv, p)
			}
		}
		for _, p := range c.out {
			if c.iv.Contains(p) {
				t.Errorf("%v should not contain %v", c.iv, p)
			}
		}
	}
}

func TestNormalize(t *testing.T) {
	cases := []struct {
		name string
		in   []Interval
		want string
	}{
		{"overlap merges", []Interval{
			New(ClosedAt(rat(1)), ClosedAt(rat(4))),
			New(ClosedAt(rat(3)), ClosedAt(rat(6))),
		}, "[1,6]"},
		{"touching closed merges", []Interval{
			New(ClosedAt(rat(1)), ClosedAt(rat(2))),
			New(ClosedAt(rat(2)), ClosedAt(rat(3))),
		}, "[1,3]"},
		{"half open touching merges", []Interval{
			New(ClosedAt(rat(1)), OpenAt(rat(2))),
			New(ClosedAt(rat(2)), ClosedAt(rat(3))),
		}, "[1,3]"},
		{"open gap at shared point stays split", []Interval{
			New(ClosedAt(rat(1)), OpenAt(rat(2))),
			New(OpenAt(rat(2)), ClosedAt(rat(3))),
		}, "[1,2) U (2,3]"},
		{"strict gap stays split", []Interval{
			New(ClosedAt(rat(1)), ClosedAt(rat(2))),
			New(ClosedAt(rat(3)), ClosedAt(rat(4))),
		}, "[1,2] U [3,4]"},
		{"single point fills gap", []Interval{
			New(ClosedAt(rat(1)), OpenAt(rat(2))),
			New(ClosedAt(rat(2)), ClosedAt(rat(2))),
			New(OpenAt(rat(2)), ClosedAt(rat(3))),
		}, "[1,3]"},
		{"single point kept", []Interval{
			New(ClosedAt(rat(2)), ClosedAt(rat(2))),
		}, "[2,2]"},
		{"empty intervals dropped", []Interval{
			New(OpenAt(rat(1)), OpenAt(rat(1))),
			New(ClosedAt(rat(1)), OpenAt(rat(1))),
			New(ClosedAt(rat(3)), ClosedAt(rat(2))),
			New(ClosedAt(rat(5)), ClosedAt(rat(5))),
		}, "[5,5]"},
		{"infinite absorbs", []Interval{
			New(ClosedAt(rat(0)), PosInf()),
			New(ClosedAt(rat(10)), ClosedAt(rat(20))),
			New(NegInf(), OpenAt(rat(-3))),
		}, "(-inf,-3) U [0,+inf)"},
		{"unsorted input sorted", []Interval{
			New(ClosedAt(rat(8)), ClosedAt(rat(9))),
			New(ClosedAt(rat(1)), ClosedAt(rat(2))),
		}, "[1,2] U [8,9]"},
	}
	for _, c := range cases {
		got := NewSet(c.in...)
		checkNormalized(t, got)
		if got.String() != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestSetOpsStructure(t *testing.T) {
	a := NewSet(New(ClosedAt(rat(1)), ClosedAt(rat(5))))
	b := NewSet(New(OpenAt(rat(2)), OpenAt(rat(3))))
	if got := a.Subtract(b).String(); got != "[1,2] U [3,5]" {
		t.Errorf("subtract open middle: got %v", got)
	}
	b = NewSet(New(ClosedAt(rat(2)), ClosedAt(rat(3))))
	if got := a.Subtract(b).String(); got != "[1,2) U (3,5]" {
		t.Errorf("subtract closed middle: got %v", got)
	}
	c := NewSet(New(ClosedAt(rat(4)), PosInf()))
	if got := a.Subtract(c).String(); got != "[1,4)" {
		t.Errorf("subtract ray: got %v", got)
	}
	if got := c.Subtract(a).String(); got != "(5,+inf)" {
		t.Errorf("ray minus finite: got %v", got)
	}
	d := NewSet(New(ClosedAt(rat(0)), ClosedAt(rat(2))), New(ClosedAt(rat(4)), ClosedAt(rat(6))))
	if got := a.Intersect(d).String(); got != "[1,2] U [4,5]" {
		t.Errorf("intersect: got %v", got)
	}
	if got := a.Union(d).String(); got != "[0,6]" {
		t.Errorf("union: got %v", got)
	}
}

// TestOpsAgainstOracle cross-checks union, intersection and difference
// against pointwise membership on a finite set of key points and
// representatives, including infinite endpoints.
func TestOpsAgainstOracle(t *testing.T) {
	sets := []Set{
		NewSet(New(NegInf(), OpenAt(half(-3))), New(ClosedAt(rat(0)), ClosedAt(half(5)))),
		NewSet(New(OpenAt(half(-5)), ClosedAt(rat(0))), New(ClosedAt(rat(2)), PosInf())),
		NewSet(New(ClosedAt(half(-1)), OpenAt(half(7)))),
		NewSet(New(ClosedAt(rat(1)), ClosedAt(rat(1)))), // single point
		NewSet(New(NegInf(), PosInf())),
		NewSet(), // empty
	}
	type op struct {
		name string
		f    func(a, b Set) Set
		or   func(a, b bool) bool
	}
	ops := []op{
		{"union", Set.Union, func(a, b bool) bool { return a || b }},
		{"intersect", Set.Intersect, func(a, b bool) bool { return a && b }},
		{"subtract", Set.Subtract, func(a, b bool) bool { return a && !b }},
	}
	for i, a := range sets {
		for j, b := range sets {
			pts := keyPoints(a, b)
			for _, o := range ops {
				got := o.f(a, b)
				checkNormalized(t, got)
				for _, p := range pts {
					want := o.or(a.Contains(p), b.Contains(p))
					if got.Contains(p) != want {
						t.Errorf("%s of sets %d,%d at %v: got %v, want %v\na=%v\nb=%v\nr=%v",
							o.name, i, j, p, !want, want, a, b, got)
					}
				}
			}
		}
	}
}

// TestComposedOps verifies nested expressions such as (A u B) \ C.
func TestComposedOps(t *testing.T) {
	a := NewSet(New(NegInf(), ClosedAt(rat(1))), New(OpenAt(rat(2)), ClosedAt(rat(4))))
	b := NewSet(New(ClosedAt(rat(0)), OpenAt(rat(3))))
	c := NewSet(New(ClosedAt(half(1)), ClosedAt(half(5))), New(ClosedAt(rat(9)), PosInf()))
	pts := keyPoints(a, b, c)
	type expr struct {
		name string
		got  Set
		or   func(x *big.Rat) bool
	}
	exprs := []expr{
		{"(A u B) \\ C", a.Union(b).Subtract(c),
			func(x *big.Rat) bool { return (a.Contains(x) || b.Contains(x)) && !c.Contains(x) }},
		{"(A n B) u C", a.Intersect(b).Union(c),
			func(x *big.Rat) bool { return (a.Contains(x) && b.Contains(x)) || c.Contains(x) }},
		{"A \\ (B u C)", a.Subtract(b.Union(c)),
			func(x *big.Rat) bool { return a.Contains(x) && !(b.Contains(x) || c.Contains(x)) }},
		{"(A \\ B) n C", a.Subtract(b).Intersect(c),
			func(x *big.Rat) bool { return (a.Contains(x) && !b.Contains(x)) && c.Contains(x) }},
	}
	for _, e := range exprs {
		checkNormalized(t, e.got)
		for _, p := range pts {
			if e.got.Contains(p) != e.or(p) {
				t.Errorf("%s at %v: got %v, want %v (result %v)",
					e.name, p, !e.or(p), e.or(p), e.got)
			}
		}
	}
}

// TestRandomized generates random sets with rational and infinite
// endpoints and checks random operation trees against the pointwise
// oracle, plus normalization invariants.
func TestRandomized(t *testing.T) {
	rnd := rand.New(rand.NewSource(42))
	randBound := func() Bound {
		switch rnd.Intn(10) {
		case 0:
			return NegInf()
		case 1:
			return PosInf()
		}
		v := big.NewRat(int64(rnd.Intn(13)-6), int64(1+rnd.Intn(2)))
		if rnd.Intn(2) == 0 {
			return ClosedAt(v)
		}
		return OpenAt(v)
	}
	randInterval := func() Interval {
		lo, hi := randBound(), randBound()
		if cmpPosition(lo, hi) > 0 {
			lo, hi = hi, lo
		}
		return New(lo, hi)
	}
	randSet := func() Set {
		n := 1 + rnd.Intn(3)
		var ivs []Interval
		for i := 0; i < n; i++ {
			ivs = append(ivs, randInterval())
		}
		return NewSet(ivs...)
	}
	for iter := 0; iter < 500; iter++ {
		a, b, c := randSet(), randSet(), randSet()
		var got Set
		var oracle func(x *big.Rat) bool
		switch rnd.Intn(4) {
		case 0:
			got = a.Union(b).Intersect(c)
			oracle = func(x *big.Rat) bool {
				return (a.Contains(x) || b.Contains(x)) && c.Contains(x)
			}
		case 1:
			got = a.Intersect(b).Union(c)
			oracle = func(x *big.Rat) bool {
				return (a.Contains(x) && b.Contains(x)) || c.Contains(x)
			}
		case 2:
			got = a.Subtract(b).Union(c.Subtract(a))
			oracle = func(x *big.Rat) bool {
				return (a.Contains(x) && !b.Contains(x)) || (c.Contains(x) && !a.Contains(x))
			}
		case 3:
			got = a.Union(b).Subtract(c)
			oracle = func(x *big.Rat) bool {
				return (a.Contains(x) || b.Contains(x)) && !c.Contains(x)
			}
		}
		checkNormalized(t, got)
		for _, p := range keyPoints(a, b, c, got) {
			if got.Contains(p) != oracle(p) {
				t.Fatalf("iter %d at %v: got %v, want %v\na=%v\nb=%v\nc=%v\nr=%v",
					iter, p, !oracle(p), oracle(p), a, b, c, got)
			}
		}
	}
}
