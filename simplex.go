package zerosum

import "math/big"

// Solve computes an exact optimal solution of the game with the simplex
// method over rational numbers. Bland's pivoting rule guarantees
// termination, and because every pivot is exact rational arithmetic the
// result is the exact game value, not an iterative approximation.
//
// The game is first shifted by a constant so every payoff is positive
// (this only shifts the value, never the optimal strategies), then the
// column player's LP
//
//	maximize   sum_j y'_j
//	subject to sum_j (A[i][j]+shift) * y'_j <= 1   for all i
//	           y' >= 0
//
// is solved. With v' = 1 / max sum y'_j, the optimal mixed strategies are
// y = v' * y' for the column player and x = v' * u for the row player,
// where u is the optimal dual solution read off the final tableau.
func (g *Game) Solve() *Solution {
	m, n := g.M, g.N

	// Shift so every entry is >= 1. Handles all-negative matrices and
	// guarantees the shifted value is positive, so the LP scaling is valid.
	minA := new(big.Rat).Set(g.A[0][0])
	for i := 0; i < m; i++ {
		for j := 0; j < n; j++ {
			if g.A[i][j].Cmp(minA) < 0 {
				minA.Set(g.A[i][j])
			}
		}
	}
	one := big.NewRat(1, 1)
	shift := new(big.Rat)
	if minA.Cmp(one) < 0 {
		shift.Sub(one, minA)
	}

	// Tableau with m constraint rows and one objective row.
	// Columns: n structural variables, m slack variables, 1 RHS.
	width := n + m + 1
	rhs := width - 1
	tab := make([][]*big.Rat, m+1)
	for i := range tab {
		tab[i] = make([]*big.Rat, width)
		for j := range tab[i] {
			tab[i][j] = new(big.Rat)
		}
	}
	for i := 0; i < m; i++ {
		for j := 0; j < n; j++ {
			tab[i][j].Add(g.A[i][j], shift)
		}
		tab[i][n+i].SetInt64(1) // slack
		tab[i][rhs].SetInt64(1)
	}
	for j := 0; j < n; j++ {
		tab[m][j].SetInt64(-1) // maximize sum y'  <=>  z - sum y' = 0
	}
	basis := make([]int, m)
	for i := range basis {
		basis[i] = n + i // slack variables form the initial basis
	}

	for {
		// Bland's rule: smallest-indexed column with negative reduced cost.
		enter := -1
		for j := 0; j < n+m; j++ {
			if tab[m][j].Sign() < 0 {
				enter = j
				break
			}
		}
		if enter < 0 {
			break // optimal
		}
		// Minimum ratio test; ties broken by smallest basic variable (Bland).
		leave := -1
		var ratio *big.Rat
		for i := 0; i < m; i++ {
			if tab[i][enter].Sign() <= 0 {
				continue
			}
			r := new(big.Rat).Quo(tab[i][rhs], tab[i][enter])
			if leave < 0 || r.Cmp(ratio) < 0 ||
				(r.Cmp(ratio) == 0 && basis[i] < basis[leave]) {
				leave, ratio = i, r
			}
		}
		if leave < 0 {
			// Impossible here: the all-ones RHS and positive shifted
			// matrix keep the LP bounded.
			panic("zerosum: unbounded LP")
		}
		pivot(tab, leave, enter)
		basis[leave] = enter
	}

	// v' = 1 / (optimal objective). The shifted matrix is strictly
	// positive, so v' >= 1/maxEntry > 0 and the objective is non-zero.
	obj := new(big.Rat).Set(tab[m][rhs])
	if obj.Sign() <= 0 {
		panic("zerosum: non-positive LP objective")
	}
	vPrime := new(big.Rat).Inv(obj)

	// Column player's strategy from the primal solution.
	y := make([]*big.Rat, n)
	for j := range y {
		y[j] = new(big.Rat)
	}
	for i := 0; i < m; i++ {
		if basis[i] < n {
			y[basis[i]].Mul(vPrime, tab[i][rhs])
		}
	}
	// Row player's strategy from the dual: optimal dual variables are the
	// objective-row coefficients of the slack columns.
	x := make([]*big.Rat, m)
	for i := 0; i < m; i++ {
		x[i] = new(big.Rat).Mul(vPrime, tab[m][n+i])
	}

	value := new(big.Rat).Sub(vPrime, shift)
	cert, ok := g.Verify(x, y, value)
	if !ok {
		panic("zerosum: internal error, simplex solution failed verification")
	}
	return &Solution{Value: value, Row: x, Col: y, Cert: cert}
}

// pivot performs a Gauss-Jordan pivot on tab[row][col] in place.
func pivot(tab [][]*big.Rat, row, col int) {
	piv := new(big.Rat).Set(tab[row][col])
	inv := new(big.Rat).Inv(piv)
	for j := range tab[row] {
		tab[row][j].Mul(tab[row][j], inv)
	}
	for i := range tab {
		if i == row {
			continue
		}
		f := tab[i][col]
		if f.Sign() == 0 {
			continue
		}
		factor := new(big.Rat).Set(f)
		for j := range tab[i] {
			tab[i][j].Sub(tab[i][j], new(big.Rat).Mul(factor, tab[row][j]))
		}
	}
}

// solveSquare solves the square linear system M * x = b exactly.
// It returns false if M is singular.
func solveSquare(M [][]*big.Rat, b []*big.Rat) ([]*big.Rat, bool) {
	n := len(M)
	// Augmented matrix, copied.
	a := make([][]*big.Rat, n)
	for i := range a {
		a[i] = make([]*big.Rat, n+1)
		for j := 0; j < n; j++ {
			a[i][j] = new(big.Rat).Set(M[i][j])
		}
		a[i][n] = new(big.Rat).Set(b[i])
	}
	for col := 0; col < n; col++ {
		piv := -1
		for r := col; r < n; r++ {
			if a[r][col].Sign() != 0 {
				piv = r
				break
			}
		}
		if piv < 0 {
			return nil, false
		}
		a[col], a[piv] = a[piv], a[col]
		inv := new(big.Rat).Inv(a[col][col])
		for j := col; j <= n; j++ {
			a[col][j].Mul(a[col][j], inv)
		}
		for r := 0; r < n; r++ {
			if r == col || a[r][col].Sign() == 0 {
				continue
			}
			f := new(big.Rat).Set(a[r][col])
			for j := col; j <= n; j++ {
				a[r][j].Sub(a[r][j], new(big.Rat).Mul(f, a[col][j]))
			}
		}
	}
	x := make([]*big.Rat, n)
	for i := range x {
		x[i] = a[i][n]
	}
	return x, true
}
