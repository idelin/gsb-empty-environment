// Command rootiso exactly counts and isolates the distinct real roots of an
// integer-coefficient polynomial in a rational open interval.
//
// Examples:
//
//	rootiso -c "-2,0,1" -i "0,2"        # x^2-2 on (0,2): isolates sqrt(2)
//	rootiso -c "-6,11,-6,1" -i "0,4"    # (x-1)(x-2)(x-3) on (0,4)
//	rootiso -c "-1,0,0,0,0,1" -all      # x^5-1, all real roots
package main

import (
	"flag"
	"fmt"
	"math/big"
	"os"
	"strings"

	"rootiso"
)

func parseCoeffs(s string) ([]*big.Int, error) {
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' })
	if len(fields) == 0 {
		return nil, fmt.Errorf("no coefficients given")
	}
	out := make([]*big.Int, len(fields))
	for i, f := range fields {
		v, ok := new(big.Int).SetString(f, 10)
		if !ok {
			return nil, fmt.Errorf("bad integer coefficient %q", f)
		}
		out[i] = v
	}
	return out, nil
}

func parseInterval(s string) (lo, hi *big.Rat, err error) {
	parts := strings.Split(s, ",")
	if len(parts) != 2 {
		return nil, nil, fmt.Errorf("interval must be \"lo,hi\", got %q", s)
	}
	lo, ok1 := new(big.Rat).SetString(strings.TrimSpace(parts[0]))
	hi, ok2 := new(big.Rat).SetString(strings.TrimSpace(parts[1]))
	if !ok1 || !ok2 {
		return nil, nil, fmt.Errorf("interval endpoints must be rationals like -3/2 or 5, got %q", s)
	}
	return lo, hi, nil
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "rootiso: "+format+"\n", args...)
	os.Exit(1)
}

func main() {
	coeffsFlag := flag.String("c", "", "integer coefficients, ascending powers of x, comma/space separated (e.g. \"-2,0,1\" for x^2-2)")
	intervalFlag := flag.String("i", "", "rational open interval \"lo,hi\" (e.g. \"-3/2,5\")")
	allFlag := flag.Bool("all", false, "isolate all real roots via a Cauchy bound (ignores -i)")
	flag.Parse()

	coeffs, err := parseCoeffs(*coeffsFlag)
	if err != nil {
		fatalf("%v", err)
	}

	var res *rootiso.Result
	var where string
	if *allFlag {
		res, err = rootiso.IsolateAll(coeffs)
		where = "all real roots (Cauchy-bounded interval)"
	} else {
		lo, hi, ierr := parseInterval(*intervalFlag)
		if ierr != nil {
			fatalf("%v", ierr)
		}
		res, err = rootiso.Isolate(coeffs, lo, hi)
		where = fmt.Sprintf("open interval (%s, %s)", lo.RatString(), hi.RatString())
	}
	if err != nil {
		fatalf("%v", err)
	}

	fmt.Printf("square-free part used: %s\n", res.SquareFree)
	fmt.Printf("distinct real roots in %s: %d\n", where, res.Count())

	if len(res.Points) > 0 {
		fmt.Println("exact rational roots (reported separately):")
		for _, p := range res.Points {
			fmt.Printf("  x = %s   [p(x) == 0 exactly]\n", p.RatString())
		}
	}
	if len(res.Intervals) > 0 {
		fmt.Println("isolating intervals (open, pairwise disjoint, exactly one root each):")
		for i, iv := range res.Intervals {
			loF, _ := iv.Lo.Float64()
			hiF, _ := iv.Hi.Float64()
			fmt.Printf("  %d. x in (%s, %s)\n", i+1, iv.Lo.RatString(), iv.Hi.RatString())
			fmt.Printf("     sign-change evidence: sign(sq(lo)) = %+d, sign(sq(hi)) = %+d\n", iv.SignLo, iv.SignHi)
			fmt.Printf("     approx (%.10g, %.10g)  [float64, informational only, never used as evidence]\n", loF, hiF)
		}
	}
	if res.Count() == 0 {
		fmt.Println("no real roots in the given interval (exact Sturm count is 0)")
	}
}
