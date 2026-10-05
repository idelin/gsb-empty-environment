package decimal

// Expr is a node in an arithmetic expression DAG. Nodes may be shared by
// any number of parents: evaluation caches each node's result and, because
// Decimal is immutable and every arithmetic operation is pure, no
// evaluation can pollute a shared node's cached value through big.Int
// reuse.
type Expr struct {
	op    op
	left  *Expr
	right *Expr
	lit   *Decimal // OpConst only
	scale uint     // OpDiv only: fractional-digit budget
	mode  RoundingMode

	evaluated bool
	res       *Result
	err       error
}

type op int

const (
	opConst op = iota
	opAdd
	opSub
	opMul
	opDiv
)

// Const wraps an exact decimal literal. The value is copied.
func Const(d *Decimal) *Expr { return &Expr{op: opConst, lit: d.Copy()} }

// Add builds l + r.
func Add(l, r *Expr) *Expr { return &Expr{op: opAdd, left: l, right: r} }

// Sub builds l - r.
func Sub(l, r *Expr) *Expr { return &Expr{op: opSub, left: l, right: r} }

// Mul builds l * r.
func Mul(l, r *Expr) *Expr { return &Expr{op: opMul, left: l, right: r} }

// Div builds l / r with the quotient rounded to scale fractional digits
// using mode. A zero divisor surfaces as ErrDivByZero at Eval time.
func Div(l, r *Expr, scale uint, mode RoundingMode) *Expr {
	return &Expr{op: opDiv, left: l, right: r, scale: scale, mode: mode}
}

// Eval evaluates the expression, caching the outcome per node so shared
// subexpressions are computed once. The returned Decimal is immutable and
// safe to retain. Exact is false if any division in the tree exhausted its
// precision budget.
func (e *Expr) Eval() (*Result, error) {
	if e.evaluated {
		return e.res, e.err
	}
	e.res, e.err = e.compute()
	e.evaluated = true
	return e.res, e.err
}

func (e *Expr) compute() (*Result, error) {
	if e.op == opConst {
		return &Result{Value: e.lit.Copy(), Exact: true}, nil
	}
	l, err := e.left.Eval()
	if err != nil {
		return nil, err
	}
	r, err := e.right.Eval()
	if err != nil {
		return nil, err
	}
	exact := l.Exact && r.Exact
	switch e.op {
	case opAdd:
		return &Result{Value: l.Value.Add(r.Value), Exact: exact}, nil
	case opSub:
		return &Result{Value: l.Value.Sub(r.Value), Exact: exact}, nil
	case opMul:
		return &Result{Value: l.Value.Mul(r.Value), Exact: exact}, nil
	case opDiv:
		q, err := l.Value.Quo(r.Value, e.scale, e.mode)
		if err != nil {
			return nil, err
		}
		return &Result{Value: q.Value, Exact: exact && q.Exact}, nil
	}
	panic("decimal: unknown expression operator")
}
