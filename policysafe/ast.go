package policysafe

import "strings"

// Term is a node in the term algebra: a variable, a constant, or a wildcard.
type Term interface {
	term()
	String() string
}

// Var is a named logic variable such as Subj or X.
type Var struct{ Name string }

// Const is a ground symbol: a bare lowercase atom, a number, or a quoted
// string.
type Const struct{ Value string }

// Wildcard is the anonymous variable '_'. Every occurrence is independent
// and never introduces a binding.
type Wildcard struct{}

func (*Var) term()      {}
func (*Const) term()    {}
func (*Wildcard) term() {}

// V is a convenience constructor for a named variable.
func V(name string) Term { return &Var{Name: name} }

// C is a convenience constructor for a constant symbol.
func C(value string) Term { return &Const{Value: value} }

// W is a convenience constructor for a wildcard.
func W() Term { return &Wildcard{} }

func (v *Var) String() string    { return v.Name }
func (*Wildcard) String() string { return "_" }
func (c *Const) String() string  { return renderConst(c.Value) }

// Atom is a predicate application, e.g. role(alice, admin) or allow(X).
type Atom struct {
	Pred string
	Args []Term
}

// AtomOf is a convenience constructor for an Atom.
func AtomOf(pred string, args ...Term) Atom {
	return Atom{Pred: pred, Args: args}
}

// Arity returns the number of argument terms.
func (a Atom) Arity() int { return len(a.Args) }

func (a Atom) String() string {
	if len(a.Args) == 0 {
		return a.Pred
	}
	parts := make([]string, len(a.Args))
	for i, t := range a.Args {
		parts[i] = t.String()
	}
	return a.Pred + "(" + strings.Join(parts, ", ") + ")"
}

// Literal is a signed atom. Neg is true for a locally-negated atom
// (not p(...)).
type Literal struct {
	Neg  bool
	Atom Atom
}

func (l Literal) String() string {
	if l.Neg {
		return "not " + l.Atom.String()
	}
	return l.Atom.String()
}

// Rule is a head atom together with its body literals.
type Rule struct {
	Head Atom
	Body []Literal
}

func (r Rule) String() string {
	if len(r.Body) == 0 {
		return r.Head.String() + "."
	}
	parts := make([]string, len(r.Body))
	for i, l := range r.Body {
		parts[i] = l.String()
	}
	return r.Head.String() + " :- " + strings.Join(parts, ", ") + "."
}

// DefaultDecl records a `default allow := false` declaration. The compiler
// always enforces fail-closed semantics whether or not such a declaration
// is present; the declaration is accepted for source-level compatibility.
type DefaultDecl struct {
	Pred string
}

// Program is a parsed rule set: ground facts, derivation rules, and optional
// default declarations.
type Program struct {
	Facts    []Atom
	Rules    []Rule
	Defaults []DefaultDecl
}

// Tuple is a ground argument vector derived for a predicate relation.
type Tuple []string

func (t Tuple) key() string { return strings.Join(t, "\x00") }

func renderConst(value string) string {
	if isSimpleIdent(value) {
		return value
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func isSimpleIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case i > 0 && ((r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_'):
		default:
			return false
		}
	}
	return true
}
