package rootfind

import (
	"math/big"
	"testing"
)

func rat(s string) *big.Rat { return Rat(s) }

// lin returns the polynomial (x - r) for a rational r given as "p/q".
func lin(s string) Poly {
	neg := new(big.Rat).Neg(rat(s))
	return NewPoly(neg, big.NewRat(1, 1))
}

func linPow(s string, n int) Poly { return Pow(lin(s), n) }

func prod(ps ...Poly) Poly {
	r := PolyFromInts(1)
	for _, p := range ps {
		r = Mul(r, p)
	}
	return r
}

type wantRoot struct {
	at   string
	mult int
}

// checkRoots verifies that every expected root is covered by exactly one
// isolating interval of the right multiplicity and precision, and that no
// extra roots were reported.
func checkRoots(t *testing.T, res Result, eps *big.Rat, want []wantRoot) {
	t.Helper()
	if !res.Complete() {
		t.Fatalf("result incomplete: %+v", res.Incomplete)
	}
	if len(res.Roots) != len(want) {
		t.Fatalf("got %d roots, want %d: %+v", len(res.Roots), len(want), res.Roots)
	}
	used := make([]bool, len(res.Roots))
	for _, w := range want {
		x := rat(w.at)
		found := false
		for i, r := range res.Roots {
			if used[i] || !r.Contains(x) {
				continue
			}
			used[i] = true
			found = true
			if r.Mult != w.mult {
				t.Errorf("root at %s: multiplicity %d, want %d", w.at, r.Mult, w.mult)
			}
			if r.Width().Cmp(eps) > 0 {
				t.Errorf("root at %s: interval width %s exceeds eps %s",
					w.at, r.Width().RatString(), eps.RatString())
			}
		}
		if !found {
			t.Errorf("root at %s (mult %d) not isolated", w.at, w.mult)
		}
	}
}

// TestSquareFactorConstruction is the main validation: a polynomial built
// from square (and higher-power) factors, whose even-multiplicity roots
// never produce a sign change.
func TestSquareFactorConstruction(t *testing.T) {
	// p = (x+1) * (x-1)^2 * (x-3/2)^2 * (x-2)^3
	p := prod(linPow("-1", 1), linPow("1", 2), linPow("3/2", 2), linPow("2", 3))
	res, err := FindRoots(p, rat("-2"), rat("5"), rat("1/1024"), -1)
	if err != nil {
		t.Fatal(err)
	}
	checkRoots(t, res, rat("1/1024"), []wantRoot{
		{"-1", 1}, {"1", 2}, {"3/2", 2}, {"2", 3},
	})
}

// TestEvenMultiplicityNotDetectableBySampling proves the point of the
// design: p = (x-2)^2 is >= 0 everywhere, so no sampled sign change ever
// reveals the root, yet the Sturm-based count isolates it.
func TestEvenMultiplicityNotDetectableBySampling(t *testing.T) {
	p := linPow("2", 2)
	// Dense sampling never sees a sign change.
	for i := 0; i <= 400; i++ {
		x := big.NewRat(int64(i), 100) // 0.00, 0.01, ..., 4.00
		if p.Eval(x).Sign() < 0 {
			t.Fatalf("p(%s) < 0; test premise broken", x.RatString())
		}
	}
	res, err := FindRoots(p, rat("0"), rat("4"), rat("1/1000"), -1)
	if err != nil {
		t.Fatal(err)
	}
	checkRoots(t, res, rat("1/1000"), []wantRoot{{"2", 2}})
}

// TestCloseRootsNeedSubdivision places two distinct roots 1/1024 apart;
// the solver must subdivide to separate them.
func TestCloseRootsNeedSubdivision(t *testing.T) {
	p := prod(lin("1"), lin("1025/1024"))
	res, err := FindRoots(p, rat("0"), rat("3"), rat("1/4096"), -1)
	if err != nil {
		t.Fatal(err)
	}
	checkRoots(t, res, rat("1/4096"), []wantRoot{{"1", 1}, {"1025/1024", 1}})
}

// TestMultiplicityChain checks multiplicities 1 through 4 in one polynomial.
func TestMultiplicityChain(t *testing.T) {
	p := prod(linPow("1", 1), linPow("2", 2), linPow("3", 3), linPow("4", 4))
	res, err := FindRoots(p, rat("0"), rat("5"), rat("1/256"), -1)
	if err != nil {
		t.Fatal(err)
	}
	checkRoots(t, res, rat("1/256"), []wantRoot{
		{"1", 1}, {"2", 2}, {"3", 3}, {"4", 4},
	})
}

// TestEndpointRoots: roots exactly on the interval boundary must be found.
func TestEndpointRoots(t *testing.T) {
	p := prod(lin("1"), lin("3"))
	res, err := FindRoots(p, rat("1"), rat("3"), rat("1/100"), -1)
	if err != nil {
		t.Fatal(err)
	}
	checkRoots(t, res, rat("1/100"), []wantRoot{{"1", 1}, {"3", 1}})
	// The left endpoint must be certified exactly.
	if res.Roots[0].Lo.Cmp(res.Roots[0].Hi) != 0 {
		t.Errorf("endpoint root not exact: [%s, %s]",
			res.Roots[0].Lo.RatString(), res.Roots[0].Hi.RatString())
	}
}

