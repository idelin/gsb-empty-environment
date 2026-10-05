package decimal

import "math/big"

// Context carries the precision, rounding mode and exactness budget for
// operations that return an Outcome.
type Context struct {
	// Precision is the number of fractional decimal digits of the
	// rounded result.
	Precision uint32
	// Mode is the rounding mode applied to produce the rounded result.
	Mode RoundingMode
	// MaxScale is the exactness budget: a division result is reported
	// exact only if its finite decimal expansion needs at most MaxScale
	// fractional digits. When the budget is insufficient the Outcome
	// reports no exact value instead of padding zeros and faking one.
	MaxScale uint32
}

// Outcome is the result of a Context operation: the exact value when it is
// representable within budget, and always the value rounded to the
// context precision.
type Outcome struct {
	// Exact is the exact result, or nil when it has no finite decimal
	// expansion within the context budget (e.g. 1/3, or 1/8192 with
	// MaxScale < 13). It is never a truncated, zero-padded stand-in.
	Exact *Decimal
	// Rounded is the result rounded to Context.Precision fractional
	// digits using Context.Mode.
	Rounded Decimal
}

// finish builds an Outcome from a known exact value.
func (c Context) finish(exact Decimal) Outcome {
	e := exact
	return Outcome{Exact: &e, Rounded: exact.Round(c.Precision, c.Mode)}
}

// Add returns the exact sum and its rounding to the context precision.
func (c Context) Add(a, b Decimal) Outcome {
	return c.finish(a.Add(b))
}

// Sub returns the exact difference and its rounding to the context precision.
func (c Context) Sub(a, b Decimal) Outcome {
	return c.finish(a.Sub(b))
}

// Mul returns the exact product and its rounding to the context precision.
func (c Context) Mul(a, b Decimal) Outcome {
	return c.finish(a.Mul(b))
}

// Div returns the quotient a / b. It reports ErrDivisionByZero when b is
// zero. The exact quotient is included only when it has a finite decimal
// expansion requiring at most MaxScale fractional digits; otherwise
// Outcome.Exact is nil and Outcome.Rounded holds the correctly rounded
// quotient at the context precision.
func (c Context) Div(a, b Decimal) (Outcome, error) {
	if b.Sign() == 0 {
		return Outcome{}, ErrDivisionByZero
	}
	if a.Sign() == 0 {
		return c.finish(Zero), nil
	}
	if exact, ok := divExact(a, b, c.MaxScale); ok {
		return c.finish(exact), nil
	}
	return Outcome{Exact: nil, Rounded: divRound(a, b, c.Precision, c.Mode)}, nil
}

// divExact computes the exact quotient a/b (both nonzero) when it has a
// finite decimal expansion needing at most maxScale fractional digits.
// Exactness is decided by factoring the reduced denominator, never by
// generating digits and inspecting a remainder, so a truncated quotient
// can never masquerade as exact.
func divExact(a, b Decimal, maxScale uint32) (Decimal, bool) {
	absA := new(big.Int).Abs(a.mantissa())
	absB := new(big.Int).Abs(b.mantissa())
	g := new(big.Int).GCD(nil, nil, absA, absB)
	n := new(big.Int).Quo(a.mantissa(), g)
	d := new(big.Int).Quo(b.mantissa(), g)
	if d.Sign() < 0 {
		n.Neg(n)
		d.Neg(d)
	}
	// The quotient terminates iff the reduced denominator has no prime
	// factors other than 2 and 5.
	twos := stripFactor(d, bigTwo)
	fives := stripFactor(d, bigFive)
	if d.Cmp(bigOne) != 0 {
		return Decimal{}, false
	}
	m := twos
	if fives > m {
		m = fives
	}
	exp := a.exp - b.exp - m
	scale := -exp
	if scale < 0 {
		scale = 0
	}
	if scale > int64(maxScale) {
		return Decimal{}, false
	}
	mant := n
	if k := m - twos; k > 0 {
		mant.Mul(mant, new(big.Int).Exp(bigTwo, big.NewInt(k), nil))
	}
	if k := m - fives; k > 0 {
		mant.Mul(mant, new(big.Int).Exp(bigFive, big.NewInt(k), nil))
	}
	return makeDecimal(mant, exp), true
}

// stripFactor divides d (positive) by f as many times as possible,
// returning the count. d is modified in place.
func stripFactor(d *big.Int, f *big.Int) int64 {
	q := new(big.Int)
	r := new(big.Int)
	var n int64
	for {
		q.QuoRem(d, f, r)
		if r.Sign() != 0 {
			return n
		}
		d.Set(q)
		n++
	}
}

// divRound returns a/b (b nonzero) rounded to p fractional digits.
func divRound(a, b Decimal, p uint32, mode RoundingMode) Decimal {
	shift := a.exp - b.exp + int64(p)
	num := new(big.Int).Set(a.mantissa())
	den := new(big.Int).Set(b.mantissa())
	if shift >= 0 {
		num.Mul(num, pow10(shift))
	} else {
		den.Mul(den, pow10(-shift))
	}
	q := new(big.Int)
	r := new(big.Int)
	q.QuoRem(num, den, r)
	roundQuotient(q, r, den, mode)
	return makeDecimal(q, -int64(p))
}
