// Package matrixgame solves two-player zero-sum matrix games exactly.
//
// The row player is the maximizer, the column player the minimizer, and all
// arithmetic is done with math/big.Rat so the returned value and strategies
// are exact — never iterative approximations. Every solution carries
// certificates (ColPayoffs / RowPayoffs) that let anyone independently
// verify optimality via Verify.
package matrixgame

import (
	"errors"
	"fmt"
	"math/big"
)

// Matrix is the row player's payoff matrix with exact rational entries.
type Matrix [][]*big.Rat

// Solution is a pair of optimal mixed strategies plus the exact game value,
// together with certificates proving both bounds.
type Solution struct {
	P     []*big.Rat // row player's optimal mixed strategy (len m)
	Q     []*big.Rat // column player's optimal mixed strategy (len n)
	Value *big.Rat   // exact value of the game

	// ColPayoffs[j] = sum_i P[i]*A[i][j]. Every entry is >= Value, which
	// certifies the row player can guarantee Value (lower bound).
	ColPayoffs []*big.Rat
	// RowPayoffs[i] = sum_j A[i][j]*Q[j]. Every entry is <= Value, which
	// certifies the column player can hold the row player to Value
	// (upper bound).
	RowPayoffs []*big.Rat
}

// Solve computes an exact solution of the zero-sum game with payoff matrix a.
//
// It enumerates supports (Shapley–Snow kernels): for every square submatrix
// it solves the two equality systems that make the support rows/columns
// indifferent, then checks the candidate globally and exactly. Every matrix
// game has at least one such kernel, so the enumeration is complete; the
// first candidate that passes the global exact checks is returned. Pure
// saddle points are the k=1 case and need no special handling.
func Solve(a Matrix) (*Solution, error) {
	m, n, err := dims(a)
	if err != nil {
		return nil, err
	}
	limit := m
	if n < limit {
		limit = n
	}
	for k := 1; k <= limit; k++ {
		for _, rows := range combinations(m, k) {
			for _, cols := range combinations(n, k) {
				if sol, ok := tryKernel(a, rows, cols); ok {
					return sol, nil
				}
			}
		}
	}
	// Unreachable: Shapley–Snow guarantees a nonsingular kernel exists.
	return nil, errors.New("matrixgame: no kernel found")
}

// Verify independently checks that s is an exact optimal solution of the
// game with payoff matrix a. It recomputes everything from (a, s.P, s.Q,
// s.Value) and confirms:
//
//   - P and Q are probability distributions (nonnegative, sum to 1);
//   - every column pays at least Value against P (lower-bound proof);
//   - every row pays at most Value against Q (upper-bound proof);
//   - the stated certificates match the recomputed ones.
//
// Since both bounds coincide at Value, optimality is proven — no trust in
// the solver's internals is required.
func Verify(a Matrix, s *Solution) error {
	m, n, err := dims(a)
	if err != nil {
		return err
	}
	if s == nil || s.Value == nil {
		return errors.New("matrixgame: nil solution")
	}
	if len(s.P) != m || len(s.Q) != n {
		return fmt.Errorf("matrixgame: dimension mismatch: P has %d entries (want %d), Q has %d (want %d)", len(s.P), m, len(s.Q), n)
	}
	if len(s.ColPayoffs) != n || len(s.RowPayoffs) != m {
		return errors.New("matrixgame: certificate vectors have wrong length")
	}
	if err := checkDistribution(s.P, "row"); err != nil {
		return err
	}
	if err := checkDistribution(s.Q, "column"); err != nil {
		return err
	}
	for j := 0; j < n; j++ {
		c := new(big.Rat)
		for i := 0; i < m; i++ {
			c.Add(c, new(big.Rat).Mul(s.P[i], a[i][j]))
		}
		if c.Cmp(s.Value) < 0 {
			return fmt.Errorf("matrixgame: lower bound fails: column %d pays %s < value %s", j, c.RatString(), s.Value.RatString())
		}
		if s.ColPayoffs[j].Cmp(c) != 0 {
			return fmt.Errorf("matrixgame: stated ColPayoffs[%d]=%s differs from recomputed %s", j, s.ColPayoffs[j].RatString(), c.RatString())
		}
	}
	for i := 0; i < m; i++ {
		r := dot(a[i], s.Q)
		if r.Cmp(s.Value) > 0 {
			return fmt.Errorf("matrixgame: upper bound fails: row %d pays %s > value %s", i, r.RatString(), s.Value.RatString())
		}
		if s.RowPayoffs[i].Cmp(r) != 0 {
			return fmt.Errorf("matrixgame: stated RowPayoffs[%d]=%s differs from recomputed %s", i, s.RowPayoffs[i].RatString(), r.RatString())
		}
	}
	return nil
}

