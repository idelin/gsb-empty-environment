// Package iqp solves bounded integer quadratic programs exactly.
//
// It minimizes
//
//	f(x) = x^T Q x + c^T x + const
//
// over integer vectors x inside an axis-aligned box lo <= x <= hi.
// Q may be indefinite: the lower bounds used for pruning are computed by
// interval arithmetic over each node's sub-box, which is valid for any
// symmetric Q (in particular it does not rely on positive definiteness).
// All arithmetic uses math/big so costs are exact for arbitrary sizes.
//
// Among all optimal points the lexicographically smallest one (compared
// coordinate by coordinate) is returned. If the node budget is exhausted
// before proving optimality, the solver reports the incumbent cost as an
// upper bound and the minimum lower bound over all open nodes as a lower
// bound on the optimum.
package iqp

import (
	"container/heap"
	"errors"
	"math/big"
)

// Problem is a bounded integer quadratic program.
type Problem struct {
	Q     [][]*big.Int // symmetric n x n matrix
	C     []*big.Int   // linear term, length n
	Const *big.Int     // constant term
	Lo    []*big.Int   // inclusive lower box bounds, length n
	Hi    []*big.Int   // inclusive upper box bounds, length n
}

// Status describes how a solve finished.
type Status int

const (
	// Optimal means the search space was exhausted; X is a proven optimum.
	Optimal Status = iota
	// BudgetExhausted means the node budget ran out; Upper/Lower bracket
	// the optimum and X (if non-nil) is the best point found.
	BudgetExhausted
)

func (s Status) String() string {
	if s == Optimal {
		return "optimal"
	}
	return "budget_exhausted"
}

// Result is the outcome of a solve.
type Result struct {
	Status Status
	X      []*big.Int // best point found (nil if none evaluated yet)
	Upper  *big.Int   // cost of X (nil if X is nil); upper bound on optimum
	Lower  *big.Int   // lower bound on the optimum over the whole box
	Nodes  int64      // number of search nodes expanded
}

// Validate checks structural consistency of the problem.
func (p *Problem) Validate() error {
	n := len(p.Q)
	if n == 0 {
		return errors.New("iqp: empty problem")
	}
	for i, row := range p.Q {
		if len(row) != n {
			return errors.New("iqp: Q must be square")
		}
		for j, q := range row {
			if q == nil {
				return errors.New("iqp: nil entry in Q")
			}
			if j > i && p.Q[j][i].Cmp(q) != 0 {
				return errors.New("iqp: Q must be symmetric")
			}
		}
	}
	if len(p.C) != n || len(p.Lo) != n || len(p.Hi) != n {
		return errors.New("iqp: C, Lo, Hi must have length n")
	}
	if p.Const == nil {
		return errors.New("iqp: nil Const")
	}
	for i := 0; i < n; i++ {
		if p.C[i] == nil || p.Lo[i] == nil || p.Hi[i] == nil {
			return errors.New("iqp: nil entry in C, Lo or Hi")
		}
		if p.Lo[i].Cmp(p.Hi[i]) > 0 {
			return errors.New("iqp: Lo[i] > Hi[i]")
		}
	}
	return nil
}

// Dim returns the number of variables.
func (p *Problem) Dim() int { return len(p.Q) }

// Eval computes the exact cost f(x).
func (p *Problem) Eval(x []*big.Int) *big.Int {
	n := len(x)
	total := new(big.Int).Set(p.Const)
	term := new(big.Int)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			term.Mul(p.Q[i][j], x[i])
			term.Mul(term, x[j])
			total.Add(total, term)
		}
		term.Mul(p.C[i], x[i])
		total.Add(total, term)
	}
	return total
}

