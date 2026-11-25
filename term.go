package symexec

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// TermKind identifies the four term constructors understood by the engine.
type TermKind uint8

const (
	// KindVar is a role-local variable. Variables are only meaningful while
	// matching a receive pattern or instantiating a role step.
	KindVar TermKind = iota + 1
	// KindAtom is an indivisible name (a principal, nonce, key, ...).
	KindAtom
	// KindPair is a binary pair. Left and Right are its components.
	KindPair
	// KindEnc is symmetric encryption. Left is the key and Right is the
	// plaintext.
	KindEnc
)

// Term is an immutable message term.
//
// Pair(left, right) and Enc(key, plaintext) are represented with the Left and
// Right fields. An encryption node is therefore written Enc(k, m), where k is
// the symmetric key. The zero Term is invalid; use the constructors below.
type Term struct {
	Kind  TermKind `json:"kind"`
	Name  string   `json:"name,omitempty"`
	Left  *Term    `json:"left,omitempty"`
	Right *Term    `json:"right,omitempty"`
}

// Var returns a role-local variable.
func Var(name string) Term {
	return Term{Kind: KindVar, Name: name}
}

// Atom returns an indivisible atom.
func Atom(name string) Term {
	return Term{Kind: KindAtom, Name: name}
}

// Pair returns the pair (left, right). For host convenience, string operands
// are treated as atoms and *Term operands are dereferenced.
func Pair(left, right any) Term {
	leftTerm, rightTerm := asTerm(left), asTerm(right)
	return Term{Kind: KindPair, Left: &leftTerm, Right: &rightTerm}
}

// Enc returns the symmetric encryption Enc(key, plaintext).
func Enc(key, plaintext any) Term {
	keyTerm, plaintextTerm := asTerm(key), asTerm(plaintext)
	return Term{Kind: KindEnc, Left: &keyTerm, Right: &plaintextTerm}
}

// Encrypt is an alias for Enc.
func Encrypt(key, plaintext Term) Term {
	return Enc(key, plaintext)
}

// NewVar, NewAtom, NewPair and NewEnc are aliases provided for hosts that
// prefer explicit constructor names.
func NewVar(name string) Term                  { return Var(name) }
func NewAtom(name string) Term                 { return Atom(name) }
func NewPair(left, right any) Term             { return Pair(left, right) }
func NewEnc(key, plaintext any) Term           { return Enc(key, plaintext) }
func SymmetricEncrypt(key, plaintext any) Term { return Enc(key, plaintext) }

// AsTerm converts a host value to a term. Supported values are Term, *Term,
// and string (treated as an atom).
func AsTerm(value any) Term { return asTerm(value) }

func asTerm(value any) Term {
	switch typed := value.(type) {
	case Term:
		return typed
	case *Term:
		if typed == nil {
			return Term{}
		}
		return *typed
	case string:
		return Atom(typed)
	default:
		return Term{}
	}
}

// IsVar reports whether t is a variable.
func (t Term) IsVar() bool { return t.Kind == KindVar }

// IsAtom reports whether t is an atom.
func (t Term) IsAtom() bool { return t.Kind == KindAtom }

// IsPair reports whether t is a pair.
func (t Term) IsPair() bool { return t.Kind == KindPair }

// IsEnc reports whether t is an encryption.
func (t Term) IsEnc() bool { return t.Kind == KindEnc }

// Valid reports whether the term is structurally well formed.
func (t Term) Valid() bool {
	switch t.Kind {
	case KindVar, KindAtom:
		return t.Name != "" && t.Left == nil && t.Right == nil
	case KindPair, KindEnc:
		return t.Name == "" && t.Left != nil && t.Right != nil &&
			t.Left.Valid() && t.Right.Valid()
	default:
		return false
	}
}

// Key returns a stable, unambiguous map key. It is also the equality key for
// terms.
func (t Term) Key() string {
	switch t.Kind {
	case KindVar:
		return "v" + strconv.Itoa(len(t.Name)) + ":" + t.Name
	case KindAtom:
		return "a" + strconv.Itoa(len(t.Name)) + ":" + t.Name
	case KindPair:
		if t.Left == nil || t.Right == nil {
			return "pair:invalid"
		}
		return "p(" + t.Left.Key() + "," + t.Right.Key() + ")"
	case KindEnc:
		if t.Left == nil || t.Right == nil {
			return "enc:invalid"
		}
		return "e(" + t.Left.Key() + "," + t.Right.Key() + ")"
	default:
		return "invalid"
	}
}

// Equal reports structural equality.
func (t Term) Equal(other Term) bool { return t.Key() == other.Key() }

// Equals is an alias for Equal.
func (t Term) Equals(other Term) bool { return t.Equal(other) }

// Depth is the number of Pair/Enc constructors on the longest path. Atoms and
// variables have depth zero. This is the measure used by the attacker
// composition budget.
func (t Term) Depth() int {
	switch t.Kind {
	case KindVar, KindAtom:
		return 0
	case KindPair, KindEnc:
		if t.Left == nil || t.Right == nil {
			return 0
		}
		ld, rd := t.Left.Depth(), t.Right.Depth()
		if rd > ld {
			ld = rd
		}
		return ld + 1
	default:
		return 0
	}
}

// Height is Depth+1. Atoms therefore have height one.
func (t Term) Height() int { return t.Depth() + 1 }

