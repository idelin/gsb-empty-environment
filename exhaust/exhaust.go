// Package exhaust is an independent satisfiability checker. It verifies
// claimed minimal unsatisfiable subsets by exhaustive enumeration of the
// entire search space, sharing no search or evaluation code with the
// diagnoser: constraints are recompiled here directly from the parsed
// JSON, and enumeration is a full recursive sweep with no early exit.
package exhaust

import (
	"errors"
	"fmt"

	"configdiag/instance"
)

// DefaultMaxAssignments bounds one exhaustive enumeration so a
// pathological instance cannot hang the checker. Exceeding the limit
// aborts with an error instead of producing a (false) proof.
const DefaultMaxAssignments = 100000000

// errLimit is returned when an enumeration exceeds the assignment limit.
var errLimit = errors.New("assignment limit exceeded")

// constraint is this package's own compiled form of a table constraint,
// built directly from the parsed instance.
type constraint struct {
	name    string
	vars    []string
	allowed map[string]struct{}
}

func compile(inst *instance.Instance) []constraint {
	out := make([]constraint, len(inst.Constraints))
	for i, c := range inst.Constraints {
		set := make(map[string]struct{}, len(c.Allowed))
		for _, t := range c.Allowed {
			set[fmt.Sprintf("%q", t)] = struct{}{}
		}
		out[i] = constraint{name: c.Name, vars: c.Vars, allowed: set}
	}
	return out
}

func holds(c *constraint, assign instance.Assignment) bool {
	tuple := make([]string, len(c.vars))
	for i, v := range c.vars {
		tuple[i] = assign[v]
	}
	_, ok := c.allowed[fmt.Sprintf("%q", tuple)]
	return ok
}

// Search summarizes one exhaustive enumeration.
type Search struct {
	Assignments int64               // total assignments enumerated
	Satisfying  int64               // assignments satisfying all constraints
	Witness     instance.Assignment // first satisfying assignment, if any
}

// run enumerates the full cartesian product of the domains of the
// variables referenced by cons (variables in declaration order, values in
// domain order) and counts the satisfying assignments. It never exits
// early, so Assignments always equals the size of the search space.
func run(inst *instance.Instance, cons []*constraint, limit int64) (*Search, error) {
	inScope := map[string]bool{}
	for _, c := range cons {
		for _, v := range c.vars {
			inScope[v] = true
		}
	}
	var vars []instance.Variable
	for _, v := range inst.Variables {
		if inScope[v.Name] {
			vars = append(vars, v)
		}
	}
	s := &Search{}
	assign := make(instance.Assignment, len(vars))
	var rec func(k int) error
	rec = func(k int) error {
		if k == len(vars) {
			s.Assignments++
			if s.Assignments > limit {
				return errLimit
			}
			ok := true
			for _, c := range cons {
				if !holds(c, assign) {
					ok = false
					break
				}
			}
			if ok {
				s.Satisfying++
				if s.Witness == nil {
					w := make(instance.Assignment, len(assign))
					for name, val := range assign {
						w[name] = val
					}
					s.Witness = w
				}
			}
			return nil
		}
		for _, val := range vars[k].Domain {
			assign[vars[k].Name] = val
			if err := rec(k + 1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := rec(0); err != nil {
		if err == errLimit {
			return nil, fmt.Errorf("exhaustive enumeration aborted: more than %d assignments", limit)
		}
		return nil, err
	}
	return s, nil
}

// pick resolves constraint names to this package's compiled constraints,
// preserving the given order.
func pick(inst *instance.Instance, names []string) ([]*constraint, error) {
	all := compile(inst)
	byName := make(map[string]int, len(all))
	for i := range all {
		byName[all[i].name] = i
	}
	seen := map[string]bool{}
	var picked []*constraint
	for _, n := range names {
		idx, ok := byName[n]
		if !ok {
			return nil, fmt.Errorf("unknown constraint %q", n)
		}
		if seen[n] {
			return nil, fmt.Errorf("duplicate constraint %q", n)
		}
		seen[n] = true
		picked = append(picked, &all[idx])
	}
	return picked, nil
}

// Check exhaustively tests the named constraints for satisfiability.
func Check(inst *instance.Instance, names []string, limit int64) (*Search, error) {
	if limit <= 0 {
		limit = DefaultMaxAssignments
	}
	picked, err := pick(inst, names)
	if err != nil {
		return nil, err
	}
	return run(inst, picked, limit)
}

// RemovalCheck reports the outcome for the candidate MUS with one
// constraint dropped.
type RemovalCheck struct {
	Dropped     string
	Satisfiable bool
	Assignments int64
	Witness     instance.Assignment
}

// Verification is the full independent check of a claimed MUS.
type Verification struct {
	MUS              []string
	WholeUnsat       bool
	WholeAssignments int64
	Removals         []RemovalCheck
	OK               bool
}

// VerifyMUS proves by exhaustive enumeration that the named constraints
// form an inclusion-minimal unsatisfiable subset of inst:
//
//  1. the whole subset has no satisfying assignment (unsatisfiable), and
//  2. dropping any single member leaves a satisfiable set (minimal under
//     inclusion, witnessed by a concrete assignment).
func VerifyMUS(inst *instance.Instance, names []string, limit int64) (*Verification, error) {
	if limit <= 0 {
		limit = DefaultMaxAssignments
	}
	if len(names) == 0 {
		return nil, errors.New("empty constraint set is not an unsatisfiable subset")
	}
	picked, err := pick(inst, names)
	if err != nil {
		return nil, err
	}
	v := &Verification{MUS: append([]string(nil), names...)}
	whole, err := run(inst, picked, limit)
	if err != nil {
		return nil, err
	}
	v.WholeUnsat = whole.Satisfying == 0
	v.WholeAssignments = whole.Assignments
	ok := v.WholeUnsat
	for i, c := range picked {
		rest := make([]*constraint, 0, len(picked)-1)
		rest = append(rest, picked[:i]...)
		rest = append(rest, picked[i+1:]...)
		s, err := run(inst, rest, limit)
		if err != nil {
			return nil, err
		}
		rc := RemovalCheck{
			Dropped:     c.name,
			Satisfiable: s.Satisfying > 0,
			Assignments: s.Assignments,
			Witness:     s.Witness,
		}
		v.Removals = append(v.Removals, rc)
		if !rc.Satisfiable {
			ok = false
		}
	}
	v.OK = ok
	return v, nil
}
