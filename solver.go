package main

import "math/big"

// constraint is a linear inequality sum(coeffs[i] * x[i]) <= rhs.
type constraint struct {
	coeffs []*big.Int
	rhs    *big.Int
}

// activeConstraint tracks the remaining right-hand side of a constraint
// after the variables with index < dim have been fixed.
type activeConstraint struct {
	id  int
	rhs *big.Int
}

// Solver counts and ranks integer points of a polytope inside a finite box.
// Pruning uses interval arithmetic on the box bounds only, so it is sound:
// a subtree is dropped only when no point in it can satisfy the constraints.
type Solver struct {
	n      int
	lo, hi []*big.Int
	cons   []constraint
	// minSum[c][d] / maxSum[c][d]: min/max of the tail sum
	// sum_{j>=d} coeffs[c][j] * x[j] over the box.
	minSum [][]*big.Int
	maxSum [][]*big.Int
	budget int64
	used   int64
}

func NewSolver(lo, hi []*big.Int, cons []constraint, budget int64) *Solver {
	n := len(lo)
	s := &Solver{n: n, lo: lo, hi: hi, cons: cons, budget: budget}
	s.minSum = make([][]*big.Int, len(cons))
	s.maxSum = make([][]*big.Int, len(cons))
	for ci, c := range cons {
		mins := make([]*big.Int, n+1)
		maxs := make([]*big.Int, n+1)
		mins[n] = new(big.Int)
		maxs[n] = new(big.Int)
		for d := n - 1; d >= 0; d-- {
			a := c.coeffs[d]
			mn := new(big.Int).Set(mins[d+1])
			mx := new(big.Int).Set(maxs[d+1])
			if a.Sign() >= 0 {
				mn.Add(mn, new(big.Int).Mul(a, lo[d]))
				mx.Add(mx, new(big.Int).Mul(a, hi[d]))
			} else {
				mn.Add(mn, new(big.Int).Mul(a, hi[d]))
				mx.Add(mx, new(big.Int).Mul(a, lo[d]))
			}
			mins[d] = mn
			maxs[d] = mx
		}
		s.minSum[ci] = mins
		s.maxSum[ci] = maxs
	}
	return s
}

// enter consumes one unit of budget; reports false when exhausted.
func (s *Solver) enter() bool {
	if s.used >= s.budget {
		return false
	}
	s.used++
	return true
}

// prune drops constraints that are redundant below dim and detects
// infeasibility. It never discards a constraint that still excludes points.
func (s *Solver) prune(dim int, cons []activeConstraint) ([]activeConstraint, bool) {
	kept := make([]activeConstraint, 0, len(cons))
	for _, c := range cons {
		if s.minSum[c.id][dim].Cmp(c.rhs) > 0 {
			return nil, false // even the minimum tail exceeds rhs: infeasible
		}
		if s.maxSum[c.id][dim].Cmp(c.rhs) <= 0 {
			continue // every tail satisfies it: redundant below dim
		}
		kept = append(kept, c)
	}
	return kept, true
}

func (s *Solver) initialActive() []activeConstraint {
	active := make([]activeConstraint, len(s.cons))
	for i, c := range s.cons {
		active[i] = activeConstraint{id: i, rhs: new(big.Int).Set(c.rhs)}
	}
	return active
}

// boxVolume returns the number of integer points in the box restricted to
// dimensions dim..n-1. It is 0 if any remaining dimension is empty.
func (s *Solver) boxVolume(dim int) *big.Int {
	vol := big.NewInt(1)
	for d := dim; d < s.n; d++ {
		w := new(big.Int).Sub(s.hi[d], s.lo[d])
		w.Add(w, big.NewInt(1))
		if w.Sign() <= 0 {
			return new(big.Int)
		}
		vol.Mul(vol, w)
	}
	return vol
}

// Count returns the number of valid points; ok is false if the budget ran out.
func (s *Solver) Count() (*big.Int, bool) {
	return s.count(0, s.initialActive())
}

func (s *Solver) count(dim int, cons []activeConstraint) (*big.Int, bool) {
	if !s.enter() {
		return nil, false
	}
	kept, feasible := s.prune(dim, cons)
	if !feasible {
		return new(big.Int), true
	}
	if len(kept) == 0 {
		// No active constraint can exclude points below: the whole
		// remaining box is valid, so count it in closed form.
		return s.boxVolume(dim), true
	}
	if dim == s.n {
		return big.NewInt(1), true
	}
	total := new(big.Int)
	one := big.NewInt(1)
	for v := new(big.Int).Set(s.lo[dim]); v.Cmp(s.hi[dim]) <= 0; v.Add(v, one) {
		next := make([]activeConstraint, len(kept))
		for i, c := range kept {
			adj := new(big.Int).Mul(s.cons[c.id].coeffs[dim], v)
			next[i] = activeConstraint{id: c.id, rhs: new(big.Int).Sub(c.rhs, adj)}
		}
		sub, ok := s.count(dim+1, next)
		if !ok {
			return nil, false
		}
		total.Add(total, sub)
	}
	return total, true
}

