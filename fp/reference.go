package fp

import "math/big"

// refCandidate is one positive target-format grid value with its encoding.
type refCandidate struct {
	v    *big.Rat
	bits uint64 // without sign bit
	tiny bool
}

// ReferenceConvert rounds an exact value into dst using big.Rat arithmetic and
// a grid-search strategy that is independent of the scaleDiv integer kernel.
// Tests assert that both implementations agree on bits and flags everywhere.
func ReferenceConvert(dst *Format, x Exact, mode Round) Result {
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
	}
	r := new(big.Rat).SetFrac(new(big.Int).Set(x.Num), new(big.Int).Set(x.Den))
	if x.Neg {
		r.Neg(r)
	}
	return refRoundRat(dst, r, x.Neg, mode)
}

// ReferenceConvertPattern decodes raw in src and converts it via the
// independent reference path.
func ReferenceConvertPattern(dst, src *Format, raw uint64, mode Round) Result {
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
		r := new(big.Rat).SetFrac(
			new(big.Int).SetUint64(mant),
			new(big.Int).Lsh(big.NewInt(1), uint(int(src.MantBits)-src.emin)),
		)
		if neg {
			r.Neg(r)
		}
		return refRoundRat(dst, r, neg, mode)
	default:
		sig := new(big.Int).SetUint64(mant | uint64(1)<<src.MantBits)
		e := int(exp) - src.Bias - int(src.MantBits)
		var r *big.Rat
		if e >= 0 {
			r = new(big.Rat).SetFrac(new(big.Int).Lsh(sig, uint(e)), big.NewInt(1))
		} else {
			r = new(big.Rat).SetFrac(sig, new(big.Int).Lsh(big.NewInt(1), uint(-e)))
		}
		if neg {
			r.Neg(r)
		}
		return refRoundRat(dst, r, neg, mode)
	}
}

// refGrid builds the positive finite grid of f: subnormals followed by
// normals, each annotated with its unsigned encoding and tininess.
func refGrid(f *Format) []refCandidate {
	out := make([]refCandidate, 0, int(1)<<f.p-int(1)<<f.MantBits+int(f.MantBits))
	// Subnormal significands 1..2^(p-1)-1, exponent field 0.
	for q := uint64(1); q < uint64(1)<<(f.p-1); q++ {
		v := new(big.Rat).SetFrac(
			new(big.Int).SetUint64(q),
			new(big.Int).Lsh(big.NewInt(1), uint(int(f.MantBits)-f.emin)),
		)
		out = append(out, refCandidate{v: v, bits: f.pack(false, 0, q), tiny: true})
	}
	// Normal significands 2^(p-1)..2^p-1, exponents emin..emax.
	for e := f.emin; e <= f.emax; e++ {
		denShift := int(f.MantBits) - e
		for s := uint64(1) << (f.p - 1); s < uint64(1)<<f.p; s++ {
			var v *big.Rat
			if denShift >= 0 {
				v = new(big.Rat).SetFrac(
					new(big.Int).SetUint64(s),
					new(big.Int).Lsh(big.NewInt(1), uint(denShift)),
				)
			} else {
				v = new(big.Rat).SetFrac(
					new(big.Int).Lsh(new(big.Int).SetUint64(s), uint(-denShift)),
					big.NewInt(1),
				)
			}
			mant := s - uint64(1)<<(f.p-1)
			out = append(out, refCandidate{v: v, bits: f.pack(false, uint64(e+f.Bias), mant)})
		}
	}
	return out
}

func refRoundRat(f *Format, r *big.Rat, neg bool, mode Round) Result {
	if r.Sign() == 0 {
		return Result{Bits: f.zeroBits(neg)}
	}
	mag := new(big.Rat).Abs(r)

	hiMid := pow2Rat(f.emax + 1)
	if mag.Cmp(hiMid) >= 0 {
		return overflowResult(f, neg, mode)
	}

	var below, above *big.Rat
	var belowBits, aboveBits uint64
	belowTiny, aboveTiny := false, false

	if f.p <= 12 {
		grid := cachedGrid(f)
		idx := -1
		if mag.Cmp(grid[0].v) >= 0 {
			lo, hi := 0, len(grid)-1
			for lo < hi {
				mid := (lo + hi + 1) / 2
				if grid[mid].v.Cmp(mag) <= 0 {
					lo = mid
				} else {
					hi = mid - 1
				}
			}
			idx = lo
		}
		if idx < 0 {
			below = big.NewRat(0, 1)
			belowBits = f.zeroBits(false)
			belowTiny = true
		} else {
			below = grid[idx].v
			belowBits = grid[idx].bits
			belowTiny = grid[idx].tiny
		}
		if idx+1 < len(grid) {
			above = grid[idx+1].v
			aboveBits = grid[idx+1].bits
			aboveTiny = grid[idx+1].tiny
		} else {
			above = hiMid
			aboveBits = f.infBits(false)
		}
	} else {
		below, above, belowBits, aboveBits, belowTiny, aboveTiny = refNeighbors(f, mag)
	}

	exact := below.Sign() > 0 && below.Cmp(mag) == 0
	if exact {
		return Result{Bits: withSign(f, belowBits, neg)}
	}

	gap := new(big.Rat).Sub(above, below)
	frac := new(big.Rat).Sub(mag, below)
	cmp := new(big.Rat).Mul(frac, big.NewRat(2, 1)).Cmp(gap)

	goUp := false
	switch mode {
	case ToNearestEven:
		if cmp > 0 {
			goUp = true
		} else if cmp == 0 {
			// Exactly one of the adjacent significands is even; round
			// toward it. Parity is taken at this binade's spacing.
			goUp = !refSigEven(f, below, above)
		}
	case ToZero:
	case ToPositiveInf:
		goUp = !neg
	case ToNegativeInf:
		goUp = neg
	}

	// Rounding upward beyond the largest finite value overflows.
	if goUp && above.Cmp(hiMid) >= 0 {
		return overflowResult(f, neg, mode)
	}

	var chosen *big.Rat
	var bits uint64
	var tiny bool
	if goUp {
		chosen, bits, tiny = above, aboveBits, aboveTiny
	} else {
		chosen, bits, tiny = below, belowBits, belowTiny
	}

	resultNeg := neg
	if chosen.Sign() == 0 {
		tiny = true
		switch mode {
		case ToPositiveInf:
			resultNeg = false
		case ToNegativeInf:
			resultNeg = true
		}
	}
	var fl Flags = FlagInexact
	if tiny {
		fl |= FlagUnderflow
	}
	return Result{Bits: withSign(f, bits, resultNeg), Flags: fl}
}

