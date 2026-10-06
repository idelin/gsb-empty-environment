// Package ftoa converts any finite IEEE 754 binary64 value into the shortest
// decimal string that parses back to exactly the same value.
//
// Among all shortest candidates the one numerically closest to the value is
// chosen; if two candidates are equally close, the one whose last digit is
// even wins. Signed zeros are preserved ("0" / "-0"). Non-finite values map
// to the independent labels "NaN", "+Inf" and "-Inf".
//
// The search over candidate lengths and the selection between candidates are
// implemented here with exact big-integer arithmetic; the standard library's
// float<->string conversions are used only by the tests as a reference
// oracle.
package ftoa

import (
	"math"
	"math/big"
	"strings"
)

// Format returns the shortest decimal string that round-trips to f.
func Format(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "+Inf"
	case math.IsInf(f, -1):
		return "-Inf"
	}
	if f == 0 {
		if math.Signbit(f) {
			return "-0"
		}
		return "0"
	}
	neg := math.Signbit(f)
	if neg {
		f = -f
	}
	digits, exp := shortestDigits(f)
	s := formatDecimal(digits, exp)
	if neg {
		return "-" + s
	}
	return s
}

var (
	bigOne = big.NewInt(1)
)

// pow10tab holds 10^e for e in [0, 350].
var pow10tab [351]*big.Int

func init() {
	pow10tab[0] = big.NewInt(1)
	for e := 1; e < len(pow10tab); e++ {
		pow10tab[e] = new(big.Int).Mul(pow10tab[e-1], big.NewInt(10))
	}
}

func pow10(e int) *big.Int { return pow10tab[e] }

// shortestDigits returns the significant decimal digits of the shortest
// round-tripping representation of f (f > 0, finite) together with the
// decimal exponent: value = digits[0].digits[1:] * 10^exp.
func shortestDigits(f float64) (string, int) {
	// Exact decomposition f = m * 2^e2 with 2^52 <= m < 2^53.
	frac, exp := math.Frexp(f)
	m := int64(math.Ldexp(frac, 53))
	e2 := exp - 53

	// Rounding interval (lo, hi): every real number inside it rounds to f
	// under round-to-nearest-even. The boundaries are the midpoints to the
	// neighbouring floats; they belong to the interval iff f's mantissa is
	// even (ties go to the even mantissa). lo, f and hi are scaled to a
	// common exponent e3: lo = mLo * 2^e3, f = mF * 2^e3, hi = mHi * 2^e3.
	// The gap to the neighbours is 2^eGap; for subnormals the spacing is
	// pinned at 2^-1074 even though the normalised decomposition uses a
	// smaller e2.
	eGap := e2
	if eGap < -1074 {
		eGap = -1074
	}
	shift := e2 - eGap + 2 // exact in both directions
	mF := m
	if shift >= 0 {
		mF = m << uint(shift)
	} else {
		mF = m >> uint(-shift)
	}
	mLo := mF - 2
	mHi := mF + 2
	if m == 1<<52 && eGap > -1074 {
		// Normal power of two: the gap below is half the gap above.
		mLo = mF - 1
	}
	e3 := eGap - 2
	inclusive := math.Float64bits(f)&1 == 0

	exp10 := decExp(m, e2) // 10^exp10 <= f < 10^(exp10+1)
	for p := 1; p <= 17; p++ {
		q := exp10 - p + 1 // candidates are k * 10^q with p-digit k

		// For v = mV * 2^e3, v / 10^q = (mV * num) / den.
		num := new(big.Int)
		den := new(big.Int)
		if e3 >= 0 {
			num.Lsh(bigOne, uint(e3))
			den.Set(bigOne)
		} else {
			num.Set(bigOne)
			den.Lsh(bigOne, uint(-e3))
		}
		if q >= 0 {
			den.Mul(den, pow10(q))
		} else {
			num.Mul(num, pow10(-q))
		}

		loN := new(big.Int).Mul(num, big.NewInt(mLo))
		fN := new(big.Int).Mul(num, big.NewInt(mF))
		hiN := new(big.Int).Mul(num, big.NewInt(mHi))

		// kLo / kHi: integer k range whose scaled value lies in (lo, hi),
		// honouring boundary inclusivity.
		kLo, rem := new(big.Int).QuoRem(loN, den, new(big.Int))
		if inclusive {
			if rem.Sign() != 0 {
				kLo.Add(kLo, bigOne)
			}
		} else {
			kLo.Add(kLo, bigOne)
		}
		kHi, rem2 := new(big.Int).QuoRem(hiN, den, new(big.Int))
		if !inclusive && rem2.Sign() == 0 {
			kHi.Sub(kHi, bigOne)
		}

		// Restrict to exactly p significant digits. 10^p itself is allowed
		// (it normalises to 1 * 10^(exp10+1)).
		if min := pow10(p - 1); kLo.Cmp(min) < 0 {
			kLo.Set(min)
		}
		if max := pow10(p); kHi.Cmp(max) > 0 {
			kHi.Set(max)
		}
		if kLo.Cmp(kHi) > 0 {
			continue // no p-digit candidate round-trips
		}

		k := closestInt(fN, den, kLo, kHi)
		if k.Cmp(pow10(p)) == 0 {
			return "1", exp10 + 1
		}
		return k.String(), exp10
	}
	panic("ftoa: no 17-digit candidate round-trips, unreachable")
}

