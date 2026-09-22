package fp

import (
	"math/big"
	"math/bits"
)

// Result is the encoded output of one conversion.
type Result struct {
	Bits  uint64
	Flags Flags
}

// ConvertValue rounds an exact value into dst using integer arithmetic only.
func ConvertValue(dst *Format, x Exact, mode Round) Result {
	if err := dst.validate(); err != nil {
		panic(err)
	}
	switch x.Kind {
	case KindNaN:
		return dst.convertNaN(x)
	case KindInf:
		return Result{Bits: dst.infBits(x.Neg)}
	case KindZero:
		return Result{Bits: dst.zeroBits(x.Neg)}
	default:
		return roundFinite(dst, x, mode)
	}
}

func (f *Format) convertNaN(x Exact) Result {
	var fl Flags
	if !x.NaN.Quiet {
		fl |= FlagInvalid
	}
	if f.MantBits == 0 {
		return Result{Bits: f.infBits(x.Neg), Flags: fl}
	}
	payload := x.NaN.Payload & (f.quietBit() - 1)
	return Result{Bits: f.qnanBits(x.Neg, payload), Flags: fl}
}

// Convert decodes a value in src and converts it to dst without using host
// floating-point arithmetic.
func Convert(dst, src *Format, raw uint64, mode Round) Result {
	if err := dst.validate(); err != nil {
		panic(err)
	}
	if err := src.validate(); err != nil {
		panic(err)
	}
	neg, exp, mant := src.unpack(raw)
	maxExp := uint64(1)<<src.ExpBits - 1
	switch {
	case exp == maxExp:
		if mant == 0 {
			return Result{Bits: dst.infBits(neg)}
		}
		info := NaNInfo{
			Neg:     neg,
			Quiet:   mant&src.quietBit() != 0 || src.MantBits == 0,
			Payload: mant & (src.quietBit() - 1),
		}
		return dst.convertNaN(Exact{Kind: KindNaN, Neg: neg, NaN: info})
	case exp == 0:
		if mant == 0 {
			return Result{Bits: dst.zeroBits(neg)}
		}
		return ConvertScaled(dst, neg, mant, src.emin-int(src.MantBits), mode)
	default:
		sig := mant | uint64(1)<<src.MantBits
		e := int(exp) - src.Bias - int(src.MantBits)
		return ConvertScaled(dst, neg, sig, e, mode)
	}
}

// scaledExact builds n * 2^k as a reduced positive-coefficient rational.
func scaledExact(neg bool, n *big.Int, k int) Exact {
	if k >= 0 {
		if k > 0 {
			n.Lsh(n, uint(k))
		}
		return Exact{Kind: KindFinite, Neg: neg, Num: n, Den: big.NewInt(1)}
	}
	d := new(big.Int).Lsh(big.NewInt(1), uint(-k))
	g := new(big.Int).GCD(nil, nil, n, d)
	if g.Sign() != 0 && g.Cmp(big.NewInt(1)) != 0 {
		n.Quo(n, g)
		d.Quo(d, g)
	}
	return Exact{Kind: KindFinite, Neg: neg, Num: n, Den: d}
}

// halfCmp classifies the remainder of floor(n*2^shift/d).
type halfCmp uint8

const (
	remExact   halfCmp = iota // remainder zero
	remLess                   // remainder < d/2
	remHalf                   // remainder == d/2
	remGreater                // remainder > d/2
)

func roundFinite(f *Format, x Exact, mode Round) Result {
	neg := x.Neg
	n, d := x.Num, x.Den

	E := binade(n, d)

	if E > f.emax {
		return overflowResult(f, neg, mode)
	}

	var q uint64
	var rem halfCmp
	if E < f.emin {
		// Subnormal unit grid: u = 2^(emin-(p-1)).
		q, rem = scaleDiv(n, d, int(f.p)-1-f.emin)
	} else {
		q, rem = scaleDiv(n, d, int(f.p)-1-E)
	}

	inexact := rem != remExact
	up := false
	switch mode {
	case ToNearestEven:
		up = rem == remGreater || rem == remHalf && q&1 == 1
	case ToZero:
		up = false
	case ToPositiveInf:
		up = !neg && inexact
	case ToNegativeInf:
		up = neg && inexact
	}
	if up {
		q++
	}

	if E < f.emin {
		// Subnormal: exponent field 0 holds the whole unit significand.
		if q == uint64(1)<<(f.p-1) {
			// Exact tie between the largest subnormal (odd unit sig) and
			// the smallest normal (even unit sig): ties-to-even picks the
			// normal, and directed modes also pick the normal away from
			// zero; so the normal is chosen in every mode. The result is
			// not tiny.
			return Result{Bits: f.pack(neg, 1, 0), Flags: inexactFlag(inexact)}
		}
		if q == 0 {
			zneg := neg
			switch mode {
			case ToPositiveInf:
				zneg = false
			case ToNegativeInf:
				zneg = true
			}
			return Result{Bits: f.zeroBits(zneg), Flags: tinyFlags(inexact)}
		}
		return Result{Bits: f.pack(neg, 0, q), Flags: tinyFlags(inexact)}
	}

	top := uint64(1) << (f.p - 1)
	if q == uint64(1)<<f.p {
		if E == f.emax {
			return overflowResult(f, neg, mode)
		}
		q = top
		E++
	}
	mant := q - top
	biased := uint64(E + f.Bias)
	return Result{Bits: f.pack(neg, biased, mant), Flags: inexactFlag(inexact)}
}

