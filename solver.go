package groupopt

import "time"

// Status describes the outcome of a Solve call.
type Status int

const (
	// Exact means the search space was exhausted; Lower == Upper is optimal.
	Exact Status = iota
	// Truncated means the time budget ran out; [Lower, Upper] brackets the
	// optimum and the search can be resumed with another Solve call.
	Truncated
	// Infeasible means the committed closure alone exceeds the budget.
	Infeasible
)

func (s Status) String() string {
	switch s {
	case Exact:
		return "Exact"
	case Truncated:
		return "Truncated"
	case Infeasible:
		return "Infeasible"
	}
	return "Unknown"
}

// FrontierNode is a snapshot of one live branch-and-bound node, included
// in the certificate of a truncated run so an independent verifier can
// recompute and confirm the reported upper bound.
type FrontierNode struct {
	Decided uint64 // bitmask of positions already branched on
	Sel     uint64 // bitmask of positions selected so far
	Bound   int64  // relaxation bound on the best completion below this node
}

// Certificate carries the proof data backing a Result. It is valid only
// for the epoch it was issued in; any checkpoint revision invalidates it.
type Certificate struct {
	Epoch    int
	Frontier []FrontierNode // live nodes at truncation; empty when Exact
}

// Result is the outcome of one Solve call.
type Result struct {
	Status    Status
	Epoch     int
	Lower     int64 // profit of the best feasible selection found
	Upper     int64 // certified upper bound on the optimum
	Cost      int64 // total cost of Selection (shared fees charged once)
	Selection []int // witness ids for Lower, sorted
	HasSel    bool  // false only when Status == Infeasible
	Cert      *Certificate
}

// node is one stack entry of the depth-first search.
type node struct {
	level   int // next free item to branch on (index into order)
	sel     uint64
	decided uint64
	cost    int64
	profit  int64
	groups  uint64 // used | paid group mask
	stage   int    // 0: try include, 1: try exclude, 2: done
	bound   int64
}

// solver is a resumable branch-and-bound search. Its state is tied to one
// epoch: a Session rebuilds it whenever commitments or revisions occur.
type solver struct {
	c          *compiled
	budget     int64
	paid       uint64 // paid group-number mask
	order      []int  // free (non-committed) positions in ratio order
	stack      []node
	incSel     uint64
	incProf    int64
	incCost    int64
	hasInc     bool
	exhausted  bool
	infeasible bool
}

func newSolver(c *compiled, budget int64, committed map[int]bool, paid map[int]bool) *solver {
	s := &solver{c: c, budget: budget}
	cm := uint64(0)
	for id := range committed {
		cm |= 1 << uint(c.index[id])
	}
	cm = c.closure(cm)
	for g := range paid {
		s.paid |= 1 << uint(c.groupNum[g])
	}
	root := node{sel: cm, decided: cm}
	root.groups = c.groupsOf(cm) | s.paid
	root.cost = c.costOf(cm, s.paid)
	root.profit = c.profitOf(cm)
	if root.cost > budget {
		s.infeasible = true
		return s
	}
	// The committed closure is itself a feasible selection.
	s.consider(root.sel, root.profit, root.cost)
	for _, i := range c.ratioOrder {
		if cm&(1<<uint(i)) == 0 {
			s.order = append(s.order, i)
		}
	}
	root.bound = s.bound(root)
	s.stack = append(s.stack, root)
	return s
}

// bound computes an upper bound on the best profit reachable below nd.
func (s *solver) bound(nd node) int64 {
	return nd.profit + s.c.relax(^nd.decided&s.c.full, s.budget-nd.cost)
}

// prunable reports whether a subtree can be discarded. Equality with the
// incumbent profit is kept: a tied-profit, cheaper selection may still
// win the tie-break.
func (s *solver) prunable(bound int64) bool {
	return s.hasInc && bound < s.incProf
}

// consider records sel if it beats the incumbent under
// (profit desc, cost asc, id set asc).
func (s *solver) consider(sel uint64, profit, cost int64) {
	if !s.hasInc ||
		profit > s.incProf ||
		(profit == s.incProf && cost < s.incCost) ||
		(profit == s.incProf && cost == s.incCost && s.c.maskLess(sel, s.incSel)) {
		s.hasInc = true
		s.incSel = sel
		s.incProf = profit
		s.incCost = cost
	}
}

// search runs until exhaustion or the deadline. A zero deadline means no
// time limit. The stack is preserved on truncation, so the next call
// resumes exactly where this one stopped.
func (s *solver) search(deadline time.Time) {
	const checkEvery = 64
	iter := 0
	for len(s.stack) > 0 {
		iter++
		if iter%checkEvery == 0 && !deadline.IsZero() && time.Now().After(deadline) {
			return
		}
		top := &s.stack[len(s.stack)-1]
		if top.level == len(s.order) {
			s.consider(top.sel, top.profit, top.cost)
			s.stack = s.stack[:len(s.stack)-1]
			continue
		}
		i := s.order[top.level]
		bit := uint64(1) << uint(i)
		switch top.stage {
		case 0: // include branch
			top.stage = 1
			// Feasible only if no already-excluded item is required.
			if s.c.reqMask[i]&top.decided&^top.sel == 0 {
				nc := top.cost + s.c.cost[i]
				ng := top.groups
				g := s.c.groupOf[i]
				if ng&(1<<uint(g)) == 0 {
					nc += s.c.fee[g]
					ng |= 1 << uint(g)
				}
				if nc <= s.budget {
					child := node{
						level:   top.level + 1,
						sel:     top.sel | bit,
						decided: top.decided | bit,
						cost:    nc,
						profit:  top.profit + s.c.profit[i],
						groups:  ng,
					}
					child.bound = s.bound(child)
					if !s.prunable(child.bound) {
						s.stack = append(s.stack, child)
					}
				}
			}
		case 1: // exclude branch
			top.stage = 2
			// Feasible only if no selected item requires i.
			if s.c.requiredBy[i]&top.sel == 0 {
				child := node{
					level:   top.level + 1,
					sel:     top.sel,
					decided: top.decided | bit,
					cost:    top.cost,
					profit:  top.profit,
					groups:  top.groups,
				}
				child.bound = s.bound(child)
				if !s.prunable(child.bound) {
					s.stack = append(s.stack, child)
				}
			}
		default:
			s.stack = s.stack[:len(s.stack)-1]
		}
	}
	s.exhausted = true
}

// result snapshots the current search state as a Result with certificate.
func (s *solver) result(epoch int) Result {
	r := Result{Epoch: epoch, Cert: &Certificate{Epoch: epoch}}
	if s.exhausted {
		r.Status = Exact
	} else {
		r.Status = Truncated
	}
	if s.hasInc {
		r.HasSel = true
		r.Lower = s.incProf
		r.Cost = s.incCost
		r.Selection = s.c.idList(s.incSel)
	}
	ub := r.Lower
	for _, nd := range s.stack {
		if nd.bound > ub {
			ub = nd.bound
		}
	}
	r.Upper = ub
	if !s.exhausted {
		for _, nd := range s.stack {
			r.Cert.Frontier = append(r.Cert.Frontier, FrontierNode{
				Decided: nd.decided,
				Sel:     nd.sel,
				Bound:   nd.bound,
			})
		}
	}
	return r
}
