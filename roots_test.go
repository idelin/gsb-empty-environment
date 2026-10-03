package ratbezier

import (
	"math/big"
	"testing"
)

func ratEq(a *big.Rat, num, den int64) bool {
	return a.Cmp(big.NewRat(num, den)) == 0
}

func TestExtractRationalRootsDistinct(t *testing.T) {
	// (2x-1)(3x-2)(x+1) = 6x^3 - x^2 - 5x + 2
	p := NewPoly(2, -5, -1, 6)
	roots, rest := extractRationalRoots(p)
	if len(roots) != 3 {
		t.Fatalf("got %d roots, want 3", len(roots))
	}
	want := map[string]bool{"1/2": false, "2/3": false, "-1": false}
	for _, r := range roots {
		s := r.RatString()
		if _, ok := want[s]; !ok {
			t.Errorf("unexpected root %s", s)
		}
		want[s] = true
	}
	for s, found := range want {
		if !found {
			t.Errorf("missing root %s", s)
		}
	}
	if rest.Degree() != 0 {
		t.Errorf("rest = %s, want constant", rest)
	}
}

func TestExtractRationalRootsRepeated(t *testing.T) {
	// (2x-1)^2 = 4x^2 - 4x + 1
	roots, rest := extractRationalRoots(NewPoly(1, -4, 4))
	if len(roots) != 2 {
		t.Fatalf("got %d roots, want 2 (with multiplicity)", len(roots))
	}
	for _, r := range roots {
		if !ratEq(r, 1, 2) {
			t.Errorf("root = %s, want 1/2", r.RatString())
		}
	}
	if rest.Degree() != 0 {
		t.Errorf("rest = %s, want constant", rest)
	}
}

func TestExtractRationalRootsNone(t *testing.T) {
	// x^2 - 2 has no rational roots
	roots, rest := extractRationalRoots(NewPoly(-2, 0, 1))
	if len(roots) != 0 {
		t.Errorf("got %d roots, want 0", len(roots))
	}
	if !polyEq(rest, -2, 0, 1) {
		t.Errorf("rest = %s, want x^2 - 2", rest)
	}
}

func TestSturmCountRoots(t *testing.T) {
	// x^3 - x has roots -1, 0, 1
	seq := buildSturmSeq(NewPoly(0, -1, 0, 1))
	cases := []struct {
		a, b *big.Rat
		want int
	}{
		{big.NewRat(-2, 1), big.NewRat(2, 1), 3},  // (-2, 2]: -1, 0, 1
		{big.NewRat(0, 1), big.NewRat(2, 1), 1},   // (0, 2]: only 1
		{big.NewRat(-2, 1), big.NewRat(-1, 1), 1}, // (-2, -1]: only -1
		{big.NewRat(2, 1), big.NewRat(3, 1), 0},   // (2, 3]: none
	}
	for _, tc := range cases {
		if got := seq.countRoots(tc.a, tc.b); got != tc.want {
			t.Errorf("countRoots(%s, %s) = %d, want %d", tc.a.RatString(), tc.b.RatString(), got, tc.want)
		}
	}
}

func TestIsolateRootsSqrt2(t *testing.T) {
	// x^2 - 2: isolate sqrt(2) in (0, 2] to 1e-20
	eps := new(big.Rat).SetFrac(big.NewInt(1), new(big.Int).Exp(big.NewInt(10), big.NewInt(20), nil))
	ivs := isolateRoots(NewPoly(-2, 0, 1), big.NewRat(0, 1), big.NewRat(2, 1), eps)
	if len(ivs) != 1 {
		t.Fatalf("got %d intervals, want 1", len(ivs))
	}
	iv := ivs[0]
	two := big.NewInt(2)
	lo2 := new(big.Rat).Mul(iv.lo, iv.lo)
	hi2 := new(big.Rat).Mul(iv.hi, iv.hi)
	if lo2.Num().Cmp(new(big.Int).Mul(two, lo2.Denom())) >= 0 {
		t.Errorf("lo = %s not below sqrt(2)", iv.lo.RatString())
	}
	if hi2.Num().Cmp(new(big.Int).Mul(two, hi2.Denom())) <= 0 {
		t.Errorf("hi = %s not above sqrt(2)", iv.hi.RatString())
	}
	if w := new(big.Rat).Sub(iv.hi, iv.lo); w.Cmp(eps) > 0 {
		t.Errorf("interval width %s exceeds eps", w.RatString())
	}
}

func TestIsolateRootsMultiple(t *testing.T) {
	// (x^2 - 2)(x^2 - 8) = x^4 - 10x^2 + 16, roots sqrt(2), 2*sqrt(2) in (0, 3]
	eps := new(big.Rat).SetFrac(big.NewInt(1), new(big.Int).Exp(big.NewInt(10), big.NewInt(15), nil))
	ivs := isolateRoots(NewPoly(16, 0, -10, 0, 1), big.NewRat(0, 1), big.NewRat(3, 1), eps)
	if len(ivs) != 2 {
		t.Fatalf("got %d intervals, want 2", len(ivs))
	}
	// First interval brackets sqrt(2) ~ 1.414, second 2*sqrt(2) ~ 2.828.
	if ivs[0].hi.Cmp(ivs[1].lo) >= 0 {
		t.Errorf("intervals not disjoint/ordered: [%s, %s], [%s, %s]",
			ivs[0].lo, ivs[0].hi, ivs[1].lo, ivs[1].hi)
	}
	f, _ := new(big.Float).SetRat(ivs[0].approx()).Float64()
	if f < 1.41 || f > 1.42 {
		t.Errorf("first root approx %v, want ~1.41421", f)
	}
	f, _ = new(big.Float).SetRat(ivs[1].approx()).Float64()
	if f < 2.82 || f > 2.83 {
		t.Errorf("second root approx %v, want ~2.82843", f)
	}
}
