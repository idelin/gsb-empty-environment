package main

import (
	"math"
	"testing"
)

// Row exchange: the leading pivot is exactly zero, so elimination without
// pivoting would fail immediately. The solver must swap rows and still
// recover the exact solution.
func TestRowExchange(t *testing.T) {
	a := [][]float64{{0, 2, 1}, {1, -2, -3}, {3, 1, 4}}
	xWant := []float64{1, 2, 3}
	b := ExactMatVec(a, xWant)

	exact, ok := ExactSolve(a, b)
	if !ok {
		t.Fatal("reference says matrix is singular")
	}
	rep := SolveWithDiagnostics(a, b, false)
	if rep.Singular || rep.NearSingular {
		t.Fatalf("unexpected singularity flag: %+v", rep)
	}
	if got := RelErrorInf(rep.X, exact); got > 1e-14 {
		t.Errorf("relative error %g too large", got)
	}
	if rep.BackwardError > 1e-15 {
		t.Errorf("backward error %g too large", rep.BackwardError)
	}
	// A row swap must actually have happened (perm != identity is
	// internal, so verify via the factorization directly).
	lu := Factorize(a)
	identity := true
	for i, p := range lu.perm {
		if p != i {
			identity = false
		}
	}
	if identity {
		t.Error("expected at least one row exchange, permutation is identity")
	}
}

// Scale disparity: rows/columns differ by 1e20. Plain partial pivoting
// loses x1 entirely; diagonal equilibration plus the map-back x = Dc*y
// recovers it.
func TestScaleDisparity(t *testing.T) {
	a := [][]float64{{1, 1e20}, {1, 1}}
	b := []float64{1e20, 2} // A*[1,1] as rounded to float64

	exact, ok := ExactSolve(a, b)
	if !ok {
		t.Fatal("reference says matrix is singular")
	}

	unscaled := SolveWithDiagnostics(a, b, false)
	scaled := SolveWithDiagnostics(a, b, true)

	errU := RelErrorInf(unscaled.X, exact)
	errS := RelErrorInf(scaled.X, exact)
	t.Logf("unscaled rel err %g, scaled rel err %g", errU, errS)
	if errU < 1e-3 {
		t.Errorf("expected unscaled solve to be inaccurate, got rel err %g", errU)
	}
	if errS > 1e-14 {
		t.Errorf("scaled solve should be accurate, got rel err %g", errS)
	}
	if scaled.BackwardError > 1e-15 {
		t.Errorf("scaled backward error %g too large", scaled.BackwardError)
	}

	// The map-back must be consistent: solving the scaled system and
	// applying x = Dc*y must equal the reported solution.
	sa, sb, sc := Equilibrate(a, b)
	y := Factorize(sa).Solve(sb)
	x := sc.MapBack(y)
	for i := range x {
		if x[i] != scaled.X[i] {
			t.Errorf("map-back mismatch at %d: %g != %g", i, x[i], scaled.X[i])
		}
	}
}

// Small residual but large solution error: Hilbert(10) has cond ~ 1.6e13,
// so a backward-stable solve gives a residual near machine precision while
// the forward error is many orders of magnitude larger. The diagnostics
// must surface this via the condition estimate, not hide it.
func TestSmallResidualLargeError(t *testing.T) {
	n := 10
	a := hilbert(n)
	b := ExactMatVec(a, ones(n)) // exact solution x = ones

	exact, ok := ExactSolve(a, b)
	if !ok {
		t.Fatal("reference says matrix is singular")
	}
	rep := SolveWithDiagnostics(a, b, false)
	if rep.Singular {
		t.Fatal("Hilbert(10) is not exactly singular")
	}
	relErr := RelErrorInf(rep.X, exact)
	t.Logf("residual %g, backward %g, rel forward err %g, cond %g",
		rep.ResidualInf, rep.BackwardError, relErr, rep.Cond1)

	if rep.ResidualInf > 1e-12 {
		t.Errorf("expected tiny residual, got %g", rep.ResidualInf)
	}
	if rep.BackwardError > 1e-14 {
		t.Errorf("expected tiny backward error, got %g", rep.BackwardError)
	}
	if relErr < 1e-6 {
		t.Errorf("expected large forward error, got %g", relErr)
	}
	// The gap between forward and backward error is explained by cond:
	// forward error <=~ cond * backward error.
	if rep.Cond1 < 1e12 {
		t.Errorf("condition estimate %g should expose the ill-conditioning", rep.Cond1)
	}
	if relErr > 10*rep.Cond1*rep.BackwardError {
		t.Errorf("forward error %g exceeds cond*backward bound (%g)", relErr, rep.Cond1*rep.BackwardError)
	}
}

// Exactly singular vs near-singular must be reported differently.
func TestSingularVsNearSingular(t *testing.T) {
	singular := SolveWithDiagnostics([][]float64{{1, 2}, {2, 4}}, []float64{3, 6}, false)
	if !singular.Singular {
		t.Error("rank-deficient matrix not flagged as exactly singular")
	}
	if singular.X != nil {
		t.Error("no solution should be returned for a singular matrix")
	}

	// Near-singular: rows almost equal, cond ~ 4e15 > 1/eps.
	near := SolveWithDiagnostics([][]float64{{1, 1}, {1, 1 + 1e-15}}, []float64{2, 2 + 1e-15}, false)
	if near.Singular {
		t.Error("near-singular matrix wrongly flagged as exactly singular")
	}
	if !near.NearSingular {
		t.Error("near-singular matrix not detected")
	}
	if near.X == nil {
		t.Error("a best-effort solution should still be returned")
	}
}

// Singularity thresholds must be relative: a tiny-norm but perfectly
// conditioned matrix is neither singular nor near-singular, and a
// huge-norm one behaves the same.
func TestNoAbsoluteThreshold(t *testing.T) {
	for _, s := range []float64{1e-200, 1e200} {
		a := [][]float64{{s, 0, 0}, {0, s, 0}, {0, 0, s}}
		b := []float64{s, s, s}
		rep := SolveWithDiagnostics(a, b, false)
		if rep.Singular || rep.NearSingular {
			t.Errorf("scale %g: well-conditioned matrix flagged (singular=%v near=%v)",
				s, rep.Singular, rep.NearSingular)
		}
		for i, xi := range rep.X {
			if math.Abs(xi-1) > 1e-12 {
				t.Errorf("scale %g: x[%d] = %g, want 1", s, i, xi)
			}
		}
		if rep.BackwardError > 1e-15 {
			t.Errorf("scale %g: backward error %g", s, rep.BackwardError)
		}
	}
}

// The condition estimate should be in the right ballpark for a known case.
func TestCondEstimate(t *testing.T) {
	a := hilbert(6)
	lu := Factorize(a)
	cond := lu.CondEst1()
	// True cond_1(H6) is about 2.9e7.
	if cond < 1e7 || cond > 1e8 {
		t.Errorf("cond estimate %g outside expected range for H6", cond)
	}
}