// ProofStep records, for one dimension, the chosen value and how many valid
// points were skipped at that dimension (same prefix, smaller value).
// The skipped counts sum to k-1 and constitute the ranking proof.
type ProofStep struct {
	Dim     int
	Value   *big.Int
	Skipped *big.Int
}

// Kth returns the 1-indexed k-th point in lexicographic order together with
// the ranking proof. Callers must ensure 1 <= k <= Count(); ok is false if
// the budget ran out.
func (s *Solver) Kth(k *big.Int) ([]*big.Int, []ProofStep, bool) {
	proof := []ProofStep{}
	pt, ok := s.kth(0, s.initialActive(), new(big.Int).Set(k), &proof)
	return pt, proof, ok
}

func (s *Solver) kth(dim int, cons []activeConstraint, k *big.Int, proof *[]ProofStep) ([]*big.Int, bool) {
	if !s.enter() {
		return nil, false
	}
	kept, feasible := s.prune(dim, cons)
	if !feasible {
		return nil, true // no points here; unreachable when k is in range
	}
	if len(kept) == 0 {
		// No active constraints: decompose k-1 in mixed radix over the
		// remaining box widths instead of walking value by value.
		pt := make([]*big.Int, 0, s.n-dim)
		rem := new(big.Int).Sub(k, big.NewInt(1))
		for d := dim; d < s.n; d++ {
			vol := s.boxVolume(d + 1)
			idx := new(big.Int)
			if vol.Sign() > 0 {
				idx.Quo(rem, vol)
				rem.Sub(rem, new(big.Int).Mul(idx, vol))
			}
			v := new(big.Int).Add(s.lo[d], idx)
			*proof = append(*proof, ProofStep{
				Dim:     d,
				Value:   new(big.Int).Set(v),
				Skipped: new(big.Int).Mul(idx, vol),
			})
			pt = append(pt, v)
		}
		return pt, true
	}
	if dim == s.n {
		return []*big.Int{}, true
	}
	skipped := new(big.Int)
	one := big.NewInt(1)
	for v := new(big.Int).Set(s.lo[dim]); v.Cmp(s.hi[dim]) <= 0; v.Add(v, one) {
		next := make([]activeConstraint, len(kept))
		for i, c := range kept {
			adj := new(big.Int).Mul(s.cons[c.id].coeffs[dim], v)
			next[i] = activeConstraint{id: c.id, rhs: new(big.Int).Sub(c.rhs, adj)}
		}
		cnt, ok := s.count(dim+1, next)
		if !ok {
			return nil, false
		}
		if k.Cmp(cnt) > 0 {
			k.Sub(k, cnt)
			skipped.Add(skipped, cnt)
			continue
		}
		*proof = append(*proof, ProofStep{
			Dim:     dim,
			Value:   new(big.Int).Set(v),
			Skipped: new(big.Int).Set(skipped),
		})
		rest, ok := s.kth(dim+1, next, k, proof)
		if !ok {
			return nil, false
		}
		return append([]*big.Int{new(big.Int).Set(v)}, rest...), true
	}
	return nil, true // k exceeds the subtree size; unreachable when k is valid
}

// Enumerate lists all valid points in lexicographic order, up to limit.
// The second return value reports whether the limit truncated the listing.
func Enumerate(lo, hi []*big.Int, cons []constraint, limit int) ([][]*big.Int, bool) {
	n := len(lo)
	pts := [][]*big.Int{}
	cur := make([]*big.Int, n)
	truncated := false
	var rec func(d int) bool
	rec = func(d int) bool {
		if d == n {
			for _, c := range cons {
				sum := new(big.Int)
				for j := 0; j < n; j++ {
					sum.Add(sum, new(big.Int).Mul(c.coeffs[j], cur[j]))
				}
				if sum.Cmp(c.rhs) > 0 {
					return true
				}
			}
			if len(pts) >= limit {
				truncated = true
				return false
			}
			p := make([]*big.Int, n)
			for j := range cur {
				p[j] = new(big.Int).Set(cur[j])
			}
			pts = append(pts, p)
			return true
		}
		one := big.NewInt(1)
		for v := new(big.Int).Set(lo[d]); v.Cmp(hi[d]) <= 0; v.Add(v, one) {
			cur[d] = new(big.Int).Set(v)
			if !rec(d + 1) {
				return false
			}
		}
		return true
	}
	rec(0)
	return pts, truncated
}
