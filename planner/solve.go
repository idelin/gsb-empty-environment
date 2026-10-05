package planner

import "time"

// Options controls the search budget.
type Options struct {
	// Deadline stops the search after this time (zero = no time limit).
	Deadline time.Time
	// MaxNodes caps enumerated search nodes (0 = unlimited).
	MaxNodes int64
	// EnumerateBelow: DAGs with at most this many blocks are solved by
	// exact enumeration of topological orders (default 12 when <= 0).
	// Always set a budget when using exact enumeration: wide DAGs have
	// super-exponentially many topological orders.
	EnumerateBelow int
}

// Budget is the internal interruptible-budget handle.
type Budget struct {
	deadline time.Time
	maxNodes int64
	nodes    int64
}

func (b *Budget) ok() bool {
	if b.maxNodes > 0 && b.nodes >= b.maxNodes {
		return false
	}
	// time.Now is throttled by the caller (checked every 4096 nodes).
	if !b.deadline.IsZero() && time.Now().After(b.deadline) {
		return false
	}
	return true
}

// Result reports the best schedule found and the certified bounds.
type Result struct {
	Order      []string `json:"order"`      // best order found (block IDs)
	Peak       int64    `json:"peak"`       // peak usage of Order (= UpperBound)
	LowerBound int64    `json:"lowerBound"` // legal: no order can peak below this
	UpperBound int64    `json:"upperBound"` // proven feasible: Order achieves this peak
	Optimal    bool     `json:"optimal"`    // true when the search space was exhausted
	Exhausted  bool     `json:"exhausted"`  // enumeration finished within budget
	Quota      int64    `json:"quota,omitempty"`
	// Feasible: the best order fits the quota (always true without quota).
	Feasible bool `json:"feasible"`
	// ProvablyInfeasible: LowerBound already exceeds the quota, so no
	// order can ever fit.
	ProvablyInfeasible bool `json:"provablyInfeasible,omitempty"`
}

// Solve finds an execution order minimizing peak disk usage.
//
// It always returns a proven-feasible upper bound (the peak of the best
// order actually constructed) and a legal lower bound, so callers may
// interrupt it at any budget and still get a certified gap. For small
// DAGs it enumerates topological orders exactly; for larger ones it falls
// back to a greedy list schedule refined by adjacent-swap descent.
func Solve(p *Planner, quota int64, opts Options) Result {
	enumBelow := opts.EnumerateBelow
	if enumBelow <= 0 {
		enumBelow = 12
	}
	b := &Budget{deadline: opts.Deadline, maxNodes: opts.MaxNodes}

	lb := p.LowerBound()

	order := p.greedy(b)
	ub := p.eval(order)

	if p.n > 1 && p.n <= 512 {
		if improved, improvedUB := p.descent(order, ub, b); improvedUB < ub {
			order, ub = improved, improvedUB
		}
	}

	optimal := false
	exhausted := false
	if p.n <= enumBelow && ub > lb {
		best, bestOrder, done := p.enumerate(b, ub, lb)
		// Even an interrupted enumeration yields a proven-feasible upper
		// bound: best is always the peak of a concrete enumerated order.
		if best < ub {
			ub = best
			order = bestOrder
		}
		exhausted = done
		optimal = done
	} else if p.n <= enumBelow && ub == lb {
		optimal = true // greedy already hit the lower bound
		exhausted = true
	}

	res := Result{
		Order:      p.OrderIDs(order),
		Peak:       ub,
		LowerBound: lb,
		UpperBound: ub,
		Optimal:    optimal,
		Exhausted:  exhausted,
		Quota:      quota,
		Feasible:   quota <= 0 || ub <= quota,
	}
	if quota > 0 && lb > quota {
		res.ProvablyInfeasible = true
		res.Feasible = false
	}
	return res
}