// tryKernel solves the equality systems determined by the square submatrix
// on the given rows and columns, and returns a full solution iff the
// candidate is feasible and globally optimal (checked exactly).
func tryKernel(a Matrix, rows, cols []int) (*Solution, bool) {
	m, n := len(a), len(a[0])
	k := len(rows)

	// Column player mixes over `cols`; every row in `rows` must pay exactly v.
	// Unknowns: q[0..k-1], v.
	coeff := make([][]*big.Rat, k+1)
	rhs := make([]*big.Rat, k+1)
	for i := 0; i < k; i++ {
		coeff[i] = make([]*big.Rat, k+1)
		for j := 0; j < k; j++ {
			coeff[i][j] = a[rows[i]][cols[j]]
		}
		coeff[i][k] = big.NewRat(-1, 1)
		rhs[i] = new(big.Rat)
	}
	coeff[k] = make([]*big.Rat, k+1)
	for j := 0; j < k; j++ {
		coeff[k][j] = big.NewRat(1, 1)
	}
	coeff[k][k] = new(big.Rat)
	rhs[k] = big.NewRat(1, 1)

	qsol := solveLinear(coeff, rhs)
	if qsol == nil {
		return nil, false
	}
	value := qsol[k]

	// Row player mixes over `rows`; every column in `cols` must pay exactly v.
	// Unknowns: p[0..k-1], v.
	pc := make([][]*big.Rat, k+1)
	pr := make([]*big.Rat, k+1)
	for j := 0; j < k; j++ {
		pc[j] = make([]*big.Rat, k+1)
		for i := 0; i < k; i++ {
			pc[j][i] = a[rows[i]][cols[j]]
		}
		pc[j][k] = big.NewRat(-1, 1)
		pr[j] = new(big.Rat)
	}
	pc[k] = make([]*big.Rat, k+1)
	for i := 0; i < k; i++ {
		pc[k][i] = big.NewRat(1, 1)
	}
	pc[k][k] = new(big.Rat)
	pr[k] = big.NewRat(1, 1)

	psol := solveLinear(pc, pr)
	if psol == nil || psol[k].Cmp(value) != 0 {
		return nil, false
	}

	// Support probabilities must be nonnegative.
	for _, x := range qsol[:k] {
		if x.Sign() < 0 {
			return nil, false
		}
	}
	for _, x := range psol[:k] {
		if x.Sign() < 0 {
			return nil, false
		}
	}

	// Lift to full-length strategies, zero outside the support.
	p := make([]*big.Rat, m)
	for i := range p {
		p[i] = new(big.Rat)
	}
	for idx, i := range rows {
		p[i].Set(psol[idx])
	}
	q := make([]*big.Rat, n)
	for j := range q {
		q[j] = new(big.Rat)
	}
	for idx, j := range cols {
		q[j].Set(qsol[idx])
	}

	// Global optimality, checked exactly over all rows and columns.
	rowPayoffs := make([]*big.Rat, m)
	for i := 0; i < m; i++ {
		rowPayoffs[i] = dot(a[i], q)
		if rowPayoffs[i].Cmp(value) > 0 {
			return nil, false
		}
	}
	colPayoffs := make([]*big.Rat, n)
	for j := 0; j < n; j++ {
		colPayoffs[j] = new(big.Rat)
		for i := 0; i < m; i++ {
			colPayoffs[j].Add(colPayoffs[j], new(big.Rat).Mul(p[i], a[i][j]))
		}
		if colPayoffs[j].Cmp(value) < 0 {
			return nil, false
		}
	}

	return &Solution{
		P:          p,
		Q:          q,
		Value:      new(big.Rat).Set(value),
		ColPayoffs: colPayoffs,
		RowPayoffs: rowPayoffs,
	}, true
}