// lowerBound returns a lower bound on f over the integer box [lo, hi].
//
// The bound is exact interval arithmetic: every term of the quadratic form
// is minimized independently over the box. It is valid for any symmetric Q,
// including indefinite and negative definite ones, because it never assumes
// convexity.
func (p *Problem) lowerBound(lo, hi []*big.Int) *big.Int {
	n := len(lo)
	total := new(big.Int).Set(p.Const)
	for i := 0; i < n; i++ {
		// Diagonal term q_ii * x_i^2.
		lo2 := new(big.Int).Mul(lo[i], lo[i])
		hi2 := new(big.Int).Mul(hi[i], hi[i])
		minSq, maxSq := lo2, hi2
		if minSq.Cmp(maxSq) > 0 {
			minSq, maxSq = maxSq, minSq
		}
		if lo[i].Sign() <= 0 && hi[i].Sign() >= 0 {
			minSq = new(big.Int) // 0 lies in [lo_i, hi_i]
		}
		if p.Q[i][i].Sign() >= 0 {
			total.Add(total, minSq.Mul(minSq, p.Q[i][i]))
		} else {
			total.Add(total, maxSq.Mul(maxSq, p.Q[i][i]))
		}
		// Linear term c_i * x_i.
		if p.C[i].Sign() >= 0 {
			total.Add(total, new(big.Int).Mul(p.C[i], lo[i]))
		} else {
			total.Add(total, new(big.Int).Mul(p.C[i], hi[i]))
		}
		// Cross terms 2 * q_ij * x_i * x_j for j > i.
		for j := i + 1; j < n; j++ {
			qij := p.Q[i][j]
			if qij.Sign() == 0 {
				continue
			}
			mn, mx := productRange(lo[i], hi[i], lo[j], hi[j])
			a := new(big.Int).Lsh(new(big.Int).Set(qij), 1) // 2*q_ij
			if a.Sign() >= 0 {
				total.Add(total, a.Mul(a, mn))
			} else {
				total.Add(total, a.Mul(a, mx))
			}
		}
	}
	return total
}

// productRange returns the min and max of u*v for u in [ulo,uhi], v in [vlo,vhi].
func productRange(ulo, uhi, vlo, vhi *big.Int) (mn, mx *big.Int) {
	corners := [4]*big.Int{
		new(big.Int).Mul(ulo, vlo),
		new(big.Int).Mul(ulo, vhi),
		new(big.Int).Mul(uhi, vlo),
		new(big.Int).Mul(uhi, vhi),
	}
	mn, mx = corners[0], corners[0]
	for _, c := range corners[1:] {
		if c.Cmp(mn) < 0 {
			mn = c
		}
		if c.Cmp(mx) > 0 {
			mx = c
		}
	}
	return mn, mx
}

// node is one sub-box of the branch-and-bound tree.
type node struct {
	lo, hi []*big.Int
	lb     *big.Int
	seq    int64
}

// nodeHeap is a best-first priority queue ordered by lower bound, with
// deterministic tie-breaking on the box coordinates.
type nodeHeap []node

func (h nodeHeap) Len() int { return len(h) }

func (h nodeHeap) Less(i, j int) bool {
	if c := h[i].lb.Cmp(h[j].lb); c != 0 {
		return c < 0
	}
	if c := cmpSlice(h[i].lo, h[j].lo); c != 0 {
		return c < 0
	}
	if c := cmpSlice(h[i].hi, h[j].hi); c != 0 {
		return c < 0
	}
	return h[i].seq < h[j].seq
}

