package fp

import "math/bits"

// ConvertScaled rounds the exact value n*2^k (n > 0) into dst using native
// integer arithmetic with no heap allocation. It is the format-to-format hot
// path and is numerically identical to the arbitrary-precision kernel.
func ConvertScaled(dst *Format, neg bool, n uint64, k int, mode Round) Result {
	if err := dst.validate(); err != nil {
		panic(err)
	}
	if n == 0 {
		return Result{Bits: dst.zeroBits(neg)}
	}

	E := bits.Len64(n) - 1 + k
	if E > dst.emax {
		return overflowResult(dst, neg, mode)
	}

	// Round the significand to precision p at binade E (or at the subnormal
	// unit grid when E < emin).
	targetE := E
	if E < dst.emin {
		targetE = dst.emin
	}
	q, rem := roundSigU64(n, k, targetE, dst.p)

	inexact := rem != remExact
	up := false
	switch mode {
	case ToNearestEven:
		up = rem == remGreater || rem == remHalf && q&1 == 1
	case ToZero:
	case ToPositiveInf:
		up = !neg && inexact
	case ToNegativeInf:
		up = neg && inexact
	}
	if up {
		q++
	}

	if E < dst.emin {
		if q == uint64(1)<<(dst.p-1) {
			return Result{Bits: dst.pack(neg, 1, 0), Flags: inexactFlag(inexact)}
		}
		if q == 0 {
			zneg := neg
			switch mode {
			case ToPositiveInf:
				zneg = false
			case ToNegativeInf:
				zneg = true
			}
			return Result{Bits: dst.zeroBits(zneg), Flags: tinyFlags(inexact)}
		}
		return Result{Bits: dst.pack(neg, 0, q), Flags: tinyFlags(inexact)}
	}

	top := uint64(1) << (dst.p - 1)
	if q == uint64(1)<<dst.p {
		if E == dst.emax {
			return overflowResult(dst, neg, mode)
		}
		q = top
		E++
	}
	return Result{Bits: dst.pack(neg, uint64(E+dst.Bias), q-top), Flags: inexactFlag(inexact)}
}

// roundSigU64 returns floor(n*2^k / 2^(Q-(p-1))) as an integer significand in
// [2^(p-1), 2^p), together with the discarded-fraction classification.
func roundSigU64(n uint64, k, Q int, p uint) (uint64, halfCmp) {
	// shift = Q - (p-1) - k: right shift of n to obtain the quotient.
	shift := Q - int(p-1) - k
	if shift <= 0 {
		// n * 2^-shift, always exact for representable inputs.
		return n << uint(-shift), remExact
	}
	// q = n >> shift; dropped = low shift bits.
	if shift >= 64 {
		// q is zero; the dropped fraction is n*2^(64-shift) relative to the
		// unit, classified against one half.
		if shift == 64 {
			switch {
			case n == 0:
				return 0, remExact
			case n < uint64(1)<<63:
				return 0, remLess
			case n == uint64(1)<<63:
				return 0, remHalf
			default:
				return 0, remGreater
			}
		}
		// divisor exceeds 2^64 and n < 2^64, so always below half.
		return 0, remLess
	}
	q := n >> uint(shift)
	dropped := n & (uint64(1)<<uint(shift) - 1)
	switch {
	case dropped == 0:
		return q, remExact
	case shift == 1:
		// dropped is the guard bit only; fraction exactly 1/2.
		return q, remHalf
	}
	half := uint64(1) << uint(shift-1)
	switch {
	case dropped == half:
		return q, remHalf
	case dropped > half:
		return q, remGreater
	default:
		return q, remLess
	}
}
