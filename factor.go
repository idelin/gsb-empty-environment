package sparselu

import (
	"errors"
	"math/big"
)

// ErrBudgetExhausted is returned by Factorizer.Run when the elimination
// budget is spent before factorization completes. The Factorizer keeps its
// full intermediate state, so calling Run again with a fresh budget resumes
// exactly where it stopped.
var ErrBudgetExhausted = errors.New("sparselu: elimination budget exhausted")

// Factorization is the result of factoring A: permutations P and Q (as
// index vectors, P[i] = original row moved to position i) and triangular
// factors L (unit lower, m x m) and U (upper, m x n) with P*A*Q == L*U.
type Factorization struct {
	P, Q []int
	L, U *Matrix
	// Rank is the numerical rank found during elimination. If Rank is less
	// than min(Rows, Cols) the remaining pivots were all zero.
	Rank int
	// FillIn counts entries of L (off-diagonal) and U that are nonzero at
	// positions where P*A*Q is zero. Cancelled entries are exact zeros and
	// are never stored, so they cannot be counted as fill.
	FillIn int
}

// Factorizer performs sparse LU factorization with full pivoting over the
// rationals. It is resumable: Run consumes a budget of row-elimination
// operations and preserves all state when the budget runs out.
type Factorizer struct {
	src  *Matrix // original matrix, never mutated
	w    *Matrix // working matrix; becomes U (upper part) as we go
	l    *Matrix // multipliers below the diagonal
	prow []int   // row permutation: position -> original row
	pcol []int   // column permutation: position -> original column

	k       int  // current pivot step
	elimRow int  // next row to eliminate in step k
	pivoted bool // pivot for step k already selected and swapped in
	rank    int
	done    bool
}

// NewFactorizer prepares to factor m. The input is cloned and never
// modified.
func NewFactorizer(m *Matrix) *Factorizer {
	prow := make([]int, m.Rows)
	pcol := make([]int, m.Cols)
	for i := range prow {
		prow[i] = i
	}
	for j := range pcol {
		pcol[j] = j
	}
	return &Factorizer{
		src:  m,
		w:    m.Clone(),
		l:    NewMatrix(m.Rows, m.Rows),
		prow: prow,
		pcol: pcol,
	}
}

// Done reports whether factorization has completed.
func (f *Factorizer) Done() bool { return f.done }

// Rank returns the rank determined so far; it is final once Done is true.
func (f *Factorizer) Rank() int { return f.rank }

// Run advances the factorization, spending at most budget row-elimination
// operations (a negative budget means unlimited). It returns nil once the
// factorization is complete and ErrBudgetExhausted if the budget ran out
// first; in that case the state is fully preserved and a later Run call
// resumes mid-step without redoing any work.
func (f *Factorizer) Run(budget int) error {
	if f.done {
		return nil
	}
	rows, cols := f.w.Rows, f.w.Cols
	limit := rows
	if cols < limit {
		limit = cols
	}
	for f.k < limit {
		if !f.pivoted {
			pi, pj, ok := f.findPivot()
			if !ok {
				// The remaining submatrix is entirely zero: report rank.
				f.rank = f.k
				f.done = true
				return nil
			}
			f.swapRows(f.k, pi)
			f.swapCols(f.k, pj)
			f.pivoted = true
			f.elimRow = f.k + 1
		}
		for f.elimRow < rows {
			if f.w.at(f.elimRow, f.k) != nil {
				if budget == 0 {
					return ErrBudgetExhausted
				}
				f.eliminate(f.elimRow)
				budget--
			}
			f.elimRow++
		}
		f.pivoted = false
		f.k++
	}
	f.rank = f.k
	f.done = true
	return nil
}

// Factorize factors m in one call with an unlimited budget.
func Factorize(m *Matrix) *Factorization {
	f := NewFactorizer(m)
	if err := f.Run(-1); err != nil {
		panic(err) // unreachable: unlimited budget never fails
	}
	return f.Result()
}

// findPivot scans the submatrix [k..) x [k..) in deterministic row-major
// order for the first nonzero entry.
func (f *Factorizer) findPivot() (pi, pj int, ok bool) {
	for i := f.k; i < f.w.Rows; i++ {
		for j := f.k; j < f.w.Cols; j++ {
			if f.w.at(i, j) != nil {
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
	f.w.rows[a], f.w.rows[b] = f.w.rows[b], f.w.rows[a]
	f.l.rows[a], f.l.rows[b] = f.l.rows[b], f.l.rows[a]
	f.prow[a], f.prow[b] = f.prow[b], f.prow[a]
}

func (f *Factorizer) swapCols(a, b int) {
	if a == b {
		return
	}
	for _, row := range f.w.rows {
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

// eliminate subtracts a multiple of pivot row k from row i, recording the
// multiplier in L. Entries that cancel to exactly zero are deleted, so no
// residual zeros remain in the sparse structure.
func (f *Factorizer) eliminate(i int) {
	k := f.k
	pivot := f.w.at(k, k)
	src := f.w.at(i, k)
	factor := new(big.Rat).Quo(src, pivot)
	f.l.Set(i, k, factor)
	f.w.delete(i, k)
	prow := f.w.rows[k]
	irow := f.w.rows[i]
	for j, pv := range prow {
		// Pivot-row entries only exist in columns >= k; column k itself
		// was already removed above and must not be reintroduced.
		if j == k {
			continue
		}
		prod := new(big.Rat).Mul(factor, pv)
		if cur := irow[j]; cur != nil {
			cur.Sub(cur, prod)
			if cur.Sign() == 0 {
				delete(irow, j)
			}
		} else {
			if irow == nil {
				irow = make(map[int]*big.Rat)
				f.w.rows[i] = irow
			}
			irow[j] = new(big.Rat).Neg(prod)
		}
	}
	if len(irow) == 0 {
		delete(f.w.rows, i)
	}
}

// Result assembles the final factorization. It returns nil if the
// factorization has not completed yet.
func (f *Factorizer) Result() *Factorization {
	if !f.done {
		return nil
	}
	m, n := f.src.Rows, f.src.Cols

	u := NewMatrix(m, n)
	for i := 0; i < f.rank; i++ {
		for j, v := range f.w.rows[i] {
			// Invariant: row i < rank holds entries only in columns >= i.
			u.Set(i, j, v)
		}
	}

	l := NewMatrix(m, m)
	one := big.NewRat(1, 1)
	for i := 0; i < m; i++ {
		l.Set(i, i, one)
	}
	for i, row := range f.l.rows {
		for j, v := range row {
			l.Set(i, j, v)
		}
	}

	p := make([]int, m)
	copy(p, f.prow)
	q := make([]int, n)
	copy(q, f.pcol)

	res := &Factorization{P: p, Q: q, L: l, U: u, Rank: f.rank}
	res.FillIn = countFill(f.src, res)
	return res
}

// countFill counts nonzeros of L (off-diagonal) and U at positions where
// P*A*Q is zero.
func countFill(src *Matrix, res *Factorization) int {
	paq := Permute(src, res.P, res.Q)
	fill := 0
	for i, row := range res.L.rows {
		for j := range row {
			if i != j && paq.at(i, j) == nil {
				fill++
			}
		}
	}
	for i, row := range res.U.rows {
		for j := range row {
			if paq.at(i, j) == nil {
				fill++
			}
		}
	}
	return fill
}
