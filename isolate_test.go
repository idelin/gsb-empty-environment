package rootfind

import (
	"math/big"
	"testing"
)

// lin builds (x - num/den).
func lin(num, den int64) Poly { return Linear(Rat(num, den)) }

// prod multiplies a list of polynomials.
func prod(ps ...Poly) Poly {
	r := Const(Rat(1, 1))
	for _, p := range ps {
		r = r.Mul(p)
	}
	return r
}

type wantRoot struct {
	at   *big.Rat
	mult int
}

// checkResult verifies that every expected root is isolated exactly once,
// with the right multiplicity, inside an interval of width <= prec, and
// that the run completed (no unfinished intervals).
func checkResult(t *testing.T, res Result, wants []wantRoot, prec *big.Rat) {
	t.Helper()
	if !res.Complete() {
		t.Fatalf("isolation incomplete, unfinished intervals: %+v", res.Unfinished)
	}
	if len(res.Roots) != len(wants) {
		t.Fatalf("got %d roots, want %d: %v", len(res.Roots), len(wants), res.Roots)
	}
	used := make([]bool, len(res.Roots))
	for _, w := range wants {
		found := false
		for i, r := range res.Roots {
			if used[i] || !r.Contains(w.at) {
				continue
			}
			used[i] = true
			found = true
			if r.Mult != w.mult {
				t.Errorf("root %s: multiplicity %d, want %d", w.at, r.Mult, w.mult)
			}
			if r.Exact == nil {
				width := new(big.Rat).Sub(r.Hi, r.Lo)
				if width.Cmp(prec) > 0 {
					t.Errorf("root %s: interval width %s exceeds precision %s", w.at, width, prec)
				}
			}
		}
		if !found {
			t.Errorf("root %s (mult %d) not isolated", w.at, w.mult)
		}
	}
	// Isolating intervals must be pairwise disjoint.
	for i := 0; i < len(res.Roots); i++ {
		for j := i + 1; j < len(res.Roots); j++ {
			a, b := res.Roots[i], res.Roots[j]
			if a.Lo.Cmp(b.Hi) < 0 && b.Lo.Cmp(a.Hi) < 0 {
				t.Errorf("overlapping isolating intervals: (%s,%s] and (%s,%s]",
					a.Lo, a.Hi, b.Lo, b.Hi)
			}
		}
	}
}

// Even-multiplicity roots produce no sign change at all; a sampler would
// see nothing. Sturm counting must still find every one of them.
func TestEvenMultiplicityRoots(t *testing.T) {
	p := prod(lin(1, 3).Pow(2), lin(7, 2).Pow(2), lin(0, 1).Pow(4))
	prec := Rat(1, 1000)
	res := Isolate(p, Rat(0, 1), Rat(4, 1), prec, -1)
	checkResult(t, res, []wantRoot{
		{Rat(0, 1), 4},
		{Rat(1, 3), 2},
		{Rat(7, 2), 2},
	}, prec)
}

func TestMixedMultiplicities(t *testing.T) {
	p := prod(lin(-2, 1), lin(1, 1).Pow(2), lin(3, 1).Pow(3))
	prec := Rat(1, 100)
	res := Isolate(p, Rat(-3, 1), Rat(4, 1), prec, -1)
	checkResult(t, res, []wantRoot{
		{Rat(-2, 1), 1},
		{Rat(1, 1), 2},
		{Rat(3, 1), 3},
	}, prec)
}

// A root lying exactly on a dyadic bisection point is reported exactly,
// even with even multiplicity.
func TestExactRationalRootHit(t *testing.T) {
	p := prod(lin(1, 2).Pow(2), lin(3, 4))
	res := Isolate(p, Rat(0, 1), Rat(1, 1), Rat(1, 1000), -1)
	checkResult(t, res, []wantRoot{
		{Rat(1, 2), 2},
		{Rat(3, 4), 1},
	}, Rat(1, 1000))
	var exact *Root
	for i := range res.Roots {
		if res.Roots[i].Exact != nil && res.Roots[i].Exact.Cmp(Rat(1, 2)) == 0 {
			exact = &res.Roots[i]
		}
	}
	if exact == nil {
		t.Fatalf("expected exact hit on 1/2, got %v", res.Roots)
	}
}

