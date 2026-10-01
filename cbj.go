package csp

import "fmt"

// Backjump records a non-chronological jump from step From to step To
// (1-based), skipping the intermediate variables.
type Backjump struct {
	From, To int
}

func (b Backjump) String() string {
	return fmt.Sprintf("step %d -> step %d (skipped steps %d..%d)", b.From, b.To, b.To+1, b.From-1)
}

// Stats collects search metrics.
type Stats struct {
	Assignments int        // total value attempts
	Backjumps   []Backjump // non-chronological jumps (To < From-1)
}

// CBJSolver solves a Problem with conflict-directed backjumping.
//
// Every variable accumulates a conflict set: the past variables whose
// assignments ruled out one of its values. When a variable's domain is
// exhausted, the search jumps back to the deepest variable in its
// conflict set (merging the sets), instead of backing up one step.
type CBJSolver struct {
	p          *Problem
	assignment []int
	Stats      Stats
}

// NewCBJSolver creates a solver for p.
func NewCBJSolver(p *Problem) *CBJSolver {
	return &CBJSolver{p: p, assignment: make([]int, p.NumVars)}
}

// Solve searches for a solution, returning the assignment if one exists.
func (s *CBJSolver) Solve() ([]int, bool) {
	if _, ok := s.search(0); !ok {
		return nil, false
	}
	out := make([]int, len(s.assignment))
	copy(out, s.assignment)
	return out, true
}

// search assigns variables level..n-1. On failure it returns the
// conflict set: the indices of the past variables responsible for the
// dead end. The caller jumps back to the deepest of them.
func (s *CBJSolver) search(level int) (map[int]struct{}, bool) {
	if level == s.p.NumVars {
		return nil, true
	}
	conf := map[int]struct{}{}
	for _, v := range s.p.Domains[level] {
		s.Stats.Assignments++
		if culprits := s.conflicts(level, v); len(culprits) > 0 {
			// v is inconsistent: blame the past variables that clash with it.
			for c := range culprits {
				conf[c] = struct{}{}
			}
			continue
		}
		s.assignment[level] = v
		childConf, ok := s.search(level + 1)
		if ok {
			return nil, true
		}
		if _, blamed := childConf[level]; !blamed {
			// The deeper failure does not involve this variable:
			// propagate the conflict set upward, jumping over level.
			return childConf, false
		}
		// This variable is blamed: absorb the child's conflict set
		// (minus itself) and try the next value.
		delete(childConf, level)
		for c := range childConf {
			conf[c] = struct{}{}
		}
	}
	if len(conf) > 0 {
		target := 0
		for c := range conf {
			if c > target {
				target = c
			}
		}
		if target < level-1 {
			s.Stats.Backjumps = append(s.Stats.Backjumps, Backjump{From: level + 1, To: target + 1})
		}
	}
	return conf, false
}

// conflicts returns the past variables whose current assignments clash
// with assigning v to variable level.
func (s *CBJSolver) conflicts(level, v int) map[int]struct{} {
	var out map[int]struct{}
	for _, ci := range s.p.byVar[level] {
		c := s.p.Constraints[ci]
		other := c.B
		if c.B == level {
			other = c.A
		}
		if other >= level {
			continue // future variable, not assigned yet
		}
		var ok bool
		if c.A == level {
			ok = c.Check(v, s.assignment[other])
		} else {
			ok = c.Check(s.assignment[other], v)
		}
		if !ok {
			if out == nil {
				out = map[int]struct{}{}
			}
			out[other] = struct{}{}
		}
	}
	return out
}
