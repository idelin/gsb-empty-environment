package quadopt

import (
	"container/heap"
	"errors"
	"math/big"
)

// Status describes how a Solve call finished.
type Status int

const (
	// Optimal means the search space was exhausted; Point/Value are the
	// exact optimum and LowerBound == Value.
	Optimal Status = iota
	// BudgetExhausted means the node budget ran out. Value is the best
	// feasible value found (upper bound) and LowerBound is a valid global
	// lower bound on the optimum.
	BudgetExhausted
)

func (s Status) String() string {
	if s == Optimal {
		return "Optimal"
	}
	return "BudgetExhausted"
}

// Options tunes Solve. The zero value means unlimited budget, no hooks.
type Options struct {
	// MaxNodes caps the number of branch-and-bound nodes visited.
	// <= 0 means unlimited.
	MaxNodes int
	// OnPrune, if set, is called every time a node is pruned by bound.
	// lb is the valid lower bound of f over the box [lo, hi] that
	// justified the prune. The slices must not be mutated by the callback.
	OnPrune func(lo, hi []*big.Int, lb *big.Int)
}

// Result is the outcome of Solve.
type Result struct {
	Status Status
	// Point is the best feasible point found. Among points attaining the
	// same value, the lexicographically smallest (by coordinate order) is
	// reported.
	Point []*big.Int
	// Value = f(Point); an upper bound on the optimum.
	Value *big.Int
	// LowerBound is a valid lower bound on the optimum over the whole box.
	LowerBound *big.Int
	// Nodes is the number of branch-and-bound nodes visited.
	Nodes int
}

type node struct {
	lo, hi []*big.Int
	lb     *big.Int
	seq    int
}

type nodeHeap []node

func (h nodeHeap) Len() int { return len(h) }
func (h nodeHeap) Less(i, j int) bool {
	if c := h[i].lb.Cmp(h[j].lb); c != 0 {
		return c < 0
	}
	return h[i].seq < h[j].seq
}
func (h nodeHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *nodeHeap) Push(x interface{}) { *h = append(*h, x.(node)) }
func (h *nodeHeap) Pop() interface{} {
	old := *h
	n := len(old)
	nd := old[n-1]
	*h = old[:n-1]
	return nd
}

// Solve minimizes f over the integer box {x : lo[i] <= x[i] <= hi[i]} via
// exact best-first branch and bound.
func Solve(f *Quadratic, lo, hi []*big.Int, opts *Options) (*Result, error) {
	n := f.N
	if len(lo) != n || len(hi) != n {
		return nil, errors.New("quadopt: box dimension mismatch")
	}
	for i := 0; i < n; i++ {
		if lo[i] == nil || hi[i] == nil {
			return nil, errors.New("quadopt: nil box bound")
		}
		if lo[i].Cmp(hi[i]) > 0 {
			return nil, errors.New("quadopt: empty box (lo > hi)")
		}
	}
	if opts == nil {
		opts = &Options{}
	}

	var bestVal *big.Int
	var bestPoint []*big.Int
	consider := func(x []*big.Int) {
		v := f.Eval(x)
		if bestVal == nil || better(v, x, bestVal, bestPoint) {
			bestVal = v
			bestPoint = cloneVec(x)
		}
	}

	rootLo := cloneVec(lo)
	rootHi := cloneVec(hi)
	consider(midpoint(rootLo, rootHi))

	h := &nodeHeap{}
	seq := 0
	push := func(lo, hi []*big.Int) {
		heap.Push(h, node{lo: lo, hi: hi, lb: f.lowerBound(lo, hi), seq: seq})
		seq++
	}
	push(rootLo, rootHi)

	res := &Result{Status: Optimal}
	for h.Len() > 0 {
		if opts.MaxNodes > 0 && res.Nodes >= opts.MaxNodes {
			res.Status = BudgetExhausted
			break
		}
		nd := heap.Pop(h).(node)
		res.Nodes++

		// Strict inequality: nodes whose bound equals the incumbent may
		// still hide a lexicographically smaller point of equal value.
		if nd.lb.Cmp(bestVal) > 0 {
			if opts.OnPrune != nil {
				opts.OnPrune(nd.lo, nd.hi, nd.lb)
			}
			continue
		}
		if isSingleton(nd.lo, nd.hi) {
			consider(nd.lo)
			continue
		}
		consider(midpoint(nd.lo, nd.hi))

		i := widestCoord(nd.lo, nd.hi)
		mid := floorAvg(nd.lo[i], nd.hi[i])

		leftHi := cloneVec(nd.hi)
		leftHi[i] = mid
		push(cloneVec(nd.lo), leftHi)

		rightLo := cloneVec(nd.lo)
		rightLo[i] = new(big.Int).Add(mid, big.NewInt(1))
		push(rightLo, cloneVec(nd.hi))
	}

	res.Point = bestPoint
	res.Value = bestVal
	if res.Status == Optimal {
		res.LowerBound = new(big.Int).Set(bestVal)
	} else {
		lb := new(big.Int).Set(bestVal)
		for _, nd := range *h {
			if nd.lb.Cmp(lb) < 0 {
				lb.Set(nd.lb)
			}
		}
		res.LowerBound = lb
	}
	return res, nil
}

// better reports whether (valA, a) beats (valB, b): smaller value wins,
// ties are broken by lexicographic coordinate order.
func better(valA *big.Int, a []*big.Int, valB *big.Int, b []*big.Int) bool {
	if c := valA.Cmp(valB); c != 0 {
		return c < 0
	}
	return lexLess(a, b)
}

func lexLess(a, b []*big.Int) bool {
	for i := range a {
		if c := a[i].Cmp(b[i]); c != 0 {
			return c < 0
		}
	}
	return false
}

func cloneVec(v []*big.Int) []*big.Int {
	out := make([]*big.Int, len(v))
	for i, x := range v {
		out[i] = new(big.Int).Set(x)
	}
	return out
}

func isSingleton(lo, hi []*big.Int) bool {
	for i := range lo {
		if lo[i].Cmp(hi[i]) != 0 {
			return false
		}
	}
	return true
}

var two = big.NewInt(2)

// floorAvg returns floor((a+b)/2); big.Int.Div is Euclidean, so it floors
// for the positive divisor 2 even when a+b is negative.
func floorAvg(a, b *big.Int) *big.Int {
	s := new(big.Int).Add(a, b)
	return s.Div(s, two)
}

func midpoint(lo, hi []*big.Int) []*big.Int {
	m := make([]*big.Int, len(lo))
	for i := range lo {
		m[i] = floorAvg(lo[i], hi[i])
	}
	return m
}

// widestCoord returns the first index attaining the maximum of hi-lo.
func widestCoord(lo, hi []*big.Int) int {
	best := 0
	bestW := new(big.Int).Sub(hi[0], lo[0])
	w := new(big.Int)
	for i := 1; i < len(lo); i++ {
		w.Sub(hi[i], lo[i])
		if w.Cmp(bestW) > 0 {
			best, bestW = i, new(big.Int).Set(w)
		}
	}
	return best
}