// Ground reports whether the term contains no variables.
func (t Term) Ground() bool {
	switch t.Kind {
	case KindVar:
		return false
	case KindAtom:
		return true
	case KindPair, KindEnc:
		return t.Left != nil && t.Right != nil && t.Left.Ground() && t.Right.Ground()
	default:
		return false
	}
}

// IsGround is an alias for Ground.
func (t Term) IsGround() bool { return t.Ground() }

// Subst is an alias for Substitute.
func (t Term) Subst(binding map[string]Term) (Term, error) {
	return t.Substitute(binding)
}

// Vars returns the free variable names in sorted order.
func (t Term) Vars() []string {
	set := make(map[string]struct{})
	t.collectVars(set)
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (t Term) collectVars(set map[string]struct{}) {
	switch t.Kind {
	case KindVar:
		set[t.Name] = struct{}{}
	case KindPair, KindEnc:
		if t.Left != nil {
			t.Left.collectVars(set)
		}
		if t.Right != nil {
			t.Right.collectVars(set)
		}
	}
}

// Substitute returns a fully instantiated term. It fails if any variable is
// missing from the substitution.
func (t Term) Substitute(binding map[string]Term) (Term, error) {
	switch t.Kind {
	case KindVar:
		value, ok := binding[t.Name]
		if !ok {
			return Term{}, fmt.Errorf("unbound variable %q", t.Name)
		}
		return value, nil
	case KindAtom:
		return t, nil
	case KindPair:
		if t.Left == nil || t.Right == nil {
			return Term{}, fmt.Errorf("invalid pair term")
		}
		left, err := t.Left.Substitute(binding)
		if err != nil {
			return Term{}, err
		}
		right, err := t.Right.Substitute(binding)
		if err != nil {
			return Term{}, err
		}
		return Pair(left, right), nil
	case KindEnc:
		if t.Left == nil || t.Right == nil {
			return Term{}, fmt.Errorf("invalid encryption term")
		}
		key, err := t.Left.Substitute(binding)
		if err != nil {
			return Term{}, err
		}
		msg, err := t.Right.Substitute(binding)
		if err != nil {
			return Term{}, err
		}
		return Enc(key, msg), nil
	default:
		return Term{}, fmt.Errorf("invalid term kind %d", t.Kind)
	}
}

// SubstitutePartial substitutes variables that are bound and leaves all other
// variables unchanged. It is used to narrow receive patterns before matching.
func (t Term) SubstitutePartial(binding map[string]Term) Term {
	switch t.Kind {
	case KindVar:
		if value, ok := binding[t.Name]; ok {
			return value
		}
		return t
	case KindAtom:
		return t
	case KindPair:
		if t.Left == nil || t.Right == nil {
			return Term{}
		}
		return Pair(t.Left.SubstitutePartial(binding), t.Right.SubstitutePartial(binding))
	case KindEnc:
		if t.Left == nil || t.Right == nil {
			return Term{}
		}
		return Enc(t.Left.SubstitutePartial(binding), t.Right.SubstitutePartial(binding))
	default:
		return Term{}
	}
}

// Match matches a pattern against a ground message. Variables in the pattern
// are bound in the returned map. Existing bindings are respected, which lets
// callers enforce repeated variables and state from earlier receives.
func Match(pattern, message Term, binding map[string]Term) (map[string]Term, bool) {
	out := make(map[string]Term, len(binding)+1)
	for name, value := range binding {
		out[name] = value
	}
	return out, matchInto(pattern, message, out)
}

func matchInto(pattern, message Term, binding map[string]Term) bool {
	switch pattern.Kind {
	case KindVar:
		if value, ok := binding[pattern.Name]; ok {
			return value.Equal(message)
		}
		binding[pattern.Name] = message
		return true
	case KindAtom:
		return message.Kind == KindAtom && pattern.Name == message.Name
	case KindPair:
		if message.Kind != KindPair || pattern.Left == nil || pattern.Right == nil ||
			message.Left == nil || message.Right == nil {
			return false
		}
		return matchInto(*pattern.Left, *message.Left, binding) &&
			matchInto(*pattern.Right, *message.Right, binding)
	case KindEnc:
		if message.Kind != KindEnc || pattern.Left == nil || pattern.Right == nil ||
			message.Left == nil || message.Right == nil {
			return false
		}
		return matchInto(*pattern.Left, *message.Left, binding) &&
			matchInto(*pattern.Right, *message.Right, binding)
	default:
		return false
	}
}

// String renders a compact human-readable form.
func (t Term) String() string {
	switch t.Kind {
	case KindVar:
		return "$" + t.Name
	case KindAtom:
		return t.Name
	case KindPair:
		if t.Left == nil || t.Right == nil {
			return "<invalid-pair>"
		}
		return "(" + t.Left.String() + "," + t.Right.String() + ")"
	case KindEnc:
		if t.Left == nil || t.Right == nil {
			return "<invalid-enc>"
		}
		return "enc(" + t.Left.String() + "," + t.Right.String() + ")"
	default:
		return "<invalid-term>"
	}
}

// DebugString is a quoted, unambiguous rendering useful in diagnostics.
func (t Term) DebugString() string {
	return strings.ReplaceAll(t.String(), "\n", "\\n")
}
