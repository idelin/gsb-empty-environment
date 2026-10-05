package sparselu

import (
	"errors"
	"math/big"
)

// ErrBudgetExhausted is returned by Factorizer.Run when applying the next
// elimination update would exceed the fill-in budget. The Factorizer state
// is fully recoverable: call Run again with a larger budget to resume
// exactly where it stopped.
var ErrBudgetExhausted = errors.New("sparselu: fill-in budget exhausted")

// Factorization is the result of LU factorization: PAQ = LU, where P and Q
// are row/column permutations in index form ((PAQ)[i][j] = A[P[i]][Q[j]]),
// L is unit lower triangular m x m, and U is upper trapezoidal m x n.
type Factorization struct {
	P []int
	Q []int
	L *Matrix
	U *Matrix
	// Rank is the number of pivots found. Rank < min(m, n) means A is
	// rank deficient; the bottom rows of U are zero.
	Rank int
	// FillIn counts nonzero entries created during elimination in
	// positions that were zero in the (permuted) input.
	FillIn int
}

// Factorizer holds the mutable state of an in-progress factorization so
// that a budget-limited run can be resumed after ErrBudgetExhausted.
type Factorizer struct {
	m, n int
	work []map[int]*big.Rat // working matrix, becomes U
	l    []map[int]*big.Rat // multipliers below the diagonal
	prow []int              // prow[i] = original row now at position i
	pcol []int              // pcol[j] = original column now at position j
	k    int                // current elimination step
	i    int                // next row to eliminate within step k
	rank int
	fill int
	done bool
}

// NewFactorizer prepares a factorization of a. The input is not modified.
func NewFactorizer(a *Matrix) *Factorizer {
	f := &Factorizer{m: a.Rows, n: a.Cols}
	f.work = make([]map[int]*big.Rat, f.m)
	f.l = make([]map[int]*big.Rat, f.m)
	for r := 0; r < f.m; r++ {
		f.work[r] = make(map[int]*big.Rat, len(a.rows[r]))
		for c, v := range a.rows[r] {
			f.work[r][c] = new(big.Rat).Set(v)
		}
		f.l[r] = make(map[int]*big.Rat)
	}
	f.prow = identity(f.m)
	f.pcol = identity(f.n)
	return f
}

// Factorize runs the factorization to completion. A negative budget means
// unlimited fill-in. It returns ErrBudgetExhausted if the budget is too
// small; use NewFactorizer directly for a resumable run.
func Factorize(a *Matrix, budget int) (*Factorization, error) {
	f := NewFactorizer(a)
	if err := f.Run(budget); err != nil {
		return nil, err
	}
	return f.Result(), nil
}

// Run advances the factorization until completion or until creating another
// fill-in entry would exceed budget (negative budget = unlimited). On
// ErrBudgetExhausted the state is intact and Run may be called again with a
// larger budget; budgets are cumulative against the total fill-in.
func (f *Factorizer) Run(budget int) error {
	if f.done {
		return nil
	}
	limit := f.m
	if f.n < limit {
		limit = f.n
	}
	for f.k < limit {
		if f.i == f.k {
			pi, pj, ok := f.findPivot()
			if !ok {
				// Zero pivot with nothing to swap: the remaining
				// submatrix is entirely zero, so the rank is final.
				f.rank = f.k
				f.done = true
				return nil
			}
			f.swapRows(f.k, pi)
			f.swapCols(f.k, pj)
			f.i = f.k + 1
		}
		for f.i < f.m {
			aik, ok := f.work[f.i][f.k]
			if !ok {
				f.i++
				continue
			}
			// Entries created by this row update: pivot-row nonzeros
			// at columns j > k where row i currently has nothing.
			// (A zero there becomes -factor*U[k][j] != 0; an existing
			// entry can only shrink or cancel, never fill.)
			needed := 0
			for j := range f.work[f.k] {
				if j > f.k {
					if _, has := f.work[f.i][j]; !has {
						needed++
					}
				}
			}
			if budget >= 0 && f.fill+needed > budget {
				return ErrBudgetExhausted
			}
			f.eliminateRow(aik)
			f.fill += needed
			f.i++
		}
		f.k++
		f.i = f.k
	}
	f.rank = limit
	f.done = true
	return nil
}

