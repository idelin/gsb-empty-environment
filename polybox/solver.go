package main

import (
	"errors"
	"fmt"
	"math/big"
)

// Constraint is a normalized linear inequality: A[0]*x0 + ... + A[n-1]*x_{n-1} <= B.
type Constraint struct {
	A []*big.Int
	B *big.Int
}

// Problem describes a bounded integer box with linear constraints.
type Problem struct {
	Dims int
	Lo   []*big.Int // inclusive lower bounds, length Dims
	Hi   []*big.Int // inclusive upper bounds, length Dims
	Cons []Constraint
}

// Solver counts and ranks lattice points of a Problem with a node budget.
type Solver struct {
	p         *Problem
	suffixMin [][]*big.Int // suffixMin[c][d] = min sum of A[c][i]*x_i over i >= d
	suffixMax [][]*big.Int
	budget    int64
	nodes     int64
	exhausted bool
}

var (
	bigZero = big.NewInt(0)
	bigOne  = big.NewInt(1)
)

// ErrBudgetExhausted is reported (as "unknown") when the node budget runs out.
var ErrBudgetExhausted = errors.New("budget exhausted")

// ErrOutOfRange is reported when K is not a valid rank.
var ErrOutOfRange = errors.New("k out of range")

// NewSolver builds a Solver. budget <= 0 means unlimited.
func NewSolver(p *Problem, budget int64) (*Solver, error) {
	if p.Dims < 0 {
		return nil, fmt.Errorf("negative dims")
	}
	if len(p.Lo) != p.Dims || len(p.Hi) != p.Dims {
		return nil, fmt.Errorf("box bounds length mismatch")
	}
	for i := 0; i < p.Dims; i++ {
		if p.Lo[i] == nil || p.Hi[i] == nil {
			return nil, fmt.Errorf("nil bound at dim %d", i)
		}
	}
	for c, con := range p.Cons {
		if len(con.A) != p.Dims {
			return nil, fmt.Errorf("constraint %d has %d coefficients, want %d", c, len(con.A), p.Dims)
		}
		if con.B == nil {
			return nil, fmt.Errorf("constraint %d has nil rhs", c)
		}
		for _, a := range con.A {
			if a == nil {
				return nil, fmt.Errorf("constraint %d has nil coefficient", c)
			}
		}
	}
	s := &Solver{p: p, budget: budget}
	m := len(p.Cons)
	s.suffixMin = make([][]*big.Int, m)
	s.suffixMax = make([][]*big.Int, m)
	for c := range p.Cons {
		smin := make([]*big.Int, p.Dims+1)
		smax := make([]*big.Int, p.Dims+1)
		smin[p.Dims] = new(big.Int)
		smax[p.Dims] = new(big.Int)
		for d := p.Dims - 1; d >= 0; d-- {
			a := p.Cons[c].A[d]
			loTerm := new(big.Int).Mul(a, p.Lo[d])
			hiTerm := new(big.Int).Mul(a, p.Hi[d])
			mn, mx := loTerm, hiTerm
			if mn.Cmp(mx) > 0 {
				mn, mx = mx, mn
			}
			smin[d] = new(big.Int).Add(smin[d+1], mn)
			smax[d] = new(big.Int).Add(smax[d+1], mx)
		}
		s.suffixMin[c] = smin
		s.suffixMax[c] = smax
	}
	return s, nil
}

// Nodes returns how many search nodes have been visited.
func (s *Solver) Nodes() int64 { return s.nodes }

// Exhausted reports whether the budget ran out during the last query.
func (s *Solver) Exhausted() bool { return s.exhausted }

// Reset clears the node counter and the exhaustion flag.
func (s *Solver) Reset() {
	s.nodes = 0
	s.exhausted = false
}

func (s *Solver) enterNode() bool {
	if s.exhausted {
		return false
	}
	s.nodes++
	if s.budget > 0 && s.nodes > s.budget {
		s.exhausted = true
		return false
	}
	return true
}

