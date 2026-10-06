package bnb

import (
	"container/heap"
	"fmt"
	"math/big"
	"sort"
)

// Status describes how a Solve call terminated.
type Status int

const (
	// StatusOptimal means the frontier is empty: LowerBound == UpperBound
	// is the proven optimum.
	StatusOptimal Status = iota
	// StatusBudgetExhausted means the node budget ran out; LowerBound and
	// UpperBound bracket the optimum.
	StatusBudgetExhausted
	// StatusInterrupted means the checkpoint hook returned an error.
	StatusInterrupted
)

func (s Status) String() string {
	switch s {
	case StatusOptimal:
		return "Optimal"
	case StatusBudgetExhausted:
		return "BudgetExhausted"
	case StatusInterrupted:
		return "Interrupted"
	}
	return "Unknown"
}

// Result summarizes a Solve call.
type Result struct {
	Status Status
	// LowerBound is the incumbent (best feasible) value.
	LowerBound int64
	// UpperBound is a proven upper bound on the optimum: the maximum of
	// the incumbent and every live frontier bound. When Status is
	// StatusOptimal it equals LowerBound.
	UpperBound int64
	// Solution is the incumbent assignment.
	Solution []int64
	// NodesExplored is the cumulative number of nodes expanded by the
	// solver (across all Solve calls, including before a restore).
	NodesExplored int64
	// FrontierSize is the number of live frontier nodes left.
	FrontierSize int
}

func (r Result) String() string {
	return fmt.Sprintf("%s: bounds=[%d, %d] nodes=%d frontier=%d",
		r.Status, r.LowerBound, r.UpperBound, r.NodesExplored, r.FrontierSize)
}

// Options controls a Solve call.
type Options struct {
	// Budget is the maximum number of nodes to expand in this call.
	// A negative budget means unlimited.
	Budget int64
	// CheckpointEvery, if positive, invokes Hook after every that many
	// node expansions, always at a clean iteration boundary so the saved
	// state is an exact prefix of the deterministic search.
	CheckpointEvery int64
	// Hook is called periodically (see CheckpointEvery); a non-nil error
	// aborts the search with StatusInterrupted.
	Hook func(*Solver) error
}

// Solver is a branch-and-bound search over a Problem. The frontier is a
// priority queue ordered by relaxation upper bound (descending), ties
// broken by the path encoding (ascending); the order is total, so the
// search is fully deterministic.
type Solver struct {
	problem *Problem
	order   []int // item indices sorted by value/weight ratio, descending

	frontier       nodeHeap
	incumbentValue int64
	incumbentSol   []int64
	nodesExplored  int64
}

// NewSolver validates the problem, seeds the incumbent with a greedy
// feasible solution and enqueues the root node.
func NewSolver(p *Problem) (*Solver, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	s := &Solver{problem: p, order: ratioOrder(p)}
	value, sol := s.greedy()
	s.setIncumbent(value, sol)
	if p.NumItems() == 0 {
		return s, nil
	}
	root := &Node{path: []int64{}}
	root.bound = s.computeBound(root)
	if root.bound > s.incumbentValue {
		heap.Push(&s.frontier, root)
	}
	return s, nil
}

// Problem returns the problem being solved.
func (s *Solver) Problem() *Problem { return s.problem }

// NodesExplored returns the cumulative number of expanded nodes.
func (s *Solver) NodesExplored() int64 { return s.nodesExplored }

// FrontierSize returns the number of live frontier nodes.
func (s *Solver) FrontierSize() int { return s.frontier.Len() }

// Incumbent returns the best feasible value and solution found so far.
func (s *Solver) Incumbent() (int64, []int64) {
	return s.incumbentValue, append([]int64(nil), s.incumbentSol...)
}

// Solve expands nodes until the frontier is empty (proven optimum) or the
// budget is exhausted. It is resumable: call it again (possibly after a
// checkpoint round-trip) to continue exactly where it stopped.
func (s *Solver) Solve(opts Options) (Result, error) {
	var expanded, sinceHook int64
	for s.frontier.Len() > 0 {
		if opts.Budget >= 0 && expanded >= opts.Budget {
			return s.result(StatusBudgetExhausted), nil
		}
		node := heap.Pop(&s.frontier).(*Node)
		if node.bound <= s.incumbentValue {
			continue // pruned by an incumbent improved since enqueue
		}
		s.expand(node)
		s.nodesExplored++
		expanded++
		sinceHook++
		if opts.Hook != nil && opts.CheckpointEvery > 0 && sinceHook >= opts.CheckpointEvery {
			sinceHook = 0
			if err := opts.Hook(s); err != nil {
				return s.result(StatusInterrupted), err
			}
		}
	}
	return s.result(StatusOptimal), nil
}