var gridCache = map[*Format][]refCandidate{}

func cachedGrid(f *Format) []refCandidate {
	if g, ok := gridCache[f]; ok {
		return g
	}
	g := refGrid(f)
	gridCache[f] = g
	return g
}

// refNeighbors locates the two surrounding grid values for wide formats using
// rational floor division instead of enumeration.
func refNeighbors(f *Format, mag *big.Rat) (below, above *big.Rat, belowBits, aboveBits uint64, belowTiny, aboveTiny bool) {
	num, den := mag.Num(), mag.Denom()
	if mag.Cmp(pow2Rat(f.emin)) < 0 {
		unit := f.emin - int(f.MantBits)
		shift := uint(int(f.MantBits) - f.emin)
		scaled := new(big.Int).Lsh(num, shift)
		q, rem := new(big.Int), new(big.Int)
		q.QuoRem(scaled, den, rem)
		below = mulRat(q, pow2Rat(unit))
		belowBits = f.pack(false, 0, q.Uint64())
		belowTiny = true
		qp := new(big.Int).Add(q, big.NewInt(1))
		aboveTiny = true
		if qp.Cmp(new(big.Int).Lsh(big.NewInt(1), f.p-1)) == 0 {
			above = pow2Rat(f.emin)
			aboveBits = f.pack(false, 1, 0)
			aboveTiny = false
		} else {
			above = mulRat(qp, pow2Rat(unit))
			aboveBits = f.pack(false, 0, qp.Uint64())
		}
		return
	}
	E := binade(num, den)
	q, rem := new(big.Int), new(big.Int)
	if k := int(f.p-1) - E; k >= 0 {
		q.QuoRem(new(big.Int).Lsh(num, uint(k)), den, rem)
	} else {
		q.QuoRem(num, new(big.Int).Lsh(den, uint(-k)), rem)
	}
	step := pow2Rat(E - int(f.MantBits))
	below = mulRat(q, step)
	mant := new(big.Int).Sub(q, new(big.Int).Lsh(big.NewInt(1), f.MantBits))
	belowBits = f.pack(false, uint64(E+f.Bias), mant.Uint64())
	qp := new(big.Int).Add(q, big.NewInt(1))
	if qp.Cmp(new(big.Int).Lsh(big.NewInt(1), f.p)) == 0 {
		if E == f.emax {
			above = pow2Rat(f.emax + 1)
			aboveBits = f.infBits(false)
		} else {
			above = mulRat(new(big.Int).Lsh(big.NewInt(1), f.MantBits), pow2Rat(E+1-int(f.MantBits)))
			aboveBits = f.pack(false, uint64(E+1+f.Bias), 0)
		}
	} else {
		above = mulRat(qp, step)
		aboveMant := new(big.Int).Sub(qp, new(big.Int).Lsh(big.NewInt(1), f.MantBits))
		aboveBits = f.pack(false, uint64(E+f.Bias), aboveMant.Uint64())
	}
	return
}

func mulRat(q *big.Int, step *big.Rat) *big.Rat {
	return new(big.Rat).Mul(new(big.Rat).SetFrac(new(big.Int).Set(q), big.NewInt(1)), step)
}

func withSign(f *Format, bits uint64, neg bool) uint64 {
	bits &^= uint64(1) << (f.total - 1)
	if neg {
		bits |= uint64(1) << (f.total - 1)
	}
	return bits
}

// refSigEven reports whether the lower of two adjacent grid values has an
// even significand at the gap spacing between them.
func refSigEven(f *Format, below, above *big.Rat) bool {
	gap := new(big.Rat).Sub(above, below)
	q := new(big.Rat).Quo(below, gap)
	if !q.IsInt() {
		return false
	}
	return q.Num().Bit(0) == 0
}

func pow2Rat(e int) *big.Rat {
	if e >= 0 {
		return new(big.Rat).SetFrac(new(big.Int).Lsh(big.NewInt(1), uint(e)), big.NewInt(1))
	}
	return new(big.Rat).SetFrac(big.NewInt(1), new(big.Int).Lsh(big.NewInt(1), uint(-e)))
}
