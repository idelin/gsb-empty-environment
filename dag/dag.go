// Package dag implements a resumable evaluator for expression DAGs whose
// nodes carry exact rational values. Nodes may be shared between multiple
// parents; each node declares its own rounding strategy and only nodes with
// an explicit RoundSpec are rounded — every other node stays an exact
// rational. Mutating a node invalidates the caches of all its transitive
// successors, and evaluation can be interrupted and resumed from cache.
package dag

import (
	"context"
	"fmt"
	"math/big"

	"gaugeconv/exact"
)

// Op is the arithmetic operation a node performs on its arguments.
type Op int

const (
	Input Op = iota // decimal fixed-point literal (Dec field)
	Const           // exact rational Num/Den
	Add
	Sub
	Mul
	Div
	Neg // unary negation of Args[0]
)

func (op Op) String() string {
	switch op {
	case Input:
		return "input"
	case Const:
		return "const"
	case Add:
		return "add"
	case Sub:
		return "sub"
	case Mul:
		return "mul"
	case Div:
		return "div"
	case Neg:
		return "neg"
	}
	return "unknown"
}

// RoundSpec marks a node for half-even rounding to Places decimal digits.
// Nodes with a nil RoundSpec keep their exact rational value.
type RoundSpec struct {
	Places int
}

// Node is a vertex of the expression DAG. It may be shared by any number of
// parents. Construct nodes with the New* helpers and mutate them only via
// the Set* methods so caches are invalidated correctly.
type Node struct {
	Name string
	Op   Op

	Dec      string // Input: decimal fixed-point literal
	Num, Den int64  // Const: exact rational Num/Den
	Round    *RoundSpec

	args   []*Node
	succs  map[*Node]struct{}
	cached *big.Rat
	ev     *exact.RoundEvidence
	valid  bool
}

// NewInput creates a decimal literal node.
func NewInput(name, dec string) *Node {
	return &Node{Name: name, Op: Input, Dec: dec}
}

// NewConst creates an exact rational constant node.
func NewConst(name string, num, den int64) *Node {
	return &Node{Name: name, Op: Const, Num: num, Den: den}
}

// NewOp creates an arithmetic node over the given argument nodes.
func NewOp(name string, op Op, args ...*Node) *Node {
	n := &Node{Name: name, Op: op}
	n.SetArgs(args...)
	return n
}

// WithRound marks the node for half-even rounding and returns it.
func (n *Node) WithRound(places int) *Node {
	n.Round = &RoundSpec{Places: places}
	return n
}

// Args returns the current argument nodes.
func (n *Node) Args() []*Node {
	out := make([]*Node, len(n.args))
	copy(out, n.args)
	return out
}

// SetDecimal replaces the literal of an Input node and invalidates successors.
func (n *Node) SetDecimal(dec string) {
	n.Op = Input
	n.Dec = dec
	n.invalidate()
}

// SetConst replaces the value of a Const node and invalidates successors.
func (n *Node) SetConst(num, den int64) {
	n.Op = Const
	n.Num, n.Den = num, den
	n.invalidate()
}

// SetOp replaces the operation and invalidates successors.
func (n *Node) SetOp(op Op, args ...*Node) {
	n.Op = op
	n.SetArgs(args...)
	n.invalidate()
}

// SetRound changes the rounding strategy (nil disables rounding) and
// invalidates successors.
func (n *Node) SetRound(spec *RoundSpec) {
	n.Round = spec
	n.invalidate()
}

// SetArgs rewires the node's inputs and invalidates successors.
func (n *Node) SetArgs(args ...*Node) {
	for _, a := range n.args {
		delete(a.succs, n)
	}
	n.args = append([]*Node(nil), args...)
	for _, a := range n.args {
		if a.succs == nil {
			a.succs = make(map[*Node]struct{})
		}
		a.succs[n] = struct{}{}
	}
	n.invalidate()
}

// invalidate clears this node's cache and the caches of every transitive
// successor, so the next evaluation recomputes exactly the affected cone.
func (n *Node) invalidate() {
	n.valid = false
	n.cached = nil
	n.ev = nil
	for s := range n.succs {
		if s.valid || s.cached != nil {
			s.invalidate()
		}
	}
}

// Step is one evaluated node: its exact value, and — when the node carries a
// RoundSpec — the pre-round value and full rounding evidence.
type Step struct {
	Node     string
	Op       Op
	Value    *big.Rat             // node value after optional rounding
	PreRound *big.Rat             // exact value before rounding, nil if unrounded
	Evidence *exact.RoundEvidence // rounding audit trail, nil if unrounded
	Cached   bool                 // true when served from the evaluator cache
}

// Evaluator evaluates DAGs with memoization. A single Evaluator can be reused
// across evaluations: shared subexpressions are computed once, and after a
// mutation only the invalidated cone is recomputed. Evaluation honours
// context cancellation; a cancelled run leaves all completed nodes cached so
// a later Eval call resumes where it stopped.
type Evaluator struct {
	steps []*Step
}

