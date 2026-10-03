// Package csp implements a finite-domain constraint solver with
// conflict-directed backjumping.
package csp

import (
	"fmt"
	"sort"
)

// Variable is a finite-domain CSP variable.
type Variable struct {
	Name   string
	Domain []int
}

// BinaryConstraint describes an allowed relation between two variables.
// Allow is called with values in Left, Right order.
type BinaryConstraint struct {
	Left  int
	Right int
	Allow func(leftValue, rightValue int) bool
}

// Problem is a binary CSP. Variables are instantiated in slice order.
type Problem struct {
	Variables   []Variable
	Constraints []BinaryConstraint
}

// Backjump records a non-chronological return from a dead end.
// Indexes are zero-based variable indexes.
type Backjump struct {
	From        int
	To          int
	ConflictSet []int
	Skipped     []int
}

// Stats contains search instrumentation collected by Solve.
type Stats struct {
	Assignments       int
	ConsistencyChecks int
	RejectedValues    int
	Backjumps         []Backjump
}

// Result is the outcome of a Solve call.
type Result struct {
	Solved     bool
	Assignment []int
	Stats      Stats
}

// Solve searches for one solution using conflict-directed backjumping.
func Solve(problem Problem) (Result, error) {
	if err := validateProblem(problem); err != nil {
		return Result{}, err
	}

	solver := &solver{
		problem:    problem,
		assignment: make([]int, len(problem.Variables)),
		assigned:   make([]bool, len(problem.Variables)),
	}

	solved, _ := solver.search(0)
	result := Result{
		Solved: solved,
		Stats:  solver.stats,
	}
	if solved {
		result.Assignment = append([]int(nil), solver.assignment...)
	}
	return result, nil
}

func validateProblem(problem Problem) error {
	for i, constraint := range problem.Constraints {
		if constraint.Left < 0 || constraint.Left >= len(problem.Variables) ||
			constraint.Right < 0 || constraint.Right >= len(problem.Variables) {
			return fmt.Errorf("constraint %d references a variable outside [0, %d)", i, len(problem.Variables))
		}
		if constraint.Left == constraint.Right {
			return fmt.Errorf("constraint %d references the same variable twice", i)
		}
		if constraint.Allow == nil {
			return fmt.Errorf("constraint %d has a nil Allow function", i)
		}
	}
	return nil
}

type solver struct {
	problem    Problem
	assignment []int
	assigned   []bool
	stats      Stats
}

// search returns a conflict set when it fails. The set contains earlier
// variables whose current values explain why no value at this subtree works.
func (s *solver) search(level int) (bool, map[int]bool) {
	if level == len(s.problem.Variables) {
		return true, nil
	}

	conflictSet := make(map[int]bool)
	for _, value := range s.problem.Variables[level].Domain {
		consistent, valueConflicts := s.checkConsistency(level, value)
		if !consistent {
			s.stats.RejectedValues++
			union(conflictSet, valueConflicts)
			continue
		}

		s.assignment[level] = value
		s.assigned[level] = true
		s.stats.Assignments++

		solved, childConflicts := s.search(level + 1)
		if solved {
			return true, nil
		}

		s.assigned[level] = false
		if !childConflicts[level] {
			// The dead end is independent of this variable, so this level is
			// skipped without trying the rest of its domain.
			return false, childConflicts
		}

		// This variable is the deepest member of the child's conflict set.
		// Try another value here, retaining the child's other causes in case
		// this variable's domain is exhausted too.
		delete(childConflicts, level)
		union(conflictSet, childConflicts)
	}

	if len(conflictSet) > 0 {
		to := deepest(conflictSet)
		s.stats.Backjumps = append(s.stats.Backjumps, Backjump{
			From:        level,
			To:          to,
			ConflictSet: sortedSet(conflictSet),
			Skipped:     skippedRange(to+1, level),
		})
	}
	return false, conflictSet
}

func (s *solver) checkConsistency(level int, value int) (bool, map[int]bool) {
	conflicts := make(map[int]bool)
	for _, constraint := range s.problem.Constraints {
		other := -1
		allowed := false

		if constraint.Left == level && s.assigned[constraint.Right] {
			other = constraint.Right
			allowed = constraint.Allow(value, s.assignment[other])
		} else if constraint.Right == level && s.assigned[constraint.Left] {
			other = constraint.Left
			allowed = constraint.Allow(s.assignment[other], value)
		} else {
			continue
		}

		s.stats.ConsistencyChecks++
		if !allowed {
			conflicts[other] = true
		}
	}
	return len(conflicts) == 0, conflicts
}

func union(destination, source map[int]bool) {
	for value := range source {
		destination[value] = true
	}
}

func deepest(conflictSet map[int]bool) int {
	deepest := -1
	for variable := range conflictSet {
		if variable > deepest {
			deepest = variable
		}
	}
	return deepest
}

func sortedSet(set map[int]bool) []int {
	values := make([]int, 0, len(set))
	for value := range set {
		values = append(values, value)
	}
	sort.Ints(values)
	return values
}

func skippedRange(start, end int) []int {
	if start >= end {
		return nil
	}
	skipped := make([]int, 0, end-start)
	for variable := start; variable < end; variable++ {
		skipped = append(skipped, variable)
	}
	return skipped
}
