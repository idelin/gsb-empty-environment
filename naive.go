package csp

// NaiveSolver is a plain chronological backtracking solver, provided
// for comparison against CBJSolver.
type NaiveSolver struct {
	p           *Problem
	assignment  []int
	Assignments int // total value attempts
}

// NewNaiveSolver creates a chronological backtracking solver for p.
func NewNaiveSolver(p *Problem) *NaiveSolver {
	return &NaiveSolver{p: p, assignment: make([]int, p.NumVars)}
}

// Solve searches for a solution, returning the assignment if one exists.
func (s *NaiveSolver) Solve() ([]int, bool) {
	if !s.search(0) {
		return nil, false
	}
	out := make([]int, len(s.assignment))
	copy(out, s.assignment)
	return out, true
}

func (s *NaiveSolver) search(level int) bool {
	if level == s.p.NumVars {
		return true
	}
	for _, v := range s.p.Domains[level] {
		s.Assignments++
		if s.consistent(level, v) {
			s.assignment[level] = v
			if s.search(level + 1) {
				return true
			}
		}
	}
	return false
}

func (s *NaiveSolver) consistent(level, v int) bool {
	for _, ci := range s.p.byVar[level] {
		c := s.p.Constraints[ci]
		other := c.B
		if c.B == level {
			other = c.A
		}
		if other >= level {
			continue
		}
		var ok bool
		if c.A == level {
			ok = c.Check(v, s.assignment[other])
		} else {
			ok = c.Check(s.assignment[other], v)
		}
		if !ok {
			return false
		}
	}
	return true
}
