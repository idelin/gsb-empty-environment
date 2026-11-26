package decimal128

import "math/bits"

// u128 is an unsigned 128-bit integer stored little-endian in two 64-bit limbs.
type u128 struct {
	lo uint64
	hi uint64
}

func u128From64(v uint64) u128 { return u128{lo: v} }

func (u u128) isZero() bool { return u.lo == 0 && u.hi == 0 }

func (u u128) cmp(v u128) int {
	switch {
	case u.hi > v.hi || (u.hi == v.hi && u.lo > v.lo):
		return 1
	case u.hi == v.hi && u.lo == v.lo:
		return 0
	default:
		return -1
	}
}

// mul64Saturating keeps only the low 128 bits; it is used to build power
// tables whose final entry is an unreachable sentinel.
func (u u128) mul64Saturating(v uint64) u128 {
	hi1, lo := bits.Mul64(u.lo, v)
	_, hi0 := bits.Mul64(u.hi, v)
	c1 := hi1
	hi := hi0
	hi, _ = bits.Add64(hi, c1, 0)
	return u128{lo: lo, hi: hi}
}

// i128 is a signed 128-bit two's-complement integer (hi signed, lo unsigned).
type i128 struct {
	lo uint64
	hi int64
}

func i128FromInt64(v int64) i128 {
	d := i128{lo: uint64(v)}
	if v < 0 {
		d.hi = -1
	}
	return d
}

func (d i128) isZero() bool { return d.lo == 0 && d.hi == 0 }

// sign returns -1, 0 or 1.
func (d i128) sign() int {
	switch {
	case d.hi < 0:
		return -1
	case d.hi > 0 || d.lo > 0:
		return 1
	default:
		return 0
	}
}

// neg returns -d in two's complement.
func (d i128) neg() i128 {
	lo := ^d.lo + 1
	hi := ^d.hi
	if lo == 0 {
		hi++
	}
	return i128{lo: lo, hi: hi}
}

// magnitude returns the absolute value as an unsigned integer and the sign.
func (d i128) magnitude() (u128, int) {
	if d.hi < 0 {
		n := d.neg()
		return u128{lo: n.lo, hi: uint64(n.hi)}, -1
	}
	return u128{lo: d.lo, hi: uint64(d.hi)}, d.sign()
}

func i128FromMagnitude(m u128, negative bool) i128 {
	d := i128{lo: m.lo, hi: int64(m.hi)}
	if negative {
		return d.neg()
	}
	return d
}
