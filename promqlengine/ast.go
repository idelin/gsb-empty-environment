package promqlengine

import "time"

// Node is the common interface of every expression tree element.
type Node interface {
	nodeType() string
}

// Expr is an evaluable expression: selectors, subqueries and function calls.
type Expr interface {
	Node
	expr()
}

// timeModifier carries the optional @ and offset modifiers shared by every
// selector and by subqueries.
//
// AtMs is the @ timestamp in Unix milliseconds. A value of 0 combined with
// HasAt == false means "no @ modifier"; 0 is a valid explicit timestamp in
// theory but Prometheus itself cannot evaluate epoch-zero queries, so the
// dedicated boolean is unambiguous in practice.
//
// Offset may be negative (PromQL permits `offset -5m`).
type timeModifier struct {
	Offset time.Duration
	HasAt  bool
	AtMs   int64
}

// VectorSelector is an instant-vector selector such as
// `http_requests_total{code="500"} offset -5m @ 1700000000000`.
type VectorSelector struct {
	Name     string
	Matchers []*Matcher
	timeModifier
}

// RangeSelector is a range-vector selector such as
// `metric[5m] offset 1m @ 1700000000000`. It can only appear as a function
// argument (or inside a subquery range) and evaluates to a matrix.
type RangeSelector struct {
	Vector *VectorSelector
	Range  time.Duration
}

// SubqueryExpr evaluates Inner on a step-aligned grid spanning Range, e.g.
// `rate(metric[1m])[5m:30s] offset 2m @ 1700000000000`.
//
// Inner must produce an instant vector. Step 0 is rejected at evaluation
// time; Range must be positive.
type SubqueryExpr struct {
	Inner Expr
	Range time.Duration
	Step  time.Duration
	timeModifier
}

// Call is a function application. Only `rate` and `increase` are registered,
// both expecting a matrix-typed argument (range selector or subquery).
type Call struct {
	Func *Function
	Args []Expr
}

func (*VectorSelector) nodeType() string { return "vectorSelector" }
func (*RangeSelector) nodeType() string  { return "rangeSelector" }
func (*SubqueryExpr) nodeType() string   { return "subquery" }
func (*Call) nodeType() string           { return "call" }

func (*VectorSelector) expr() {}
func (*RangeSelector) expr()  {}
func (*SubqueryExpr) expr()   {}
func (*Call) expr()           {}

// effectiveTime resolves the evaluation timestamp after applying @ and offset.
//
// With an @ modifier the base timestamp is pinned to AtMs regardless of the
// query time; otherwise the query timestamp t is used. offset is then always
// added on top (a negative offset therefore moves the base into the future).
func (m timeModifier) effectiveTime(t int64) int64 {
	base := t
	if m.HasAt {
		base = m.AtMs
	}
	return base - m.Offset.Milliseconds()
}

// walk traverses an expression tree depth-first, invoking f on every node.
func walk(n Node, f func(Node)) {
	f(n)
	switch e := n.(type) {
	case *SubqueryExpr:
		walk(e.Inner, f)
	case *Call:
		for _, a := range e.Args {
			walk(a, f)
		}
	case *RangeSelector:
		walk(e.Vector, f)
	}
}
