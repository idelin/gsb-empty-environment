// Package rematch re-solves an employee/position assignment after some
// of the originally used edges have become invalid.
//
// Objectives are lexicographic, in decreasing priority:
//
//  1. maximize the number of matched employees;
//  2. minimize the number of position changes — an employee who originally
//     held a position and now holds a different one (or none) counts as one
//     change; an originally unassigned employee who receives a position does
//     NOT count as a change;
//  3. tie-break by the assignment vector taken in employee order: each entry
//     is the position index (unassigned counts as larger than any index) and
//     the lexicographically smallest vector wins.
package rematch

import "fmt"

// Problem describes one re-matching instance.
type Problem struct {
	// Employees lists employee names; its order defines the order of the
	// assignment vector used by the final tie-break.
	Employees []string
	// Positions lists position names; its order defines the index order of
	// the assignment vector.
	Positions []string
	// CanWork holds the currently valid edges: CanWork[employee][position]
	// is true when the employee may take the position. Edges absent from the
	// map are invalid.
	CanWork map[string]map[string]bool
	// Original is the previous assignment (employee -> position). It may be
	// partial and may reference edges that are no longer valid.
	Original map[string]string
}

// ChainStep is one employee's move inside an adjustment chain.
type ChainStep struct {
	Employee string
	From     string // original position, "" if previously unassigned
	To       string // new position, "" if now unassigned
}

// Solution is the result of Solve.
type Solution struct {
	// Assignment maps each matched employee to the new position.
	Assignment map[string]string
	// Unmatched lists employees without a position, in employee order.
	Unmatched []string
	// Displaced lists employees who originally held a position but are now
	// unmatched (the "mismatched" personnel), in employee order.
	Displaced []string
	// Chains decomposes the difference between the original and the new
	// assignment into alternating adjustment chains/cycles. Employees whose
	// position did not change appear in no chain.
	Chains [][]ChainStep
	// Matched is the number of matched employees (objective 1).
	Matched int
	// Changes is the number of position changes (objective 2).
	Changes int
}

const (
	undecided  = -2
	unassigned = -1
)

type solver struct {
	n, m    int
	allowed [][]bool
	orig    []int // original position index per employee, -1 if none
	assign  []int // undecided / unassigned / position index
	matched int   // objective 1: maximum matching size
	changes int   // objective 2: minimum number of changes
}

// Solve computes the optimal re-assignment for p.
func Solve(p Problem) (Solution, error) {
	s, err := newSolver(p)
	if err != nil {
		return Solution{}, err
	}
	s.matched = maxMatching(s.allowed, s.m)
	c, ok := s.remainderCost()
	if !ok {
		return Solution{}, fmt.Errorf("rematch: internal error: infeasible at full size")
	}
	s.changes = c
	if err := s.fixVector(); err != nil {
		return Solution{}, err
	}
	return s.solution(p), nil
}

func newSolver(p Problem) (*solver, error) {
	n, m := len(p.Employees), len(p.Positions)
	empIdx := make(map[string]int, n)
	for i, e := range p.Employees {
		if _, dup := empIdx[e]; dup {
			return nil, fmt.Errorf("rematch: duplicate employee %q", e)
		}
		empIdx[e] = i
	}
	posIdx := make(map[string]int, m)
	for j, pos := range p.Positions {
		if _, dup := posIdx[pos]; dup {
			return nil, fmt.Errorf("rematch: duplicate position %q", pos)
		}
		posIdx[pos] = j
	}
	allowed := make([][]bool, n)
	for i := range allowed {
		allowed[i] = make([]bool, m)
	}
	for e, set := range p.CanWork {
		i, ok := empIdx[e]
		if !ok {
			return nil, fmt.Errorf("rematch: CanWork references unknown employee %q", e)
		}
		for pos, ok2 := range set {
			j, ok3 := posIdx[pos]
			if !ok3 {
				return nil, fmt.Errorf("rematch: CanWork references unknown position %q", pos)
			}
			if ok2 {
				allowed[i][j] = true
			}
		}
	}
	orig := make([]int, n)
	for i := range orig {
		orig[i] = -1
	}
	used := make(map[string]string, len(p.Original))
	for e, pos := range p.Original {
		i, ok := empIdx[e]
		if !ok {
			return nil, fmt.Errorf("rematch: Original references unknown employee %q", e)
		}
		j, ok := posIdx[pos]
		if !ok {
			return nil, fmt.Errorf("rematch: Original references unknown position %q", pos)
		}
		if prev, dup := used[pos]; dup {
			return nil, fmt.Errorf("rematch: Original assigns position %q to both %q and %q", pos, prev, e)
		}
		used[pos] = e
		orig[i] = j
	}
	assign := make([]int, n)
	for i := range assign {
		assign[i] = undecided
	}
	return &solver{n: n, m: m, allowed: allowed, orig: orig, assign: assign}, nil
}