// greedy builds a topological order by repeatedly picking the ready block
// that minimizes (peak-so-far, usage-after), a one-step lookahead.
func (p *Planner) greedy(b *Budget) []int {
	s := newState(p)
	order := make([]int, 0, p.n)
	for len(order) < p.n {
		best := -1
		var bestPeak, bestUsage int64
		for i := 0; i < p.n; i++ {
			if s.done[i] || s.indeg[i] != 0 {
				continue
			}
			freed, oldPeak := s.apply(p, i)
			peak, usage := s.peak, s.usage
			s.undo(p, i, freed, oldPeak)
			if best == -1 || peak < bestPeak || (peak == bestPeak && usage < bestUsage) {
				best, bestPeak, bestUsage = i, peak, usage
			}
		}
		s.apply(p, best)
		order = append(order, best)
	}
	return order
}

// descent improves order by adjacent swaps that keep it topological.
// Swapping neighbors i,j is valid iff j is not a descendant of i.
func (p *Planner) descent(order []int, ub int64, b *Budget) ([]int, int64) {
	desc := p.reachability()
	cur := append([]int(nil), order...)
	improved := true
	for improved && b.ok() {
		improved = false
		for k := 0; k+1 < len(cur); k++ {
			i, j := cur[k], cur[k+1]
			if desc[i].get(j) {
				continue // j must stay after i
			}
			cur[k], cur[k+1] = j, i
			if peak := p.eval(cur); peak < ub {
				ub = peak
				improved = true
			} else {
				cur[k], cur[k+1] = i, j // revert
			}
		}
	}
	return cur, ub
}

// enumerate explores all topological orders with branch-and-bound and
// returns the best peak found. done is false when the budget interrupted
// the search; in that case the returned bound is still a valid upper bound
// (it is the peak of a concrete order), just not proven optimal.
func (p *Planner) enumerate(b *Budget, initBest, lb int64) (best int64, bestOrder []int, done bool) {
	best = initBest
	s := newState(p)
	cur := make([]int, 0, p.n)
	done = true

	var dfs func() bool // false = abort (budget exhausted or lb reached)
	dfs = func() bool {
		if len(cur) == p.n {
			if s.peak < best {
				best = s.peak
				bestOrder = append([]int(nil), cur...)
			}
			return best > lb // reaching lb proves optimality: stop early
		}
		b.nodes++
		if b.nodes&4095 == 0 && !b.ok() {
			done = false
			return false
		}
		for i := 0; i < p.n; i++ {
			if s.done[i] || s.indeg[i] != 0 {
				continue
			}
			freed, oldPeak := s.apply(p, i)
			// Peak-so-far is monotone along any extension, so a partial
			// schedule already at or above best can never improve it.
			if s.peak < best {
				cur = append(cur, i)
				cont := dfs()
				cur = cur[:len(cur)-1]
				s.undo(p, i, freed, oldPeak)
				if !cont {
					return false
				}
			} else {
				s.undo(p, i, freed, oldPeak)
			}
		}
		return true
	}
	if !dfs() && best == initBest {
		bestOrder = nil
	}
	return best, bestOrder, done
}

// bitset is a small helper for reachability over up to 512 nodes.
type bitset []uint64

func (b bitset) get(i int) bool { return b[i>>6]>>(uint(i)&63)&1 == 1 }
func (b bitset) set(i int)      { b[i>>6] |= 1 << (uint(i) & 63) }
func (b bitset) union(o bitset) {
	for k := range b {
		b[k] |= o[k]
	}
}

// reachability returns descendant closures: desc[i] holds every j that
// (transitively) depends on i.
func (p *Planner) reachability() []bitset {
	words := (p.n + 63) / 64
	desc := make([]bitset, p.n)
	for i := range desc {
		desc[i] = make(bitset, words)
	}
	topo, _ := p.topoOrder()
	// Process in reverse topological order so children are closed first.
	for k := len(topo) - 1; k >= 0; k-- {
		i := topo[k]
		for _, j := range p.dependents[i] {
			desc[i].set(j)
			desc[i].union(desc[j])
		}
	}
	return desc
}
