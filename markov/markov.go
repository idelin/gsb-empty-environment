// Package markov analyzes finite Markov chains with rational transition
// probabilities. All arithmetic is exact (math/big.Rat).
//
// Given a target absorbing set A, it computes for every start state i:
//
//	h_i = P_i(T_A < infinity)          (hitting probability)
//	E_i[T_A | T_A < infinity]          (conditional expected hitting time)
//
// and classifies each state as never reaching A, reaching A with
// probability < 1 (infinite unconditional expectation), or reaching A
// almost surely.
package markov

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
)

var (
	zero = big.NewRat(0, 1)
	one  = big.NewRat(1, 1)
)

// Chain is a finite Markov chain with rational transition probabilities.
type Chain struct {
	// P[i][j] is the probability of moving from state i to state j.
	P [][]*big.Rat
}

// NewChain validates p and returns the corresponding chain.
// p must be a non-empty square matrix with entries in [0,1] whose rows
// each sum to exactly 1.
func NewChain(p [][]*big.Rat) (*Chain, error) {
	n := len(p)
	if n == 0 {
		return nil, errors.New("markov: transition matrix is empty")
	}
	for i, row := range p {
		if len(row) != n {
			return nil, fmt.Errorf("markov: row %d has length %d, want %d (matrix must be square)", i, len(row), n)
		}
		sum := new(big.Rat)
		for j, v := range row {
			if v == nil {
				return nil, fmt.Errorf("markov: P[%d][%d] is nil", i, j)
			}
			if v.Sign() < 0 {
				return nil, fmt.Errorf("markov: P[%d][%d] = %s is negative", i, j, v.RatString())
			}
			if v.Cmp(one) > 0 {
				return nil, fmt.Errorf("markov: P[%d][%d] = %s exceeds 1", i, j, v.RatString())
			}
			sum.Add(sum, v)
		}
		if sum.Cmp(one) != 0 {
			return nil, fmt.Errorf("markov: row %d sums to %s, want 1", i, sum.RatString())
		}
	}
	return &Chain{P: p}, nil
}

// ParseMatrix parses rows of whitespace-separated rationals (e.g. "1/2",
// "3", "0.25") into a transition matrix accepted by NewChain.
func ParseMatrix(lines []string) ([][]*big.Rat, error) {
	p := make([][]*big.Rat, len(lines))
	for i, line := range lines {
		fields := strings.Fields(line)
		row := make([]*big.Rat, len(fields))
		for j, f := range fields {
			v, ok := new(big.Rat).SetString(f)
			if !ok {
				return nil, fmt.Errorf("markov: row %d: cannot parse %q as a rational", i, f)
			}
			row[j] = v
		}
		p[i] = row
	}
	return p, nil
}

// Status describes how a start state relates to the target absorbing set.
type Status int

const (
	// NeverReaches: the hitting probability is 0 (e.g. the state belongs
	// to a closed class disjoint from the target set).
	NeverReaches Status = iota
	// InfiniteExpectation: the hitting probability is strictly between 0
	// and 1, so the unconditional expected hitting time is infinite.
	// The conditional expectation (given absorption) is still finite.
	InfiniteExpectation
	// CertainAbsorption: the hitting probability is 1 and the expected
	// hitting time is finite.
	CertainAbsorption
)

func (s Status) String() string {
	switch s {
	case NeverReaches:
		return "never reaches target"
	case InfiniteExpectation:
		return "hits with probability < 1 (unconditional expectation infinite)"
	case CertainAbsorption:
		return "certain absorption"
	}
	return "unknown"
}

// Result holds the analysis for one start state.
type Result struct {
	// HitProb is the probability of ever reaching the target set.
	HitProb *big.Rat
	// ConditionalExpectedSteps is E[T | T < infinity], the expected
	// number of steps to reach the target set conditioned on reaching
	// it. It is nil when HitProb is 0.
	ConditionalExpectedSteps *big.Rat
	Status                   Status
}

