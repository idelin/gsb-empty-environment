package netsec

import (
	"fmt"
	"strconv"
	"strings"
)

// Kind identifies an expression node type.
type Kind uint8

const (
	KindInvalid Kind = iota
	KindRule
	KindAnd
	KindOr
	KindNot
)

func (k Kind) String() string {
	switch k {
	case KindRule:
		return "rule"
	case KindAnd:
		return "and"
	case KindOr:
		return "or"
	case KindNot:
		return "not"
	default:
		return "invalid"
	}
}

// Expr is an immutable policy expression. Expressions may be shared by
// multiple parents, forming a DAG. Shared nodes still have distinct trace
// sites at each parent position.
type Expr struct {
	kind     Kind
	ruleID   string
	children []*Expr
}

// RuleRef returns a leaf expression that evaluates the named rule.
func RuleRef(ruleID string) *Expr {
	return &Expr{kind: KindRule, ruleID: ruleID}
}

// And returns a short-circuiting conjunction of children. It evaluates
// children left to right and stops at the first deny.
func And(children ...*Expr) *Expr {
	return &Expr{kind: KindAnd, children: append([]*Expr(nil), children...)}
}

// Or returns a short-circuiting disjunction of children. It evaluates children
// left to right and stops at the first allow.
func Or(children ...*Expr) *Expr {
	return &Expr{kind: KindOr, children: append([]*Expr(nil), children...)}
}

// Not returns the logical negation of child.
func Not(child *Expr) *Expr {
	return &Expr{kind: KindNot, children: []*Expr{child}}
}

// Kind returns the node type.
func (e *Expr) Kind() Kind {
	if e == nil {
		return KindInvalid
	}
	return e.kind
}

// RuleID returns the referenced rule for a leaf, or the empty string.
func (e *Expr) RuleID() string {
	if e == nil || e.kind != KindRule {
		return ""
	}
	return e.ruleID
}

// Children returns a copy of the child slice.
func (e *Expr) Children() []*Expr {
	if e == nil {
		return nil
	}
	return append([]*Expr(nil), e.children...)
}

// String renders the expression in a compact, stable form for diagnostics.
func (e *Expr) String() string {
	if e == nil {
		return "<nil>"
	}
	switch e.kind {
	case KindRule:
		return e.ruleID
	case KindAnd, KindOr:
		parts := make([]string, 0, len(e.children))
		for _, child := range e.children {
			parts = append(parts, child.String())
		}
		return strings.ToLower(e.kind.String()) + "(" + strings.Join(parts, ",") + ")"
	case KindNot:
		if len(e.children) == 1 {
			return "not(" + e.children[0].String() + ")"
		}
		return "not(<invalid>)"
	default:
		return "<invalid>"
	}
}

const rootSite = "r"

func childSite(parent string, kind Kind, index int) string {
	switch kind {
	case KindAnd:
		return parent + "/and/" + strconv.Itoa(index)
	case KindOr:
		return parent + "/or/" + strconv.Itoa(index)
	case KindNot:
		return parent + "/not"
	default:
		return parent + "/?" + strconv.Itoa(index)
	}
}

func validateExpr(root *Expr, rules map[string]Rule) error {
	const maxDepth = 1024
	stack := make(map[*Expr]bool)

	var walk func(expr *Expr, path string, depth int) error
	walk = func(expr *Expr, path string, depth int) error {
		if expr == nil {
			return fmt.Errorf("%s: nil expression node", path)
		}
		if depth > maxDepth {
			return fmt.Errorf("%s: expression nesting exceeds %d", path, maxDepth)
		}
		if stack[expr] {
			return fmt.Errorf("%s: expression graph contains a cycle", path)
		}
		stack[expr] = true
		defer delete(stack, expr)

		switch expr.kind {
		case KindRule:
			if expr.ruleID == "" {
				return fmt.Errorf("%s: empty rule id", path)
			}
			if _, ok := rules[expr.ruleID]; !ok {
				return fmt.Errorf("%s: rule %q is not registered", path, expr.ruleID)
			}
		case KindAnd, KindOr:
			if len(expr.children) == 0 {
				return fmt.Errorf("%s: %s node has no children", path, expr.kind)
			}
			for i, child := range expr.children {
				if err := walk(child, childSite(path, expr.kind, i), depth+1); err != nil {
					return err
				}
			}
		case KindNot:
			if len(expr.children) != 1 {
				return fmt.Errorf("%s: not node has %d children", path, len(expr.children))
			}
			if err := walk(expr.children[0], childSite(path, KindNot, 0), depth+1); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%s: unknown expression kind %d", path, expr.kind)
		}
		return nil
	}

	return walk(root, rootSite, 0)
}
