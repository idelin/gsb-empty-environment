package flow

import (
	"time"
)

// Budget caps the branch-and-bound search effort. Zero means no limit.
type Budget struct {
	MaxNodes  int64 `json:"max_nodes"`
	MaxMillis int64 `json:"max_millis"`
}

func (b Budget) nodesLimited() bool { return b.MaxNodes > 0 }
func (b Budget) timeLimited() bool  { return b.MaxMillis > 0 }

// Frame is one level of the search stack: the edge being assigned at
// this depth, the next candidate value to try, the value currently
// applied (valid when the frame has a child on the stack) and the
// relaxation bound computed when the frame was entered.
type Frame struct {
	Edge   int   `json:"edge"`
	Next   int64 `json:"next"`
	Chosen int64 `json:"chosen"`
	Bound  int64 `json:"bound"`
}

// Frontier is a serializable snapshot of an interrupted search. It can
// be saved to disk and resumed later without losing any work.
type Frontier struct {
	Stack         []Frame `json:"stack"`
	BestFlows     []int64 `json:"best_flows,omitempty"`
	BestCost      int64   `json:"best_cost"`
	HasBest       bool    `json:"has_best"`
	LowerBound    int64   `json:"lower_bound"`
	NodesExplored int64   `json:"nodes_explored"`
}

// SearchResult is the outcome of a (possibly budget-limited) search.
type SearchResult struct {
	Status        string    `json:"status"` // "optimal" | "infeasible" | "budget_exceeded"
	Flows         []int64   `json:"flows,omitempty"`
	Cost          int64     `json:"cost,omitempty"`
	LowerBound    int64     `json:"lower_bound"`
	UpperBound    int64     `json:"upper_bound,omitempty"`
	HasUpperBound bool      `json:"has_upper_bound"`
	NodesExplored int64     `json:"nodes_explored"`
	Frontier      *Frontier `json:"frontier,omitempty"` // set when budget_exceeded
}

// searcher holds the mutable state of a branch-and-bound run.
type searcher struct {
	p      *Problem
	lo, hi []int64
	net    []int64
	m      int

	// suffixMaxNet[d][v] / suffixMinNet[d][v]: max/min additional net
	// outflow node v can still receive from edges at depths >= d.
	suffixMaxNet [][]int64
	suffixMinNet [][]int64
	// suffixMinCost[d]: min possible cost contribution of edges >= d.
	suffixMinCost []int64

	stack   []Frame
	curNet  []int64
	curCost int64

	best     []int64
	bestCost int64
	hasBest  bool
	lb       int64

	nodes     int64
	deadline  time.Time
	hasDl     bool
	maxNodes  int64
	exhausted bool
}

// newSearcher precomputes the static pruning tables.
func newSearcher(p *Problem) (*searcher, error) {
	lo, hi, err := Bounds(p)
	if err != nil {
		return nil, err
	}
	s := &searcher{p: p, lo: lo, hi: hi, net: p.Net, m: len(p.Edges)}
	n := p.NumNodes
	m := s.m
	s.suffixMaxNet = make([][]int64, m+1)
	s.suffixMinNet = make([][]int64, m+1)
	for d := 0; d <= m; d++ {
		s.suffixMaxNet[d] = make([]int64, n)
		s.suffixMinNet[d] = make([]int64, n)
	}
	s.suffixMinCost = make([]int64, m+1)
	for d := m - 1; d >= 0; d-- {
		copy(s.suffixMaxNet[d], s.suffixMaxNet[d+1])
		copy(s.suffixMinNet[d], s.suffixMinNet[d+1])
		e := p.Edges[d]
		s.suffixMaxNet[d][e.From] += hi[d]
		s.suffixMinNet[d][e.From] += lo[d]
		s.suffixMaxNet[d][e.To] -= lo[d]
		s.suffixMinNet[d][e.To] -= hi[d]
		minC := e.Cost * lo[d]
		if c := e.Cost * hi[d]; c < minC {
			minC = c
		}
		s.suffixMinCost[d] = s.suffixMinCost[d+1] + minC
	}
	s.curNet = make([]int64, n)
	s.lb = s.suffixMinCost[0]
	return s, nil
}

// balanceFeasible reports whether assigning edge e (at depth d) the
// value v keeps every node balance reachable with the remaining edges.
func (s *searcher) balanceFeasible(d, e int, v int64) bool {
	edge := s.p.Edges[e]
	s.curNet[edge.From] += v
	s.curNet[edge.To] -= v
	ok := true
	for node := 0; node < s.p.NumNodes; node++ {
		rem := s.net[node] - s.curNet[node]
		if rem < s.suffixMinNet[d+1][node] || rem > s.suffixMaxNet[d+1][node] {
			ok = false
			break
		}
	}
	s.curNet[edge.From] -= v
	s.curNet[edge.To] += v
	return ok
}

// bound is a relaxation lower bound on the total cost of any completion
// of the current partial assignment plus candidate (e, v) at depth d.
func (s *searcher) bound(d, e int, v int64) int64 {
	return s.curCost + s.p.Edges[e].Cost*v + s.suffixMinCost[d+1]
}

func (s *searcher) apply(e int, v int64) {
	edge := s.p.Edges[e]
	s.curNet[edge.From] += v
	s.curNet[edge.To] -= v
	s.curCost += edge.Cost * v
}

func (s *searcher) unapply(e int, v int64) {
	edge := s.p.Edges[e]
	s.curNet[edge.From] -= v
	s.curNet[edge.To] += v
	s.curCost -= edge.Cost * v
}

