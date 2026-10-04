// Command zgsolve solves a zero-sum matrix game exactly.
//
// Usage:
//
//	zgsolve '3 1; 1 3; 2 2'
//	zgsolve -enumerate '2 0; 0 2; 1 1'
//
// The matrix is given as rows separated by ';' and entries by spaces or
// commas. Entries may be integers or fractions like -3/4. Output is the
// exact rational game value, both optimal mixed strategies, and the
// verifiable certificate bounds.
package main

import (
	"flag"
	"fmt"
	"math/big"
	"os"
	"strings"

	"zerosum"
)

func parseMatrix(s string) ([][]*big.Rat, error) {
	var m [][]*big.Rat
	for _, row := range strings.Split(s, ";") {
		row = strings.TrimSpace(row)
		if row == "" {
			continue
		}
		var r []*big.Rat
		for _, f := range strings.FieldsFunc(row, func(c rune) bool {
			return c == ' ' || c == ',' || c == '\t'
		}) {
			v, ok := new(big.Rat).SetString(f)
			if !ok {
				return nil, fmt.Errorf("invalid rational entry %q", f)
			}
			r = append(r, v)
		}
		m = append(m, r)
	}
	if len(m) == 0 {
		return nil, fmt.Errorf("empty matrix")
	}
	return m, nil
}

func printVec(name string, v []*big.Rat) {
	parts := make([]string, len(v))
	for i, p := range v {
		parts[i] = p.RatString()
	}
	fmt.Printf("%s = [ %s ]\n", name, strings.Join(parts, "  "))
}

func main() {
	enumerate := flag.Bool("enumerate", false, "also recompute all optima by support enumeration")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintf(os.Stderr, "usage: zgsolve [-enumerate] 'a b; c d; ...'\n")
		os.Exit(2)
	}
	a, err := parseMatrix(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	g, err := zerosum.NewGame(a)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}

	sol := g.Solve()
	fmt.Println("exact value v =", sol.Value.RatString())
	printVec("row strategy x    ", sol.Row)
	printVec("column strategy y ", sol.Col)
	fmt.Println("certificate:", sol.Cert.String())

	if *enumerate {
		sols := g.EnumerateOptima()
		fmt.Printf("support enumeration found %d distinct optimal solution(s):\n", len(sols))
		for i, es := range sols {
			fmt.Printf("  %d: %s\n", i+1, zerosum.Describe(es))
		}
	}
}