// emptyBox reports whether any dimension has an empty domain.
func (s *Solver) emptyBox() bool {
	for i := 0; i < s.p.Dims; i++ {
		if s.p.Lo[i].Cmp(s.p.Hi[i]) > 0 {
			return true
		}
	}
	return false
}

// floorDiv returns floor(a/b) for b != 0.
func floorDiv(a, b *big.Int) *big.Int {
	q := new(big.Int).Quo(a, b) // truncated toward zero
	r := new(big.Int).Rem(a, b)
	if r.Sign() != 0 && r.Sign() != b.Sign() {
		q.Sub(q, bigOne)
	}
	return q
}

// ceilDiv returns ceil(a/b) for b != 0.
func ceilDiv(a, b *big.Int) *big.Int {
	q := floorDiv(a, b)
	if new(big.Int).Mul(q, b).Cmp(a) != 0 {
		q.Add(q, bigOne)
	}
	return q
}

// boxProduct returns the number of integer points in dims [d, n) of the box,
// or nil if any of those dimensions is empty.
func (s *Solver) boxProduct(d int) *big.Int {
	prod := big.NewInt(1)
	for i := d; i < s.p.Dims; i++ {
		width := new(big.Int).Sub(s.p.Hi[i], s.p.Lo[i])
		width.Add(width, bigOne)
		if width.Sign() <= 0 {
			return nil
		}
		prod.Mul(prod, width)
	}
	return prod
}

// tightenDim intersects the box domain of dimension d with the bounds implied
// by every constraint, assuming dims > d can still take any box value.
// Sound for pruning: it never removes a value that extends to a real solution.
func (s *Solver) tightenDim(d int, cur []*big.Int) (vLo, vHi *big.Int) {
	vLo = new(big.Int).Set(s.p.Lo[d])
	vHi = new(big.Int).Set(s.p.Hi[d])
	for c := range s.p.Cons {
		a := s.p.Cons[c].A[d]
		if a.Sign() == 0 {
			continue
		}
		// Need some completion of dims > d with A*x <= B, i.e.
		// a*x_d + restMin <= B - cur, where restMin is the min possible tail sum.
		rhs := new(big.Int).Sub(s.p.Cons[c].B, cur[c])
		rhs.Sub(rhs, s.suffixMin[c][d+1])
		if a.Sign() > 0 {
			bound := floorDiv(rhs, a)
			if bound.Cmp(vHi) < 0 {
				vHi.Set(bound)
			}
		} else {
			bound := ceilDiv(rhs, a)
			if bound.Cmp(vLo) > 0 {
				vLo.Set(bound)
			}
		}
	}
	return vLo, vHi
}

// countRec counts solutions with dims [0,d) fixed; cur[c] holds the partial
// sum of constraint c over the fixed dims. cur is mutated and restored.
func (s *Solver) countRec(d int, cur []*big.Int) *big.Int {
	if !s.enterNode() {
		return bigZero
	}
	allSat := true
	for c := range s.p.Cons {
		// Provably infeasible: even the cheapest completion violates the bound.
		lo := new(big.Int).Add(cur[c], s.suffixMin[c][d])
		if lo.Cmp(s.p.Cons[c].B) > 0 {
			return bigZero
		}
		// Not provably satisfied for every completion: keep branching.
		hi := new(big.Int).Add(cur[c], s.suffixMax[c][d])
		if hi.Cmp(s.p.Cons[c].B) > 0 {
			allSat = false
		}
	}
	if d == s.p.Dims {
		return bigOne
	}
	if allSat {
		// Every completion of the remaining box is feasible: multiply out.
		if prod := s.boxProduct(d); prod != nil {
			return prod
		}
		return bigZero
	}
	vLo, vHi := s.tightenDim(d, cur)
	if vLo.Cmp(vHi) > 0 {
		return bigZero
	}
	total := new(big.Int)
	v := new(big.Int).Set(vLo)
	for ; v.Cmp(vHi) <= 0; v.Add(v, bigOne) {
		for c := range s.p.Cons {
			cur[c].Add(cur[c], new(big.Int).Mul(s.p.Cons[c].A[d], v))
		}
		sub := s.countRec(d+1, cur)
		for c := range s.p.Cons {
			cur[c].Sub(cur[c], new(big.Int).Mul(s.p.Cons[c].A[d], v))
		}
		if s.exhausted {
			return bigZero
		}
		total.Add(total, sub)
	}
	return total
}

