package decimal128

import "math/bits"

// u128 is an unsigned 128-bit integer stored as two 64-bit limbs,
// little-endian (lo is the least significant limb).
type u128 struct {
	hi uint64
	lo uint64
}

// u256 is an unsigned 256-bit integer stored as four 64-bit limbs,
// little-endian (l[0] is the least significant limb).
type u256 struct {
	l [4]uint64
}

// i128 is a signed 128-bit two's-complement integer: hi is the signed high
// 64 bits and lo is the unsigned low 64 bits.
type i128 struct {
	hi int64
	lo uint64
}

func (a u128) isZero() bool { return a.hi == 0 && a.lo == 0 }

func (a u128) cmp(b u128) int {
	if a.hi != b.hi {
		if a.hi < b.hi {
			return -1
		}
		return 1
	}
	if a.lo != b.lo {
		if a.lo < b.lo {
			return -1
		}
		return 1
	}
	return 0
}

// add returns a+b plus the incoming carry, plus the outgoing carry.
func (a u128) add(b u128, carry uint64) (u128, uint64) {
	lo, c := bits.Add64(a.lo, b.lo, carry)
	hi, c2 := bits.Add64(a.hi, b.hi, c)
	return u128{hi: hi, lo: lo}, c2
}

// mul returns the full 128-bit product a*b.
func (a u128) mul(b u128) u128 {
	var acc [4]uint64
	av := [2]uint64{a.lo, a.hi}
	bv := [2]uint64{b.lo, b.hi}
	for i := 0; i < 2; i++ {
		for j := 0; j < 2; j++ {
			hi, lo := bits.Mul64(av[i], bv[j]) // returns (hi, lo)
			addToAcc(acc[:], i+j, lo)
			addToAcc(acc[:], i+j+1, hi)
		}
	}
	return u128{lo: acc[0], hi: acc[1]}
}

// mulSmall returns a*d plus the incoming carry, plus the outgoing carry.
func (a u128) mulSmall(d, carry uint64) (u128, uint64) {
	loHi, loLo := bits.Mul64(a.lo, d)
	lo, c1 := bits.Add64(loLo, carry, 0)
	hiHi, hiLo := bits.Mul64(a.hi, d)
	hi, c2 := bits.Add64(loHi, hiLo, c1)
	hi, c3 := bits.Add64(hi, hiHi, 0)
	out := c2 + c3
	return u128{hi: hi, lo: lo}, out
}

// divSmall returns the quotient and remainder of a/d for a single-limb d>0.
func (a u128) divSmall(d uint64) (u128, uint64) {
	// Divide limb by limb; every intermediate remainder is strictly below d,
	// so bits.Div64 never overflows.
	hiQ, r := bits.Div64(0, a.hi, d)
	loQ, r2 := bits.Div64(r, a.lo, d)
	return u128{hi: hiQ, lo: loQ}, r2
}

// digits returns the number of decimal digits in a (0 for zero).
func (a u128) digits() int {
	if a.isZero() {
		return 0
	}
	// Binary bit length gives a tight bracket: 2^(n-1) <= a < 2^n,
	// so digits is either floor(n*log10(2)) or one more.
	n := a.bitLen()
	d := int(float64(n) * 0.3010299956639812)
	if d < 1 {
		d = 1
	}
	if pow10u128[d].cmp(a) <= 0 {
		d++
	}
	return d
}

func (a u128) bitLen() int {
	if a.hi != 0 {
		return 64 + bits.Len64(a.hi)
	}
	return bits.Len64(a.lo)
}

func (a u128) to256() u256 {
	return u256{l: [4]uint64{a.lo, a.hi, 0, 0}}
}

func (x u256) isZero() bool {
	return x.l[0] == 0 && x.l[1] == 0 && x.l[2] == 0 && x.l[3] == 0
}

