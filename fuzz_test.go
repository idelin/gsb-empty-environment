package decimal

import (
	"math/big"
	"math/rand"
	"testing"
)

var allModes = []RoundingMode{ToZero, ToPosInf, ToNegInf, HalfEven}

// randDecimal builds a random decimal with up to maxDigits mantissa
// digits and exponent in [-maxExp, maxExp].
func randDecimal(rng *rand.Rand, maxDigits int, maxExp int64) Decimal {
	if rng.Intn(25) == 0 {
		return Zero
	}
	nd := 1 + rng.Intn(maxDigits)
	b := make([]byte, nd)
	b[0] = byte('1' + rng.Intn(9))
	for i := 1; i < nd; i++ {
		b[i] = byte('0' + rng.Intn(10))
	}
	m, _ := new(big.Int).SetString(string(b), 10)
	if rng.Intn(2) == 0 {
		m.Neg(m)
	}
	e := int64(rng.Intn(int(2*maxExp+1))) - maxExp
	return NewFromInt(m, e)
}

// TestFuzzOpsAgainstRat cross-checks add/sub/mul/div outcomes against
// the rational reference with random scales, precisions and modes.
func TestFuzzOpsAgainstRat(t *testing.T) {
	rng := rand.New(rand.NewSource(20261005))
	for i := 0; i < 400; i++ {
		a := randDecimal(rng, 30, 15)
		b := randDecimal(rng, 30, 15)
		ctx := Context{
			Precision: uint32(rng.Intn(16)),
			Mode:      allModes[rng.Intn(4)],
			MaxScale:  uint32(rng.Intn(45)),
		}
		ra, rb := a.Rat(), b.Rat()
		checkOutcome(t, "add", ctx.Add(a, b), new(big.Rat).Add(ra, rb), ctx, true)
		checkOutcome(t, "sub", ctx.Sub(a, b), new(big.Rat).Sub(ra, rb), ctx, true)
		checkOutcome(t, "mul", ctx.Mul(a, b), new(big.Rat).Mul(ra, rb), ctx, true)
		if b.Sign() == 0 {
			if _, err := ctx.Div(a, b); err != ErrDivisionByZero {
				t.Fatalf("div by zero: err = %v", err)
			}
			continue
		}
		o, err := ctx.Div(a, b)
		if err != nil {
			t.Fatalf("div: %v", err)
		}
		checkDiv(t, o, new(big.Rat).Quo(ra, rb), ctx)
	}
}

// TestFuzzRoundAgainstRat cross-checks Decimal.Round on random values
// and scales against the independent rational reference.
func TestFuzzRoundAgainstRat(t *testing.T) {
	rng := rand.New(rand.NewSource(777))
	for i := 0; i < 500; i++ {
		d := randDecimal(rng, 40, 25)
		scale := uint32(rng.Intn(30))
		mode := allModes[rng.Intn(4)]
		got := d.Round(scale, mode).Rat()
		want := roundRatRef(d.Rat(), scale, mode)
		if got.Cmp(want) != 0 {
			t.Fatalf("Round(%s, %d, %d) = %s, want %s", d, scale, mode, got, want)
		}
	}
}

// TestFuzzExprAgainstRat evaluates random expression DAGs (with shared
// subtrees) and checks every node against the rational reference.
func TestFuzzExprAgainstRat(t *testing.T) {
	rng := rand.New(rand.NewSource(4242))
	for i := 0; i < 200; i++ {
		ctx := Context{
			Precision: uint32(rng.Intn(12)),
			Mode:      allModes[rng.Intn(4)],
			MaxScale:  uint32(rng.Intn(30)),
		}
		e := randExpr(rng, 3)
		checkExpr(t, e, ctx)
	}
}

// randExpr builds a random expression DAG of the given depth; subtrees
// are occasionally shared between parents.
func randExpr(rng *rand.Rand, depth int) *Expr {
	if depth == 0 || rng.Intn(4) == 0 {
		return Const(randDecimal(rng, 12, 8))
	}
	left := randExpr(rng, depth-1)
	right := randExpr(rng, depth-1)
	if rng.Intn(3) == 0 {
		right = left // share the subtree
	}
	switch rng.Intn(5) {
	case 0:
		return Add(left, right)
	case 1:
		return Sub(left, right)
	case 2:
		return Mul(left, right)
	case 3:
		return Neg(left)
	default:
		return Div(left, right)
	}
}
