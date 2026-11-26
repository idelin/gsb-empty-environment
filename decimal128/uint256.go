package decimal128

import "math/bits"

// u256 is an unsigned 256-bit integer stored little-endian in four 64-bit limbs.
// It is wide enough to hold the full product of two 38-digit decimal
// coefficients (each below 10^38): 10^76 < 2^253.
type u256 struct {
	v [4]uint64
}

func u256FromU128(u u128) u256 {
	return u256{v: [4]uint64{u.lo, u.hi, 0, 0}}
}

func (u u256) isZero() bool {
	return u.v[0] == 0 && u.v[1] == 0 && u.v[2] == 0 && u.v[3] == 0
}

func (u u256) cmp(v u256) int {
	for i := 3; i >= 0; i-- {
		if u.v[i] != v.v[i] {
			if u.v[i] > v.v[i] {
				return 1
			}
			return -1
		}
	}
	return 0
}

// add64 returns u + v where v fits in 64 bits.
func (u u256) add64(v uint64) u256 {
	r := u
	var carry uint64
	r.v[0], carry = bits.Add64(r.v[0], v, 0)
	for i := 1; i < 4 && carry != 0; i++ {
		r.v[i], carry = bits.Add64(r.v[i], 0, carry)
	}
	return r
}

// sub returns u - v and requires u >= v.
func (u u256) sub(v u256) u256 {
	var r u256
	var borrow uint64
	for i := 0; i < 4; i++ {
		r.v[i], borrow = bits.Sub64(u.v[i], v.v[i], borrow)
	}
	return r
}

// lsh1 returns u << 1.
func (u u256) lsh1() u256 {
	var r u256
	for i := 3; i > 0; i-- {
		r.v[i] = (u.v[i] << 1) | (u.v[i-1] >> 63)
	}
	r.v[0] = u.v[0] << 1
	return r
}

// bit returns the i-th bit (0 is the least significant bit).
func (u u256) bit(i int) uint64 {
	return (u.v[i/64] >> uint(i%64)) & 1
}

// mulU128 returns the full 256-bit product of two 128-bit unsigned integers.
func mulU128(a, b u128) u256 {
	av := [4]uint64{a.lo, a.hi, 0, 0}
	bv := [2]uint64{b.lo, b.hi}
	var z [4]uint64
	for i := 0; i < 2; i++ {
		var carry uint64
		for j := 0; j < 2; j++ {
			hi, lo := bits.Mul64(av[i], bv[j])
			sum, c1 := bits.Add64(z[i+j], lo, 0)
			sum, c2 := bits.Add64(sum, carry, 0)
			z[i+j] = sum
			carry = hi + c1 + c2
		}
		// Propagate carry into the remaining high limbs.
		for k := i + 2; k < 4 && carry != 0; k++ {
			z[k], carry = bits.Add64(z[k], carry, 0)
		}
	}
	return u256{v: z}
}

// mulU256U128 returns the low 256 bits of u * b. Callers guarantee the exact
// product fits (used with operands bounded by 10^38 * 10^38 < 2^253).
func (u u256) mulU256U128(b u128) u256 {
	bv := [2]uint64{b.lo, b.hi}
	var z [4]uint64
	for i := 0; i < 4; i++ {
		var carry uint64
		for j := 0; j < 2; j++ {
			k := i + j
			if k >= 4 {
				continue
			}
			hi, lo := bits.Mul64(u.v[i], bv[j])
			sum, c1 := bits.Add64(z[k], lo, 0)
			sum, c2 := bits.Add64(sum, carry, 0)
			z[k] = sum
			carry = hi + c1 + c2
		}
		for k := i + 2; k < 4 && carry != 0; k++ {
			z[k], carry = bits.Add64(z[k], carry, 0)
		}
	}
	return u256{v: z}
}

// mulPow10 returns u * 10^k for k <= maxScale*2 (76).
func (u u256) mulPow10(k int) u256 {
	r := u
	for i := 0; i < k; i++ {
		var carry uint64
		for j := 0; j < 4; j++ {
			hi, lo := bits.Mul64(r.v[j], 10)
			sum, c := bits.Add64(lo, carry, 0)
			r.v[j] = sum
			carry = hi + c
		}
	}
	return r
}

// divMod returns (q, r) such that u = q*v + r with 0 <= r < v. v must be
// non-zero. It uses a bit-by-bit shift-subtract long division.
func (u u256) divMod(v u256) (u256, u256) {
	if v.isZero() {
		panic("decimal128: divide by zero")
	}
	var q, rem u256
	for i := 255; i >= 0; i-- {
		rem = rem.lsh1()
		rem.v[0] |= u.bit(i)
		if rem.cmp(v) >= 0 {
			rem = rem.sub(v)
			q.v[i/64] |= 1 << uint(i%64)
		}
	}
	return q, rem
}

// decimalDigits returns the number of decimal digits needed to represent u.
func (u u256) decimalDigits() int {
	if u.isZero() {
		return 1
	}
	for d := 1; d < len(pow10u256); d++ {
		if u.cmp(pow10u256[d]) < 0 {
			return d
		}
	}
	return len(pow10u256)
}

// decimalDigits returns the number of decimal digits needed to represent u.
func (u u128) decimalDigits() int {
	if u.isZero() {
		return 1
	}
	for d := 1; d < maxPrecision; d++ {
		if u.cmp(pow10u128[d]) < 0 {
			return d
		}
	}
	// [10^37, 10^38): 38 digits; larger intermediate values use the 256-bit
	// table below.
	if u256FromU128(u).cmp(pow10u256[maxPrecision]) < 0 {
		return maxPrecision
	}
	return u256FromU128(u).decimalDigits()
}

// pow10u128[k] = 10^k, for 0 <= k < maxPrecision. Note that 10^38 itself
// exceeds the u128 coefficient limit and is represented via pow10u256.
var pow10u128 [maxPrecision + 1]u128

// pow10u256[k] = 10^k, for 0 <= k <= 2*maxPrecision.
var pow10u256 [2*maxPrecision + 1]u256

func init() {
	pow10u128[0] = u128From64(1)
	for k := 1; k < maxPrecision; k++ {
		pow10u128[k] = pow10u128[k-1].mul64Saturating(10)
	}
	pow10u128[maxPrecision] = u128{} // sentinel: 10^38 is unreachable in u128
	pow10u256[0] = u256FromU128(u128From64(1))
	for k := 1; k <= 2*maxPrecision; k++ {
		pow10u256[k] = pow10u256[k-1].mulPow10(1)
	}
}
