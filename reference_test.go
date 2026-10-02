package floatwire

// Exact integer reference implementation of the conversion spec, built on
// math/big. It shares no code with the fast path in convert.go and never
// touches host floating point, so agreement between the two is strong
// evidence of correct rounding and flag behavior.

import "math/big"

type refLayout struct {
	manBits uint
	expBits uint
	bias    int
}

func refLayoutOf(w Width) refLayout {
	switch w {
	case Float16:
		return refLayout{10, 5, 15}
	case Float32:
		return refLayout{23, 8, 127}
	default:
		return refLayout{52, 11, 1023}
	}
}

func (l refLayout) expMaskField() uint64 { return mask(l.expBits) }
func (l refLayout) manMask() uint64      { return mask(l.manBits) }

// refConvert converts b from width from to width to using only big.Int
// arithmetic, returning the target bits and the exception flags.
func refConvert(from, to Width, b uint64) (uint64, Flags) {
	fl := refLayoutOf(from)
	tl := refLayoutOf(to)
	var flags Flags

	sign := (b >> (uint(from) - 1)) & 1
	exp := (b >> fl.manBits) & fl.expMaskField()
	man := b & fl.manMask()
	tsign := sign << (uint(to) - 1)
	tExpMask := mask(tl.expBits) << tl.manBits

	if exp == fl.expMaskField() {
		if man == 0 {
			return tsign | tExpMask, flags
		}
		if man&(uint64(1)<<(fl.manBits-1)) == 0 {
			flags.Invalid = true // signaling NaN
		}
		var out uint64
		if tl.manBits >= fl.manBits {
			out = man << (tl.manBits - fl.manBits)
		} else {
			out = man >> (fl.manBits - tl.manBits)
			if out == 0 {
				out = 1
			}
		}
		return tsign | tExpMask | out, flags
	}
	if exp == 0 && man == 0 {
		return tsign, flags
	}

	mant := new(big.Int).SetUint64(man)
	e2 := 1 - fl.bias - int(fl.manBits)
	if exp != 0 {
		mant.SetUint64(man | (uint64(1) << fl.manBits))
		e2 = int(exp) - fl.bias - int(fl.manBits)
	}
	return tsign | refRound(tl, mant, e2, &flags), flags
}

// refRound rounds mant * 2^e2 into the target format with big.Int math.
// Like the fast path, every value is rounded exactly once.
func refRound(tl refLayout, mant *big.Int, e2 int, flags *Flags) uint64 {
	p := int(tl.manBits) + 1
	one := big.NewInt(1)

	bl := mant.BitLen()
	emax := int(mask(tl.expBits)) - 1 - tl.bias
	emin := 1 - tl.bias
	expUnb := e2 + bl - 1

	if expUnb >= emin {
		shift := bl - p
		q := new(big.Int)
		if shift > 0 {
			q.Rsh(mant, uint(shift))
			r := new(big.Int).Sub(mant, new(big.Int).Lsh(q, uint(shift)))
			if r.Sign() != 0 {
				flags.Inexact = true
				twoR := new(big.Int).Lsh(r, 1)
				bound := new(big.Int).Lsh(one, uint(shift))
				c := twoR.Cmp(bound)
				if c > 0 || (c == 0 && q.Bit(0) == 1) {
					q.Add(q, one)
				}
				if q.BitLen() > p {
					q.Rsh(q, 1)
					expUnb++
				}
			}
		} else {
			q.Lsh(mant, uint(-shift))
		}
		if expUnb > emax {
			flags.Overflow = true
			flags.Inexact = true
			return mask(tl.expBits) << tl.manBits
		}
		field := uint64(expUnb + tl.bias)
		return field<<tl.manBits | (q.Uint64() & tl.manMask())
	}

	// Subnormal: round the original significand once at the quantum.
	total := emin - e2 - p + 1
	if total <= 0 {
		return new(big.Int).Lsh(mant, uint(-total)).Uint64()
	}
	out := new(big.Int).Rsh(mant, uint(total))
	remMask := new(big.Int).Sub(new(big.Int).Lsh(one, uint(total)), one)
	r := new(big.Int).And(mant, remMask)
	if r.Sign() != 0 {
		flags.Inexact = true
		twoR := new(big.Int).Lsh(r, 1)
		bound := new(big.Int).Lsh(one, uint(total))
		c := twoR.Cmp(bound)
		if c > 0 || (c == 0 && out.Bit(0) == 1) {
			out.Add(out, one)
		}
	}
	res := out.Uint64()
	if flags.Inexact && res < (uint64(1)<<tl.manBits) {
		flags.Underflow = true
	}
	return res
}
