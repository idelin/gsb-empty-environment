package main

import (
	"fmt"
	"math/big"
	"strings"
)

// Check is one named verification check outcome.
type Check struct {
	Name     string   `json:"name"`
	OK       bool     `json:"ok"`
	Detail   string   `json:"detail,omitempty"`
	Tested   int      `json:"tested,omitempty"`
	Failures []string `json:"failures,omitempty"`
}

// VerifyReport aggregates all projection checks.
type VerifyReport struct {
	OK     bool    `json:"ok"`
	Checks []Check `json:"checks"`
}

// gridValues are the small rational sample values used for lift checks.
var gridValues = []*big.Rat{
	big.NewRat(-2, 1), big.NewRat(-1, 1), big.NewRat(-1, 2), big.NewRat(0, 1),
	big.NewRat(1, 2), big.NewRat(1, 1), big.NewRat(2, 1),
}

const gridCap = 500
const maxRecordedFailures = 10

// Verify cross-checks a projection result against the original system using
// an independent exact LP solver and small rational sample points:
//  1. feasibility match (empty-set agreement)
//  2. per-variable axis bound match (unboundedness / extreme agreement)
//  3. forward: points of the original system project into the output
//  4. backward: grid points satisfy the output iff they lift to the original
//     (the opposite inclusion)
func Verify(orig []*Constraint, numVars int, kept []int, out []*Constraint, outFeasible bool) *VerifyReport {
	rep := &VerifyReport{OK: true}
	add := func(c Check) {
		if !c.OK {
			rep.OK = false
		}
		rep.Checks = append(rep.Checks, c)
	}

	// 1. empty-set agreement
	origFeas := FeasibleMixed(orig, numVars)
	outFeas := outFeasible && FeasibleMixed(out, numVars)
	add(Check{
		Name:   "feasibility_match",
		OK:     origFeas == outFeas,
		Detail: fmt.Sprintf("original_feasible=%v projection_feasible=%v", origFeas, outFeas),
	})
	if !origFeas || !outFeas {
		// both empty (or a mismatch already flagged): remaining checks are vacuous
		return rep
	}

	// 2. axis bounds: for every kept variable, sup and inf over the closure of
	//    the original system must equal those over the closure of the output.
	{
		chk := Check{Name: "axis_bounds_match", OK: true}
		for _, v := range kept {
			for _, sign := range []int64{1, -1} {
				obj := make([]*big.Rat, numVars)
				for j := range obj {
					obj[j] = zeroRat()
				}
				obj[v] = big.NewRat(sign, 1)
				r1 := OptimizeClosure(orig, numVars, obj)
				r2 := OptimizeClosure(out, numVars, obj)
				if r1.Status != r2.Status {
					chk.OK = false
					chk.Failures = append(chk.Failures, fmt.Sprintf(
						"x%d sign=%d: original=%s projection=%s", v, sign, r1.Status, r2.Status))
				} else if r1.Status == LPOptimal && r1.Value.Cmp(r2.Value) != 0 {
					chk.OK = false
					chk.Failures = append(chk.Failures, fmt.Sprintf(
						"x%d sign=%d: original=%s projection=%s", v, sign, r1.Value.RatString(), r2.Value.RatString()))
				}
				chk.Tested++
			}
		}
		add(chk)
	}

	// 3. forward sampling: points of the original system must project into
	//    the output. Strictly feasible points (margin center) are checked
	//    against the strict output; closure boundary points (objective
	//    optima) are checked against the closure of the output.
	{
		chk := Check{Name: "forward_samples", OK: true}
		for _, sp := range samplePoints(orig, numVars) {
			chk.Tested++
			for _, c := range out {
				ok := c.closureSatisfiedAt(sp.pt)
				if sp.strict {
					ok = c.satisfiedAt(sp.pt)
				}
				if !ok {
					chk.OK = false
					if len(chk.Failures) < maxRecordedFailures {
						chk.Failures = append(chk.Failures, fmt.Sprintf(
							"point %s violates %s", formatPoint(sp.pt), c))
					}
				}
			}
		}
		add(chk)
	}

	// 4. backward grid: a kept-space point satisfies the output iff it can be
	//    lifted to a feasible point of the original system.
	{
		chk := Check{Name: "lift_grid", OK: true}
		d := len(kept)
		total := 1
		for i := 0; i < d; i++ {
			total *= len(gridValues)
		}
		stride := 1
		if total > gridCap {
			stride = (total + gridCap - 1) / gridCap
		}
		tuple := make([]int, d)
		for idx := 0; idx < total; idx += stride {
			// decode mixed-radix tuple
			rem := idx
			for i := d - 1; i >= 0; i-- {
				tuple[i] = rem % len(gridValues)
				rem /= len(gridValues)
			}
			point := make([]*big.Rat, numVars)
			for j := range point {
				point[j] = zeroRat()
			}
			for i, v := range kept {
				point[v] = gridValues[tuple[i]]
			}
			satOut := outFeasible
			if satOut {
				for _, c := range out {
					if !c.satisfiedAt(point) {
						satOut = false
						break
					}
				}
			}
			liftSys := append([]*Constraint(nil), orig...)
			for i, v := range kept {
				eq := newConstraint(numVars, Eq, nil)
				eq.Coeff[v] = big.NewRat(1, 1)
				eq.Const = new(big.Rat).Neg(gridValues[tuple[i]])
				liftSys = append(liftSys, eq)
			}
			lift := FeasibleMixed(liftSys, numVars)
			chk.Tested++
			if satOut != lift {
				chk.OK = false
				if len(chk.Failures) < maxRecordedFailures {
					chk.Failures = append(chk.Failures, fmt.Sprintf(
						"kept-point %s: satisfies_projection=%v lifts_to_original=%v",
						formatKept(point, kept), satOut, lift))
				}
			}
		}
		add(chk)
	}

	return rep
}

