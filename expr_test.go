package decimal

import (
	"strings"
	"testing"
)

// TestSharedNodesNoPollution builds a DAG with a shared subtree, evaluates
// it repeatedly and under different contexts, and verifies that neither
// the operands nor the shared node's results are polluted by big.Int
// reuse across evaluations.
func TestSharedNodesNoPollution(t *testing.T) {
	a := Const(mustParse(t, "1.25"))
	b := Const(mustParse(t, "-3.5"))
	shared := Mul(a, b)
	e := Add(shared, Div(shared, Const(mustParse(t, "8"))))

	ctx1 := Context{Precision: 6, Mode: HalfEven, MaxScale: 20}
	ctx2 := Context{Precision: 2, Mode: ToNegInf, MaxScale: 3}

	o1, err := e.Eval(ctx1)
	if err != nil {
		t.Fatal(err)
	}
	o2, err := e.Eval(ctx1) // evaluate again: results must be stable
	if err != nil {
		t.Fatal(err)
	}
	if o1.Exact.Rat().Cmp(o2.Exact.Rat()) != 0 || o1.Rounded.Rat().Cmp(o2.Rounded.Rat()) != 0 {
		t.Fatalf("repeated eval differs: %+v vs %+v", o1, o2)
	}
	// The shared node evaluated alone must match its value inside the DAG.
	s1, err := shared.Eval(ctx1)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := shared.Eval(ctx2)
	if err != nil {
		t.Fatal(err)
	}
	if s1.Exact.Rat().Cmp(s2.Exact.Rat()) != 0 {
		t.Fatalf("shared node exact value changed across contexts: %s vs %s", s1.Exact, s2.Exact)
	}
	if got := s1.Exact.String(); got != "-4.375" {
		t.Fatalf("shared exact = %s, want -4.375", got)
	}
	// Leaf operands must be untouched.
	if a.val.String() != "1.25" || b.val.String() != "-3.5" {
		t.Fatalf("leaves mutated: a=%s b=%s", a.val, b.val)
	}
	// Whole-expression value: -4.375 + -4.375/8 = -4.375 + -0.546875 = -4.921875.
	if got := o1.Exact.String(); got != "-4.921875" {
		t.Fatalf("expr exact = %s, want -4.921875", got)
	}
	if got := o1.Rounded.String(); got != "-4.921875" {
		t.Fatalf("expr rounded = %s, want -4.921875", got)
	}
	o3, err := e.Eval(ctx2)
	if err != nil {
		t.Fatal(err)
	}
	if got := o3.Rounded.String(); got != "-4.93" {
		t.Fatalf("expr rounded(ctx2) = %s, want -4.93", got)
	}
}

// TestExprIntermediatePrecision verifies per-node exact and rounded
// values against the rational reference, including mixed scales.
func TestExprIntermediatePrecision(t *testing.T) {
	ctx := Context{Precision: 3, Mode: HalfEven, MaxScale: 12}
	e := Mul(
		Add(Const(mustParse(t, "0.1")), Const(mustParse(t, "0.005"))),
		Div(Const(mustParse(t, "22")), Const(mustParse(t, "8"))),
	)
	checkExpr(t, e, ctx)

	e2 := Sub(
		Div(Const(mustParse(t, "-7")), Const(mustParse(t, "2"))),
		Neg(Const(mustParse(t, "0.125"))),
	)
	checkExpr(t, e2, Context{Precision: 1, Mode: ToZero, MaxScale: 4})
}

// TestExprInexactPropagation checks that a non-terminating division
// nils Exact up the tree while rounded values still track the reference.
func TestExprInexactPropagation(t *testing.T) {
	ctx := Context{Precision: 5, Mode: HalfEven, MaxScale: 10}
	e := Add(
		Div(Const(mustParse(t, "1")), Const(mustParse(t, "3"))),
		Const(mustParse(t, "0.25")),
	)
	o, err := e.Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if o.Exact != nil {
		t.Fatalf("Exact = %s, want nil (1/3 is non-terminating)", o.Exact)
	}
	checkExpr(t, e, ctx)
	// Rounded: round(1/3, 5) + 0.25 = 0.33333 + 0.25 = 0.58333.
	if got := o.Rounded.String(); got != "0.58333" {
		t.Fatalf("Rounded = %s, want 0.58333", got)
	}
}

func TestExprDivByZero(t *testing.T) {
	ctx := Context{Precision: 2, Mode: HalfEven, MaxScale: 4}
	e := Div(Const(mustParse(t, "1")), Sub(Const(mustParse(t, "0.5")), Const(mustParse(t, "0.5"))))
	if _, err := e.Eval(ctx); err != ErrDivisionByZero {
		t.Fatalf("err = %v, want ErrDivisionByZero", err)
	}
}

// TestExprHugeExponents evaluates expressions over very large exponents.
func TestExprHugeExponents(t *testing.T) {
	ctx := Context{Precision: 4, Mode: HalfEven, MaxScale: 8}
	e := Add(
		Const(New(1, 20000)),
		Mul(Const(New(15, -1)), Const(New(1, -20000))),
	)
	checkExpr(t, e, ctx)
	o, err := e.Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if o.Exact == nil {
		t.Fatal("exact value must exist for add/mul")
	}
	// 1e20000 + 1.5e-20000: "1" + 20000 zeros + "." + 19999 zeros + "15".
	s := o.Exact.String()
	if len(s) != 40003 || !strings.HasPrefix(s, "1") || !strings.HasSuffix(s, "15") {
		t.Fatalf("unexpected exact form, len=%d head=%.6s tail=%.6s", len(s), s, s[len(s)-6:])
	}
	ex, _, err := refEval(e, ctx)
	if err != nil || ex == nil {
		t.Fatalf("refEval: %v", err)
	}
	if o.Exact.Rat().Cmp(ex) != 0 {
		t.Fatalf("exact mismatch on huge exponent expr")
	}
}
