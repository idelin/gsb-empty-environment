// Package plancmp compares an old, partially executed plan with a new plan
// and computes the minimal set of supplementary operations needed to reach
// the new goal, without silently revoking effects that were already committed
// by executed operations.
//
// The world state is a bitmask of up to MaxVars boolean variables, so the
// reachable space is exhaustively searchable with breadth-first search.
package plancmp

import (
	"fmt"
	"sort"
)

// MaxVars bounds the number of boolean state variables.
const MaxVars = 12

// MaxOps bounds the number of operations tracked during a search.
const MaxOps = 64

// State packs up to MaxVars boolean variables into a bitmask.
type State uint16

// Assign is a partial assignment of variable indices to values.
type Assign map[int]bool

// Op is a single operation: it may run once its dependencies have executed
// and its preconditions hold, and then applies its effects to the state.
type Op struct {
	Name string
	Deps []string
	Pre  Assign
	Eff  Assign
}

// Plan is a set of named operations.
type Plan struct {
	Ops map[string]Op
}

// NewPlan builds a Plan from a list of operations. Duplicate or invalid
// definitions are reported as errors. Dependency targets are not checked
// here: they may reference operations of the other group and are resolved
// against the merged pool at MergePlans/Reconcile/Verify time.
func NewPlan(ops ...Op) (*Plan, error) {
	p := &Plan{Ops: make(map[string]Op, len(ops))}
	for _, op := range ops {
		if op.Name == "" {
			return nil, fmt.Errorf("plancmp: operation with empty name")
		}
		if _, dup := p.Ops[op.Name]; dup {
			return nil, fmt.Errorf("plancmp: duplicate operation %q", op.Name)
		}
		p.Ops[op.Name] = op
	}
	if err := p.validateShape(); err != nil {
		return nil, err
	}
	return p, nil
}

// MergePlans combines the old and new operation groups into the search pool.
// Identical duplicate definitions are allowed; conflicting definitions of the
// same name are an error, since the pool would be ambiguous.
func MergePlans(old, newPlan *Plan) (*Plan, error) {
	merged := &Plan{Ops: make(map[string]Op)}
	for _, src := range []*Plan{old, newPlan} {
		if src == nil {
			continue
		}
		for name, op := range src.Ops {
			if prev, ok := merged.Ops[name]; ok {
				if !equalOp(prev, op) {
					return nil, fmt.Errorf("plancmp: conflicting definitions of operation %q", name)
				}
				continue
			}
			merged.Ops[name] = op
		}
	}
	if err := merged.validate(); err != nil {
		return nil, err
	}
	return merged, nil
}

// validateShape checks everything that does not require knowing the full
// operation pool: operation count, names, variable ranges and self-deps.
func (p *Plan) validateShape() error {
	if len(p.Ops) > MaxOps {
		return fmt.Errorf("plancmp: %d operations exceed limit %d", len(p.Ops), MaxOps)
	}
	for name, op := range p.Ops {
		if op.Name != name {
			return fmt.Errorf("plancmp: operation %q registered as %q", op.Name, name)
		}
		if err := checkAssign(op.Pre); err != nil {
			return fmt.Errorf("plancmp: op %q preconditions: %v", name, err)
		}
		if err := checkAssign(op.Eff); err != nil {
			return fmt.Errorf("plancmp: op %q effects: %v", name, err)
		}
		for _, dep := range op.Deps {
			if dep == name {
				return fmt.Errorf("plancmp: op %q depends on itself", name)
			}
		}
	}
	return nil
}

// validate extends validateShape by resolving every dependency target
// against the complete pool.
func (p *Plan) validate() error {
	if err := p.validateShape(); err != nil {
		return err
	}
	for name, op := range p.Ops {
		for _, dep := range op.Deps {
			if _, ok := p.Ops[dep]; !ok {
				return fmt.Errorf("plancmp: op %q depends on unknown op %q", name, dep)
			}
		}
	}
	return nil
}

func checkAssign(a Assign) error {
	for v := range a {
		if v < 0 || v >= MaxVars {
			return fmt.Errorf("variable %d out of range [0,%d)", v, MaxVars)
		}
	}
	return nil
}

func equalOp(a, b Op) bool {
	return a.Name == b.Name &&
		equalDeps(a.Deps, b.Deps) &&
		equalAssign(a.Pre, b.Pre) &&
		equalAssign(a.Eff, b.Eff)
}

func equalDeps(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	as := sortedCopy(a)
	bs := sortedCopy(b)
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

func equalAssign(a, b Assign) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok || va != vb {
			return false
		}
	}
	return true
}

func sortedCopy(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	sort.Strings(out)
	return out
}

func (s State) bit(v int) bool { return s&(1<<uint(v)) != 0 }

func (s State) apply(eff Assign) State {
	for v, val := range eff {
		if val {
			s |= 1 << uint(v)
		} else {
			s &^= 1 << uint(v)
		}
	}
	return s
}

func (a Assign) satisfiedBy(s State) bool {
	for v, want := range a {
		if s.bit(v) != want {
			return false
		}
	}
	return true
}

// conflictsWith reports whether applying eff would reset a committed variable
// to a different value.
func conflictsWith(eff Assign, commits Assign) bool {
	for v, val := range eff {
		if cv, ok := commits[v]; ok && cv != val {
			return true
		}
	}
	return false
}