// sampled is a sample point; strict marks points known to lie strictly
// inside the system (vs. closure boundary points from optimization).
type sampled struct {
	pt     []*big.Rat
	strict bool
}

// samplePoints returns points of the system: the margin-maximizing
// (strictly feasible) point plus optimizers of axis and deterministic
// pseudo-random objectives over the closure.
func samplePoints(sys []*Constraint, numVars int) []sampled {
	if numVars == 0 {
		if FeasibleMixed(sys, numVars) {
			return []sampled{{pt: []*big.Rat{}, strict: true}}
		}
		return nil
	}
	var pts []sampled
	// margin center
	{
		A, b := toRows(sys, numVars, true)
		capRow := make([]*big.Rat, numVars+1)
		for j := 0; j < numVars; j++ {
			capRow[j] = zeroRat()
		}
		capRow[numVars] = big.NewRat(1, 1)
		A = append(A, capRow)
		b = append(b, big.NewRat(1, 1))
		obj := make([]*big.Rat, numVars+1)
		for j := 0; j < numVars; j++ {
			obj[j] = zeroRat()
		}
		obj[numVars] = big.NewRat(1, 1)
		if res := SolveLP(A, b, obj); res.Status == LPOptimal && res.Value.Sign() > 0 {
			pts = append(pts, sampled{pt: res.Point[:numVars], strict: true})
		}
	}
	// axis objectives
	for v := 0; v < numVars; v++ {
		for _, sign := range []int64{1, -1} {
			obj := make([]*big.Rat, numVars)
			for j := range obj {
				obj[j] = zeroRat()
			}
			obj[v] = big.NewRat(sign, 1)
			if res := OptimizeClosure(sys, numVars, obj); res.Status == LPOptimal {
				pts = append(pts, sampled{pt: res.Point})
			}
		}
	}
	// deterministic pseudo-random rational objectives
	seed := uint64(88172645463325252)
	next := func() int64 {
		seed ^= seed << 13
		seed ^= seed >> 7
		seed ^= seed << 17
		return int64(seed%5) - 2
	}
	for k := 0; k < 2*numVars+2; k++ {
		obj := make([]*big.Rat, numVars)
		allZero := true
		for j := range obj {
			c := next()
			if c != 0 {
				allZero = false
			}
			obj[j] = big.NewRat(c, 2)
		}
		if allZero {
			continue
		}
		if res := OptimizeClosure(sys, numVars, obj); res.Status == LPOptimal {
			pts = append(pts, sampled{pt: res.Point})
		}
	}
	return pts
}

func formatPoint(p []*big.Rat) string {
	parts := make([]string, len(p))
	for i, r := range p {
		parts[i] = fmt.Sprintf("x%d=%s", i, r.RatString())
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func formatKept(p []*big.Rat, kept []int) string {
	parts := make([]string, len(kept))
	for i, v := range kept {
		parts[i] = fmt.Sprintf("x%d=%s", v, p[v].RatString())
	}
	return "(" + strings.Join(parts, ", ") + ")"
}