// Count returns the exact number of lattice points, or ErrBudgetExhausted.
func (s *Solver) Count() (*big.Int, error) {
	if s.emptyBox() {
		return big.NewInt(0), nil
	}
	cur := make([]*big.Int, len(s.p.Cons))
	for c := range cur {
		cur[c] = new(big.Int)
	}
	n := s.countRec(0, cur)
	if s.exhausted {
		return nil, ErrBudgetExhausted
	}
	return n, nil
}

// ProofStep records one dimension of the ranking proof: how many points were
// skipped at this dimension (prefix counts) before fixing the chosen value.
type ProofStep struct {
	Dim       int      `json:"dim"`
	Value     *big.Int `json:"value"`
	Skipped   *big.Int `json:"skipped"`   // points with a smaller value at this dim (same prefix)
	Remaining *big.Int `json:"remaining"` // rank still to place after skipping (1-based)
}

// Kth returns the 1-based k-th lattice point in lexicographic order
// (x0 most significant) together with the prefix-count ranking proof.
func (s *Solver) Kth(k *big.Int) ([]*big.Int, []ProofStep, error) {
	if k.Sign() <= 0 {
		return nil, nil, ErrOutOfRange
	}
	if s.emptyBox() {
		return nil, nil, ErrOutOfRange
	}
	cur := make([]*big.Int, len(s.p.Cons))
	for c := range cur {
		cur[c] = new(big.Int)
	}
	// Total count first, to validate K and to reuse the prune machinery.
	total := s.countRec(0, cur)
	if s.exhausted {
		return nil, nil, ErrBudgetExhausted
	}
	if k.Cmp(total) > 0 {
		return nil, nil, ErrOutOfRange
	}
	point := make([]*big.Int, s.p.Dims)
	proof := make([]ProofStep, 0, s.p.Dims)
	rem := new(big.Int).Set(k)
	for d := 0; d < s.p.Dims; d++ {
		vLo, vHi := s.tightenDim(d, cur)
		if vLo.Cmp(vHi) > 0 {
			return nil, nil, fmt.Errorf("internal: empty domain at dim %d", d)
		}
		skipped := new(big.Int)
		chosen := (*big.Int)(nil)
		v := new(big.Int).Set(vLo)
		for ; v.Cmp(vHi) <= 0; v.Add(v, bigOne) {
			for c := range s.p.Cons {
				cur[c].Add(cur[c], new(big.Int).Mul(s.p.Cons[c].A[d], v))
			}
			cnt := s.countRec(d+1, cur)
			if s.exhausted {
				return nil, nil, ErrBudgetExhausted
			}
			if cnt.Sign() > 0 && rem.Cmp(cnt) <= 0 {
				chosen = new(big.Int).Set(v)
				break
			}
			skipped.Add(skipped, cnt)
			rem.Sub(rem, cnt)
			for c := range s.p.Cons {
				cur[c].Sub(cur[c], new(big.Int).Mul(s.p.Cons[c].A[d], v))
			}
		}
		if chosen == nil {
			return nil, nil, fmt.Errorf("internal: no value at dim %d", d)
		}
		// Keep the chosen value's contribution in cur for deeper dims.
		point[d] = chosen
		proof = append(proof, ProofStep{
			Dim:       d,
			Value:     new(big.Int).Set(chosen),
			Skipped:   skipped,
			Remaining: new(big.Int).Set(rem),
		})
	}
	return point, proof, nil
}
