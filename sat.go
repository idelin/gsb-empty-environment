package netsec

type satStatus uint8

const (
	satUnknown satStatus = iota
	satTrue
	satFalse
)

// satSolver is a deliberately small DPLL implementation. The formulas in
// this package come from little compiler peepholes; clarity and exactness are
// more important here than competing with a production SAT solver.
type satSolver struct {
	clauses     [][]Lit
	assign      []int8
	trail       []Lit
	occurrences []int
	maxNodes    int
	maxConflict int
	nodes       int
	conflicts   int
	exhausted   bool
}

func solveCNF(b *cnfBuilder, maxNodes, maxConflicts int) (sat bool, model []int8, exhausted bool) {
	if b.unsat {
		return false, nil, false
	}
	s := &satSolver{
		clauses:     make([][]Lit, len(b.clauses)),
		assign:      make([]int8, b.maxVar()+1),
		occurrences: make([]int, b.maxVar()+1),
		maxNodes:    maxNodes,
		maxConflict: maxConflicts,
	}
	for i, clause := range b.clauses {
		s.clauses[i] = append([]Lit(nil), clause...)
		for _, l := range clause {
			if v := absLit(l); v < len(s.occurrences) {
				s.occurrences[v]++
			}
		}
	}

	conflict, allSat := s.propagate()
	if conflict {
		if s.exhausted {
			return false, nil, true
		}
		return false, nil, false
	}
	if allSat {
		return true, append([]int8(nil), s.assign...), false
	}

	switch s.search() {
	case satTrue:
		return true, append([]int8(nil), s.assign...), false
	case satFalse:
		return false, nil, false
	default:
		return false, nil, true
	}
}

func absLit(l Lit) int {
	if l < 0 {
		return int(-l)
	}
	return int(l)
}

func (s *satSolver) litValue(l Lit) int8 {
	switch l {
	case litFalse:
		return -1
	case litTrue:
		return 1
	}
	v := absLit(l)
	if v >= len(s.assign) {
		return 0
	}
	a := s.assign[v]
	if l < 0 {
		a = -a
	}
	return a
}

func (s *satSolver) enqueue(l Lit) bool {
	switch l {
	case litTrue:
		return true
	case litFalse:
		return false
	}
	v := absLit(l)
	if v >= len(s.assign) {
		return false
	}
	want := int8(1)
	if l < 0 {
		want = -1
	}
	if s.assign[v] != 0 {
		return s.assign[v] == want
	}
	s.assign[v] = want
	s.trail = append(s.trail, l)
	return true
}

func (s *satSolver) undo(mark int) {
	for i := len(s.trail) - 1; i >= mark; i-- {
		s.assign[absLit(s.trail[i])] = 0
	}
	s.trail = s.trail[:mark]
}

func (s *satSolver) propagate() (conflict, allSatisfied bool) {
	for {
		changed := false
		allSatisfied = true
		for _, clause := range s.clauses {
			satisfied := false
			freeLit := litFalse
			freeCount := 0
			for _, l := range clause {
				switch s.litValue(l) {
				case 1:
					satisfied = true
				case 0:
					freeCount++
					freeLit = l
				}
				if satisfied {
					break
				}
			}
			if satisfied {
				continue
			}
			allSatisfied = false
			if freeCount == 0 {
				s.conflicts++
				if s.maxConflict >= 0 && s.conflicts > s.maxConflict {
					s.exhausted = true
				}
				return true, false
			}
			if freeCount == 1 {
				if !s.enqueue(freeLit) {
					s.conflicts++
					if s.maxConflict >= 0 && s.conflicts > s.maxConflict {
						s.exhausted = true
					}
					return true, false
				}
				changed = true
			}
		}
		if !changed {
			return false, allSatisfied
		}
	}
}

func (s *satSolver) choose() Lit {
	bestVar := 0
	bestScore := -1
	for _, clause := range s.clauses {
		satisfied := false
		for _, l := range clause {
			if s.litValue(l) == 1 {
				satisfied = true
				break
			}
		}
		if satisfied {
			continue
		}
		for _, l := range clause {
			v := absLit(l)
			if v >= len(s.assign) || s.assign[v] != 0 {
				continue
			}
			if s.occurrences[v] > bestScore {
				bestVar = v
				bestScore = s.occurrences[v]
			}
		}
		if bestVar != 0 {
			break
		}
	}
	if bestVar == 0 {
		return litFalse
	}
	return Lit(bestVar)
}

func (s *satSolver) search() satStatus {
	if s.exhausted {
		return satUnknown
	}
	s.nodes++
	if s.maxNodes >= 0 && s.nodes > s.maxNodes {
		s.exhausted = true
		return satUnknown
	}

	conflict, allSat := s.propagate()
	if conflict {
		if s.exhausted {
			return satUnknown
		}
		return satFalse
	}
	if allSat {
		return satTrue
	}

	next := s.choose()
	if next == litFalse {
		return satFalse
	}
	mark := len(s.trail)

	if s.enqueue(next) {
		if res := s.search(); res != satFalse {
			return res
		}
	}
	s.undo(mark)

	if s.exhausted {
		return satUnknown
	}
	if s.enqueue(neg(next)) {
		if res := s.search(); res != satFalse {
			return res
		}
	}
	s.undo(mark)
	if s.exhausted {
		return satUnknown
	}
	return satFalse
}
