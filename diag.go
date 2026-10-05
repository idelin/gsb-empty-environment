package main

import (
	"fmt"
	"math"
	"strings"
)

// Report bundles the numerical diagnostics for one solve of A*x = b.
type Report struct {
	X []float64 // computed solution (nil if A is exactly singular)
	N int       // dimension of the system

	ResidualInf      float64 // ||b - A*x||_inf, on the ORIGINAL system
	BackwardError    float64 // componentwise (Oettli-Prager) backward error
	NormwiseBackward float64 // normwise backward error

	Cond1 float64 // estimated 1-norm condition number of the matrix solved
	RCond float64 // reciprocal condition estimate, 1/Cond1

	Singular      bool    // exactly singular (zero pivot encountered)
	NearSingular  bool    // singular to working precision (relative criteria)
	MinPivotRatio float64 // smallest pivot magnitude relative to its row max
	Growth        float64 // pivot growth factor

	Scaled bool // whether diagonal equilibration was used
}

// Residual returns r = b - A*x.
func Residual(a [][]float64, x, b []float64) []float64 {
	n := len(a)
	r := make([]float64, n)
	for i := 0; i < n; i++ {
		s := b[i]
		for j := 0; j < n; j++ {
			s -= a[i][j] * x[j]
		}
		r[i] = s
	}
	return r
}

// ComponentwiseBackwardError computes the Oettli-Prager backward error
//
//	eta = max_i |r_i| / (|A|*|x| + |b|)_i
//
// the smallest perturbation of the DATA (A and b, componentwise) that makes
// x an exact solution. This is the right yardstick: a tiny residual alone
// does not imply a tiny solution error when the matrix is ill conditioned.
func ComponentwiseBackwardError(a [][]float64, x, b, r []float64) float64 {
	eta := 0.0
	for i := range a {
		denom := math.Abs(b[i])
		for j := range x {
			denom += math.Abs(a[i][j]) * math.Abs(x[j])
		}
		if denom == 0 {
			if r[i] != 0 {
				return math.Inf(1)
			}
			continue
		}
		if v := math.Abs(r[i]) / denom; v > eta {
			eta = v
		}
	}
	return eta
}

// SolveWithDiagnostics solves A*x = b (optionally with diagonal
// equilibration) and returns a full diagnostic report. Residual and
// backward error are always measured against the original system.
func SolveWithDiagnostics(a [][]float64, b []float64, useScaling bool) *Report {
	rep := &Report{Scaled: useScaling, N: len(a)}

	solveA, solveB := a, b
	var sc Scaling
	if useScaling {
		solveA, solveB, sc = Equilibrate(a, b)
	}

	lu := Factorize(solveA)
	rep.Singular = lu.Singular
	rep.MinPivotRatio = lu.MinPivotRatio
	rep.Growth = lu.Growth
	rep.Cond1 = lu.CondEst1()
	if math.IsInf(rep.Cond1, 1) {
		rep.RCond = 0
	} else {
		rep.RCond = 1 / rep.Cond1
	}
	// Near-singular is a RELATIVE judgement: either a pivot collapsed
	// relative to the matrix norm, or the reciprocal condition estimate
	// is below n*eps (singular to working precision).
	n := len(a)
	rep.NearSingular = lu.NearSingular || rep.RCond < float64(n)*machineEps

	if lu.Singular {
		return rep
	}

	y := lu.Solve(solveB)
	if useScaling {
		rep.X = sc.MapBack(y) // x = Dc * y, back in original variables
	} else {
		rep.X = y
	}

	r := Residual(a, rep.X, b)
	for _, ri := range r {
		if v := math.Abs(ri); v > rep.ResidualInf {
			rep.ResidualInf = v
		}
	}
	rep.BackwardError = ComponentwiseBackwardError(a, rep.X, b, r)
	xNorm, bNorm := 0.0, 0.0
	for _, xi := range rep.X {
		if v := math.Abs(xi); v > xNorm {
			xNorm = v
		}
	}
	for _, bi := range b {
		if v := math.Abs(bi); v > bNorm {
			bNorm = v
		}
	}
	if denom := lu.NormInf*xNorm + bNorm; denom > 0 {
		rep.NormwiseBackward = rep.ResidualInf / denom
	}
	return rep
}

func formatFloat(v float64) string {
	if math.IsInf(v, 1) {
		return "       +Inf"
	}
	return fmt.Sprintf("%12.4e", v)
}

// String renders a human-readable diagnostic summary.
func (r *Report) String() string {
	var sb strings.Builder
	status := "OK"
	if r.Singular {
		status = "SINGULAR (exact zero pivot; no solution computed)"
	} else if r.NearSingular {
		status = "NEAR-SINGULAR (singular to working precision; solution untrustworthy)"
	}
	fmt.Fprintf(&sb, "  status:              %s\n", status)
	fmt.Fprintf(&sb, "  scaled:              %v\n", r.Scaled)
	fmt.Fprintf(&sb, "  cond_1 estimate:     %s\n", formatFloat(r.Cond1))
	fmt.Fprintf(&sb, "  rcond estimate:      %s  (n*eps = %.2e)\n", formatFloat(r.RCond), float64(r.N)*machineEps)
	fmt.Fprintf(&sb, "  min pivot ratio:     %s\n", formatFloat(r.MinPivotRatio))
	fmt.Fprintf(&sb, "  pivot growth:        %s\n", formatFloat(r.Growth))
	if r.Singular {
		return sb.String()
	}
	fmt.Fprintf(&sb, "  ||residual||_inf:    %s\n", formatFloat(r.ResidualInf))
	fmt.Fprintf(&sb, "  backward err (cmp):  %s\n", formatFloat(r.BackwardError))
	fmt.Fprintf(&sb, "  backward err (nrm):  %s\n", formatFloat(r.NormwiseBackward))
	return sb.String()
}