func TestNoRoots(t *testing.T) {
	p := prod(lin(0, 1).Pow(2)).Add(Const(Rat(1, 1))) // x^2 + 1
	res := Isolate(p, Rat(-10, 1), Rat(10, 1), Rat(1, 10), -1)
	if !res.Complete() || len(res.Roots) != 0 {
		t.Fatalf("x^2+1 should have no real roots, got %+v", res)
	}
}

// Roots at the endpoints of the closed search interval must be included.
func TestRootsAtEndpoints(t *testing.T) {
	p := prod(lin(0, 1), lin(2, 1).Pow(2))
	res := Isolate(p, Rat(0, 1), Rat(2, 1), Rat(1, 100), -1)
	checkResult(t, res, []wantRoot{
		{Rat(0, 1), 1},
		{Rat(2, 1), 2},
	}, Rat(1, 100))
}

// Two distinct roots closer together than the requested precision must
// still be isolated individually; precision bounds interval width, not
// root separation.
func TestClusteredRoots(t *testing.T) {
	p := prod(lin(1, 3), lin(10003, 30000)) // distance 1/10000
	prec := Rat(1, 100)
	res := Isolate(p, Rat(0, 1), Rat(1, 1), prec, -1)
	checkResult(t, res, []wantRoot{
		{Rat(1, 3), 1},
		{Rat(10003, 30000), 1},
	}, prec)
}

// With zero budget the isolator must report the search interval as
// unfinished instead of silently dropping roots.
func TestBudgetExhaustion(t *testing.T) {
	p := prod(lin(1, 3).Pow(2), lin(1, 2), lin(3, 1))
	res := Isolate(p, Rat(0, 1), Rat(4, 1), Rat(1, 1000), 0)
	if res.Complete() {
		t.Fatalf("budget 0 must not complete, got %+v", res)
	}
	total := 0
	for _, g := range res.Unfinished {
		total += g.Roots
	}
	if total != 3 {
		t.Fatalf("unfinished intervals account for %d distinct roots, want 3", total)
	}
	// A generous budget must isolate everything.
	res = Isolate(p, Rat(0, 1), Rat(4, 1), Rat(1, 1000), 1000)
	checkResult(t, res, []wantRoot{
		{Rat(1, 3), 2},
		{Rat(1, 2), 1},
		{Rat(3, 1), 1},
	}, Rat(1, 1000))
}

// Sampling is not a completeness proof: for a polynomial whose roots all
// have even multiplicity, no amount of point sampling ever observes a sign
// change, yet the roots exist. This test documents that the isolator finds
// roots that sampling fundamentally cannot certify.
func TestSamplingWouldMissEvenRoots(t *testing.T) {
	p := prod(lin(1, 3).Pow(2), lin(2, 7).Pow(2))
	a, b := Rat(0, 1), Rat(1, 1)

	// Uniform sampling at 10001 points: no sign change, no exact zero
	// (1/3 and 2/7 never land on a 1/10000 grid point).
	const n = 10000
	prev := p.Eval(a)
	signChanges := 0
	for i := 1; i <= n; i++ {
		x := Rat(int64(i), n)
		v := p.Eval(x)
		if v.Sign() == 0 {
			t.Fatalf("test premise broken: sample hit a root exactly at %s", x)
		}
		if v.Sign() != prev.Sign() {
			signChanges++
		}
		prev = v
	}
	if signChanges != 0 {
		t.Fatalf("test premise broken: sampling saw %d sign changes", signChanges)
	}

	// Sturm-based isolation still finds both double roots.
	res := Isolate(p, a, b, Rat(1, 1000), -1)
	checkResult(t, res, []wantRoot{
		{Rat(2, 7), 2},
		{Rat(1, 3), 2},
	}, Rat(1, 1000))
}

// Square-free factorization must assign multiplicities exactly.
func TestSqFreeMultiplicities(t *testing.T) {
	p := prod(lin(1, 1).Pow(1), lin(2, 1).Pow(2), lin(3, 1).Pow(3), lin(4, 1).Pow(4))
	fs := SqFree(p)
	got := map[int]int{} // root -> multiplicity
	for _, f := range fs {
		for r := int64(1); r <= 4; r++ {
			if f.F.Eval(Rat(r, 1)).Sign() == 0 {
				got[int(r)] = f.Exp
			}
		}
	}
	want := map[int]int{1: 1, 2: 2, 3: 3, 4: 4}
	for r, m := range want {
		if got[r] != m {
			t.Errorf("root %d: multiplicity %d, want %d", r, got[r], m)
		}
	}
}
