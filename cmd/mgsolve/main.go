// Command mgsolve solves a two-player zero-sum matrix game exactly.
//
// Input on stdin: first line "m n", then m lines with n rational entries
// each (integers or fractions like -3/2). Output: the exact game value,
// both players' optimal mixed strategies, and the optimality certificates,
// which are re-verified independently before printing.
package main

import (
	"bufio"
	"fmt"
	"math/big"
	"os"
	"strings"

	"matrixgame"
)

func main() {
	a, err := readMatrix(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mgsolve:", err)
		os.Exit(1)
	}
	sol, err := matrixgame.Solve(a)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mgsolve:", err)
		os.Exit(1)
	}
	if err := matrixgame.Verify(a, sol); err != nil {
		fmt.Fprintln(os.Stderr, "mgsolve: internal error, solution failed verification:", err)
		os.Exit(1)
	}

	fmt.Printf("value: %s\n", sol.Value.RatString())
	fmt.Printf("row strategy p:    %s\n", formatVec(sol.P))
	fmt.Printf("column strategy q: %s\n", formatVec(sol.Q))
	fmt.Println("certificates (independently verifiable):")
	fmt.Printf("  p^T A = %s   (all >= value: row player guarantees it)\n", formatVec(sol.ColPayoffs))
	fmt.Printf("  A q   = %s   (all <= value: column player enforces it)\n", formatVec(sol.RowPayoffs))
	fmt.Println("verification: OK (exact rational arithmetic)")
}

func readMatrix(f *os.File) (matrixgame.Matrix, error) {
	sc := bufio.NewScanner(f)
	sc.Split(bufio.ScanWords)
	next := func() (string, error) {
		if !sc.Scan() {
			if err := sc.Err(); err != nil {
				return "", err
			}
			return "", fmt.Errorf("unexpected end of input")
		}
		return sc.Text(), nil
	}
	tok, err := next()
	if err != nil {
		return nil, err
	}
	var m, n int
	if _, err := fmt.Sscanf(tok, "%d", &m); err != nil {
		return nil, fmt.Errorf("bad row count %q", tok)
	}
	tok, err = next()
	if err != nil {
		return nil, err
	}
	if _, err := fmt.Sscanf(tok, "%d", &n); err != nil {
		return nil, fmt.Errorf("bad column count %q", tok)
	}
	if m <= 0 || n <= 0 {
		return nil, fmt.Errorf("dimensions must be positive, got %d x %d", m, n)
	}
	a := make(matrixgame.Matrix, m)
	for i := 0; i < m; i++ {
		a[i] = make([]*big.Rat, n)
		for j := 0; j < n; j++ {
			tok, err := next()
			if err != nil {
				return nil, fmt.Errorf("reading entry (%d,%d): %v", i, j, err)
			}
			r, ok := new(big.Rat).SetString(tok)
			if !ok {
				return nil, fmt.Errorf("entry (%d,%d): %q is not a rational number", i, j, tok)
			}
			a[i][j] = r
		}
	}
	return a, nil
}

func formatVec(v []*big.Rat) string {
	parts := make([]string, len(v))
	for i, x := range v {
		parts[i] = x.RatString()
	}
	return "(" + strings.Join(parts, ", ") + ")"
}