// closestInt returns the integer in [lo, hi] closest to the rational
// num/den; ties are broken towards the even integer.
func closestInt(num, den, lo, hi *big.Int) *big.Int {
	clamp := func(k *big.Int) *big.Int {
		if k.Cmp(lo) < 0 {
			return new(big.Int).Set(lo)
		}
		if k.Cmp(hi) > 0 {
			return new(big.Int).Set(hi)
		}
		return k
	}
	floor := new(big.Int).Quo(num, den) // num, den > 0
	a := clamp(floor)
	b := clamp(new(big.Int).Add(floor, bigOne))
	if a.Cmp(b) == 0 {
		return a
	}
	da := new(big.Int).Sub(new(big.Int).Mul(a, den), num)
	da.Abs(da)
	db := new(big.Int).Sub(new(big.Int).Mul(b, den), num)
	db.Abs(db)
	switch da.Cmp(db) {
	case -1:
		return a
	case 1:
		return b
	}
	if a.Bit(0) == 0 {
		return a
	}
	return b
}

// decExp returns e such that 10^e <= m * 2^e2 < 10^(e+1), using exact
// big-integer comparisons.
func decExp(m int64, e2 int) int {
	f := math.Ldexp(float64(m), e2)
	e := int(math.Floor(math.Log10(f)))
	for cmpPow10(m, e2, e) < 0 {
		e--
	}
	for cmpPow10(m, e2, e+1) >= 0 {
		e++
	}
	return e
}

// cmpPow10 compares m * 2^e2 with 10^e.
func cmpPow10(m int64, e2, e int) int {
	lhs := new(big.Int).SetInt64(m)
	rhs := new(big.Int).Set(bigOne)
	if e2 >= 0 {
		lhs.Lsh(lhs, uint(e2))
	} else {
		rhs.Lsh(bigOne, uint(-e2))
	}
	if e >= 0 {
		rhs.Mul(rhs, pow10(e))
	} else {
		lhs.Mul(lhs, pow10(-e))
	}
	return lhs.Cmp(rhs)
}

// formatDecimal renders digits with decimal exponent exp using the same
// layout rules as Go's 'g' verb for shortest output: scientific notation
// when exp < -4 || exp >= 6, plain decimal notation otherwise.
func formatDecimal(digits string, exp int) string {
	if exp < -4 || exp >= 6 {
		var b strings.Builder
		b.WriteByte(digits[0])
		if len(digits) > 1 {
			b.WriteByte('.')
			b.WriteString(digits[1:])
		}
		b.WriteByte('e')
		if exp < 0 {
			b.WriteByte('-')
			exp = -exp
		} else {
			b.WriteByte('+')
		}
		es := itoa(exp)
		if len(es) < 2 {
			b.WriteByte('0')
		}
		b.WriteString(es)
		return b.String()
	}
	dp := exp + 1 // digits before the decimal point
	switch {
	case dp <= 0:
		return "0." + strings.Repeat("0", -dp) + digits
	case dp >= len(digits):
		return digits + strings.Repeat("0", dp-len(digits))
	default:
		return digits[:dp] + "." + digits[dp:]
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
