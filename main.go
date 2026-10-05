package main

import (
	"fmt"
	"math/big"
	"os"
)

const usage = `usage: realroots <c_n> <c_{n-1}> ... <c_0> <a> <b>

Counts and isolates the distinct real roots of the integer-coefficient
polynomial p(x) = c_n x^n + ... + c_0 in the rational open interval (a, b).

  c_i   integer coefficients, highest degree first
  a, b  rational endpoints (integer or fraction like -3/2)

example: realroots 1 0 -2 0 2     # x^2 - 2 on (0, 2), isolates sqrt(2)
`

// rs formats a rational without a "/1" denominator.
func rs(r *big.Rat) string { return r.RatString() }

func parseRat(s string) (*big.Rat, error) {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, fmt.Errorf("invalid rational number %q", s)
	}
	return r, nil
}

func main() {
	args := os.Args[1:]
	if len(args) < 3 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	a, err := parseRat(args[len(args)-2])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	b, err := parseRat(args[len(args)-1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	// Coefficients are given highest degree first; stored lowest first.
	n := len(args) - 2
	coeffs := make([]*big.Rat, n)
	for i, s := range args[:n] {
		c, err := parseRat(s)
		if err != nil || !c.IsInt() {
			fmt.Fprintf(os.Stderr, "coefficient %q is not an integer\n", s)
			os.Exit(2)
		}
		coeffs[n-1-i] = c
	}
	p := NewPoly(coeffs...)

	rep, err := Analyze(p, a, b)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	fmt.Printf("polynomial:        p(x) = %s\n", rep.Original)
	if rep.SquareFree.Degree() != rep.Original.Degree() {
		fmt.Printf("square-free part:  q(x) = %s   (multiplicities removed; roots unchanged)\n", rep.SquareFree)
	} else {
		fmt.Println("square-free part:  p is already square-free")
	}
	fmt.Printf("open interval:     (%s, %s)\n", rs(rep.A), rs(rep.B))
	if rep.RootAtA {
		fmt.Printf("endpoint root:     x = %s is a root (excluded from the open interval)\n", rs(rep.A))
	}
	if rep.RootAtB {
		fmt.Printf("endpoint root:     x = %s is a root (excluded from the open interval)\n", rs(rep.B))
	}
	fmt.Printf("distinct real roots in (%s, %s): %d\n", rs(rep.A), rs(rep.B), rep.Count)
	for i, ev := range rep.Roots {
		if ev.Exact != nil {
			fmt.Printf("  root %d: x = %s exactly (rational)\n", i+1, rs(ev.Exact))
			continue
		}
		fmt.Printf("  root %d: x in (%s, %s)\n", i+1, rs(ev.Lower), rs(ev.Upper))
		fmt.Printf("    sign evidence: q(%s) = %s, q(%s) = %s (opposite signs)\n",
			rs(ev.Lower), rs(ev.LowerVal), rs(ev.Upper), rs(ev.UpperVal))
	}
}
