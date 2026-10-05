package exactmat

import (
	"fmt"
	"math/big"
	"strings"
)

// SolveKind classifies the outcome of Solve.
type SolveKind int

const (
	// Unique means the matrix is invertible and X is the only solution.
	Unique SolveKind = iota
	// NoSolution means the system is inconsistent; Evidence explains why.
	NoSolution
	// Underdetermined means the system is consistent but has infinitely
	// many solutions: X is one particular solution and NullSpace is a
	// basis of the kernel, so the general solution is X + span(NullSpace).
	Underdetermined
)

func (k SolveKind) String() string {
	switch k {
	case Unique:
		return "unique"
	case NoSolution:
		return "no-solution"
	case Underdetermined:
		return "underdetermined"
	}
	return "unknown"
}

// SolveResult is the exact outcome of Solve. Every field is exact rational
// data that can be substituted back into the system for verification, e.g.
// with Matrix.CheckSolution.
type SolveResult struct {
	Kind SolveKind
	// X is the unique solution (Unique) or a particular solution
	// (Underdetermined). Nil for NoSolution.
	X []*big.Rat
	// NullSpace is a linearly independent basis of the kernel, present
	// only for Underdetermined. Each basis vector v satisfies A v = 0.
	NullSpace [][]*big.Rat
	Rank      int
	Nullity   int
	// Evidence is a human-readable justification, in particular the
	// contradictory equation for NoSolution and the rank/nullity count
	// for Underdetermined.
	Evidence string
}

// Solve solves A x = b exactly.
//
// When the matrix is invertible the maintained inverse gives the unique
// solution directly. When it is singular, the augmented matrix [A|b] is
// reduced with full exact elimination: a row reducing to 0 = c with c != 0
// proves inconsistency (NoSolution), otherwise the RREF yields a particular
// solution plus a null-space basis (Underdetermined).
func (m *Matrix) Solve(b []*big.Rat) (*SolveResult, error) {
	if len(b) != m.n {
		return nil, fmt.Errorf("exactmat: right-hand side has length %d, want %d", len(b), m.n)
	}
	rhs := make([]*big.Rat, m.n)
	for i, v := range b {
		if v == nil {
			return nil, fmt.Errorf("exactmat: nil entry %d of right-hand side", i)
		}
		rhs[i] = new(big.Rat).Set(v)
	}

	if m.inv != nil {
		return &SolveResult{
			Kind:     Unique,
			X:        matVec(m.inv, rhs),
			Rank:     m.n,
			Nullity:  0,
			Evidence: fmt.Sprintf("coefficient matrix invertible (rank %d); unique exact solution", m.n),
		}, nil
	}

	n := m.n
	aug := make([][]*big.Rat, n)
	for i := 0; i < n; i++ {
		aug[i] = make([]*big.Rat, n+1)
		for j := 0; j < n; j++ {
			aug[i][j] = new(big.Rat).Set(m.a[i][j])
		}
		aug[i][n] = new(big.Rat).Set(rhs[i])
	}
	pivots := rref(aug)
	inPivot := make([]bool, n+1)
	for _, p := range pivots {
		inPivot[p] = true
	}

	if inPivot[n] {
		badRow := 0
		for k, p := range pivots {
			if p == n {
				badRow = k
				break
			}
		}
		c := new(big.Rat).Set(aug[badRow][n])
		return &SolveResult{
			Kind: NoSolution,
			Rank: m.rank,
			Evidence: fmt.Sprintf("inconsistent: exact elimination of [A|b] yields row 0 = %s; rank(A) = %d < rank([A|b]) = %d",
				c.RatString(), m.rank, m.rank+1),
		}, nil
	}

	pivotOfRow := pivots
	x := make([]*big.Rat, n)
	for j := range x {
		x[j] = big.NewRat(0, 1)
	}
	for k, p := range pivotOfRow {
		x[p] = new(big.Rat).Set(aug[k][n])
	}
	free := []int{}
	for j := 0; j < n; j++ {
		if !inPivot[j] {
			free = append(free, j)
		}
	}
	nullSpace := make([][]*big.Rat, 0, len(free))
	for _, f := range free {
		v := make([]*big.Rat, n)
		for j := range v {
			v[j] = big.NewRat(0, 1)
		}
		v[f].SetInt64(1)
		for k, p := range pivotOfRow {
			v[p] = new(big.Rat).Neg(aug[k][f])
		}
		nullSpace = append(nullSpace, v)
	}
	return &SolveResult{
		Kind:      Underdetermined,
		X:         x,
		NullSpace: nullSpace,
		Rank:      m.rank,
		Nullity:   len(free),
		Evidence: fmt.Sprintf("consistent but rank(A) = %d < %d unknowns; nullity %d; general solution = particular + span of %d basis vector(s)",
			m.rank, n, len(free), len(free)),
	}, nil
}

// CheckSolution independently verifies a SolveResult against A x = b by
// substitution, using only exact arithmetic:
//
//   - Unique / Underdetermined: A X must equal b and every null-space
//     basis vector v must satisfy A v = 0.
//   - NoSolution: the claimed evidence is rechecked by computing
//     rank([A|b]) > rank(A) from scratch.
func (m *Matrix) CheckSolution(res *SolveResult, b []*big.Rat) bool {
	if res == nil || len(b) != m.n {
		return false
	}
	switch res.Kind {
	case Unique, Underdetermined:
		if len(res.X) != m.n {
			return false
		}
		if !vecEqual(matVec(m.a, res.X), b) {
			return false
		}
		if res.Kind == Underdetermined && len(res.NullSpace) != res.Nullity {
			return false
		}
		for _, v := range res.NullSpace {
			if len(v) != m.n || !isZeroVec(matVec(m.a, v)) {
				return false
			}
		}
		return true
	case NoSolution:
		return augmentedRank(m.a, b) > m.rank
	}
	return false
}

// augmentedRank computes rank([A|b]) by exact elimination on a fresh copy.
func augmentedRank(a [][]*big.Rat, b []*big.Rat) int {
	n := len(a)
	aug := make([][]*big.Rat, n)
	for i := 0; i < n; i++ {
		aug[i] = make([]*big.Rat, n+1)
		for j := 0; j < n; j++ {
			aug[i][j] = new(big.Rat).Set(a[i][j])
		}
		aug[i][n] = new(big.Rat).Set(b[i])
	}
	return len(rref(aug))
}

// String renders the result compactly for diagnostics.
func (r *SolveResult) String() string {
	var sb strings.Builder
	sb.WriteString(r.Kind.String())
	sb.WriteString(": ")
	sb.WriteString(r.Evidence)
	if r.X != nil {
		sb.WriteString("\n  x = [")
		for i, v := range r.X {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(v.RatString())
		}
		sb.WriteString("]")
	}
	for k, v := range r.NullSpace {
		fmt.Fprintf(&sb, "\n  null[%d] = [", k)
		for i, e := range v {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(e.RatString())
		}
		sb.WriteString("]")
	}
	return sb.String()
}