func (s *searcher) budgetExceeded() bool {
	if s.maxNodes > 0 && s.nodes >= s.maxNodes {
		return true
	}
	if s.hasDl && s.nodes%256 == 0 && time.Now().After(s.deadline) {
		return true
	}
	return false
}

// run executes the DFS until completion or budget exhaustion.
func (s *searcher) run() {
	if len(s.stack) == 0 && s.m > 0 {
		s.stack = []Frame{{Edge: 0, Next: s.lo[0], Bound: s.suffixMinCost[0]}}
	}
	for len(s.stack) > 0 {
		s.nodes++
		if s.budgetExceeded() {
			s.exhausted = true
			return
		}
		d := len(s.stack) - 1
		f := &s.stack[d]
		e := f.Edge
		if f.Next > s.hi[e] {
			// Frame exhausted: pop and undo the parent's assignment.
			s.stack = s.stack[:d]
			if d > 0 {
				pf := &s.stack[d-1]
				s.unapply(pf.Edge, pf.Chosen)
			}
			continue
		}
		v := f.Next
		f.Next++
		if !s.balanceFeasible(d, e, v) {
			continue
		}
		b := s.bound(d, e, v)
		if s.hasBest && b >= s.bestCost {
			if b < s.lb {
				s.lb = b
			}
			continue
		}
		s.apply(e, v)
		f.Chosen = v
		if d == s.m-1 {
			// Leaf: all edges assigned, balances feasible by construction.
			if !s.hasBest || s.curCost < s.bestCost {
				s.bestCost = s.curCost
				s.hasBest = true
				s.best = make([]int64, s.m)
				for _, fr := range s.stack {
					s.best[fr.Edge] = fr.Chosen
				}
				s.best[e] = v
			}
			s.unapply(e, v)
			continue
		}
		next := s.stack[d].Edge + 1
		s.stack = append(s.stack, Frame{Edge: next, Next: s.lo[next], Bound: b})
	}
}

// snapshot captures the resumable frontier, folding the bounds of all
// open stack frames into the global lower bound.
func (s *searcher) snapshot() *Frontier {
	lb := s.lb
	for _, f := range s.stack {
		if f.Bound < lb {
			lb = f.Bound
		}
	}
	fr := &Frontier{
		Stack:         append([]Frame(nil), s.stack...),
		BestCost:      s.bestCost,
		HasBest:       s.hasBest,
		LowerBound:    lb,
		NodesExplored: s.nodes,
	}
	if s.hasBest {
		fr.BestFlows = append([]int64(nil), s.best...)
	}
	return fr
}

// Search runs a budget-limited branch-and-bound. On budget exhaustion
// it returns a resumable Frontier plus valid [LowerBound, UpperBound]
// brackets instead of an optimality claim.
func Search(p *Problem, budget Budget) (*SearchResult, error) {
	return ResumeSearch(p, nil, budget)
}

// ResumeSearch continues a previously interrupted search from its
// saved frontier (nil starts a fresh search).
func ResumeSearch(p *Problem, fr *Frontier, budget Budget) (*SearchResult, error) {
	s, err := newSearcher(p)
	if err != nil {
		return nil, err
	}
	if budget.nodesLimited() {
		s.maxNodes = budget.MaxNodes
	}
	if budget.timeLimited() {
		s.hasDl = true
		s.deadline = time.Now().Add(time.Duration(budget.MaxMillis) * time.Millisecond)
	}
	if fr != nil {
		if len(fr.Stack) > 0 {
			// Replay applied assignments for all frames below the top.
			for i := 0; i+1 < len(fr.Stack); i++ {
				s.apply(fr.Stack[i].Edge, fr.Stack[i].Chosen)
			}
		}
		s.stack = append([]Frame(nil), fr.Stack...)
		s.best = fr.BestFlows
		s.bestCost = fr.BestCost
		s.hasBest = fr.HasBest
		s.lb = fr.LowerBound
		s.nodes = fr.NodesExplored
	}
	if s.m == 0 {
		// Degenerate case: no edges. Feasible iff all balances are zero.
		balanced := true
		for _, b := range s.net {
			if b != 0 {
				balanced = false
			}
		}
		if balanced {
			s.hasBest = true
			s.bestCost = 0
			s.best = []int64{}
		}
	} else {
		s.run()
	}

	res := &SearchResult{NodesExplored: s.nodes}
	switch {
	case s.exhausted:
		res.Status = "budget_exceeded"
		res.Frontier = s.snapshot()
		res.LowerBound = res.Frontier.LowerBound
		if s.hasBest {
			res.HasUpperBound = true
			res.UpperBound = s.bestCost
			res.Flows = append([]int64(nil), s.best...)
		}
	case s.hasBest:
		res.Status = "optimal"
		res.Flows = s.best
		res.Cost = s.bestCost
		res.LowerBound = s.bestCost
		res.HasUpperBound = true
		res.UpperBound = s.bestCost
	default:
		res.Status = "infeasible"
		res.LowerBound = INF
	}
	return res, nil
}

// Verify independently confirms an exact Solution by exhaustive
// branch-and-bound: feasibility must agree and, when feasible, the
// optimum cost must match.
func Verify(p *Problem, sol *Solution) (match bool, detail string, err error) {
	res, err := Search(p, Budget{})
	if err != nil {
		return false, "", err
	}
	if res.Status == "infeasible" {
		if sol.Feasible {
			return false, "solver found a flow but exhaustive search proves infeasible", nil
		}
		return true, "both report infeasible", nil
	}
	if !sol.Feasible {
		return false, "exhaustive search found a feasible flow but solver reports infeasible", nil
	}
	if res.Cost != sol.Cost {
		return false, "cost mismatch", nil
	}
	return true, "optimal costs match", nil
}
