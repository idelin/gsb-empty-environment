package algebraic

import "testing"

func intTestPoly(coeffs ...int64) poly {
	out := make(poly, len(coeffs))
	for i, c := range coeffs {
		out[i] = ratInt(c)
	}
	return normPoly(out)
}

func factorStrings(factors []poly) []string {
	out := make([]string, len(factors))
	for i, factor := range factors {
		out[i] = polyString(factor)
	}
	return out
}

func assertFactorStrings(t *testing.T, p poly, want []string) {
	t.Helper()
	got := factorStrings(factorPolynomial(p))
	if len(got) != len(want) {
		t.Fatalf("factor count = %d (%v), want %d (%v)", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("factor %d = %s, want %s (all factors: %v)", i, got[i], want[i], got)
		}
	}
}

func TestPolynomialFactorization(t *testing.T) {
	assertFactorStrings(t,
		intTestPoly(6, 0, -5, 0, 1),
		[]string{"x^2 - 3", "x^2 - 2"},
	)
	assertFactorStrings(t,
		intTestPoly(0, -1, 0, 1),
		[]string{"x - 1", "x", "x + 1"},
	)
	assertFactorStrings(t,
		intTestPoly(1, -2, 2, -2, 1),
		[]string{"x - 1", "x^2 + 1"},
	)
}

func TestSturmRootIsolation(t *testing.T) {
	p := intTestPoly(-2, 0, 1)
	roots := isolateRealRoots(p)
	if len(roots) != 2 {
		t.Fatalf("root interval count = %d, want 2", len(roots))
	}
	for step := 0; step < 2; step++ {
		for i := range roots {
			roots[i] = refineRootInterval(p, roots[i])
		}
	}
	if roots[0].Hi.Sign() >= 0 || roots[1].Lo.Sign() <= 0 {
		t.Fatalf("roots are not separated by sign: %s, %s", roots[0], roots[1])
	}
	for _, root := range roots {
		loValue := polyEval(p, root.Lo)
		hiValue := polyEval(p, root.Hi)
		if loValue.Sign()*hiValue.Sign() != -1 {
			t.Fatalf("interval %s does not bracket a root", root)
		}
	}
}
