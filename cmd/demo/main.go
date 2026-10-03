// Command demo runs the rational Bezier extremum locator on two sample
// curves: one whose critical point is an exact rational, and one whose
// critical point is irrational (isolated to 1e-30 by default).
package main

import (
	"fmt"
	"math/big"

	"ratbezier"
)

func ratStr(r *big.Rat) string {
	f, _ := new(big.Float).SetPrec(256).SetRat(r).Float64()
	if s := r.RatString(); len(s) > 40 {
		return fmt.Sprintf("%.15g (exact rational, %d-digit numerator)", f, len(r.Num().String()))
	}
	if r.IsInt() {
		return r.RatString()
	}
	return fmt.Sprintf("%s (approx %.15g)", r.RatString(), f)
}

func paramStr(p ratbezier.Param) string {
	if p.IsExact() {
		return ratStr(p.Exact)
	}
	flo, _ := new(big.Float).SetPrec(256).SetRat(p.Lo).Float64()
	fhi, _ := new(big.Float).SetPrec(256).SetRat(p.Hi).Float64()
	f, _ := new(big.Float).SetPrec(256).SetRat(p.Approx()).Float64()
	return fmt.Sprintf("irrational in [%.12g, %.12g], approx %.15g", flo, fhi, f)
}

func analyze(title string, points, weights []float64) {
	fmt.Printf("=== %s ===\n", title)
	fmt.Printf("control points: %v, weights: %v\n", points, weights)
	c, err := ratbezier.NewCurve(points, weights)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	res := c.Analyze(nil)
	fmt.Printf("derivative numerator polynomial N(t) = %s\n", res.Derivative)
	if res.Constant {
		fmt.Println("curve is constant on [0, 1]")
		fmt.Printf("global max = global min = %s\n\n", ratStr(res.Max.Value))
		return
	}
	fmt.Println("critical points in (0, 1):")
	if len(res.CriticalPoints) == 0 {
		fmt.Println("  (none)")
	}
	for _, p := range res.CriticalPoints {
		fmt.Printf("  t = %s\n", paramStr(p))
	}
	fmt.Println("monotonicity on [0, 1]:")
	for _, m := range res.Monotonicity {
		dir := "decreasing"
		if m.Increasing {
			dir = "increasing"
		}
		fmt.Printf("  [%s, %s]: %s\n", paramStr(m.From), paramStr(m.To), dir)
	}
	fmt.Println("extremum candidates (endpoints always included):")
	for _, e := range res.Candidates {
		fmt.Printf("  t = %-60s value = %-30s kind = %s\n", paramStr(e.T), ratStr(e.Value), e.Kind)
	}
	fmt.Printf("GLOBAL MAX: value %s at t = %s\n", ratStr(res.Max.Value), paramStr(res.Max.T))
	fmt.Printf("GLOBAL MIN: value %s at t = %s\n", ratStr(res.Min.Value), paramStr(res.Min.T))
	fmt.Println()
}

func main() {
	// Symmetric curve: derivative numerator 1 - 2t, exact rational root t = 1/2.
	analyze("Example 1: exact rational critical point", []float64{0, 1, 0}, []float64{1, 2, 1})

	// Asymmetric curve: derivative numerator 4 - 7t + t^2, irrational root (7 - sqrt(33))/2.
	analyze("Example 2: irrational critical point", []float64{0, 2, 1}, []float64{1, 2, 1})

	// Monotone curve: no interior critical points, extrema at endpoints.
	analyze("Example 3: monotone curve", []float64{0, 1, 2}, []float64{1, 1, 1})
}
