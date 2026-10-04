package zerosum

import (
	"fmt"
	"math/big"
	"strings"
)

// maxEnumerationSupport bounds the support sizes tried by
// EnumerateOptima. Enumeration over square submatrices is exponential and
// intended for small games; larger games should use Solve.
const maxEnumerationSupport = 8

// EnumerateOptima independently recomputes optimal solutions by support
// enumeration, following the Shapley-Snow theorem: every matrix game has
// optimal strategies whose supports form a square submatrix (a "kernel").
//
// For every k x k submatrix A[I][J] (k = 1..min(M,N)) it solves the exact
// linear systems
//
//	A[I][J] * y = v * 1,  sum y = 1   (rows of I indifferent)
//	x^T * A[I][J] = w * 1^T, sum x = 1 (columns of J indifferent)
//
// in rational arithmetic. A candidate is kept only if v == w, the
// strategies are non-negative, and the full-game optimality conditions
// hold (verified via Verify). Because solutions are re-checked against
// the whole matrix, duplicate rows/columns and degenerate supports are
// handled soundly; distinct optimal strategies (games with multiple
// optima) are all reported, deduplicated.
//
// It is intended for small matrices: the number of candidate submatrices
// grows combinatorially, and supports larger than maxEnumerationSupport
// are not enumerated.
func (g *Game) EnumerateOptima() []*Solution {
	kmax := g.M
	if g.N < kmax {
		kmax = g.N
	}
	if kmax > maxEnumerationSupport {
		kmax = maxEnumerationSupport
	}
	var sols []*Solution
	seen := make(map[string]bool)
	for k := 1; k <= kmax; k++ {
		for _, rows := range combinations(g.M, k) {
			for _, cols := range combinations(g.N, k) {
				if sol, ok := g.solveKernel(rows, cols); ok {
					key := solutionKey(sol)
					if !seen[key] {
						seen[key] = true
						sols = append(sols, sol)
					}
				}
			}
		}
	}
	return sols
}

// solveKernel solves the candidate kernel defined by the given row and
// column supports and returns a verified solution, if the kernel yields
// one.
func (g *Game) solveKernel(rows, cols []int) (*Solution, bool) {
	k := len(rows)

	// Unknowns (y_1..y_k, v): A[I][J] y - v*1 = 0, sum y = 1.
	my := make([][]*big.Rat, k+1)
	by := make([]*big.Rat, k+1)
	for i := 0; i <= k; i++ {
		my[i] = make([]*big.Rat, k+1)
		for j := 0; j <= k; j++ {
			my[i][j] = new(big.Rat)
		}
		by[i] = new(big.Rat)
	}
	for i := 0; i < k; i++ {
		for j := 0; j < k; j++ {
			my[i][j].Set(g.A[rows[i]][cols[j]])
		}
		my[i][k].SetInt64(-1)
	}
	for j := 0; j < k; j++ {
		my[k][j].SetInt64(1)
	}
	by[k].SetInt64(1)
	solY, ok := solveSquare(my, by)
	if !ok {
		return nil, false
	}
	vy := solY[k]

	// Unknowns (x_1..x_k, w): A[I][J]^T x - w*1 = 0, sum x = 1.
	mx := make([][]*big.Rat, k+1)
	bx := make([]*big.Rat, k+1)
	for i := 0; i <= k; i++ {
		mx[i] = make([]*big.Rat, k+1)
		for j := 0; j <= k; j++ {
			mx[i][j] = new(big.Rat)
		}
		bx[i] = new(big.Rat)
	}
	for j := 0; j < k; j++ {
		for i := 0; i < k; i++ {
			mx[j][i].Set(g.A[rows[i]][cols[j]])
		}
		mx[j][k].SetInt64(-1)
	}
	for i := 0; i < k; i++ {
		mx[k][i].SetInt64(1)
	}
	bx[k].SetInt64(1)
	solX, ok := solveSquare(mx, bx)
	if !ok {
		return nil, false
	}
	wx := solX[k]

	// Both sides must agree on the value.
	if vy.Cmp(wx) != 0 {
		return nil, false
	}
	// Kernel strategies must be non-negative.
	for j := 0; j < k; j++ {
		if solY[j].Sign() < 0 || solX[j].Sign() < 0 {
			return nil, false
		}
	}

	x := make([]*big.Rat, g.M)
	for i := range x {
		x[i] = new(big.Rat)
	}
	for i, r := range rows {
		x[r].Set(solX[i])
	}
	y := make([]*big.Rat, g.N)
	for j := range y {
		y[j] = new(big.Rat)
	}
	for j, c := range cols {
		y[c].Set(solY[j])
	}

	// Re-check against the full game: rows/columns outside the support
	// must not beat the candidate value.
	cert, ok := g.Verify(x, y, vy)
	if !ok {
		return nil, false
	}
	return &Solution{Value: new(big.Rat).Set(vy), Row: x, Col: y, Cert: cert}, true
}

// combinations returns all size-k index subsets of {0..n-1}.
func combinations(n, k int) [][]int {
	var out [][]int
	cur := make([]int, 0, k)
	var rec func(start int)
	rec = func(start int) {
		if len(cur) == k {
			c := make([]int, k)
			copy(c, cur)
			out = append(out, c)
			return
		}
		for i := start; i <= n-(k-len(cur)); i++ {
			cur = append(cur, i)
			rec(i + 1)
			cur = cur[:len(cur)-1]
		}
	}
	rec(0)
	return out
}

func solutionKey(s *Solution) string {
	var b strings.Builder
	for _, v := range s.Row {
		b.WriteString(v.RatString())
		b.WriteByte(',')
	}
	b.WriteByte('|')
	for _, v := range s.Col {
		b.WriteString(v.RatString())
		b.WriteByte(',')
	}
	return b.String()
}

// Describe renders a solution with its supports for display.
func Describe(s *Solution) string {
	return fmt.Sprintf("value %s, row support %v, column support %v, %s",
		s.Value.RatString(), s.RowSupport(), s.ColSupport(), s.Cert.String())
}
