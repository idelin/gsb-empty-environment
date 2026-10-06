// Package diag implements the configuration diagnoser: it reduces an
// unsatisfiable constraint set to an inclusion-minimal unsatisfiable
// subset (MUS) by deletion, considering constraints in input order.
//
// "Minimal" here means minimality under inclusion: removing any single
// remaining constraint makes the subset satisfiable. It does not mean
// minimum cardinality; the result is one MUS, determined by the input
// order of the constraints.
package diag

import (
	"fmt"

	"configdiag/instance"
)

// MaxConstraints is the largest instance the diagnoser accepts.
const MaxConstraints = 20

// Step records one reduction decision, in the order it was made.
type Step struct {
	Constraint string `json:"constraint"`
	Kept       bool   `json:"kept"`
	Reason     string `json:"reason"`
}

// Result is the diagnoser report.
type Result struct {
	Status string   `json:"status"` // "unsatisfiable" or "satisfiable"
	MUS    []string `json:"mus,omitempty"`
	Steps  []Step   `json:"reduction"`
}

// FirstSatisfying enumerates the cartesian product of the domains of the
// variables in the scope of set (variables in declaration order, values in
// domain order) and returns the first assignment satisfying every
// constraint in set. It reports false if none exists.
func FirstSatisfying(c *instance.Compiled, set []int) (instance.Assignment, bool) {
	scope := c.Scope(set)
	idx := make([]int, len(scope))
	assign := make(instance.Assignment, len(scope))
	for {
		for i, vi := range scope {
			v := c.Inst.Variables[vi]
			assign[v.Name] = v.Domain[idx[i]]
		}
		if c.SatisfiesAll(set, assign) {
			return assign, true
		}
		i := len(scope) - 1
		for ; i >= 0; i-- {
			idx[i]++
			if idx[i] < len(c.Inst.Variables[scope[i]].Domain) {
				break
			}
			idx[i] = 0
		}
		if i < 0 {
			return nil, false
		}
	}
}

func satisfiable(c *instance.Compiled, set []int) bool {
	_, ok := FirstSatisfying(c, set)
	return ok
}

// FindMUS reduces the constraint set of inst to an inclusion-minimal
// unsatisfiable subset. Constraints are considered for removal in input
// order; a constraint is dropped exactly when the set remaining after its
// removal is still unsatisfiable. Because each decision depends only on
// the current set and the input order, the whole reduction is
// deterministic for a given input.
func FindMUS(inst *instance.Instance) (*Result, error) {
	if len(inst.Constraints) > MaxConstraints {
		return nil, fmt.Errorf("instance has %d constraints, maximum is %d",
			len(inst.Constraints), MaxConstraints)
	}
	c := instance.Compile(inst)
	set := make([]int, len(inst.Constraints))
	for i := range inst.Constraints {
		set[i] = i
	}
	res := &Result{Steps: []Step{}}
	if satisfiable(c, set) {
		res.Status = "satisfiable"
		return res, nil
	}
	res.Status = "unsatisfiable"
	for i := range inst.Constraints {
		trial := remove(set, i)
		if satisfiable(c, trial) {
			res.Steps = append(res.Steps, Step{
				Constraint: inst.Constraints[i].Name,
				Kept:       true,
				Reason:     "removing it makes the remaining set satisfiable",
			})
		} else {
			set = trial
			res.Steps = append(res.Steps, Step{
				Constraint: inst.Constraints[i].Name,
				Kept:       false,
				Reason:     "still unsatisfiable without it",
			})
		}
	}
	for _, ci := range set {
		res.MUS = append(res.MUS, inst.Constraints[ci].Name)
	}
	return res, nil
}

func remove(set []int, drop int) []int {
	out := make([]int, 0, len(set))
	for _, ci := range set {
		if ci != drop {
			out = append(out, ci)
		}
	}
	return out
}