// NewEvaluator creates an empty evaluator.
func NewEvaluator() *Evaluator { return &Evaluator{} }

// Steps returns the per-step record accumulated by the last Eval calls since
// the most recent ResetSteps.
func (e *Evaluator) Steps() []*Step {
	return append([]*Step(nil), e.steps...)
}

// ResetSteps clears the recorded steps without touching node caches.
func (e *Evaluator) ResetSteps() { e.steps = nil }

// Eval computes the value of n, using and filling node caches. It returns
// the node's value (rounded iff the node declares a RoundSpec).
func (e *Evaluator) Eval(ctx context.Context, n *Node) (*big.Rat, error) {
	v, _, err := e.eval(ctx, n)
	return v, err
}

func (e *Evaluator) eval(ctx context.Context, n *Node) (*big.Rat, *exact.RoundEvidence, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if n.valid && n.cached != nil {
		e.steps = append(e.steps, &Step{
			Node: n.Name, Op: n.Op,
			Value: new(big.Rat).Set(n.cached), Evidence: n.ev, Cached: true,
		})
		return new(big.Rat).Set(n.cached), n.ev, nil
	}

	var v *big.Rat
	switch n.Op {
	case Input:
		r, err := exact.ParseDecimal(n.Dec)
		if err != nil {
			return nil, nil, fmt.Errorf("node %q: %v", n.Name, err)
		}
		v = r
	case Const:
		if n.Den == 0 {
			return nil, nil, fmt.Errorf("node %q: %v", n.Name, exact.ErrDivideByZero)
		}
		v = big.NewRat(n.Num, n.Den)
	case Neg:
		if err := e.arity(n, 1); err != nil {
			return nil, nil, err
		}
		a, _, err := e.eval(ctx, n.args[0])
		if err != nil {
			return nil, nil, err
		}
		v = new(big.Rat).Neg(a)
	case Add, Sub, Mul, Div:
		if err := e.arity(n, 2); err != nil {
			return nil, nil, err
		}
		a, _, err := e.eval(ctx, n.args[0])
		if err != nil {
			return nil, nil, err
		}
		b, _, err := e.eval(ctx, n.args[1])
		if err != nil {
			return nil, nil, err
		}
		v = new(big.Rat)
		switch n.Op {
		case Add:
			v.Add(a, b)
		case Sub:
			v.Sub(a, b)
		case Mul:
			v.Mul(a, b)
		case Div:
			if b.Sign() == 0 {
				return nil, nil, fmt.Errorf("node %q: %v", n.Name, exact.ErrDivideByZero)
			}
			v.Quo(a, b)
		}
	default:
		return nil, nil, fmt.Errorf("node %q: unknown op %d", n.Name, int(n.Op))
	}

	step := &Step{Node: n.Name, Op: n.Op}
	if n.Round != nil {
		step.PreRound = new(big.Rat).Set(v)
		rounded, ev := exact.RoundHalfEven(v, n.Round.Places)
		step.Evidence = ev
		v = rounded
	}
	step.Value = new(big.Rat).Set(v)
	e.steps = append(e.steps, step)

	n.cached = new(big.Rat).Set(v)
	n.ev = step.Evidence
	n.valid = true
	return new(big.Rat).Set(v), step.Evidence, nil
}

func (e *Evaluator) arity(n *Node, want int) error {
	if len(n.args) != want {
		return fmt.Errorf("node %q: op %s wants %d args, got %d", n.Name, n.Op, want, len(n.args))
	}
	return nil
}

// EvalFresh recomputes n from scratch, ignoring and not touching any caches.
// It is the reference path used to cross-check cached evaluations.
func EvalFresh(n *Node) (*big.Rat, error) {
	switch n.Op {
	case Input:
		return exact.ParseDecimal(n.Dec)
	case Const:
		if n.Den == 0 {
			return nil, exact.ErrDivideByZero
		}
		return big.NewRat(n.Num, n.Den), nil
	case Neg:
		a, err := EvalFresh(n.args[0])
		if err != nil {
			return nil, err
		}
		return new(big.Rat).Neg(a), nil
	}
	a, err := EvalFresh(n.args[0])
	if err != nil {
		return nil, err
	}
	b, err := EvalFresh(n.args[1])
	if err != nil {
		return nil, err
	}
	v := new(big.Rat)
	switch n.Op {
	case Add:
		v.Add(a, b)
	case Sub:
		v.Sub(a, b)
	case Mul:
		v.Mul(a, b)
	case Div:
		if b.Sign() == 0 {
			return nil, exact.ErrDivideByZero
		}
		v.Quo(a, b)
	default:
		return nil, fmt.Errorf("unknown op %d", int(n.Op))
	}
	if n.Round != nil {
		rounded, _ := exact.RoundHalfEven(v, n.Round.Places)
		v = rounded
	}
	return v, nil
}
