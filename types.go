package unify

import "strings"

// Kind discriminates the four sorts of type terms.
type Kind int

const (
	KVar   Kind = iota // flexible type variable, may be bound
	KRigid             // explicit rigid variable, never binds to anything but itself
	KFun               // function type Args[0] -> Args[1]
	KTuple             // tuple type (Args...)
)

// Type is an immutable type term built by the client.
type Type struct {
	Kind Kind
	Name string  // KVar / KRigid
	Args []*Type // KFun: exactly 2, KTuple: any arity
}

func Var(name string) *Type   { return &Type{Kind: KVar, Name: name} }
func Rigid(name string) *Type { return &Type{Kind: KRigid, Name: name} }
func Fun(a, b *Type) *Type    { return &Type{Kind: KFun, Args: []*Type{a, b}} }
func Tuple(elems ...*Type) *Type {
	return &Type{Kind: KTuple, Args: elems}
}

func (t *Type) String() string {
	switch t.Kind {
	case KVar:
		return t.Name
	case KRigid:
		return t.Name + "!"
	case KFun:
		return "(" + t.Args[0].String() + " -> " + t.Args[1].String() + ")"
	case KTuple:
		parts := make([]string, len(t.Args))
		for i, a := range t.Args {
			parts[i] = a.String()
		}
		return "(" + strings.Join(parts, ", ") + ")"
	}
	return "?"
}

// Equal reports structural equality.
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

// Rename returns a copy of t with variable names (flexible and rigid)
// rewritten through m; names absent from m are kept.
func Rename(t *Type, m map[string]string) *Type {
	out := &Type{Kind: t.Kind, Name: t.Name}
	if newName, ok := m[t.Name]; ok {
		out.Name = newName
	}
	if t.Args != nil {
		out.Args = make([]*Type, len(t.Args))
		for i, a := range t.Args {
			out.Args[i] = Rename(a, m)
		}
	}
	return out
}
