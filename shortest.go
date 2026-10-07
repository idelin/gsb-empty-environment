// Package shortest converts any finite IEEE 754 binary64 value to the shortest
// decimal string that parses back to the exact same value. Among shortest
// candidates it picks the numerically closest one, breaking exact ties toward
// an even last digit. Signed zero is preserved; NaN and infinities map to
// dedicated labels ("NaN", "+Inf", "-Inf").
//
// The digit search, rounding-interval computation and candidate selection are
// implemented from scratch on exact big-integer arithmetic. The standard
// library's strconv is only used by the tests as a cross-check.
package shortest

import (
	"math"
	"math/big"
	"strconv"
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
	a := f
	if neg {
		a = -a
	}
	digits, dp := shortestDigits(a)
	s := formatG(digits, dp)
	if neg {
		return "-" + s
	}
	return s
}

var pow10 [341]*big.Int // 10^0 .. 10^340, covers every decimal exponent of float64

func init() {
	pow10[0] = big.NewInt(1)
	for i := 1; i < len(pow10); i++ {
		pow10[i] = new(big.Int).Mul(pow10[i-1], big.NewInt(10))
	}
}

// shortestDigits returns the significant digits (no trailing zeros) and the
// decimal point position dp of the shortest decimal that rounds to f > 0,
// such that value = 0.digits * 10^dp.
func shortestDigits(f float64) (string, int) {
	bits := math.Float64bits(f)
	bexp := int(bits>>52) & 0x7ff
	mant := bits & (1<<52 - 1)
	var m uint64
	var e int // f = m * 2^e
	if bexp == 0 {
		m = mant
		e = -1074
	} else {
		m = mant | 1<<52
		e = bexp - 1075
	}

	// Rounding interval (low, high): every real in it rounds to f under
	// round-to-nearest-even. Boundaries are inclusive iff f's mantissa is
	// even. At a power of two the gap to the lower neighbor is halved.
	lowerGapHalf := m == 1<<52 && bexp > 1
	inclusive := m&1 == 0

	loP, loS := new(big.Int), e-1 // low  = loP * 2^loS
	hiP, hiS := new(big.Int), e-1 // high = hiP * 2^hiS
	if lowerGapHalf {
		loP.SetUint64(4*m - 1)
		loS--
	} else {
		loP.SetUint64(2*m - 1)
	}
	hiP.SetUint64(2*m + 1)

	v := ratPow2(m, e)
	decExp := decExpOf(v, f)

	for k := 1; k <= 17; k++ {
		E := decExp - k + 1 // candidates are d * 10^E with k-digit d

		// Smallest d with d*10^E >= low (resp. > low when exclusive).
		num, den := fracNumDen(loP, loS, E)
		q, r := new(big.Int).QuoRem(num, den, new(big.Int))
		dLo := q
		if r.Sign() != 0 || !inclusive {
			dLo = new(big.Int).Add(q, big.NewInt(1))
		}
		// Largest d with d*10^E <= high (resp. < high when exclusive).
		num, den = fracNumDen(hiP, hiS, E)
		q, r = new(big.Int).QuoRem(num, den, new(big.Int))
		dHi := q
		if r.Sign() == 0 && !inclusive {
			dHi = new(big.Int).Sub(q, big.NewInt(1))
		}

		// Restrict to k-digit coefficients; 10^k itself is allowed and
		// normalizes to 1 * 10^(E+k).
		if dLo.Cmp(pow10[k-1]) < 0 {
			dLo = new(big.Int).Set(pow10[k-1])
		}
		if dHi.Cmp(pow10[k]) > 0 {
			dHi = new(big.Int).Set(pow10[k])
		}
		if dLo.Cmp(dHi) > 0 {
			continue
		}

		best := selectClosest(dLo, dHi, E, v)
		return normalize(best, E)
	}
	panic("shortest: no representation found")
}

