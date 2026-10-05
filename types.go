package main

import (
	"fmt"
	"sort"
	"strings"
)

// State maps variable names to their integer values.
type State map[string]int64

func (s State) String() string {
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, s[k]))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// Constraint is a linear integer constraint: sum(coeffs[v]*v) op const.
type Constraint struct {
	Coeffs map[string]int64 `json:"coeffs"`
	Op     string           `json:"op"`
	Const  int64            `json:"const"`
}

func (c Constraint) lhs(s State) int64 {
	sum := int64(0)
	for v, coeff := range c.Coeffs {
		sum += coeff * s[v]
	}
	return sum
}

// Holds reports whether the constraint is satisfied in the given state.
func (c Constraint) Holds(s State) bool {
	l := c.lhs(s)
	switch c.Op {
	case "<=":
		return l <= c.Const
	case "<":
		return l < c.Const
	case ">=":
		return l >= c.Const
	case ">":
		return l > c.Const
	case "==":
		return l == c.Const
	case "!=":
		return l != c.Const
	}
	return false
}

func validOp(op string) bool {
	switch op {
	case "<=", "<", ">=", ">", "==", "!=":
		return true
	}
	return false
}

func (c Constraint) String() string {
	keys := make([]string, 0, len(c.Coeffs))
	for k := range c.Coeffs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%d*%s", c.Coeffs[k], k))
	}
	return fmt.Sprintf("%s %s %d", strings.Join(parts, " + "), c.Op, c.Const)
}

// Assignment is a parallel affine update: var := sum(coeffs[v]*v) + const.
// All right-hand sides are evaluated in the pre-state.
type Assignment struct {
	Var    string           `json:"var"`
	Coeffs map[string]int64 `json:"coeffs"`
	Const  int64            `json:"const"`
}

// Bound is an inclusive integer range.
type Bound struct {
	Min int64 `json:"min"`
	Max int64 `json:"max"`
}

// Bounds gives a default range plus optional per-variable overrides.
type Bounds struct {
	Min  int64            `json:"min"`
	Max  int64            `json:"max"`
	Vars map[string]Bound `json:"vars,omitempty"`
}

func (b Bounds) forVar(v string) Bound {
	if vb, ok := b.Vars[v]; ok {
		return vb
	}
	return Bound{Min: b.Min, Max: b.Max}
}

// Program is the full checker input.
type Program struct {
	Variables []string     `json:"variables"`
	Bounds    Bounds       `json:"bounds"`
	Init      []Constraint `json:"init"`
	Guard     []Constraint `json:"guard"`
	Assign    []Assignment `json:"assign"`
	Invariant []Constraint `json:"invariant"`
	Post      []Constraint `json:"post"`
}

func allHold(cs []Constraint, s State) bool {
	for _, c := range cs {
		if !c.Holds(s) {
			return false
		}
	}
	return true
}

// Validate checks structural well-formedness of the program.
func (p *Program) Validate() error {
	if len(p.Variables) == 0 {
		return fmt.Errorf("no variables declared")
	}
	seen := map[string]bool{}
	for _, v := range p.Variables {
		if seen[v] {
			return fmt.Errorf("duplicate variable %q", v)
		}
		seen[v] = true
		b := p.Bounds.forVar(v)
		if b.Min > b.Max {
			return fmt.Errorf("variable %q: empty range [%d, %d]", v, b.Min, b.Max)
		}
	}
	checkConstraints := func(name string, cs []Constraint) error {
		for i, c := range cs {
			if !validOp(c.Op) {
				return fmt.Errorf("%s[%d]: invalid op %q", name, i, c.Op)
			}
			for v := range c.Coeffs {
				if !seen[v] {
					return fmt.Errorf("%s[%d]: unknown variable %q", name, i, v)
				}
			}
		}
		return nil
	}
	for _, item := range []struct {
		name string
		cs   []Constraint
	}{
		{"init", p.Init}, {"guard", p.Guard},
		{"invariant", p.Invariant}, {"post", p.Post},
	} {
		if err := checkConstraints(item.name, item.cs); err != nil {
			return err
		}
	}
	assigned := map[string]bool{}
	for i, a := range p.Assign {
		if !seen[a.Var] {
			return fmt.Errorf("assign[%d]: unknown variable %q", i, a.Var)
		}
		if assigned[a.Var] {
			return fmt.Errorf("assign[%d]: variable %q assigned twice", i, a.Var)
		}
		assigned[a.Var] = true
		for v := range a.Coeffs {
			if !seen[v] {
				return fmt.Errorf("assign[%d]: unknown variable %q", i, v)
			}
		}
	}
	return nil
}

// step applies the parallel assignments to s. Unassigned variables keep
// their values. The result is the post-state.
func (p *Program) step(s State) State {
	next := State{}
	for _, v := range p.Variables {
		next[v] = s[v]
	}
	for _, a := range p.Assign {
		val := a.Const
		for v, c := range a.Coeffs {
			val += c * s[v]
		}
		next[a.Var] = val
	}
	return next
}

// inRange reports whether every variable of s lies within its bound.
func (p *Program) inRange(s State) bool {
	for _, v := range p.Variables {
		b := p.Bounds.forVar(v)
		if s[v] < b.Min || s[v] > b.Max {
			return false
		}
	}
	return true
}
