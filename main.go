package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	p := flag.Int("p", 0, "decimal precision: keep this many digits after the decimal point")
	check := flag.Bool("check", true, "for up to 8 inputs, verify against an independent exhaustive permutation search")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: errbudget -p <digits> <rational> [rational...]\n")
		fmt.Fprintf(os.Stderr, "  each input is num/den (den a positive integer) or a plain integer, e.g. 1/2 -3/4 5\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		os.Exit(2)
	}
	terms, err := parseTerms(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	res, err := Solve(terms, *p)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	printResult(terms, res)

	if *check && len(terms) <= 8 {
		bf, err := BruteForce(terms, *p)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		if bf.Deviation.Cmp(res.Deviation) != 0 || !equalPerm(bf.Perm, res.Perm) {
			fmt.Fprintf(os.Stderr, "VERIFICATION FAILED: dp dev=%s perm=%v, brute force dev=%s perm=%v\n",
				res.Deviation, res.Perm, bf.Deviation, bf.Perm)
			os.Exit(1)
		}
		n := 1
		for i := 2; i <= len(terms); i++ {
			n *= i
		}
		fmt.Printf("verification: independent exhaustive search over %d permutations agrees (deviation %s)\n",
			n, bf.Deviation)
	}
}

func equalPerm(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func printResult(terms []Term, res *Result) {
	fmt.Printf("precision p = %d (fixed-point quantum 1e-%d, half-even rounding)\n", res.P, res.P)
	fmt.Println("inputs:")
	for i, t := range terms {
		fmt.Printf("  x[%d] = %s\n", i, termRat(t).RatString())
	}
	permStrs := make([]string, len(res.Perm))
	for i, idx := range res.Perm {
		permStrs[i] = fmt.Sprintf("%d", idx)
	}
	fmt.Printf("optimal permutation (original indices): %s\n", strings.Join(permStrs, " "))
	fmt.Println("steps:")
	for k, s := range res.Steps {
		fmt.Printf("  step %2d: input x[%d] = %s\n", k+1, s.Index, s.Input.RatString())
		fmt.Printf("           pre-rounding rational: %s\n", s.Pre.RatString())
		fmt.Printf("           rounded fixed-point:   %s\n", formatRatFixed(s.Post, res.P))
	}
	fmt.Printf("exact sum:         %s (approx %s)\n", res.Exact.RatString(), approxDecimal(res.Exact))
	fmt.Printf("final accumulator: %s\n", formatRatFixed(res.Final, res.P))
	fmt.Printf("absolute deviation: %s (approx %s)\n", res.Deviation.RatString(), approxDecimal(res.Deviation))
}