// solveLinear solves an n×n system exactly by Gauss–Jordan elimination over
// big.Rat. It returns nil if the system is singular.
func solveLinear(coeff [][]*big.Rat, rhs []*big.Rat) []*big.Rat {
	n := len(coeff)
	aug := make([][]*big.Rat, n)
	for i := range aug {
		aug[i] = make([]*big.Rat, n+1)
		for j := 0; j < n; j++ {
			aug[i][j] = new(big.Rat).Set(coeff[i][j])
		}
		aug[i][n] = new(big.Rat).Set(rhs[i])
	}
	for col := 0; col < n; col++ {
		piv := -1
		for r := col; r < n; r++ {
			if aug[r][col].Sign() != 0 {
				piv = r
				break
			}
		}
		if piv < 0 {
			return nil
		}
		aug[col], aug[piv] = aug[piv], aug[col]
		pv := new(big.Rat).Set(aug[col][col])
		for j := col; j <= n; j++ {
			aug[col][j].Quo(aug[col][j], pv)
		}
		for r := 0; r < n; r++ {
			if r == col {
				continue
			}
			f := new(big.Rat).Set(aug[r][col])
			if f.Sign() == 0 {
				continue
			}
			for j := col; j <= n; j++ {
				aug[r][j].Sub(aug[r][j], new(big.Rat).Mul(f, aug[col][j]))
			}
		}
	}
	x := make([]*big.Rat, n)
	for i := range x {
		x[i] = aug[i][n]
	}
	return x
}

// combinations returns all k-element subsets of {0,...,n-1} in
// lexicographic order.
func combinations(n, k int) [][]int {
	var out [][]int
	cur := make([]int, k)
	var rec func(start, pos int)
	rec = func(start, pos int) {
		if pos == k {
			cp := make([]int, k)
			copy(cp, cur)
			out = append(out, cp)
			return
		}
		for i := start; i <= n-(k-pos); i++ {
			cur[pos] = i
			rec(i+1, pos+1)
		}
	}
	rec(0, 0)
	return out
}

func dims(a Matrix) (m, n int, err error) {
	if len(a) == 0 || len(a[0]) == 0 {
		return 0, 0, errors.New("matrixgame: empty matrix")
	}
	n = len(a[0])
	for i, row := range a {
		if len(row) != n {
			return 0, 0, fmt.Errorf("matrixgame: row %d has length %d, want %d", i, len(row), n)
		}
		for j, x := range row {
			if x == nil {
				return 0, 0, fmt.Errorf("matrixgame: nil entry at (%d,%d)", i, j)
			}
		}
	}
	return len(a), n, nil
}

func checkDistribution(x []*big.Rat, who string) error {
	sum := new(big.Rat)
	for i, p := range x {
		if p == nil {
			return fmt.Errorf("matrixgame: %s strategy has nil entry %d", who, i)
		}
		if p.Sign() < 0 {
			return fmt.Errorf("matrixgame: %s strategy has negative probability %s at index %d", who, p.RatString(), i)
		}
		sum.Add(sum, p)
	}
	if sum.Cmp(big.NewRat(1, 1)) != 0 {
		return fmt.Errorf("matrixgame: %s strategy probabilities sum to %s, not 1", who, sum.RatString())
	}
	return nil
}

func dot(row, x []*big.Rat) *big.Rat {
	acc := new(big.Rat)
	for j := range row {
		acc.Add(acc, new(big.Rat).Mul(row[j], x[j]))
	}
	return acc
}