// selectClosest returns the integer in [dLo, dHi] whose scaled value d*10^E is
// closest to v, breaking exact ties toward the even coefficient.
func selectClosest(dLo, dHi *big.Int, E int, v *big.Rat) *big.Int {
	best := new(big.Int)
	var bestDist *big.Rat
	d := new(big.Int).Set(dLo)
	one := big.NewInt(1)
	for d.Cmp(dHi) <= 0 {
		dist := distance(d, E, v)
		if bestDist == nil || dist.Cmp(bestDist) < 0 ||
			(dist.Cmp(bestDist) == 0 && d.Bit(0) == 0 && best.Bit(0) == 1) {
			best.Set(d)
			bestDist = dist
		}
		d.Add(d, one)
	}
	return best
}

func distance(d *big.Int, E int, v *big.Rat) *big.Rat {
	c := new(big.Rat).SetInt(d)
	if E >= 0 {
		c.Mul(c, new(big.Rat).SetInt(pow10[E]))
	} else {
		c.Quo(c, new(big.Rat).SetInt(pow10[-E]))
	}
	c.Sub(c, v)
	return c.Abs(c)
}

// normalize strips trailing zeros from d and returns its digit string plus
// the decimal point position dp with value = 0.digits * 10^dp.
func normalize(d *big.Int, E int) (string, int) {
	s := d.String()
	t := strings.TrimRight(s, "0")
	E += len(s) - len(t)
	return t, E + len(t)
}

// fracNumDen expresses p * 2^s / 10^E as num/den with positive integers.
func fracNumDen(p *big.Int, s, E int) (num, den *big.Int) {
	num = new(big.Int).Set(p)
	den = big.NewInt(1)
	if s >= 0 {
		num.Lsh(num, uint(s))
	} else {
		den.Lsh(den, uint(-s))
	}
	if E >= 0 {
		den.Mul(den, pow10[E])
	} else {
		num.Mul(num, pow10[-E])
	}
	return num, den
}

func ratPow2(m uint64, e int) *big.Rat {
	r := new(big.Rat).SetInt(new(big.Int).SetUint64(m))
	if e >= 0 {
		return r.Mul(r, new(big.Rat).SetInt(new(big.Int).Lsh(big.NewInt(1), uint(e))))
	}
	return r.Quo(r, new(big.Rat).SetInt(new(big.Int).Lsh(big.NewInt(1), uint(-e))))
}

// decExpOf returns the exact floor(log10(v)); the float64 estimate may be off
// by one near powers of ten and is corrected with exact comparisons.
func decExpOf(v *big.Rat, f float64) int {
	e := int(math.Floor(math.Log10(f)))
	for cmpPow10(v, e) < 0 {
		e--
	}
	for cmpPow10(v, e+1) >= 0 {
		e++
	}
	return e
}

func cmpPow10(v *big.Rat, e int) int {
	if e >= 0 {
		return v.Cmp(new(big.Rat).SetInt(pow10[e]))
	}
	t := new(big.Rat).Mul(v, new(big.Rat).SetInt(pow10[-e]))
	return t.Cmp(big.NewRat(1, 1))
}

// formatG renders digits/dp like strconv's 'g' format with shortest
// precision: exponent notation below 1e-4 or from 1e6 up, plain decimal
// otherwise.
func formatG(digits string, dp int) string {
	exp := dp - 1
	if exp < -4 || exp >= 6 {
		return fmtE(digits, exp)
	}
	return fmtF(digits, dp)
}

func fmtE(digits string, exp int) string {
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
	es := strconv.Itoa(exp)
	if len(es) < 2 {
		b.WriteByte('0')
	}
	b.WriteString(es)
	return b.String()
}

func fmtF(digits string, dp int) string {
	nd := len(digits)
	switch {
	case dp <= 0:
		return "0." + strings.Repeat("0", -dp) + digits
	case dp >= nd:
		return digits + strings.Repeat("0", dp-nd)
	default:
		return digits[:dp] + "." + digits[dp:]
	}
}
