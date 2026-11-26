package decimal128

// ResultTypeAdd derives the SQL DECIMAL(p, s) result type for addition and
// subtraction (SQL standard / most engines):
//
//	s = max(s1, s2)
//	p = max(p1-s1, p2-s2) + s + 1
//
// capped at precision 38 with the canonical scale reduction rule applied.
func ResultTypeAdd(p1, s1, p2, s2 int) (p int, s int) {
	s = s1
	if s2 > s {
		s = s2
	}
	integer := p1 - s1
	if p2-s2 > integer {
		integer = p2 - s2
	}
	p = integer + s + 1
	if p > MaxPrecision {
		p = MaxPrecision
		if s > MaxPrecision-integer-1 {
			s = MaxPrecision - integer - 1
		}
		if s < 0 {
			s = 0
		}
	}
	return p, s
}

// ResultTypeMul derives the SQL result type for multiplication:
//
//	s = s1 + s2 (capped at 38)
//	p = p1 + p2 + 1 (capped at 38)
func ResultTypeMul(p1, s1, p2, s2 int) (precision, scale int) {
	scale = s1 + s2
	if scale > MaxScale {
		scale = MaxScale
	}
	precision = p1 + p2 + 1
	if precision > MaxPrecision {
		precision = MaxPrecision
	}
	return precision, scale
}

// ResultTypeDiv derives the SQL Server / common-engineering result type for
// division:
//
//	s = max(6, s1 + p2 + 1)
//	p = max(p1 - s1, p2 - s2) + s + 1
//
// both capped at 38. When the cap binds, the scale is reduced so the result
// still reserves enough integer digits.
func ResultTypeDiv(p1, s1, p2, s2 int) (precision, scale int) {
	integer := p1 - s1
	if p2-s2 > integer {
		integer = p2 - s2
	}
	scale = 6
	if cand := s1 + p2 + 1; cand > scale {
		scale = cand
	}
	if scale > MaxScale {
		scale = MaxScale
	}
	precision = integer + scale + 1
	if precision > MaxPrecision {
		precision = MaxPrecision
		scale = MaxPrecision - integer - 1
		if scale < 0 {
			scale = 0
		}
	}
	return precision, scale
}

// Add returns d + e using the default rounding mode.
func Add(d, e Decimal128) (Decimal128, error) {
	return AddMode(d, e, DefaultRoundingMode)
}

// Sub returns d - e using the default rounding mode.
func Sub(d, e Decimal128) (Decimal128, error) {
	return SubMode(d, e, DefaultRoundingMode)
}

// AddMode returns d + e with explicit rounding for any required scale cut.
func AddMode(d, e Decimal128, mode RoundingMode) (Decimal128, error) {
	a, b, scale := alignWide(d, e)
	return canonicalize(addWide(a, b, false), scale, mode)
}

// SubMode returns d - e with explicit rounding for any required scale cut.
func SubMode(d, e Decimal128, mode RoundingMode) (Decimal128, error) {
	a, b, scale := alignWide(d, e)
	return canonicalize(addWide(a, b, true), scale, mode)
}

// Neg returns -d.
func Neg(d Decimal128) Decimal128 {
	return Decimal128{coeff: d.coeff.neg(), precision: d.precision, scale: d.scale}
}

// Mul returns d * e using the default rounding mode.
func Mul(d, e Decimal128) (Decimal128, error) {
	return MulMode(d, e, DefaultRoundingMode)
}

// MulMode multiplies via the exact internal 256-bit product (10^76 < 2^253)
// and then reduces the scale s1+s2 according to mode.
func MulMode(d, e Decimal128, mode RoundingMode) (Decimal128, error) {
	am, asign := d.coeff.magnitude()
	bm, bsign := e.coeff.magnitude()
	w := wide{
		mag:      mulU128(am, bm),
		negative: (asign < 0) != (bsign < 0),
	}
	// SQL derivation: s = s1+s2 capped at 38; canonicalize performs any
	// rounding needed to reach the capped scale.
	return canonicalize(w, int(d.scale)+int(e.scale), mode)
}

// Div returns d / e using the default rounding mode.
func Div(d, e Decimal128) (Decimal128, error) {
	return DivMode(d, e, DefaultRoundingMode)
}

