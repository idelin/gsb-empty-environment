// Command demo runs the rational Bezier extremum locator on control points
// and weights given as exact rationals (e.g. "0,1/2,3").
package main

import (
	"flag"
	"fmt"
	"math/big"
	"os"
	"strings"

	"ratbezier"
)

func parseRats(s string) ([]*big.Rat, error) {
	parts := strings.Split(s, ",")
	out := make([]*big.Rat, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		r, ok := new(big.Rat).SetString(p)
		if !ok {
			return nil, fmt.Errorf("invalid rational number %q", p)
		}
		out = append(out, r)
	}
	return out, nil
}

func ratList(rs []*big.Rat) string {
	parts := make([]string, len(rs))
	for i, r := range rs {
		parts[i] = r.RatString()
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func tString(cp ratbezier.CriticalPoint) string {
	if cp.IsExactT() {
		return "t = " + cp.T.RatString() + " (exact)"
	}
	return fmt.Sprintf("t in (%s, %s)  approx t = %s",
		cp.TLo.RatString(), cp.THi.RatString(), cp.T.RatString())
}

func main() {
	points := flag.String("points", "0,1,0", "comma-separated control point values (rationals)")
	weights := flag.String("weights", "1,2,1", "comma-separated weights (rationals)")
	flag.Parse()

	y, err := parseRats(*points)
	if err != nil {
		fmt.Fprintln(os.Stderr, "points:", err)
		os.Exit(2)
	}
	w, err := parseRats(*weights)
	if err != nil {
		fmt.Fprintln(os.Stderr, "weights:", err)
		os.Exit(2)
	}
	curve, err := ratbezier.NewCurve(y, w)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	a := curve.Analyze()

	fmt.Printf("control points y = %s\nweights        w = %s\n", ratList(curve.Y), ratList(curve.W))
	fmt.Printf("y(t) = N(t)/D(t) with\n  N(t) = %s\n  D(t) = %s\n", a.Numerator, a.Denominator)
	fmt.Printf("extremum equation P(t) = N'(t)D(t) - N(t)D'(t) = 0\n")
	fmt.Printf("  P(t) = %s   (integer coefficients)\n", ratbezier.IntPolyString(a.Derivative.ToIntPoly()))

	fmt.Println("sign-changing roots of P in [0,1]:")
	roots := ratbezier.IsolateRoots(a.Derivative)
	if len(roots) == 0 {
		fmt.Println("  (none)")
	}
	for _, r := range roots {
		if r.IsExact() {
			fmt.Printf("  t = %s (exact rational)\n", r.Exact.RatString())
		} else {
			width := new(big.Rat).Sub(r.Hi, r.Lo)
			f, _ := r.Approx().Float64()
			fmt.Printf("  t in (%s, %s)  width <= %s, approx t = %.10g\n",
				r.Lo.RatString(), r.Hi.RatString(), width.RatString(), f)
		}
	}

	fmt.Println("monotonic segments:")
	for _, s := range a.Segments {
		fmt.Printf("  [%s, %s]  %s\n", s.From.RatString(), s.To.RatString(), s.Dir)
	}

	fmt.Println("critical points (endpoints included):")
	for _, cp := range a.Critical {
		tags := cp.Kind
		if cp.Global != "" {
			tags += ", " + cp.Global
		}
		fmt.Printf("  %-45s y = %s   [%s]\n", tString(cp), cp.Y.RatString(), tags)
	}

	maxY, maxPts := a.Max()
	minY, minPts := a.Min()
	fmt.Printf("GLOBAL MAX: y = %s at %s\n", maxY.RatString(), joinT(maxPts))
	fmt.Printf("GLOBAL MIN: y = %s at %s\n", minY.RatString(), joinT(minPts))
}

func joinT(pts []ratbezier.CriticalPoint) string {
	parts := make([]string, len(pts))
	for i, cp := range pts {
		if cp.IsExactT() {
			parts[i] = "t = " + cp.T.RatString()
		} else {
			f, _ := cp.T.Float64()
			parts[i] = fmt.Sprintf("t in (%s, %s) (approx %.10g)", cp.TLo.RatString(), cp.THi.RatString(), f)
		}
	}
	return strings.Join(parts, " and ")
}
