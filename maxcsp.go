// Package maxcsp implements a weighted Max-CSP (Maximum Constraint
// Satisfaction Problem) solver using branch and bound.
//
// A problem consists of finite-domain integer variables, hard constraints
// that must always hold, and soft constraints that each carry a penalty
// weight. When not all constraints can be satisfied simultaneously, the
// solver searches for an assignment that satisfies every hard constraint
// while minimizing the total weight of violated soft constraints.
package maxcsp

import "sort"

// Variable is a finite-domain integer variable.
type Variable struct {
	Name   string
	Domain []int
}

// Constraint is a hard or soft constraint over the problem variables.
//
// Violated inspects a (possibly partial) assignment and reports whether the
// constraint is violated. The second return value reports whether the
// constraint is fully decided: a constraint is decided only when every
// variable it mentions has been assigned. Undecided constraints contribute
// nothing to the branch-and-bound lower bound.
type Constraint struct {
	Name     string
	Hard     bool
	Weight   int // penalty for violating a soft constraint; ignored for hard ones
	Violated func(assign map[string]int) (violated, decided bool)
}

// Problem is a weighted Max-CSP instance.
type Problem struct {
	Variables   []Variable
	Constraints []Constraint
}

// Result is the outcome of a Solve call.
type Result struct {
	Assignment map[string]int // optimal assignment (nil if infeasible)
	Cost       int            // total weight of violated soft constraints
	Violated   []string       // names of violated soft constraints, sorted
	Nodes      int            // search nodes expanded
	Pruned     int            // nodes pruned by the bound or hard constraints
}

// Solve finds a hard-constraint-satisfying assignment with minimum total
// soft-constraint violation weight using depth-first branch and bound.
//
// The incumbent bound starts at +infinity (no initial solution assumed), so
// the first complete feasible assignment becomes the initial incumbent and
// every later improvement tightens the bound. A node is pruned when the
// penalty already incurred by decided soft constraints is not smaller than
// the incumbent, because extending the partial assignment can never reduce
// that cost. This guarantees the returned assignment is globally optimal.
func Solve(p *Problem) Result {
	s := &solver{
		p:      p,
		assign: make(map[string]int, len(p.Variables)),
		best:   -1, // -1 means "no incumbent yet"
	}
	s.search(0, 0)
	res := Result{Cost: s.best, Nodes: s.nodes, Pruned: s.pruned}
	if s.bestAssign == nil {
		return res // infeasible: hard constraints cannot all be satisfied
	}
	res.Assignment = s.bestAssign
	for _, c := range p.Constraints {
		if c.Hard {
			continue
		}
		if v, _ := c.Violated(res.Assignment); v {
			res.Violated = append(res.Violated, c.Name)
		}
	}
	sort.Strings(res.Violated)
	return res
}

type solver struct {
	p          *Problem
	assign     map[string]int
	best       int
	bestAssign map[string]int
	nodes      int
	pruned     int
}

// search assigns p.Variables[idx] and recurses. cost is the lower bound: the
// total weight of soft constraints already decided and violated.
func (s *solver) search(idx, cost int) {
	s.nodes++
	// Bound: the incurred cost can only grow as more variables are assigned.
	if s.best >= 0 && cost >= s.best {
		s.pruned++
		return
	}
	if idx == len(s.p.Variables) {
		s.best = cost
		s.bestAssign = make(map[string]int, len(s.assign))
		for k, v := range s.assign {
			s.bestAssign[k] = v
		}
		return
	}
	v := s.p.Variables[idx]
	// A constraint becomes decided exactly when its last unassigned variable
	// is bound, so its penalty must be charged only at that moment. Snapshot
	// the decided flags to charge each constraint exactly once.
	decidedBefore := s.decidedFlags()
	for _, val := range v.Domain {
		s.assign[v.Name] = val
		if delta, ok := s.checkNewlyDecided(decidedBefore); ok {
			s.search(idx+1, cost+delta)
		} else {
			s.pruned++ // a decided hard constraint is violated
		}
		delete(s.assign, v.Name)
	}
}

func (s *solver) decidedFlags() []bool {
	flags := make([]bool, len(s.p.Constraints))
	for i, c := range s.p.Constraints {
		_, flags[i] = c.Violated(s.assign)
	}
	return flags
}

// checkNewlyDecided evaluates constraints that became decided with the
// latest assignment. It returns their added soft penalty, or ok=false if a
// decided hard constraint is violated.
func (s *solver) checkNewlyDecided(decidedBefore []bool) (delta int, ok bool) {
	for i, c := range s.p.Constraints {
		violated, decided := c.Violated(s.assign)
		if !decided || !violated {
			continue
		}
		if c.Hard {
			return 0, false
		}
		if !decidedBefore[i] {
			delta += c.Weight
		}
	}
	return delta, true
}

// BruteForce enumerates the full Cartesian product of the domains and
// returns the optimal cost and one optimal assignment. It is exponentially
// slow and intended only for cross-checking Solve in tests.
func BruteForce(p *Problem) (bestCost int, best map[string]int) {
	bestCost = -1
	assign := make(map[string]int, len(p.Variables))
	var rec func(idx int)
	rec = func(idx int) {
		if idx == len(p.Variables) {
			cost := 0
			for _, c := range p.Constraints {
				violated, _ := c.Violated(assign)
				if !violated {
					continue
				}
				if c.Hard {
					return
				}
				cost += c.Weight
			}
			if bestCost < 0 || cost < bestCost {
				bestCost = cost
				best = make(map[string]int, len(assign))
				for k, v := range assign {
					best[k] = v
				}
			}
			return
		}
		v := p.Variables[idx]
		for _, val := range v.Domain {
			assign[v.Name] = val
			rec(idx + 1)
		}
		delete(assign, v.Name)
	}
	rec(0)
	return bestCost, best
}
