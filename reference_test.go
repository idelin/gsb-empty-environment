package decimal

import (
	"math/big"
	"testing"
)

// mustParse parses s or fails the test.
func mustParse(t *testing.T, s string) Decimal {
	t.Helper()
	d, err := Parse(s)
	if err != nil {
		t.Fatalf("Parse(%q): %v", s, err)
	}
	return d
}

// roundRatRef rounds a rational to scale fractional digits. It is an
// independent reference implementation built on Euclidean floor division
// of the scaled rational, structurally different from the library's
// truncated-quotient rounding.
func roundRatRef(x *big.Rat, scale uint32, mode RoundingMode) *big.Rat {
	p := pow10(int64(scale))
	scaled := new(big.Rat).Mul(x, new(big.Rat).SetInt(p))
	num := new(big.Int).Set(scaled.Num())
	den := new(big.Int).Set(scaled.Denom()) // always positive
	floor := new(big.Int)
	mod := new(big.Int)
	floor.DivMod(num, den, mod) // mod >= 0, floor = floor(scaled)
	q := new(big.Int).Set(floor)
	switch mode {
	case ToNegInf:
		// floor is the answer.
	case ToPosInf:
		if mod.Sign() != 0 {
			q.Add(q, bigOne)
		}
	case ToZero:
		if scaled.Sign() < 0 && mod.Sign() != 0 {
			q.Add(q, bigOne)
		}
	case HalfEven:
		twice := new(big.Int).Lsh(new(big.Int).Set(mod), 1)
		c := twice.Cmp(den)
		if c > 0 || (c == 0 && floor.Bit(0) == 1) {
			q.Add(q, bigOne)
		}
	}
	return new(big.Rat).SetFrac(q, p)
}

// terminatingWithin reports whether the rational x has a finite decimal
// expansion needing at most maxScale fractional digits.
func terminatingWithin(x *big.Rat, maxScale uint32) bool {
	d := new(big.Int).Set(x.Denom())
	twos := stripFactor(d, bigTwo)
	fives := stripFactor(d, bigFive)
	if d.Cmp(bigOne) != 0 {
		return false
	}
	m := twos
	if fives > m {
		m = fives
	}
	return m <= int64(maxScale)
}

// checkOutcome verifies an Outcome against the exact rational value of
// the operation: Exact (when required to exist) must equal want, and
// Rounded must equal the reference rounding of want.
func checkOutcome(t *testing.T, what string, o Outcome, want *big.Rat, c Context, exactOK bool) {
	t.Helper()
	if exactOK {
		if o.Exact == nil {
			t.Fatalf("%s: Exact is nil, want %s", what, want)
		}
		if got := o.Exact.Rat(); got.Cmp(want) != 0 {
			t.Fatalf("%s: Exact = %s, want %s", what, got, want)
		}
	} else if o.Exact != nil {
		t.Fatalf("%s: Exact = %s, want nil (budget insufficient)", what, o.Exact.Rat())
	}
	wantRounded := roundRatRef(want, c.Precision, c.Mode)
	if got := o.Rounded.Rat(); got.Cmp(wantRounded) != 0 {
		t.Fatalf("%s: Rounded = %s (%s), want %s", what, got, o.Rounded, wantRounded)
	}
}

// checkDiv verifies a division Outcome against the exact rational
// quotient, deriving exactness from the reduced denominator.
func checkDiv(t *testing.T, o Outcome, quotient *big.Rat, c Context) {
	t.Helper()
	checkOutcome(t, "div", o, quotient, c, terminatingWithin(quotient, c.MaxScale))
}

// refEval mirrors Expr.Eval over rationals: exact values propagate while
// representable, and every node rounds to the context precision.
func refEval(e *Expr, c Context) (exact *big.Rat, rounded *big.Rat, err error) {
	switch e.op {
	case opConst:
		r := e.val.Rat()
		return r, roundRatRef(r, c.Precision, c.Mode), nil
	case opNeg:
		ex, ro, err := refEval(e.args[0], c)
		if err != nil {
			return nil, nil, err
		}
		if ex != nil {
			nex := new(big.Rat).Neg(ex)
			return nex, roundRatRef(nex, c.Precision, c.Mode), nil
		}
		return nil, new(big.Rat).Neg(ro), nil
	}
	e1, r1, err := refEval(e.args[0], c)
	if err != nil {
		return nil, nil, err
	}
	e2, r2, err := refEval(e.args[1], c)
	if err != nil {
		return nil, nil, err
	}
	switch e.op {
	case opAdd, opSub, opMul:
		op := map[opKind]func(x, y *big.Rat) *big.Rat{
			opAdd: func(x, y *big.Rat) *big.Rat { return new(big.Rat).Add(x, y) },
			opSub: func(x, y *big.Rat) *big.Rat { return new(big.Rat).Sub(x, y) },
			opMul: func(x, y *big.Rat) *big.Rat { return new(big.Rat).Mul(x, y) },
		}[e.op]
		if e1 != nil && e2 != nil {
			ex := op(e1, e2)
			return ex, roundRatRef(ex, c.Precision, c.Mode), nil
		}
		return nil, roundRatRef(op(r1, r2), c.Precision, c.Mode), nil
	case opDiv:
		if e1 != nil && e2 != nil {
			if e2.Sign() == 0 {
				return nil, nil, ErrDivisionByZero
			}
			q := new(big.Rat).Quo(e1, e2)
			if terminatingWithin(q, c.MaxScale) {
				return q, roundRatRef(q, c.Precision, c.Mode), nil
			}
			return nil, roundRatRef(q, c.Precision, c.Mode), nil
		}
		if r2.Sign() == 0 {
			return nil, nil, ErrDivisionByZero
		}
		return nil, roundRatRef(new(big.Rat).Quo(r1, r2), c.Precision, c.Mode), nil
	}
	return nil, nil, ErrDivisionByZero
}

// collect flattens an expression DAG into a node list (duplicates kept).
func collect(e *Expr) []*Expr {
	nodes := []*Expr{e}
	for _, a := range e.args {
		nodes = append(nodes, collect(a)...)
	}
	return nodes
}

// checkExpr evaluates every node of e under c and compares against the
// rational reference, verifying intermediate precision at each node.
func checkExpr(t *testing.T, e *Expr, c Context) {
	t.Helper()
	for _, n := range collect(e) {
		o, err := n.Eval(c)
		ex, ro, rerr := refEval(n, c)
		if rerr != nil {
			if err != rerr {
				t.Fatalf("node op=%d: err = %v, want %v", n.op, err, rerr)
			}
			continue
		}
		if err != nil {
			t.Fatalf("node op=%d: unexpected err %v", n.op, err)
		}
		if ex == nil {
			if o.Exact != nil {
				t.Fatalf("node op=%d: Exact = %s, want nil", n.op, o.Exact)
			}
		} else {
			if o.Exact == nil {
				t.Fatalf("node op=%d: Exact nil, want %s", n.op, ex)
			}
			if got := o.Exact.Rat(); got.Cmp(ex) != 0 {
				t.Fatalf("node op=%d: Exact = %s, want %s", n.op, got, ex)
			}
		}
		if got := o.Rounded.Rat(); got.Cmp(ro) != 0 {
			t.Fatalf("node op=%d: Rounded = %s, want %s", n.op, got, ro)
		}
	}
}