// Done reports whether the factorization has completed.
func (f *Factorizer) Done() bool { return f.done }

// FillIn returns the fill-in count accumulated so far.
func (f *Factorizer) FillIn() int { return f.fill }

// findPivot returns the lexicographically smallest nonzero position in the
// remaining submatrix. Full pivoting means a zero pivot is always resolved
// by a row swap, a column swap, or both, before rank is reported.
func (f *Factorizer) findPivot() (int, int, bool) {
	for i := f.k; i < f.m; i++ {
		for j := f.k; j < f.n; j++ {
			if v, ok := f.work[i][j]; ok && v.Sign() != 0 {
				return i, j, true
			}
		}
	}
	return 0, 0, false
}

func (f *Factorizer) swapRows(a, b int) {
	if a == b {
		return
	}
	f.work[a], f.work[b] = f.work[b], f.work[a]
	f.l[a], f.l[b] = f.l[b], f.l[a]
	f.prow[a], f.prow[b] = f.prow[b], f.prow[a]
}

func (f *Factorizer) swapCols(a, b int) {
	if a == b {
		return
	}
	for _, row := range f.work {
		va, oka := row[a]
		vb, okb := row[b]
		switch {
		case oka && okb:
			row[a], row[b] = vb, va
		case oka:
			row[b] = va
			delete(row, a)
		case okb:
			row[a] = vb
			delete(row, b)
		}
	}
	f.pcol[a], f.pcol[b] = f.pcol[b], f.pcol[a]
}

// eliminateRow subtracts factor times pivot row k from row i, records the
// multiplier in L, and drops any entries that cancel to exactly zero.
func (f *Factorizer) eliminateRow(aik *big.Rat) {
	k, i := f.k, f.i
	factor := new(big.Rat).Quo(aik, f.work[k][k])
	f.l[i][k] = factor
	delete(f.work[i], k)
	term := new(big.Rat)
	for j, ukj := range f.work[k] {
		if j <= k {
			continue
		}
		term.Mul(factor, ukj)
		if cur, ok := f.work[i][j]; ok {
			cur.Sub(cur, term)
			if cur.Sign() == 0 {
				delete(f.work[i], j)
			}
		} else {
			f.work[i][j] = new(big.Rat).Neg(term)
		}
	}
}

// Result assembles the final factorization. It returns nil if the
// factorization has not completed yet.
func (f *Factorizer) Result() *Factorization {
	if !f.done {
		return nil
	}
	L := New(f.m, f.m)
	one := big.NewRat(1, 1)
	for i := 0; i < f.m; i++ {
		L.Set(i, i, one)
		for j, v := range f.l[i] {
			L.rows[i][j] = new(big.Rat).Set(v)
		}
	}
	U := New(f.m, f.n)
	for i := 0; i < f.m; i++ {
		for j, v := range f.work[i] {
			U.rows[i][j] = new(big.Rat).Set(v)
		}
	}
	return &Factorization{
		P:      append([]int(nil), f.prow...),
		Q:      append([]int(nil), f.pcol...),
		L:      L,
		U:      U,
		Rank:   f.rank,
		FillIn: f.fill,
	}
}

// Verify independently checks that PAQ = LU by applying the permutations to
// a copy of a and comparing against Mul(f.L, f.U) with exact arithmetic.
func Verify(a *Matrix, f *Factorization) bool {
	if f == nil || f.L == nil || f.U == nil {
		return false
	}
	if f.L.Rows != a.Rows || f.L.Cols != a.Rows ||
		f.U.Rows != a.Rows || f.U.Cols != a.Cols {
		return false
	}
	if len(f.P) != a.Rows || len(f.Q) != a.Cols ||
		!isPermutation(f.P) || !isPermutation(f.Q) {
		return false
	}
	paq := New(a.Rows, a.Cols)
	for i := 0; i < a.Rows; i++ {
		for j := 0; j < a.Cols; j++ {
			paq.Set(i, j, a.At(f.P[i], f.Q[j]))
		}
	}
	return Mul(f.L, f.U).Equal(paq)
}
