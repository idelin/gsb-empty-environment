package evidence

import (
	"errors"
	"sort"
	"strings"
)

// Lookup resolves a fact name to its strong Kleene value. An unbound name
// must resolve to Unknown.
type Lookup func(name string) Value

// Expr is a release-rule expression evaluated with strong Kleene logic.
type Expr interface {
	// Eval computes the expression's truth value.
	Eval(Lookup) Value
	// String renders a canonical, re-parseable form of the expression.
	String() string
}

// Atom references an authenticated fact by name.
type Atom struct {
	Name string
}

// Eval returns the fact's resolved value.
func (a *Atom) Eval(lookup Lookup) Value {
	return lookup(a.Name)
}

func (a *Atom) String() string {
	return a.Name
}

// NotExpr is a strong Kleene negation.
type NotExpr struct {
	Inner Expr
}

// Eval returns Not(Inner.Eval(lookup)); Unknown propagates as Unknown.
func (e *NotExpr) Eval(lookup Lookup) Value {
	return Not(e.Inner.Eval(lookup))
}

func (e *NotExpr) String() string {
	return "not " + renderChild(e.Inner)
}

// AndExpr is a strong Kleene conjunction.
type AndExpr struct {
	Operands []Expr
}

// Eval returns the strong Kleene conjunction of all operands.
func (e *AndExpr) Eval(lookup Lookup) Value {
	values := make([]Value, len(e.Operands))
	for i, operand := range e.Operands {
		values[i] = operand.Eval(lookup)
	}
	return And(values...)
}

func (e *AndExpr) String() string {
	return joinOperands(e.Operands, " and ")
}

// OrExpr is a strong Kleene disjunction.
type OrExpr struct {
	Operands []Expr
}

// Eval returns the strong Kleene disjunction of all operands.
func (e *OrExpr) Eval(lookup Lookup) Value {
	values := make([]Value, len(e.Operands))
	for i, operand := range e.Operands {
		values[i] = operand.Eval(lookup)
	}
	return Or(values...)
}

func (e *OrExpr) String() string {
	return joinOperands(e.Operands, " or ")
}

func joinOperands(operands []Expr, keyword string) string {
	parts := make([]string, len(operands))
	for i, operand := range operands {
		parts[i] = renderChild(operand)
	}
	return strings.Join(parts, keyword)
}

func renderChild(expr Expr) string {
	switch expr.(type) {
	case *Atom, *NotExpr:
		return expr.String()
	default:
		return "(" + expr.String() + ")"
	}
}

// Names returns every fact name referenced by an expression in sorted order
// and without duplicates.
func Names(expr Expr) []string {
	seen := make(map[string]struct{})
	var walk func(Expr)
	walk = func(node Expr) {
		switch n := node.(type) {
		case *Atom:
			seen[n.Name] = struct{}{}
		case *NotExpr:
			walk(n.Inner)
		case *AndExpr:
			for _, operand := range n.Operands {
				walk(operand)
			}
		case *OrExpr:
			for _, operand := range n.Operands {
				walk(operand)
			}
		}
	}
	walk(expr)
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Constructors for hosts that build rules programmatically.

// AtomOf builds an atomic fact reference.
func AtomOf(name string) Expr { return &Atom{Name: name} }

// NotOf builds a negation.
func NotOf(inner Expr) Expr { return &NotExpr{Inner: inner} }

// AndOf builds a conjunction.
func AndOf(operands ...Expr) (Expr, error) {
	if err := checkOperands(operands); err != nil {
		return nil, err
	}
	return &AndExpr{Operands: append([]Expr(nil), operands...)}, nil
}

// OrOf builds a disjunction.
func OrOf(operands ...Expr) (Expr, error) {
	if err := checkOperands(operands); err != nil {
		return nil, err
	}
	return &OrExpr{Operands: append([]Expr(nil), operands...)}, nil
}

func checkOperands(operands []Expr) error {
	if len(operands) == 0 {
		return errors.New("evidence: boolean expression requires at least one operand")
	}
	for _, operand := range operands {
		if operand == nil {
			return errors.New("evidence: nil rule operand")
		}
	}
	return nil
}
