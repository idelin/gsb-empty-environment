// Package rulexplain explains boolean rule trees: given a rule and a
// current variable assignment, it finds a minimal set of variables whose
// pinned values guarantee the evaluation outcome (conclusion and error
// behaviour) stays the same no matter how the remaining variables vary
// over their finite domains.
package rulexplain

import (
	"errors"
	"fmt"
)

// ErrUnknownInput is returned (wrapped) when a node needs a variable that
// is not present in the environment.
var ErrUnknownInput = errors.New("rulexplain: unknown input")

// Env maps variable names to their current values.
type Env map[string]int

// Outcome is the observable result of evaluating a rule: the boolean
// conclusion and whether evaluation reported an error. Value is
// meaningless when Errored is true.
type Outcome struct {
	Value   bool
	Errored bool
}

// Node is a node of a boolean rule tree.
type Node interface {
	// Eval evaluates the node. Implementations must short-circuit where
	// applicable and must return false together with any error.
	Eval(env Env) (bool, error)
}

// Evaluate evaluates n and collapses the result into an Outcome.
func Evaluate(n Node, env Env) Outcome {
	v, err := n.Eval(env)
	return Outcome{Value: v, Errored: err != nil}
}

// Op is a comparison operator used by Condition.
type Op int

const (
	Eq Op = iota
	Ne
	Lt
	Le
	Gt
	Ge
)

// Condition is a leaf comparing a variable against a constant. It fails
// with ErrUnknownInput when the variable is absent from the environment.
type Condition struct {
	Var   string
	Op    Op
	Const int
}

// Eval implements Node.
func (c Condition) Eval(env Env) (bool, error) {
	v, ok := env[c.Var]
	if !ok {
		return false, fmt.Errorf("%v: %s", ErrUnknownInput, c.Var)
	}
	switch c.Op {
	case Eq:
		return v == c.Const, nil
	case Ne:
		return v != c.Const, nil
	case Lt:
		return v < c.Const, nil
	case Le:
		return v <= c.Const, nil
	case Gt:
		return v > c.Const, nil
	case Ge:
		return v >= c.Const, nil
	}
	return false, fmt.Errorf("rulexplain: bad operator %d", int(c.Op))
}

// Pred is a leaf backed by a custom function. Use it for inputs that may
// be unknown or computations that may fail (e.g. division by zero); the
// error participates in short-circuit semantics like any other.
type Pred struct {
	Fn func(env Env) (bool, error)
}

// Eval implements Node.
func (p Pred) Eval(env Env) (bool, error) { return p.Fn(env) }

// And evaluates its children left to right, stopping at the first false
// result or error. Errors in children that are never reached are masked.
type And []Node

// Eval implements Node.
func (a And) Eval(env Env) (bool, error) {
	for _, c := range a {
		v, err := c.Eval(env)
		if err != nil {
			return false, err
		}
		if !v {
			return false, nil
		}
	}
	return true, nil
}

// Or evaluates its children left to right, stopping at the first true
// result or error. Errors in children that are never reached are masked.
type Or []Node

// Eval implements Node.
func (o Or) Eval(env Env) (bool, error) {
	for _, c := range o {
		v, err := c.Eval(env)
		if err != nil {
			return false, err
		}
		if v {
			return true, nil
		}
	}
	return false, nil
}

// Not negates its child, propagating errors.
type Not struct {
	Child Node
}

// Eval implements Node.
func (n Not) Eval(env Env) (bool, error) {
	v, err := n.Child.Eval(env)
	if err != nil {
		return false, err
	}
	return !v, nil
}

// Rule is a versioned boolean rule tree. Every mutation bumps the
// version, which invalidates explanations cached against the old tree.
type Rule struct {
	root    Node
	version uint64
}

// NewRule returns a Rule wrapping root.
func NewRule(root Node) *Rule { return &Rule{root: root, version: 1} }

// Root returns the current root node.
func (r *Rule) Root() Node { return r.root }

// Version returns the current rule version.
func (r *Rule) Version() uint64 { return r.version }

// SetRoot replaces the root node and bumps the version.
func (r *Rule) SetRoot(root Node) {
	r.root = root
	r.version++
}
