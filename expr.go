package decimal

import "fmt"

// opKind identifies the operation of an Expr node.
type opKind int

const (
	opConst opKind = iota
	opNeg
	opAdd
	opSub
	opMul
	opDiv
)

// Expr is a node in an arithmetic expression DAG. Nodes are immutable
// after construction; a node may be shared by any number of parents and
// evaluated any number of times without its value being polluted by the
// big.Int arithmetic of sibling evaluations.
type Expr struct {
	op   opKind
	val  Decimal
	args []*Expr
}

// Const returns a leaf node holding d.
func Const(d Decimal) *Expr { return &Expr{op: opConst, val: d} }

// Neg returns a node computing -e.
func Neg(e *Expr) *Expr { return &Expr{op: opNeg, args: []*Expr{e}} }

// Add returns a node computing a + b.
func Add(a, b *Expr) *Expr { return &Expr{op: opAdd, args: []*Expr{a, b}} }

// Sub returns a node computing a - b.
func Sub(a, b *Expr) *Expr { return &Expr{op: opSub, args: []*Expr{a, b}} }

// Mul returns a node computing a * b.
func Mul(a, b *Expr) *Expr { return &Expr{op: opMul, args: []*Expr{a, b}} }

// Div returns a node computing a / b.
func Div(a, b *Expr) *Expr { return &Expr{op: opDiv, args: []*Expr{a, b}} }

// Eval evaluates the expression under ctx. Every node yields both the
// exact subexpression value (when representable within the context
// budget) and the value rounded to the context precision; rounding is
// applied per node, so intermediate precision follows ctx. When an exact
// value is unavailable below a node, the rounded result is propagated
// from the rounded operands and Exact stays nil.
func (e *Expr) Eval(c Context) (Outcome, error) {
	switch e.op {
	case opConst:
		return c.finish(e.val), nil
	case opNeg:
		o, err := e.args[0].Eval(c)
		if err != nil {
			return Outcome{}, err
		}
		if o.Exact != nil {
			return c.finish(o.Exact.Neg()), nil
		}
		return Outcome{Rounded: o.Rounded.Neg()}, nil
	}
	if len(e.args) != 2 {
		return Outcome{}, fmt.Errorf("decimal: expression node needs 2 operands, has %d", len(e.args))
	}
	o1, err := e.args[0].Eval(c)
	if err != nil {
		return Outcome{}, err
	}
	o2, err := e.args[1].Eval(c)
	if err != nil {
		return Outcome{}, err
	}
	switch e.op {
	case opAdd, opSub, opMul:
		var op func(Decimal, Decimal) Decimal
		switch e.op {
		case opAdd:
			op = Decimal.Add
		case opSub:
			op = Decimal.Sub
		default:
			op = Decimal.Mul
		}
		if o1.Exact != nil && o2.Exact != nil {
			return c.finish(op(*o1.Exact, *o2.Exact)), nil
		}
		return Outcome{Rounded: op(o1.Rounded, o2.Rounded).Round(c.Precision, c.Mode)}, nil
	case opDiv:
		if o1.Exact != nil && o2.Exact != nil {
			return c.Div(*o1.Exact, *o2.Exact)
		}
		if o2.Rounded.Sign() == 0 {
			return Outcome{}, ErrDivisionByZero
		}
		return Outcome{Rounded: divRound(o1.Rounded, o2.Rounded, c.Precision, c.Mode)}, nil
	}
	return Outcome{}, fmt.Errorf("decimal: unknown expression operator %d", int(e.op))
}
