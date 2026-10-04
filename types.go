package unify

import "strings"

// Kind discriminates the four forms of types the solver understands.
type Kind int

const (
	// Var is a flexible unification variable; it may be bound to any type.
	Var Kind = iota
	// Rigid is an explicit rigid (skolem) variable; it unifies only with a
	// rigid variable of the same name and can never be bound to an
	// arbitrary type.
	Rigid
	// Fun is a function type Args[0] -> Args[1].
	Fun
	// Tuple is a product type (Args[0], ..., Args[n-1]).
	Tuple
)

// Type is an immutable type term. Variable nodes that share a Name are
// considered the same variable even if they are distinct pointers.
type Type struct {
	Kind Kind
	Name string
	Args []*Type
}

// V builds a flexible variable.
func V(name string) *Type { return &Type{Kind: Var, Name: name} }

// R builds an explicit rigid variable.
func R(name string) *Type { return &Type{Kind: Rigid, Name: name} }

// Fn builds the function type a -> b.
func Fn(a, b *Type) *Type { return &Type{Kind: Fun, Args: []*Type{a, b}} }

// Tup builds a tuple type.
func Tup(elems ...*Type) *Type { return &Type{Kind: Tuple, Args: elems} }

func (t *Type) String() string {
	switch t.Kind {
	case Var:
		return t.Name
	case Rigid:
		return "!" + t.Name
	case Fun:
		return "(" + t.Args[0].String() + " -> " + t.Args[1].String() + ")"
	case Tuple:
		parts := make([]string, len(t.Args))
		for i, a := range t.Args {
			parts[i] = a.String()
		}
		return "(" + strings.Join(parts, ", ") + ")"
	}
	return "?"
}

// Equal reports structural equality of two type terms.
func Equal(a, b *Type) bool {
	if a.Kind != b.Kind || a.Name != b.Name || len(a.Args) != len(b.Args) {
		return false
	}
	for i := range a.Args {
		if !Equal(a.Args[i], b.Args[i]) {
			return false
		}
	}
	return true
}

// Vars lists the flexible variable names occurring in t, in first-occurrence
// order, without duplicates.
func Vars(t *Type) []string {
	seen := map[string]bool{}
	var out []string
	var walk func(t *Type)
	walk = func(t *Type) {
		if t.Kind == Var && !seen[t.Name] {
			seen[t.Name] = true
			out = append(out, t.Name)
		}
		for _, a := range t.Args {
			walk(a)
		}
	}
	walk(t)
	return out
}

// ContainsVar reports whether flexible variable name occurs in t.
func ContainsVar(t *Type, name string) bool {
	if t.Kind == Var && t.Name == name {
		return true
	}
	for _, a := range t.Args {
		if ContainsVar(a, name) {
			return true
		}
	}
	return false
}

// Rename returns a copy of t with flexible variables renamed through m.
// Names absent from m are kept. Rigid variables are never renamed.
func Rename(t *Type, m map[string]string) *Type {
	switch t.Kind {
	case Var:
		if n, ok := m[t.Name]; ok {
			return V(n)
		}
		return V(t.Name)
	case Rigid:
		return R(t.Name)
	default:
		args := make([]*Type, len(t.Args))
		for i, a := range t.Args {
			args[i] = Rename(a, m)
		}
		return &Type{Kind: t.Kind, Args: args}
	}
}
