package netsec

// solverState is the internal, mutable state of the construction-time DPLL
// solver. It is deliberately separate from the enumeration-time checker in
// verify.go so the two reasoning engines do not share implementation details.
type solverState struct {
	nVars int

	// clauses is the formula being solved, kept inside the state so every
	// solver invocation is fully self-contained and safe for concurrent use.
	clauses CNF

	// assignment[v] is 0 (unassigned), 1 (true) or -1 (false).
	assignment []int

	// openCount[c] is the number of literals of clause c that are not yet
	// falsified. The clause is a unit when it drops to 1 while unsat.
	openCount []int

	// trueCount[c] is the number of currently satisfied literals.
	trueCount []int

	// occurrences maps each literal to the clauses containing it.
	occurrences map[Literal][]int

	// trail holds every assigned literal in assignment order.
	trail []Literal

	// queue holds literals still waiting for unit propagation.
	queue []Literal
}

// solveCNF decides f under the given assumption literals. When satisfiable it
// also returns a full (unassigned variables default to false) model.
func solveCNF(f CNF, assumptions []Literal) (bool, map[int]bool) {
	n := f.MaxVar()
	for _, l := range assumptions {
		if v := l.Var(); v > n {
			n = v
		}
	}
	st := &solverState{
		nVars:       n,
		clauses:     f,
		assignment:  make([]int, n+1),
		openCount:   make([]int, len(f)),
		trueCount:   make([]int, len(f)),
		occurrences: make(map[Literal][]int),
	}

	for ci, c := range f {
		if len(c) == 0 {
			return false, nil
		}
		st.openCount[ci] = len(c)
		for _, l := range c {
			st.occurrences[l] = append(st.occurrences[l], ci)
		}
	}

	for _, a := range assumptions {
		if !st.push(a) {
			return false, nil
		}
	}
	if !st.propagate() {
		return false, nil
	}
	if !st.search() {
		return false, nil
	}

	model := make(map[int]bool, n)
	for v := 1; v <= n; v++ {
		model[v] = st.assignment[v] == 1
	}
	return true, model
}

// push records the assignment implied by literal l at the given level. It
// reports false when the variable is already assigned the opposite value.
func (s *solverState) push(l Literal) bool {
	v := l.Var()
	want := 1
	if !l.IsPositive() {
		want = -1
	}
	switch s.assignment[v] {
	case 0:
		s.assignment[v] = want
		s.trail = append(s.trail, l)
		s.queue = append(s.queue, l)
		for _, ci := range s.occurrences[l] {
			s.trueCount[ci]++
		}
		for _, ci := range s.occurrences[l.Not()] {
			s.openCount[ci]--
		}
		return true
	case want:
		return true
	default:
		return false
	}
}

// propagate drains the literal queue, firing unit clauses. On conflict it
// undoes the work performed since propagation started and reports false.
func (s *solverState) propagate() bool {
	mark := len(s.trail)
	for len(s.queue) > 0 {
		l := s.queue[0]
		s.queue = s.queue[1:]

		for _, ci := range s.occurrences[l.Not()] {
			if s.trueCount[ci] > 0 {
				continue
			}
			if s.openCount[ci] == 0 {
				s.rollback(mark)
				return false
			}
			if s.openCount[ci] == 1 {
				var unit Literal
				for _, cand := range s.clauses[ci] {
					if s.assignment[cand.Var()] == 0 {
						unit = cand
						break
					}
				}
				if unit == 0 || !s.push(unit) {
					s.rollback(mark)
					return false
				}
			}
		}
	}
	return true
}

// search performs chronological backtracking over unassigned variables.
func (s *solverState) search() bool {
	v := s.firstUnassigned()
	if v == 0 {
		return true
	}

	for _, sign := range []bool{true, false} {
		l := Literal(v)
		if !sign {
			l = -l
		}
		mark := len(s.trail)
		if !s.push(l) {
			continue
		}
		if s.propagate() && s.search() {
			return true
		}
		s.rollback(mark)
	}
	return false
}

// rollback restores the state held before trail position mark.
func (s *solverState) rollback(mark int) {
	for len(s.trail) > mark {
		last := len(s.trail) - 1
		l := s.trail[last]
		v := l.Var()

		for _, ci := range s.occurrences[l] {
			s.trueCount[ci]--
		}
		for _, ci := range s.occurrences[l.Not()] {
			s.openCount[ci]++
		}
		s.assignment[v] = 0
		s.trail = s.trail[:last]
	}
	s.queue = s.queue[:0]
}

func (s *solverState) firstUnassigned() int {
	for v := 1; v <= s.nVars; v++ {
		if s.assignment[v] == 0 {
			return v
		}
	}
	return 0
}
