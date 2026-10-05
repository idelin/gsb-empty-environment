// Command ilse solves integer linear systems A x = b over the integers.
//
// Input on stdin:
//
//	m n
//	a_11 a_12 ... a_1n
//	...
//	a_m1 ... a_mn
//	b_1 b_2 ... b_m
//
// Output: a particular integer solution and a basis of the homogeneous
// solution lattice, or an infeasibility certificate; each outcome is
// independently verified before printing.
package main

import (
	"bufio"
	"fmt"
	"math/big"
	"os"
	"strings"

	"ilse"
)

func main() {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	tokens := []string{}
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		tokens = append(tokens, strings.Fields(line)...)
	}
	if len(tokens) < 2 {
		fmt.Fprintln(os.Stderr, "usage: ilse <<EOF\nm n\n<matrix rows>\n<b entries>\nEOF")
		os.Exit(2)
	}
	pos := 0
	next := func() *big.Int {
		v, ok := new(big.Int).SetString(tokens[pos], 10)
		if !ok {
			fmt.Fprintf(os.Stderr, "bad integer: %q\n", tokens[pos])
			os.Exit(2)
		}
		pos++
		return v
	}
	m := int(next().Int64())
	n := int(next().Int64())
	if len(tokens) != 2+m*n+m {
		fmt.Fprintf(os.Stderr, "expected %d numbers, got %d\n", 2+m*n+m, len(tokens))
		os.Exit(2)
	}
	A := ilse.NewMat(m, n)
	for i := 0; i < m; i++ {
		for j := 0; j < n; j++ {
			A.Data[i*n+j] = next()
		}
	}
	b := make([]*big.Int, m)
	for i := range b {
		b[i] = next()
	}

	sol, inf := ilse.Solve(A, b)
	if inf != nil {
		if err := ilse.VerifyInfeasible(A, b, inf); err != nil {
			fmt.Fprintln(os.Stderr, "internal error: invalid certificate:", err)
			os.Exit(1)
		}
		fmt.Println("INFEASIBLE")
		fmt.Println("certificate c (c*A has integer entries, c*b not in (c*A)Z^n):")
		for i, c := range inf.C {
			if c.Sign() != 0 {
				fmt.Printf("  c[%d] = %s\n", i, c.RatString())
			}
		}
		return
	}
	if err := ilse.VerifySolution(A, b, sol); err != nil {
		fmt.Fprintln(os.Stderr, "internal error: invalid solution:", err)
		os.Exit(1)
	}
	fmt.Println("FEASIBLE  (verified)")
	fmt.Println("particular solution x0:")
	fmt.Print("  ")
	for _, v := range sol.X0 {
		fmt.Printf("%s ", v)
	}
	fmt.Println()
	fmt.Printf("homogeneous lattice basis B (%d vectors, columns):\n", sol.B.Cols)
	for i := 0; i < sol.B.Rows; i++ {
		fmt.Print("  ")
		for j := 0; j < sol.B.Cols; j++ {
			fmt.Printf("%s ", sol.B.At(i, j))
		}
		fmt.Println()
	}
	fmt.Println("general integer solution: x = x0 + B*z, z in Z^" + fmt.Sprint(sol.B.Cols))
}
