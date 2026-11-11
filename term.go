package netsec

import "strings"

// Kind identifies the shape of a Term.
type Kind uint8

const (
	Atom Kind = iota // atomic value (name, key, nonce material)
	Var              // pattern variable, only allowed inside step patterns
	Pair             // binary tuple (A, B)
	Enc              // symmetric encryption {B}A: A is the key, B is the body
)

// Term is a message term. Pair uses A/B as left/right, Enc uses A as the
// key and B as the encrypted body.
type Term struct {
	K    Kind
	Name string // Atom and Var only
	A    *Term
	B    *Term
}

// AtomOf builds an atomic term.
func AtomOf(name string) *Term { return &Term{K: Atom, Name: name} }

// VarOf builds a pattern variable (only valid inside step patterns).
func VarOf(name string) *Term { return &Term{K: Var, Name: name} }

// PairOf builds the tuple (a, b).
func PairOf(a, b *Term) *Term { return &Term{K: Pair, A: a, B: b} }

// EncOf builds the symmetric encryption of body under key.
func EncOf(key, body *Term) *Term { return &Term{K: Enc, A: key, B: body} }

// String renders the term in a canonical, unambiguous form. It is also
// used as the map key for term sets, so it is injective for valid terms.
func (t *Term) String() string {
	switch t.K {
	case Atom:
		return t.Name
	case Var:
		return "?" + t.Name
	case Pair:
		return "(" + t.A.String() + "," + t.B.String() + ")"
	case Enc:
		return "{" + t.B.String() + "}" + t.A.String()
	}
	return "<bad>"
}

// Equal reports structural equality.
func (t *Term) Equal(o *Term) bool { return t.String() == o.String() }

// Ground reports whether the term contains no pattern variables.
func (t *Term) Ground() bool {
	switch t.K {
	case Atom:
		return true
	case Var:
		return false
	default:
		return t.A.Ground() && t.B.Ground()
	}
}

// validName reports whether s may be used as an atom or variable name.
// The excluded characters keep String() injective and leave "!" free as
// the fresh-atom separator.
func validName(s string) bool {
	if s == "" {
		return false
	}
	return !strings.ContainsAny(s, "{}(),?!| \t\n\r")
}

// resolve substitutes pattern variables using env. It fails if any
// variable is unbound, i.e. the result would not be ground.
func resolve(t *Term, env map[string]*Term) (*Term, bool) {
	switch t.K {
	case Atom:
		return t, true
	case Var:
		b, ok := env[t.Name]
		if !ok || b == nil {
			return nil, false
		}
		return b, true
	default:
		a, ok := resolve(t.A, env)
		if !ok {
			return nil, false
		}
		b, ok := resolve(t.B, env)
		if !ok {
			return nil, false
		}
		return &Term{K: t.K, A: a, B: b}, true
	}
}

// match unifies a pattern with a ground term, extending env with new
// bindings. Variables already bound in env must match equal terms.
// On failure env may be partially mutated; callers pass a scratch copy.
func match(pat, t *Term, env map[string]*Term) bool {
	switch pat.K {
	case Var:
		if bound, ok := env[pat.Name]; ok {
			return bound.Equal(t)
		}
		env[pat.Name] = t
		return true
	case Atom:
		return t.K == Atom && t.Name == pat.Name
	default:
		if t.K != pat.K {
			return false
		}
		return match(pat.A, t.A, env) && match(pat.B, t.B, env)
	}
}
