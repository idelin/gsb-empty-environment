package planner

import (
	"math"
	"sort"
	"time"
)

// Status describes how much the solver can certify about the answer.
type Status string

const (
	// StatusOptimal: search completed; Peak is the provable minimum.
	StatusOptimal Status = "optimal"
	// StatusFeasible: search was interrupted; the returned plan fits the
	// budget but a better plan may exist.
	StatusFeasible Status = "feasible"
	// StatusUnknown: search was interrupted; the best plan found does not
	// fit the budget and the lower bound does not rule one out either.
	StatusUnknown Status = "unknown"
	// StatusInfeasible: the lower bound exceeds the budget, so no plan can
	// ever fit (proven even when the search was interrupted).
	StatusInfeasible Status = "infeasible"
)

// Result is the solver output. UpperBound is always the peak of a concrete,
// executable plan (a proven feasible upper bound). LowerBound is a legal
// lower bound: no execution order can ever peak below it.
type Result struct {
	Status      Status   `json:"status"`
	Order       []string `json:"order,omitempty"`
	Peak        int64    `json:"peak"`
	Final       int64    `json:"final"`
	UpperBound  int64    `json:"upper_bound"`
	LowerBound  int64    `json:"lower_bound"`
	Budget      int64    `json:"budget"`
	Expanded    int64    `json:"expanded"`
	Interrupted bool     `json:"interrupted"`
}

// StaticLowerBound computes a cheap, always-legal lower bound on the peak:
// the union of all chunks that must be live at end of plan, and the peak
// each block would need if executed entirely on its own.
func (p *Problem) StaticLowerBound() int64 {
	end := newDiskState()
	for i := range p.blocks {
		for _, s := range []Stage{p.blocks[i].Download, p.blocks[i].Temp, p.blocks[i].Final} {
			if s.Release == ReleaseEndOfPlan {
				end.alloc(s.Chunks)
			}
		}
	}
	lb := end.used
	for b := range p.blocks {
		st := p.newState()
		p.apply(st, b)
		if st.disk.peak > lb {
			lb = st.disk.peak
		}
	}
	return lb
}

// Greedy builds a valid topological order by always scheduling the ready
// block whose execution raises the peak the least. It runs in polynomial
// time and yields a proven feasible upper bound.
func (p *Problem) Greedy() ([]int, int64) {
	n := len(p.blocks)
	st := p.newState()
	order := make([]int, 0, n)
	for st.nScheduled < n {
		bestB := -1
		var bestSt *state
		bestPeak, bestUsed := int64(math.MaxInt64), int64(math.MaxInt64)
		for b := 0; b < n; b++ {
			if st.scheduled[b] || !p.ready(st, b) {
				continue
			}
			child := st.clone()
			p.apply(child, b)
			if child.disk.peak < bestPeak ||
				(child.disk.peak == bestPeak && child.disk.used < bestUsed) {
				bestB, bestSt = b, child
				bestPeak, bestUsed = child.disk.peak, child.disk.used
			}
		}
		st = bestSt
		order = append(order, bestB)
	}
	return order, st.disk.peak
}

func (p *Problem) ready(st *state, b int) bool {
	for _, d := range p.deps[b] {
		if !st.scheduled[d] {
			return false
		}
	}
	return true
}

// Solve searches for an execution order minimizing peak disk usage.
//
// For small DAGs it enumerates topological orders (branch and bound over
// the simulated peak) and proves optimality. When the search budget
// (Limits.MaxExpanded / Limits.TimeoutMs) runs out, it still returns a
// proven feasible upper bound (the best concrete plan found, seeded by a
// greedy pass) together with a legal lower bound.
func (p *Problem) Solve(lim Limits) Result {
	greedyOrder, greedyPeak := p.Greedy()
	s := &solver{
		p:           p,
		best:        greedyPeak,
		bestOrder:   greedyOrder,
		maxExpanded: lim.MaxExpanded,
		frontier:    math.MaxInt64,
	}
	if lim.TimeoutMs > 0 {
		s.deadline = time.Now().Add(time.Duration(lim.TimeoutMs) * time.Millisecond)
	}
	s.search(p.newState(), nil)

	staticLB := p.StaticLowerBound()
	res := Result{
		Order:       p.IDs(s.bestOrder),
		Peak:        s.best,
		UpperBound:  s.best,
		Budget:      p.Budget,
		Expanded:    s.expanded,
		Interrupted: s.interrupted,
	}
	if sim, err := p.Simulate(s.bestOrder); err == nil {
		res.Final = sim.Final
	}
	if !s.interrupted {
		res.LowerBound = s.best
		if p.Budget > 0 && s.best > p.Budget {
			res.Status = StatusInfeasible
		} else {
			res.Status = StatusOptimal
		}
		return res
	}
	// Interrupted: combine the static bound with the frontier bound. Every
	// unexplored schedule extends some frontier node, and a frontier node's
	// prefix peak lower-bounds every completion through it. Candidates with
	// prefix peak >= best are pruned, so frontier < best whenever the
	// optimum could still improve; clamping by best keeps the bound legal.
	lb := staticLB
	if f := s.frontier; f != math.MaxInt64 {
		if f > s.best {
			f = s.best
		}
		if f > lb {
			lb = f
		}
	}
	res.LowerBound = lb
	switch {
	case p.Budget > 0 && lb > p.Budget:
		res.Status = StatusInfeasible
	case p.Budget <= 0 || s.best <= p.Budget:
		res.Status = StatusFeasible
	default:
		res.Status = StatusUnknown
	}
	return res
}

type solver struct {
	p           *Problem
	best        int64
	bestOrder   []int
	expanded    int64
	maxExpanded int64
	deadline    time.Time
	interrupted bool
	frontier    int64
}

func (s *solver) noteFrontier(peak int64) {
	if peak < s.frontier {
		s.frontier = peak
	}
}

func (s *solver) search(st *state, order []int) {
	if s.interrupted {
		s.noteFrontier(st.disk.peak)
		return
	}
	s.expanded++
	if (s.maxExpanded > 0 && s.expanded >= s.maxExpanded) ||
		(!s.deadline.IsZero() && s.expanded%512 == 0 && time.Now().After(s.deadline)) {
		s.interrupted = true
		s.noteFrontier(st.disk.peak)
		return
	}
	n := len(s.p.blocks)
	if st.nScheduled == n {
		if st.disk.peak < s.best {
			s.best = st.disk.peak
			s.bestOrder = append([]int(nil), order...)
		}
		return
	}
	type cand struct {
		b    int
		st   *state
		peak int64
	}
	var cands []cand
	for b := 0; b < n; b++ {
		if st.scheduled[b] || !s.p.ready(st, b) {
			continue
		}
		child := st.clone()
		s.p.apply(child, b)
		if child.disk.peak >= s.best {
			continue // cannot improve on the incumbent
		}
		cands = append(cands, cand{b, child, child.disk.peak})
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].peak != cands[j].peak {
			return cands[i].peak < cands[j].peak
		}
		return cands[i].b < cands[j].b
	})
	for i := range cands {
		if s.interrupted {
			// Unvisited sibling on the current DFS path: its prefix peak
			// lower-bounds every unexplored schedule passing through it.
			s.noteFrontier(cands[i].peak)
			continue
		}
		s.search(cands[i].st, append(order, cands[i].b))
	}
}
