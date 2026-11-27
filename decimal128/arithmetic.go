package decimal128

// Result type inference follows the conventional SQL rules (the same family
// used by SQL Server's decimal arithmetic), clamped to MaxPrecision:
//
//   add/sub: p = p1 - s1 + s2 + max(6, s1 - p2 + 1), s = max(6, s1 - p2 + 1)
//   mul:     p = p1 + p2 + 1,                                  s = s1 + s2
//   div:     p = p1 - s1 + s2 + max(6, s1 + p2 + 1),           s = max(6, s1 + p2 + 1)
//
// Whenever the inferred precision exceeds 38 the scale is reduced by the
// excess first; scale never drops below 6 for mul/div, and never below zero
// for add/sub. If even scale zero cannot keep precision within 38 the
// operation fails with ErrOverflow.

func inferAddSub(p1, s1, p2, s2 int) (int, int) {
	p := p1 - s1 + s2 + max6(6, s1-p2+1)
	s := max6(6, s1-p2+1)
	return clampType(p, s)
}

func inferMul(p1, s1, p2, s2 int) (int, int) {
	return clampType(p1+p2+1, s1+s2)
}

func inferDiv(p1, s1, p2, s2 int) (int, int) {
	p := p1 - s1 + s2 + max6(6, s1+p2+1)
	s := max6(6, s1+p2+1)
	return clampType(p, s)
}

