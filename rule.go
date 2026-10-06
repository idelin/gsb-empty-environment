// Package explain finds, for a boolean rule tree and a concrete variable
// assignment, the smallest set of variables that must be kept known so that
// the rule's conclusion (and whether it errors) is guaranteed to stay the
// same no matter how the remaining variables range over their domains.
//
// Sufficiency and minimality are always established by exhaustive
// enumeration of the remaining variable domains (at most 12 variables),
// never by trusting a single evaluation path.
package explain

import "errors"

// ErrUnknown is returned by leaf predicates when a rule references an input
// that is not present in the current assignment (an unknown input).
var ErrUnknown = errors.New("explain: unknown input")

// Assignment maps variable names to their current values.
type Assignment map[string]int64

// Getter resolves a variable name inside a leaf predicate.
type Getter func(name string) (int64, bool)

// Outcome is the observable result of evaluating a rule: the boolean
// conclusion and whether evaluation reported an error. Two outcomes are
// considered identical when both components match.
type Outcome struct {
	Value bool
	Err   error
}

func (o Outcome) same(p Outcome) bool {
	return o.Value == p.Value && (o.Err != nil) == (p.Err != nil)
}

// Node is a node of a boolean rule tree.
type Node interface {
	eval(get Getter) Outcome
}

// Const is a constant boolean leaf.
type Const struct{ V bool }

func (c Const) eval(get Getter) Outcome { return Outcome{Value: c.V} }

// And evaluates L first and short-circuits on false or on error.
type And struct{ L, R Node }

func (n And) eval(get Getter) Outcome {
	l := n.L.eval(get)
	if l.Err != nil || !l.Value {
		return Outcome{Value: false, Err: l.Err}
	}
	return n.R.eval(get)
}

// Or evaluates L first and short-circuits on true or on error.
type Or struct{ L, R Node }

func (n Or) eval(get Getter) Outcome {
	l := n.L.eval(get)
	if l.Err != nil || l.Value {
		return Outcome{Value: true, Err: l.Err}
	}
	return n.R.eval(get)
}

// Not negates its child; errors propagate.
type Not struct{ X Node }

func (n Not) eval(get Getter) Outcome {
	o := n.X.eval(get)
	if o.Err != nil {
		return o
	}
	return Outcome{Value: !o.Value}
}

// Leaf is a custom predicate. Fn receives a Getter so a leaf may read zero
// or more variables, fail with ErrUnknown for missing inputs, or report any
// other error (e.g. division by zero). Short-circuiting ancestors may
// prevent a leaf from being evaluated at all.
type Leaf struct {
	Name string
	Fn   func(get Getter) (bool, error)
}

func (l Leaf) eval(get Getter) Outcome {
	v, err := l.Fn(get)
	return Outcome{Value: v, Err: err}
}

// CmpOp is a comparison operator for the Cmp leaf.
type CmpOp int

const (
	Eq CmpOp = iota
	Ne
	Lt
	Le
	Gt
	Ge
)

// Cmp builds a leaf comparing variable `name` against constant k.
func Cmp(name string, op CmpOp, k int64) Leaf {
	return Leaf{
		Name: name,
		Fn: func(get Getter) (bool, error) {
			v, ok := get(name)
			if !ok {
				return false, ErrUnknown
			}
			switch op {
			case Eq:
				return v == k, nil
			case Ne:
				return v != k, nil
			case Lt:
				return v < k, nil
			case Le:
				return v <= k, nil
			case Gt:
				return v > k, nil
			case Ge:
				return v >= k, nil
			}
			return false, errors.New("explain: bad comparison operator")
		},
	}
}

// DivGt builds a leaf computing num/den > k. It errors when the variable is
// unknown or zero, modelling short-circuitable runtime errors.
func DivGt(num int64, den string, k int64) Leaf {
	return Leaf{
		Name: "div",
		Fn: func(get Getter) (bool, error) {
			d, ok := get(den)
			if !ok {
				return false, ErrUnknown
			}
			if d == 0 {
				return false, errors.New("explain: division by zero")
			}
			return num/d > k, nil
		},
	}
}

// Eval evaluates a rule tree against a complete assignment.
func Eval(n Node, a Assignment) Outcome {
	return n.eval(func(name string) (int64, bool) {
		v, ok := a[name]
		return v, ok
	})
}