// Analyze computes, for every start state, the probability of hitting the
// absorbing set and the conditional expected number of steps to hit it.
//
// Hitting probabilities h solve h_i = sum_j P[i][j] h_j with h = 1 on the
// target set; the system is solved on the states that can reach the target
// (where I-Q is non-singular), and h = 0 elsewhere. Conditional
// expectations come from g_i = E[T * 1{T < infinity}], which solves
// g_i = h_i + sum_j P[i][j] g_j; the conditional expectation is g_i / h_i.
func (c *Chain) Analyze(absorbing []int) ([]Result, error) {
	n := len(c.P)
	if len(absorbing) == 0 {
		return nil, errors.New("markov: absorbing set is empty")
	}
	isAbs := make([]bool, n)
	for _, s := range absorbing {
		if s < 0 || s >= n {
			return nil, fmt.Errorf("markov: absorbing state %d out of range [0,%d)", s, n)
		}
		isAbs[s] = true
	}

	// States that can reach the absorbing set in the support graph
	// (reverse BFS from the absorbing states).
	reach := make([]bool, n)
	copy(reach, isAbs)
	for changed := true; changed; {
		changed = false
		for i := 0; i < n; i++ {
			if reach[i] {
				continue
			}
			for j := 0; j < n; j++ {
				if reach[j] && c.P[i][j].Sign() > 0 {
					reach[i] = true
					changed = true
					break
				}
			}
		}
	}

	// Free variables: non-absorbing states that can reach the target.
	idx := make([]int, 0, n)
	pos := make([]int, n)
	for i := 0; i < n; i++ {
		pos[i] = -1
		if !isAbs[i] && reach[i] {
			pos[i] = len(idx)
			idx = append(idx, i)
		}
	}
	m := len(idx)

	// Build (I - Q) restricted to the free states.
	coeff := make([][]*big.Rat, m)
	for r, i := range idx {
		row := make([]*big.Rat, m)
		for col, j := range idx {
			v := new(big.Rat).Neg(c.P[i][j])
			if r == col {
				v.Add(v, one)
			}
			row[col] = v
		}
		coeff[r] = row
	}

	// Right-hand side for h: sum of P[i][j] over absorbing j.
	rhsH := make([]*big.Rat, m)
	for r, i := range idx {
		s := new(big.Rat)
		for j := 0; j < n; j++ {
			if isAbs[j] {
				s.Add(s, c.P[i][j])
			}
		}
		rhsH[r] = s
	}
	hSol, err := solve(coeff, rhsH)
	if err != nil {
		return nil, fmt.Errorf("markov: solving hitting probabilities: %v", err)
	}

	// Right-hand side for g: h_i over the free states.
	rhsG := make([]*big.Rat, m)
	for r := range idx {
		rhsG[r] = new(big.Rat).Set(hSol[r])
	}
	gSol, err := solve(coeff, rhsG)
	if err != nil {
		return nil, fmt.Errorf("markov: solving conditional expectations: %v", err)
	}

	results := make([]Result, n)
	for i := 0; i < n; i++ {
		switch {
		case isAbs[i]:
			results[i] = Result{
				HitProb:                  new(big.Rat).Set(one),
				ConditionalExpectedSteps: new(big.Rat).Set(zero),
				Status:                   CertainAbsorption,
			}
		case !reach[i]:
			results[i] = Result{
				HitProb:                  new(big.Rat).Set(zero),
				ConditionalExpectedSteps: nil,
				Status:                   NeverReaches,
			}
		default:
			h := hSol[pos[i]]
			g := gSol[pos[i]]
			cond := new(big.Rat).Quo(g, h)
			st := InfiniteExpectation
			if h.Cmp(one) == 0 {
				st = CertainAbsorption
			}
			results[i] = Result{
				HitProb:                  new(big.Rat).Set(h),
				ConditionalExpectedSteps: cond,
				Status:                   st,
			}
		}
	}
	return results, nil
}

// solve returns x with a x = b for a square non-singular rational matrix a,
// using Gaussian elimination with partial pivoting. It does not modify its
// arguments.
func solve(a [][]*big.Rat, b []*big.Rat) ([]*big.Rat, error) {
	n := len(a)
	m := make([][]*big.Rat, n)
	rhs := make([]*big.Rat, n)
	for i := 0; i < n; i++ {
		m[i] = make([]*big.Rat, n)
		for j := 0; j < n; j++ {
			m[i][j] = new(big.Rat).Set(a[i][j])
		}
		rhs[i] = new(big.Rat).Set(b[i])
	}

	for col := 0; col < n; col++ {
		piv := -1
		for r := col; r < n; r++ {
			if m[r][col].Sign() != 0 {
				piv = r
				break
			}
		}
		if piv < 0 {
			return nil, errors.New("singular system")
		}
		m[col], m[piv] = m[piv], m[col]
		rhs[col], rhs[piv] = rhs[piv], rhs[col]

		pv := m[col][col]
		for r := col + 1; r < n; r++ {
			if m[r][col].Sign() == 0 {
				continue
			}
			factor := new(big.Rat).Quo(m[r][col], pv)
			for c := col; c < n; c++ {
				term := new(big.Rat).Mul(factor, m[col][c])
				m[r][c].Sub(m[r][c], term)
			}
			term := new(big.Rat).Mul(factor, rhs[col])
			rhs[r].Sub(rhs[r], term)
		}
	}

	x := make([]*big.Rat, n)
	for i := n - 1; i >= 0; i-- {
		s := new(big.Rat).Set(rhs[i])
		for j := i + 1; j < n; j++ {
			term := new(big.Rat).Mul(m[i][j], x[j])
			s.Sub(s, term)
		}
		x[i] = s.Quo(s, m[i][i])
	}
	return x, nil
}
