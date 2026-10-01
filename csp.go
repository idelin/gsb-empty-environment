// Package csp implements a constraint satisfaction problem (CSP) solver
// with conflict-directed backjumping (CBJ): on a dead end, the search
// jumps back to the deepest variable actually responsible for the
// conflict instead of chronologically undoing the previous variable.
package csp

import "fmt"

// Constraint is a binary constraint between variables A and B.
// Check must return true when the two values are mutually consistent.
type Constraint struct {
	A, B  int
	Name  string
	Check func(a, b int) bool
}

// Problem is a binary CSP with ordered domains.
type Problem struct {
	NumVars     int
	Domains     [][]int
	Constraints []Constraint

	byVar [][]int // constraint indexes per variable
}

// NewProblem creates a CSP over len(domains) variables.
// Variables are instantiated in index order (step i+1 assigns variable i).
func NewProblem(domains [][]int) *Problem {
	return &Problem{
		NumVars: len(domains),
		Domains: domains,
		byVar:   make([][]int, len(domains)),
	}
}

// AddConstraint adds a binary constraint between variables a and b.
func (p *Problem) AddConstraint(a, b int, name string, check func(a, b int) bool) {
	p.Constraints = append(p.Constraints, Constraint{A: a, B: b, Name: name, Check: check})
	idx := len(p.Constraints) - 1
	p.byVar[a] = append(p.byVar[a], idx)
	p.byVar[b] = append(p.byVar[b], idx)
}

// Validate reports whether assign satisfies every constraint.
func (p *Problem) Validate(assign []int) bool {
	for _, c := range p.Constraints {
		if !c.Check(assign[c.A], assign[c.B]) {
			return false
		}
	}
	return true
}

// NewBenchmarkProblem builds the 10-variable acceptance scenario:
//   - steps 1..10 assign variables X1..X10 in order;
//   - X8 (step 8) has a single candidate value and is constrained only
//     against X2 (step 2), so the dead end at step 8 is caused solely by
//     the value chosen at step 2;
//   - steps 3..7 form a benign inequality chain that always succeeds,
//     so a chronological solver wastes ~3^5 failed attempts there while
//     CBJ jumps straight from step 8 back to step 2.
func NewBenchmarkProblem() *Problem {
	domains := make([][]int, 10)
	for i := range domains {
		domains[i] = []int{1, 2, 3}
	}
	domains[7] = []int{1} // X8: single candidate value

	p := NewProblem(domains)
	// The only constraint touching X8: it clashes with X2 = 1.
	p.AddConstraint(1, 7, "X2 != X8", func(a, b int) bool { return a != b })
	// Benign chain among X3..X7: always satisfiable, never the culprit.
	for i := 2; i < 6; i++ {
		p.AddConstraint(i, i+1,
			fmt.Sprintf("X%d != X%d", i+1, i+2),
			func(a, b int) bool { return a != b })
	}
	return p
}
