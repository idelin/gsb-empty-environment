package netsec

import "strings"

// Formula is a Boolean expression over shared variables. It is produced by
// Interpolate and only ever references variables from the shared set.
type Formula interface {
	// Eval evaluates the formula under a total assignment of its variables.
	Eval(map[int]bool) bool
	// String renders a readable, parenthesized expression.
	String() string
	varsInto(map[int]struct{})
}

// Var is a Boolean variable reference.
type Var struct{ V int }

// Not is logical negation.
type Not struct{ F Formula }

// And is logical conjunction.
type And struct{ Fs []Formula }

// Or is logical disjunction.
type Or struct{ Fs []Formula }

// Const is a Boolean constant.
type Const struct{ V bool }

func (v Var) Eval(a map[int]bool) bool { return a[v.V] }

func (v Var) String() string { return "x" + itoa(v.V) }

func (n Not) Eval(a map[int]bool) bool { return !n.F.Eval(a) }

func (n Not) String() string { return "!" + renderAtom(n.F) }

func (a And) Eval(env map[int]bool) bool {
	for _, f := range a.Fs {
		if !f.Eval(env) {
			return false
		}
	}
	return true
}

func (a And) String() string {
	parts := make([]string, len(a.Fs))
	for i, f := range a.Fs {
		parts[i] = renderAtom(f)
	}
	return "(" + strings.Join(parts, " & ") + ")"
}

func (o Or) Eval(env map[int]bool) bool {
	for _, f := range o.Fs {
		if f.Eval(env) {
			return true
		}
	}
	return false
}

func (o Or) String() string {
	if len(o.Fs) == 0 {
		return "false"
	}
	parts := make([]string, len(o.Fs))
	for i, f := range o.Fs {
		parts[i] = renderAtom(f)
	}
	return "(" + strings.Join(parts, " | ") + ")"
}

func (c Const) Eval(map[int]bool) bool { return c.V }

func (c Const) String() string {
	if c.V {
		return "true"
	}
	return "false"
}

// renderAtom wraps compound expressions in parentheses and leaves atoms bare.
func renderAtom(f Formula) string {
	switch f.(type) {
	case Var, Const, Not:
		return f.String()
	default:
		return f.String()
	}
}

func (v Var) varsInto(m map[int]struct{}) { m[v.V] = struct{}{} }
func (n Not) varsInto(m map[int]struct{}) { n.F.varsInto(m) }
func (c Const) varsInto(map[int]struct{}) {}
func (a And) varsInto(m map[int]struct{}) {
	for _, f := range a.Fs {
		f.varsInto(m)
	}
}
func (o Or) varsInto(m map[int]struct{}) {
	for _, f := range o.Fs {
		f.varsInto(m)
	}
}

// Vars returns the sorted, deduplicated variables referenced by the formula.
func Vars(f Formula) []int {
	m := map[int]struct{}{}
	f.varsInto(m)
	out := make([]int, 0, len(m))
	for v := range m {
		out = append(out, v)
	}
	sortInts(out)
	return out
}

// conjunction builds a conjunction term from a full shared assignment. Each
// shared variable appears exactly once, in the polarity given by assignment.
func conjunction(shared []int, assignment []bool) Formula {
	lits := make([]Formula, len(shared))
	for i, v := range shared {
		if assignment[i] {
			lits[i] = Var{V: v}
		} else {
			lits[i] = Not{F: Var{V: v}}
		}
	}
	if len(lits) == 1 {
		return lits[0]
	}
	return And{Fs: lits}
}