// DivMode performs exact long division of d by e. The result scale follows the
// standard derivation s' = max(6, s1 + p2 + 1) capped at 38; the coefficient
// is computed to that scale and rounded with mode using the exact remainder.
func DivMode(d, e Decimal128, mode RoundingMode) (Decimal128, error) {
	if e.IsZero() {
		return Decimal128{}, ErrDivisionByZero
	}
	targetScale := 6
	if cand := int(d.scale) + int(e.precision) + 1; cand > targetScale {
		targetScale = cand
	}
	if targetScale > maxScale {
		targetScale = maxScale
	}

	nm, nsign := d.coeff.magnitude()
	dm, dsign := e.coeff.magnitude()
	negative := (nsign < 0) != (dsign < 0)

	// The desired coefficient is |d|/|e| * 10^targetScale. We first long-divide
	// |d| / |e| and then scale the quotient by 10^k. Scaling after division
	// keeps every intermediate value bounded by 10^38 on each side.
	k := targetScale + int(e.scale) - int(d.scale)

	q, rem := u256FromU128(nm).divMod(u256FromU128(dm))
	if k >= 0 {
		// q is the integer quotient below 10^38; multiplying by 10^k with
		// k <= 38 stays inside 256 bits. The fractional part rem/dm is scaled
		// alongside it so the exact rounding remainder is preserved.
		scaledQ := q.mulPow10(k)
		scaledRem := rem.mulPow10(k)
		fq, frem := scaledRem.divMod(u256FromU128(dm))
		scaledQ = scaledQ.add256(fq)
		return finishDivision(scaledQ, frem, u256FromU128(dm), negative, targetScale, mode)
	}

	divisor := u256FromU128(pow10u128[-k])
	sq, srem := q.divMod(divisor)
	// Combine the two remainders into the residual against divisor*dm:
	// residual = srem*dm + rem.
	residual := srem.mulU256U128(dm).add256(rem)
	fullDivisor := divisor.mulU256U128(dm)
	return finishDivision(sq, residual, fullDivisor, negative, targetScale, mode)
}

func finishDivision(q, rem, divisor u256, negative bool, targetScale int, mode RoundingMode) (Decimal128, error) {
	up := false
	if !rem.isZero() {
		twice := rem.lsh1()
		switch mode {
		case Truncate:
			up = false
		case HalfAwayFromZero:
			// 2*rem >= divisor iff rem >= divisor/2 (half point, round away).
			up = twice.cmp(divisor) >= 0
		case HalfToEven:
			switch twice.cmp(divisor) {
			case 1:
				up = true
			case 0:
				up = q.v[0]&1 == 1
			}
		}
	}
	if up {
		q = q.add64(1)
	}
	return canonicalize(wide{mag: q, negative: negative}, targetScale, mode)
}

// Mod returns the remainder of d / e. Its sign follows the dividend d, and it
// satisfies d = Trunc(d/e)*e + Mod(d, e). Division by zero is an error.
func Mod(d, e Decimal128) (Decimal128, error) {
	if e.IsZero() {
		return Decimal128{}, ErrDivisionByZero
	}
	a, b, scale := alignWide(d, e)
	// Signed remainder: work on magnitudes, then reattach the dividend's sign.
	am, bm := a.mag, b.mag
	_, r := am.divMod(bm)
	return canonicalize(wide{mag: r, negative: a.negative && !r.isZero()}, scale, Truncate)
}

// QuoRem returns the truncated-toward-zero quotient and the remainder of
// d / e. The identity d = quo*e + rem always holds and rem shares the sign of
// d. Division by zero is an error.
func QuoRem(d, e Decimal128) (quo, rem Decimal128, err error) {
	if e.IsZero() {
		return Decimal128{}, Decimal128{}, ErrDivisionByZero
	}
	a, b, scale := alignWide(d, e)
	q, r := a.mag.divMod(b.mag)
	negQuo := (a.negative != b.negative) && !q.isZero()
	negRem := a.negative && !r.isZero()
	// The quotient counts whole aligned units; it is an integer in the
	// aligned representation, so it canonicalizes at scale 0.
	quo, err = canonicalize(wide{mag: q, negative: negQuo}, 0, Truncate)
	if err != nil {
		return Decimal128{}, Decimal128{}, err
	}
	rem, err = canonicalize(wide{mag: r, negative: negRem}, scale, Truncate)
	if err != nil {
		return Decimal128{}, Decimal128{}, err
	}
	return quo, rem, nil
}

// Cmp compares two decimals exactly after lossless scale alignment.
func Cmp(d, e Decimal128) (int, error) {
	a, b, _ := alignWide(d, e)
	if a.negative == b.negative {
		c := a.mag.cmp(b.mag)
		if a.negative {
			c = -c
		}
		return c, nil
	}
	if a.mag.isZero() && b.mag.isZero() {
		return 0, nil
	}
	if a.negative {
		return -1, nil
	}
	return 1, nil
}