func (h nodeHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *nodeHeap) Push(x interface{}) { *h = append(*h, x.(node)) }
func (h *nodeHeap) Pop() interface{} {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

func cmpSlice(a, b []*big.Int) int {
	for i := range a {
		if c := a[i].Cmp(b[i]); c != 0 {
			return c
		}
	}
	return 0
}

// lessLex reports whether a is lexicographically smaller than b by
// coordinate order.
func lessLex(a, b []*big.Int) bool { return cmpSlice(a, b) < 0 }

// Solver runs the branch-and-bound search for one problem.
type Solver struct {
	P *Problem
	// OnNode, if set, is called with (lo, hi, lb) for every node of the
	// search tree as it is created. Intended for testing and logging.
	OnNode func(lo, hi []*big.Int, lb *big.Int)
}

// Solve runs branch and bound, expanding at most maxNodes nodes.
// A negative maxNodes means no budget limit.
func (p *Problem) Solve(maxNodes int64) *Result {
	return (&Solver{P: p}).Solve(maxNodes)
}

// Solve runs the search. It never returns nil.
func (s *Solver) Solve(maxNodes int64) *Result {
	p := s.P
	if err := p.Validate(); err != nil {
		panic(err)
	}
	n := p.Dim()
	res := &Result{}

	rootLo := cloneSlice(p.Lo)
	rootHi := cloneSlice(p.Hi)
	rootLB := p.lowerBound(rootLo, rootHi)

	pq := &nodeHeap{}
	heap.Push(pq, node{lo: rootLo, hi: rootHi, lb: rootLB, seq: 0})
	if s.OnNode != nil {
		s.OnNode(cloneSlice(rootLo), cloneSlice(rootHi), new(big.Int).Set(rootLB))
	}
	seq := int64(1)

	var bestX []*big.Int
	var bestCost *big.Int
	consider := func(x []*big.Int) {
		cost := p.Eval(x)
		if bestCost == nil || cost.Cmp(bestCost) < 0 ||
			(cost.Cmp(bestCost) == 0 && lessLex(x, bestX)) {
			bestCost = cost
			bestX = cloneSlice(x)
		}
	}

	for pq.Len() > 0 && (maxNodes < 0 || res.Nodes < maxNodes) {
		nd := heap.Pop(pq).(node)
		res.Nodes++

		// Prune: a node whose bound exceeds the incumbent cannot contain a
		// better (or lexicographically smaller equal-cost) point.
		if bestCost != nil && nd.lb.Cmp(bestCost) > 0 {
			continue
		}

		// Probe the box centre for a feasible incumbent.
		probe := make([]*big.Int, n)
		for i := 0; i < n; i++ {
			probe[i] = new(big.Int).Add(nd.lo[i], nd.hi[i])
			probe[i].Rsh(probe[i], 1) // floor((lo+hi)/2)
		}
		consider(probe)

		// Find the widest coordinate to branch on.
		k, width := -1, new(big.Int)
		for i := 0; i < n; i++ {
			w := new(big.Int).Sub(nd.hi[i], nd.lo[i])
			if w.Cmp(width) > 0 {
				width, k = w, i
			}
		}
		if k < 0 {
			continue // single point, already evaluated by the probe
		}

		// Split coordinate k at mid = floor((lo+hi)/2):
		// left child [lo, mid], right child [mid+1, hi].
		mid := new(big.Int).Add(nd.lo[k], nd.hi[k])
		mid.Rsh(mid, 1)

		leftLo := cloneSlice(nd.lo)
		leftHi := cloneSlice(nd.hi)
		leftHi[k] = mid
		rightLo := cloneSlice(nd.lo)
		rightHi := cloneSlice(nd.hi)
		rightLo[k] = new(big.Int).Add(mid, big.NewInt(1))

		for _, child := range []node{
			{lo: leftLo, hi: leftHi},
			{lo: rightLo, hi: rightHi},
		} {
			child.lb = p.lowerBound(child.lo, child.hi)
			child.seq = seq
			seq++
			// Keep nodes whose bound equals the incumbent: they may hide a
			// lexicographically smaller point with the same cost.
			if bestCost != nil && child.lb.Cmp(bestCost) > 0 {
				if s.OnNode != nil {
					s.OnNode(cloneSlice(child.lo), cloneSlice(child.hi), new(big.Int).Set(child.lb))
				}
				continue
			}
			heap.Push(pq, child)
			if s.OnNode != nil {
				s.OnNode(cloneSlice(child.lo), cloneSlice(child.hi), new(big.Int).Set(child.lb))
			}
		}
	}

	res.X = bestX
	res.Upper = bestCost
	if pq.Len() == 0 {
		res.Status = Optimal
		// With no open nodes the incumbent cost is the exact optimum, which
		// is also the tightest possible lower bound.
		res.Lower = bestCost
	} else {
		res.Status = BudgetExhausted
		res.Lower = new(big.Int).Set((*pq)[0].lb)
	}
	return res
}

func cloneSlice(in []*big.Int) []*big.Int {
	out := make([]*big.Int, len(in))
	for i, v := range in {
		out[i] = new(big.Int).Set(v)
	}
	return out
}