func tinyFlags(inexact bool) Flags {
	if inexact {
		return FlagInexact | FlagUnderflow
	}
	return 0
}

func inexactFlag(inexact bool) Flags {
	if inexact {
		return FlagInexact
	}
	return 0
}

func overflowResult(f *Format, neg bool, mode Round) Result {
	fl := FlagOverflow | FlagInexact
	switch mode {
	case ToZero:
		return Result{Bits: f.maxFinite() | signBit(f, neg), Flags: fl}
	case ToPositiveInf:
		if neg {
			return Result{Bits: f.maxFinite() | signBit(f, true), Flags: fl}
		}
		return Result{Bits: f.infBits(false), Flags: fl}
	case ToNegativeInf:
		if neg {
			return Result{Bits: f.infBits(true), Flags: fl}
		}
		return Result{Bits: f.maxFinite(), Flags: fl}
	default:
		return Result{Bits: f.infBits(neg), Flags: fl}
	}
}

func signBit(f *Format, neg bool) uint64 {
	if neg {
		return uint64(1) << (f.total - 1)
	}
	return 0
}

// binade returns E with 2^E <= n/d < 2^(E+1), n,d > 0.
func binade(n, d *big.Int) int {
	e := n.BitLen() - d.BitLen()
	if cmpShift(n, d, e) >= 0 {
		return e
	}
	return e - 1
}

// cmpShift compares n with d*2^shift for any (possibly negative) shift.
func cmpShift(n, d *big.Int, shift int) int {
	if shift >= 0 {
		t := new(big.Int).Lsh(d, uint(shift))
		return n.Cmp(t)
	}
	t := new(big.Int).Lsh(n, uint(-shift))
	return t.Cmp(d)
}

// scaleDiv returns (q, rem) where q = floor(n*2^shift/d) and rem classifies
// the fractional part against one half. It uses uint64 division on the hot
// path and falls back to arbitrary-precision integers.
func scaleDiv(n, d *big.Int, shift int) (uint64, halfCmp) {
	if q, rem, ok := scaleDivFast(n, d, shift); ok {
		return q, rem
	}
	num := new(big.Int).Set(n)
	den := new(big.Int).Set(d)
	if shift >= 0 {
		num.Lsh(num, uint(shift))
	} else {
		den.Lsh(den, uint(-shift))
	}
	qb, rb := new(big.Int), new(big.Int)
	qb.QuoRem(num, den, rb)
	q := qb.Uint64()
	switch {
	case rb.Sign() == 0:
		return q, remExact
	default:
		twice := new(big.Int).Lsh(rb, 1)
		switch twice.Cmp(den) {
		case -1:
			return q, remLess
		case 0:
			return q, remHalf
		default:
			return q, remGreater
		}
	}
}

func scaleDivFast(n, d *big.Int, shift int) (uint64, halfCmp, bool) {
	if !d.IsUint64() {
		return 0, 0, false
	}
	du := d.Uint64()
	if du == 0 {
		return 0, 0, false
	}
	if shift >= 0 {
		if n.IsUint64() && n.BitLen()+shift <= 64 {
			return udiv(n.Uint64()<<uint(shift), 0, du)
		}
		if n.BitLen()+shift <= 128 {
			hi, lo := shifted128(n, shift)
			if hi >= du {
				return 0, 0, false
			}
			return udiv(lo, hi, du)
		}
		return 0, 0, false
	}
	k := uint(-shift)
	if bits.Len64(du)+int(k) <= 64 {
		y := du << k
		if n.IsUint64() {
			return udiv(n.Uint64(), 0, y)
		}
		if n.BitLen() <= 128 {
			hi, lo := wordAt(n, 1), wordAt(n, 0)
			if hi >= y {
				return 0, 0, false
			}
			return udiv(lo, hi, y)
		}
	}
	return 0, 0, false
}

// shifted128 returns n<<shift as a 128-bit hi:lo pair; callers guarantee the
// result fits in 128 bits and shift > 0 when n exceeds 64 bits.
func shifted128(n *big.Int, shift int) (hi, lo uint64) {
	lo = wordAt(n, 0)
	hi = wordAt(n, 1)
	if n.IsUint64() {
		if shift < 64 {
			hi = lo >> uint(64-shift)
			lo <<= uint(shift)
		} else {
			hi = lo << uint(shift-64)
			lo = 0
		}
		return hi, lo
	}
	if shift == 0 {
		return hi, lo
	}
	if shift < 64 {
		hi = hi<<uint(shift) | lo>>uint(64-shift)
		lo <<= uint(shift)
	} else {
		hi = lo << uint(shift-64)
		lo = 0
	}
	return hi, lo
}

func wordAt(n *big.Int, i int) uint64 {
	w := n.Bits()
	if i < len(w) {
		return uint64(w[i])
	}
	return 0
}

// udiv divides the 128-bit number hi:lo by y and classifies the remainder.
func udiv(lo, hi, y uint64) (uint64, halfCmp, bool) {
	if y == 0 || hi >= y {
		return 0, 0, false
	}
	q, r := bits.Div64(hi, lo, y)
	switch {
	case r == 0:
		return q, remExact, true
	case r > y-r: // 2r > y
		return q, remGreater, true
	case r == y-r: // 2r == y
		return q, remHalf, true
	default:
		return q, remLess, true
	}
}
