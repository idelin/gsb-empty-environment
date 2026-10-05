package decimal

import (
	"errors"
	"math/big"
	"testing"
)

func TestSharedExprNodes(t *testing.T) {
	x := Const(mustParse(t, "123456789.987654321"))
	y := Const(mustParse(t, "-0.000000001"))

	sum := Add(x, y)    // shared by two parents
	sq := Mul(sum, sum) // uses sum twice
	diff := Sub(sum, x) // should recover y exactly
	root := Add(sq, diff)

	res, err := root.Eval()
	if err != nil {
		t.Fatal(err)
	}
	if !res.Exact {
		t.Fatal("pure add/sub/mul tree must be exact")
	}

	xr := mustParse(t, "123456789.987654321").Rat()
	yr := mustParse(t, "-0.000000001").Rat()
	s := new(big.Rat).Add(xr, yr)
	want := new(big.Rat).Add(new(big.Rat).Mul(s, s), yr)
	checkRatEqual(t, "shared dag", res.Value, want)

	// Re-evaluation hits the cache and must return the same value.
	again, err := root.Eval()
	if err != nil || !again.Value.Equal(res.Value) {
		t.Fatalf("cached re-eval mismatch: %v vs %v (err %v)", again.Value, res.Value, err)
	}

	// Heavy use of a shared node's cached value in further pure operations
	// must not pollute it.
	sharedVal, _ := sum.Eval()
	before := sharedVal.Value.String()
	acc := sharedVal.Value
	for i := 0; i < 50; i++ {
		acc = acc.Mul(sharedVal.Value).Add(sharedVal.Value)
	}
	if after := sharedVal.Value.String(); after != before {
		t.Fatalf("shared node polluted: %s -> %s", before, after)
	}
	// Mutating an extracted coefficient copy must not reach the cache either.
	sharedVal.Value.Coeff().SetInt64(0)
	if after := sharedVal.Value.String(); after != before {
		t.Fatalf("shared node polluted via Coeff: %s -> %s", before, after)
	}
}

func TestExprDivAndErrors(t *testing.T) {
	third := Div(Const(NewInt(1)), Const(NewInt(3)), 10, TiesToEven)
	// Shared inexact node feeding two parents: inexactness propagates.
	tree := Add(Mul(third, Const(NewInt(3))), third)
	res, err := tree.Eval()
	if err != nil {
		t.Fatal(err)
	}
	if res.Exact {
		t.Fatal("tree containing inexact division must not claim exactness")
	}
	want := new(big.Rat).Add(
		new(big.Rat).Mul(big.NewRat(1, 3), big.NewRat(3, 1)),
		big.NewRat(1, 3),
	)
	rounded, _ := refRound(big.NewRat(1, 3), 10, TiesToEven)
	want = new(big.Rat).Add(new(big.Rat).Mul(rounded, big.NewRat(3, 1)), rounded)
	checkRatEqual(t, "expr div", res.Value, want)

	// Division by zero surfaces as its own error, even when shared.
	bad := Div(Const(NewInt(1)), Const(NewInt(0)), 5, ToZero)
	root := Add(bad, bad)
	if _, err := root.Eval(); !errors.Is(err, ErrDivByZero) {
		t.Fatalf("Eval err = %v, want ErrDivByZero", err)
	}
	if _, err := root.Eval(); !errors.Is(err, ErrDivByZero) {
		t.Fatalf("cached Eval err = %v, want ErrDivByZero", err)
	}
}

func TestExprExactnessPropagation(t *testing.T) {
	exact := Div(Const(NewInt(1)), Const(NewInt(2)), 5, TiesToEven)
	r, err := exact.Eval()
	if err != nil || !r.Exact || !r.Value.Equal(mustParse(t, "0.5")) {
		t.Fatalf("1/2 @5 = %s exact=%v err=%v", r.Value, r.Exact, err)
	}
	inexact := Div(Const(NewInt(2)), Const(NewInt(4)), 0, TiesToEven)
	r, err = inexact.Eval()
	if err != nil {
		t.Fatal(err)
	}
	if r.Exact {
		t.Fatal("0.5 rounded to integer budget must be inexact")
	}
	if r.Value.Cmp(NewInt(0)) != 0 {
		t.Fatalf("0.5 ties-to-even at scale 0 = %s, want 0", r.Value)
	}
}