func (x u256) cmp(y u256) int {
	for i := 3; i >= 0; i-- {
		if x.l[i] != y.l[i] {
			if x.l[i] < y.l[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func (x u256) shlOne() u256 {
	var r u256
	for i := 3; i > 0; i-- {
		r.l[i] = (x.l[i] << 1) | (x.l[i-1] >> 63)
	}
	r.l[0] = x.l[0] << 1
	return r
}

// sub returns x-y. The caller guarantees x >= y.
func (x u256) sub(y u256) u256 {
	var r u256
	var borrow uint64
	for i := 0; i < 4; i++ {
		r.l[i], borrow = bits.Sub64(x.l[i], y.l[i], borrow)
	}
	return r
}

// mul returns the low 256 bits of x*y. Every product formed inside this
// package (coefficient products and power-of-ten widening) fits in 256
// bits, so the upper four accumulator limbs stay zero; the limb tests
// verify this invariant against math/big.
func (x u256) mul(y u256) u256 {
	// Schoolbook multiply into an 8-limb (512-bit) accumulator; the upper
	// four limbs stay zero for every in-range product produced here.
	var acc [8]uint64
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			hi, lo := bits.Mul64(x.l[i], y.l[j])
			k := i + j
			addToAcc(acc[:], k, lo)
			addToAcc(acc[:], k+1, hi)
		}
	}
	var r u256
	copy(r.l[:], acc[:4])
	return r
}

func addToAcc(acc []uint64, k int, v uint64) {
	sum, carry := bits.Add64(acc[k], v, 0)
	acc[k] = sum
	for n := k + 1; carry != 0 && n < len(acc); n++ {
		acc[n], carry = bits.Add64(acc[n], 0, carry)
	}
}

// mulSmall returns x*d plus the incoming carry. The only multiplier used in
// this package is 10 (power-of-ten tables and parsing), for which the
// outgoing carry is always a single limb with no overflow.
func (x u256) mulSmall(d, carryIn uint64) (u256, uint64) {
	var r u256
	carry := carryIn
	for i := 0; i < 4; i++ {
		hi, lo := bits.Mul64(x.l[i], d)
		lo2, c1 := bits.Add64(lo, carry, 0)
		next, c2 := bits.Add64(hi, 0, c1)
		r.l[i] = lo2
		carry = next + c2
	}
	return r, carry
}

// div returns the quotient and remainder of x/y via binary long division.
// y must be non-zero; the caller guarantees the quotient fits in 256 bits.
func (x u256) div(y u256) (u256, u256) {
	var q, rem u256
	for i := 255; i >= 0; i-- {
		rem = rem.shlOne()
		rem.l[0] |= (x.l[i/64] >> uint(i%64)) & 1
		if rem.cmp(y) >= 0 {
			rem = rem.sub(y)
			q.l[i/64] |= 1 << uint(i%64)
		}
	}
	return q, rem
}

// fits128 reports whether x fits in an unsigned 128-bit integer.
func (x u256) fits128() bool {
	return x.l[2] == 0 && x.l[3] == 0
}

func (x u256) as128() u128 {
	return u128{lo: x.l[0], hi: x.l[1]}
}

func (x u256) bitLen() int {
	for i := 3; i >= 0; i-- {
		if x.l[i] != 0 {
			return i*64 + bits.Len64(x.l[i])
		}
	}
	return 0
}

func (x u256) digits() int {
	if x.isZero() {
		return 0
	}
	n := x.bitLen()
	d := int(float64(n) * 0.3010299956639812)
	if d < 1 {
		d = 1
	}
	if d <= 38 {
		if pow10u128[d].to256().cmp(x) <= 0 {
			d++
		}
	} else {
		if pow10u256[d].cmp(x) <= 0 {
			d++
		}
	}
	return d
}

func (v i128) isZero() bool { return v.hi == 0 && v.lo == 0 }

func (v i128) neg() i128 {
	lo, borrow := bits.Sub64(0, v.lo, 0)
	hi, _ := bits.Sub64(uint64(-v.hi), 0, borrow)
	return i128{hi: int64(hi), lo: lo}
}

// mag returns the absolute value as an unsigned magnitude.
func (v i128) mag() u128 {
	if v.hi < 0 {
		return v.neg().mag()
	}
	return u128{hi: uint64(v.hi), lo: v.lo}
}

func fromMag(m u128, neg bool) i128 {
	v := i128{hi: int64(m.hi), lo: m.lo}
	if neg {
		return v.neg()
	}
	return v
}

// addSigned returns a+b using magnitude arithmetic so no intermediate result
// can overflow the signed 128-bit range.
func addSigned(a, b i128) i128 {
	an, bn := a.hi < 0, b.hi < 0
	am, bm := a.mag(), b.mag()
	if an == bn {
		sum, carry := am.add(bm, 0)
		// carry must be zero: magnitudes are bounded by 10^38 < 2^127.
		_ = carry
		return fromMag(sum, an)
	}
	switch am.cmp(bm) {
	case 0:
		return i128{}
	case 1:
		diff, _ := am.sub128(bm)
		return fromMag(diff, an)
	default:
		diff, _ := bm.sub128(am)
		return fromMag(diff, bn)
	}
}

func (a u128) sub128(b u128) (u128, uint64) {
	lo, borrow := bits.Sub64(a.lo, b.lo, 0)
	hi, borrow2 := bits.Sub64(a.hi, b.hi, borrow)
	return u128{hi: hi, lo: lo}, borrow2
}

// divmodSigned returns truncated-toward-zero quotient and remainder with
// sign(rem) == sign(a), matching SQL MOD semantics.
func divmodSigned(a, b i128) (i128, i128) {
	if b.isZero() {
		panic("division by zero")
	}
	am, bm := a.mag(), b.mag()
	qm, rm := am.div128(bm)
	negQ := (a.hi < 0) != (b.hi < 0)
	return fromMag(qm, negQ), fromMag(rm, a.hi < 0)
}

// div128 divides two unsigned 128-bit values via the 256-bit binary divider.
func (a u128) div128(b u128) (u128, u128) {
	q, r := a.to256().div(b.to256())
	return q.as128(), r.as128()
}