// result builds the Result for the current state. For a non-optimal stop
// the upper bound is the maximum of the incumbent and all frontier bounds,
// which is a valid global bound because every not-yet-explored solution
// lives below some frontier node.
func (s *Solver) result(status Status) Result {
	r := Result{
		Status:        status,
		LowerBound:    s.incumbentValue,
		UpperBound:    s.incumbentValue,
		Solution:      append([]int64(nil), s.incumbentSol...),
		NodesExplored: s.nodesExplored,
		FrontierSize:  s.frontier.Len(),
	}
	if status != StatusOptimal {
		if mb := s.frontier.maxBound(); mb > r.UpperBound {
			r.UpperBound = mb
		}
	}
	return r
}

// expand branches a node on its next free variable, trying every feasible
// value 0..maxTake. Children at the last depth are leaves and go straight
// to the incumbent; the rest are bounded and enqueued unless pruned.
func (s *Solver) expand(node *Node) {
	p := s.problem
	d := node.Depth()
	n := p.NumItems()
	rem := p.Capacity - node.weight
	maxTake := p.Bounds[d]
	if w := p.Weights[d]; w > 0 {
		if t := rem / w; t < maxTake {
			maxTake = t
		}
	}
	for x := int64(0); x <= maxTake; x++ {
		childPath := make([]int64, d+1)
		copy(childPath, node.path)
		childPath[d] = x
		child := &Node{
			path:   childPath,
			weight: node.weight + x*p.Weights[d],
			value:  node.value + x*p.Values[d],
		}
		if d+1 == n {
			if child.value > s.incumbentValue {
				s.setIncumbent(child.value, child.path)
			}
			continue
		}
		child.bound = s.computeBound(child)
		if child.bound > s.incumbentValue {
			heap.Push(&s.frontier, child)
		}
	}
}

// computeBound returns floor of the fractional-knapsack relaxation over
// the node's free variables: a valid upper bound on any integer
// completion, tight enough to prune with.
func (s *Solver) computeBound(node *Node) int64 {
	p := s.problem
	bound := node.value
	rem := p.Capacity - node.weight
	depth := node.Depth()
	for _, i := range s.order {
		if i < depth {
			continue
		}
		v, w := p.Values[i], p.Weights[i]
		if v <= 0 {
			break // order is ratio-descending; nothing beneficial remains
		}
		if w == 0 {
			bound += p.Bounds[i] * v
			continue
		}
		take := p.Bounds[i]
		if t := rem / w; t < take {
			take = t
		}
		bound += take * v
		rem -= take * w
		if take < p.Bounds[i] {
			bound += rem * v / w // fractional part, floored
			rem = 0
		}
		if rem == 0 {
			break
		}
	}
	return bound
}

// greedy builds a feasible solution by taking items in ratio order.
func (s *Solver) greedy() (int64, []int64) {
	p := s.problem
	sol := make([]int64, p.NumItems())
	rem := p.Capacity
	var value int64
	for _, i := range s.order {
		if p.Values[i] <= 0 {
			break
		}
		take := p.Bounds[i]
		if w := p.Weights[i]; w > 0 {
			if t := rem / w; t < take {
				take = t
			}
		}
		sol[i] = take
		value += take * p.Values[i]
		rem -= take * p.Weights[i]
		if rem == 0 {
			break
		}
	}
	return value, sol
}

func (s *Solver) setIncumbent(value int64, path []int64) {
	s.incumbentValue = value
	s.incumbentSol = append([]int64(nil), path...)
}

// ratioOrder sorts item indices by value/weight ratio, descending, using
// exact big-integer cross multiplication so the relaxation bound is always
// valid. Zero-weight items with positive value have infinite ratio and
// sort first.
func ratioOrder(p *Problem) []int {
	order := make([]int, p.NumItems())
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool {
		i, j := order[a], order[b]
		vi, wi := p.Values[i], p.Weights[i]
		vj, wj := p.Values[j], p.Weights[j]
		infi := wi == 0 && vi > 0
		infj := wj == 0 && vj > 0
		if infi != infj {
			return infi
		}
		if infi {
			return false
		}
		lhs := new(big.Int).Mul(big.NewInt(vi), big.NewInt(wj))
		rhs := new(big.Int).Mul(big.NewInt(vj), big.NewInt(wi))
		return lhs.Cmp(rhs) > 0
	})
	return order
}