func max6(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// clampType reduces scale while precision exceeds MaxPrecision. The scale
// is cut by the exact excess precision, never below zero; if not enough
// scale exists to absorb the excess, the returned precision stays above
// MaxPrecision as an overflow marker that callers check via finish().
func clampType(p, s int) (int, int) {
	if s > p {
		s = p
	}
	if p <= MaxPrecision {
		return p, s
	}
	// SQL rule: reduce the scale by exactly the excess precision; the
	// minScale floor applies to the *inferred* scale before clamping, and
	// the result scale simply cannot go below zero.
	drop := p - MaxPrecision
	if drop > s {
		// Not enough scale to absorb the excess: the value overflows.
		return p - s, 0
	}
	return p - drop, s - drop
}

// Add returns d1+d2. Operands are losslessly aligned to the larger scale;
// the result type is derived with inferAddSub and excess digits are rounded.
func (c Context) Add(d1, d2 Decimal128) (Decimal128, error) {
	return c.addSub(d1, d2, false)
}

// Sub returns d1-d2 with the same alignment and inference rules as Add.
func (c Context) Sub(d1, d2 Decimal128) (Decimal128, error) {
	return c.addSub(d1, d2, true)
}

func (c Context) addSub(d1, d2 Decimal128, negateSecond bool) (Decimal128, error) {
	_, s := inferAddSub(d1.prec, d1.scale, d2.prec, d2.scale)
	alignScale := d1.scale
	if d2.scale > alignScale {
		alignScale = d2.scale
	}

	v1, err := c.rescaleTo(d1.coeff, d1.scale, alignScale)
	if err != nil {
		return Decimal128{}, err
	}
	v2, err := c.rescaleTo(d2.coeff, d2.scale, alignScale)
	if err != nil {
		return Decimal128{}, err
	}
	if negateSecond {
		v2 = v2.neg()
	}
	sum := addSigned(v1, v2)

	if s < alignScale {
		sum, err = c.rescaleTo(sum, alignScale, s)
		if err != nil {
			return Decimal128{}, err
		}
	} else if s > alignScale {
		sum, err = c.rescaleTo(sum, alignScale, s)
		if err != nil {
			return Decimal128{}, err
		}
	}
	// The inferred p is a capacity; the stored precision tracks the actual
	// significant digits so later operations get tight bounds.
	actual := precisionFor(sum, s)
	return finish(sum, actual, s)
}

// Mul returns d1*d2. The raw product is kept at scale s1+s2 in a 256-bit
// accumulator before any scale reduction, so no digit is lost prematurely.
func (c Context) Mul(d1, d2 Decimal128) (Decimal128, error) {
	_, s := inferMul(d1.prec, d1.scale, d2.prec, d2.scale)
	rawScale := d1.scale + d2.scale
	prod := d1.coeff.mag().to256().mul(d2.coeff.mag().to256())
	negative := (d1.coeff.hi < 0) != (d2.coeff.hi < 0)

	if s >= rawScale {
		prod = prod.mul(pow10u256[s-rawScale])
		if !prod.fits128() {
			return Decimal128{}, ErrOverflow
		}
		v := fromMag(prod.as128(), negative)
		return finish(v, precisionFor(v, s), s)
	}
	qm, err := c.roundMag(prod, rawScale-s)
	if err != nil {
		return Decimal128{}, ErrOverflow
	}
	v := fromMag(qm, negative)
	return finish(v, precisionFor(v, s), s)
}

// Div returns d1/d2. Division works on the exact rational ratio of the
// coefficients: the numerator is widened to 256 bits and multiplied by
// 10^k before an unsigned long division, whose exact remainder drives the
// half-way rounding decision.
func (c Context) Div(d1, d2 Decimal128) (Decimal128, error) {
	if d2.coeff.isZero() {
		return Decimal128{}, ErrDivisionByZero
	}
	_, s := inferDiv(d1.prec, d1.scale, d2.prec, d2.scale)

	numMag := d1.coeff.mag()
	denMag := d2.coeff.mag()
	// coeff(result) = (coeff(d1) * 10^(s + s2 - s1)) / coeff(d2)
	k := s + d2.scale - d1.scale
	var num, den u256
	switch {
	case k > 0:
		num = numMag.to256().mul(pow10u256[k])
		den = denMag.to256()
	case k < 0:
		// Fold 10^-k into the divisor instead of truncating the numerator,
		// so the division still produces an exact half-point remainder.
		num = numMag.to256()
		den = denMag.to256().mul(pow10u256[-k])
	default:
		num = numMag.to256()
		den = denMag.to256()
	}

	q256, rem := num.div(den)
	if !q256.fits128() {
		return Decimal128{}, ErrOverflow
	}
	q := q256.as128()
	if !rem.isZero() {
		twice := rem.shlOne()
		cmp := twice.cmp(den)
		if cmp > 0 {
			q, _ = q.add(u128{lo: 1}, 0)
		} else if cmp == 0 {
			switch c.Mode {
			case HalfAwayFromZero:
				q, _ = q.add(u128{lo: 1}, 0)
			case HalfToEven:
				if q.lo&1 == 1 {
					q, _ = q.add(u128{lo: 1}, 0)
				}
			}
		}
	}
	negative := (d1.coeff.hi < 0) != (d2.coeff.hi < 0)
	v := fromMag(q, negative)
	return finish(v, precisionFor(v, s), s)
}

// Mod returns the SQL remainder d1 MOD d2. The quotient truncates toward
// zero, so the result keeps the sign of the dividend. A zero divisor yields
// ErrDivisionByZero. Both operands are aligned losslessly first.
func (c Context) Mod(d1, d2 Decimal128) (Decimal128, error) {
	if d2.coeff.isZero() {
		return Decimal128{}, ErrDivisionByZero
	}
	s := d1.scale
	if d2.scale > s {
		s = d2.scale
	}
	v1, err := c.rescaleTo(d1.coeff, d1.scale, s)
	if err != nil {
		return Decimal128{}, err
	}
	v2, err := c.rescaleTo(d2.coeff, d2.scale, s)
	if err != nil {
		return Decimal128{}, err
	}
	_, rem := divmodSigned(v1, v2)
	p := precisionFor(rem, s)
	return finish(rem, p, s)
}

// QuoRem returns the truncated-toward-zero quotient and remainder together.
// sign(rem) == sign(d1); d2 == 0 returns ErrDivisionByZero.
func (c Context) QuoRem(d1, d2 Decimal128) (Decimal128, Decimal128, error) {
	if d2.coeff.isZero() {
		return Decimal128{}, Decimal128{}, ErrDivisionByZero
	}
	s := d1.scale
	if d2.scale > s {
		s = d2.scale
	}
	v1, err := c.rescaleTo(d1.coeff, d1.scale, s)
	if err != nil {
		return Decimal128{}, Decimal128{}, err
	}
	v2, err := c.rescaleTo(d2.coeff, d2.scale, s)
	if err != nil {
		return Decimal128{}, Decimal128{}, err
	}
	quot, rem := divmodSigned(v1, v2)
	// divmodSigned returns the integer quotient at scale 0; re-widen it to
	// the aligned scale so q*b + r reconstructs d1 exactly. The remainder is
	// already expressed in the aligned coefficient space and stays as is.
	quot, err = c.rescaleTo(quot, 0, s)
	if err != nil {
		return Decimal128{}, Decimal128{}, err
	}
	qp := precisionFor(quot, s)
	q, err := finish(quot, qp, s)
	if err != nil {
		return Decimal128{}, Decimal128{}, err
	}
	// Both outputs share the aligned scale so q*b + r reconstructs d1 with
	// the same scale the operands were aligned to.
	rp := precisionFor(rem, s)
	r, err := finish(rem, rp, s)
	if err != nil {
		return Decimal128{}, Decimal128{}, err
	}
	return q, r, nil
}

// Cmp compares the exact values of d1 and d2 without any rounding:
// it returns -1, 0 or +1.
func Cmp(d1, d2 Decimal128) int {
	s1, s2 := d1.Sign(), d2.Sign()
	if s1 != s2 {
		return s1
	}
	if s1 == 0 {
		return 0
	}
	// Cross-multiply magnitudes in 256 bits: |c1|*10^s2 vs |c2|*10^s1.
	left := d1.coeff.mag().to256().mul(pow10u256[d2.scale])
	right := d2.coeff.mag().to256().mul(pow10u256[d1.scale])
	cmp := left.cmp(right)
	if s1 < 0 {
		return -cmp
	}
	return cmp
}

// Package-level wrappers using DefaultContext (HalfAwayFromZero).

func Add(d1, d2 Decimal128) (Decimal128, error) { return DefaultContext.Add(d1, d2) }
func Sub(d1, d2 Decimal128) (Decimal128, error) { return DefaultContext.Sub(d1, d2) }
func Mul(d1, d2 Decimal128) (Decimal128, error) { return DefaultContext.Mul(d1, d2) }
func Div(d1, d2 Decimal128) (Decimal128, error) { return DefaultContext.Div(d1, d2) }
func Mod(d1, d2 Decimal128) (Decimal128, error) { return DefaultContext.Mod(d1, d2) }

// QuoRem package-level wrapper using DefaultContext.
func QuoRem(d1, d2 Decimal128) (Decimal128, Decimal128, error) {
	return DefaultContext.QuoRem(d1, d2)
}