// changeCost reports the objective-2 cost of assigning employee i to
// position j (j == unassigned means no position).
func (s *solver) changeCost(i, j int) int {
	if s.orig[i] != -1 && s.orig[i] != j {
		return 1
	}
	return 0
}

// remainderCost solves the subproblem over the still-undecided employees:
// route every one of them either to a free position or to "unassigned" so
// that the total matched count reaches s.matched, minimizing the change
// cost. It returns the minimum cost and whether the subproblem is feasible.
func (s *solver) remainderCost() (int, bool) {
	var rem []int
	usedPos := make([]bool, s.m)
	fixedMatched := 0
	for i := 0; i < s.n; i++ {
		switch {
		case s.assign[i] == undecided:
			rem = append(rem, i)
		case s.assign[i] >= 0:
			fixedMatched++
			usedPos[s.assign[i]] = true
		}
	}
	k := len(rem)
	requiredMatched := s.matched - fixedMatched
	dummyCap := k - requiredMatched
	if requiredMatched < 0 || dummyCap < 0 {
		return 0, false
	}
	// Node layout: source, employees, positions, dummy, sink.
	src := 0
	empBase := 1
	posBase := empBase + k
	dummy := posBase + s.m
	sink := dummy + 1
	g := newFlowGraph(sink + 1)
	for idx, i := range rem {
		u := empBase + idx
		g.addEdge(src, u, 1, 0)
		for j := 0; j < s.m; j++ {
			if s.allowed[i][j] && !usedPos[j] {
				g.addEdge(u, posBase+j, 1, s.changeCost(i, j))
			}
		}
		g.addEdge(u, dummy, 1, s.changeCost(i, unassigned))
	}
	for j := 0; j < s.m; j++ {
		if !usedPos[j] {
			g.addEdge(posBase+j, sink, 1, 0)
		}
	}
	g.addEdge(dummy, sink, dummyCap, 0)
	flow, cost := minCostFlow(g, src, sink, k)
	if flow < k {
		return 0, false
	}
	return cost, true
}

// fixVector fixes employees one by one, in employee order, each to the
// smallest feasible vector entry (positions in index order, unassigned
// last), keeping objectives 1 and 2 optimal.
func (s *solver) fixVector() error {
	costSoFar := 0
	for i := 0; i < s.n; i++ {
		chosen := undecided
		chosenCost := 0
		for j := 0; j < s.m && chosen == undecided; j++ {
			if !s.canTake(i, j) {
				continue
			}
			s.assign[i] = j
			if c, ok := s.remainderCost(); ok && costSoFar+s.changeCost(i, j)+c <= s.changes {
				chosen, chosenCost = j, s.changeCost(i, j)
			}
		}
		if chosen == undecided {
			s.assign[i] = unassigned
			c, ok := s.remainderCost()
			if !ok || costSoFar+s.changeCost(i, unassigned)+c > s.changes {
				return fmt.Errorf("rematch: internal error: no feasible entry for employee %d", i)
			}
			chosen, chosenCost = unassigned, s.changeCost(i, unassigned)
		}
		s.assign[i] = chosen
		costSoFar += chosenCost
	}
	return nil
}

// canTake reports whether employee i may take position j and j is not
// already taken by a fixed employee.
func (s *solver) canTake(i, j int) bool {
	if !s.allowed[i][j] {
		return false
	}
	for k := 0; k < s.n; k++ {
		if k != i && s.assign[k] == j {
			return false
		}
	}
	return true
}

func (s *solver) solution(p Problem) Solution {
	sol := Solution{
		Assignment: make(map[string]string),
		Matched:    s.matched,
		Changes:    s.changes,
	}
	newPos := make([]int, s.n)
	for i := 0; i < s.n; i++ {
		newPos[i] = s.assign[i]
		switch {
		case s.assign[i] >= 0:
			sol.Assignment[p.Employees[i]] = p.Positions[s.assign[i]]
		default:
			sol.Unmatched = append(sol.Unmatched, p.Employees[i])
			if s.orig[i] != -1 {
				sol.Displaced = append(sol.Displaced, p.Employees[i])
			}
		}
	}
	sol.Chains = buildChains(s.n, s.m, s.orig, newPos, p.Employees, p.Positions)
	return sol
}