// TestRationalRootHitByBisection: a dyadic root is hit exactly by a
// bisection midpoint and must come out as a point interval.
func TestRationalRootHitByBisection(t *testing.T) {
	p := lin("1/2")
	res, err := FindRoots(p, rat("0"), rat("1"), rat("1/1000"), -1)
	if err != nil {
		t.Fatal(err)
	}
	checkRoots(t, res, rat("1/1000"), []wantRoot{{"1/2", 1}})
	if res.Roots[0].Width().Sign() != 0 {
		t.Errorf("expected exact point root, got width %s", res.Roots[0].Width().RatString())
	}
}

// TestBudgetExhausted: with a tiny budget the solver must stop, report the
// unfinished intervals, and still account for every root.
func TestBudgetExhausted(t *testing.T) {
	p := prod(lin("1"), lin("2"), lin("3"), lin("4"), lin("5"))
	res, err := FindRoots(p, rat("0"), rat("6"), rat("1/1000"), 3)
	if err != nil {
		t.Fatal(err)
	}
	if res.Complete() {
		t.Fatal("expected incomplete result with budget 3")
	}
	accounted := len(res.Roots)
	for _, g := range res.Incomplete {
		accounted += g.Roots
		if g.Roots <= 0 {
			t.Errorf("gap [%s, %s] claims %d roots", g.Lo.RatString(), g.Hi.RatString(), g.Roots)
		}
	}
	if accounted != 5 {
		t.Errorf("roots accounted for = %d, want 5 (found %d, gaps %+v)",
			accounted, len(res.Roots), res.Incomplete)
	}
}

// TestBudgetEventuallySuffices: rerunning with a generous budget finishes
// what the small budget could not.
func TestBudgetEventuallySuffices(t *testing.T) {
	p := prod(lin("1"), lin("2"), lin("3"), lin("4"), lin("5"))
	res, err := FindRoots(p, rat("0"), rat("6"), rat("1/1000"), 1000)
	if err != nil {
		t.Fatal(err)
	}
	checkRoots(t, res, rat("1/1000"), []wantRoot{
		{"1", 1}, {"2", 1}, {"3", 1}, {"4", 1}, {"5", 1},
	})
}

// TestNoRoots and degenerate inputs.
func TestDegenerate(t *testing.T) {
	res, err := FindRoots(PolyFromInts(7), rat("0"), rat("1"), rat("1/10"), -1)
	if err != nil || len(res.Roots) != 0 {
		t.Errorf("constant poly: got %+v, %v", res, err)
	}
	if _, err := FindRoots(Poly{}, rat("0"), rat("1"), rat("1/10"), -1); err == nil {
		t.Error("zero polynomial must be rejected")
	}
	if _, err := FindRoots(PolyFromInts(1, 1), rat("1"), rat("1"), rat("1/10"), -1); err == nil {
		t.Error("empty interval must be rejected")
	}
	if _, err := FindRoots(PolyFromInts(1, 1), rat("0"), rat("1"), rat("0"), -1); err == nil {
		t.Error("non-positive eps must be rejected")
	}
}

// TestIrrationalRootIsolated: x^2 - 2 has no rational root; the interval
// must still converge around sqrt(2).
func TestIrrationalRootIsolated(t *testing.T) {
	p := PolyFromStrings("-2", "0", "1")
	res, err := FindRoots(p, rat("0"), rat("2"), rat("1/1048576"), -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Roots) != 1 {
		t.Fatalf("got %+v", res)
	}
	r := res.Roots[0]
	// Exact certification: Lo^2 < 2 < Hi^2.
	two := big.NewRat(2, 1)
	lo2 := new(big.Rat).Mul(r.Lo, r.Lo)
	hi2 := new(big.Rat).Mul(r.Hi, r.Hi)
	if lo2.Cmp(two) >= 0 || hi2.Cmp(two) <= 0 {
		t.Errorf("sqrt(2) not bracketed: [%s, %s]",
			r.Lo.FloatString(12), r.Hi.FloatString(12))
	}
}

// TestSturmCountMatchesIsolation cross-checks completeness: the exact
// Sturm root count over the whole interval equals the number of isolated
// roots plus roots certified inside gaps.
func TestSturmCountMatchesIsolation(t *testing.T) {
	p := prod(linPow("1/3", 2), lin("7/2"), linPow("5", 3))
	lo, hi := rat("0"), rat("10")
	res, err := FindRoots(p, lo, hi, rat("1/512"), -1)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, f := range squareFreeFactors(p) {
		st := buildSturm(f.Q)
		n := st.countRoots(lo, hi)
		if f.Q.Eval(lo).Sign() == 0 {
			n++
		}
		total += n
	}
	if total != len(res.Roots) {
		t.Errorf("sturm count %d != isolated roots %d", total, len(res.Roots))
	}
}
