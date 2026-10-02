package mini

import (
	"fmt"
	"sort"
	"strings"
)

// Pos is a 1-based source position.
type Pos struct {
	Line, Col int
}

func (p Pos) String() string { return fmt.Sprintf("%d:%d", p.Line, p.Col) }

// Type is the (synthesized) type attribute of an expression.
type Type int

const (
	TError Type = iota
	TInt
	TFloat
	TBool
	TString
)

func (t Type) String() string {
	switch t {
	case TInt:
		return "int"
	case TFloat:
		return "float"
	case TBool:
		return "bool"
	case TString:
		return "string"
	default:
		return "<error>"
	}
}

func numeric(t Type) bool { return t == TInt || t == TFloat }

// promote computes the common numeric type: int+float -> float.
func promote(a, b Type) Type {
	if a == TFloat || b == TFloat {
		return TFloat
	}
	return TInt
}

// assignable reports whether a value of type from may initialize a variable
// declared with type to (int widens to float).
func assignable(to, from Type) bool {
	return to == from || (to == TFloat && from == TInt)
}

// Env is the (inherited/synthesized) symbol-table attribute mapping variable
// names to their types. It is immutable: Extend returns a copy.
type Env struct {
	vars map[string]Type
}

func NewEnv() *Env { return &Env{vars: make(map[string]Type)} }

func (e *Env) Lookup(name string) (Type, bool) {
	t, ok := e.vars[name]
	return t, ok
}

func (e *Env) Extend(name string, t Type) *Env {
	vars := make(map[string]Type, len(e.vars)+1)
	for k, v := range e.vars {
		vars[k] = v
	}
	vars[name] = t
	return &Env{vars: vars}
}

func (e *Env) String() string {
	names := make([]string, 0, len(e.vars))
	for n := range e.vars {
		names = append(names, n)
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = fmt.Sprintf("%s: %s", n, e.vars[n])
	}
	return "{" + strings.Join(parts, ", ") + "}"
}
