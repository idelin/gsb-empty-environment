package fp

import (
	"fmt"
	"math/big"
	"testing"
)

// exactPow2 returns n * 2^e reduced like scaledExact.
func exactPow2(neg bool, n *big.Int, e int) Exact {
	nn := new(big.Int).Set(n)
	return scaledExact(neg, nn, e)
}

// TestMidpoints enumerates adjacent grid pairs for f16 and bf16 (feasible),
// and targeted binades for f32, then perturbs each exact midpoint by ± a finer
// quantum and checks all rounding modes against the reference.
func TestMidpoints(t *testing.T) {
	type pair struct {
		lo, hi *big.Int // unit significands
		eLo    int      // exponent of lo grid spacing (lo*2^eLo, hi*2^eLo or next)
		bound  bool     // hi lies in the next binade
	}
	formats := []*Format{Binary16, BFloat16}
	for _, f := range formats {
		var pairs []pair
		// subnormal adjacent pairs: q and q+1 for q in [1, 2^(p-1)-1].
		for q := uint64(1); q < (uint64(1) << (f.p - 1)); q++ {
			pairs = append(pairs, pair{
				lo:  new(big.Int).SetUint64(q),
				hi:  new(big.Int).SetUint64(q + 1),
				eLo: f.emin - int(f.MantBits),
			})
		}
		// normal binades
		for e := f.emin; e < f.emax; e++ {
			step := e - int(f.MantBits)
			// pairs within binade
			for s := uint64(1) << (f.p - 1); s < (uint64(1)<<f.p)-1; s++ {
				pairs = append(pairs, pair{
					lo:  new(big.Int).SetUint64(s),
					hi:  new(big.Int).SetUint64(s + 1),
					eLo: step,
				})
			}
			// pair across binade boundary: max sig of binade e -> min sig of e+1
			pairs = append(pairs, pair{
				lo:    new(big.Int).SetUint64((uint64(1) << f.p) - 1),
				hi:    new(big.Int).SetUint64(uint64(1) << (f.p - 1)),
				eLo:   step,
				bound: true,
			})
		}
		// within the top binade up to max finite
		step := f.emax - int(f.MantBits)
		for s := uint64(1) << (f.p - 1); s < (uint64(1)<<f.p)-1; s++ {
			pairs = append(pairs, pair{
				lo:  new(big.Int).SetUint64(s),
				hi:  new(big.Int).SetUint64(s + 1),
				eLo: step,
			})
		}

		for _, p := range pairs {
			checkPair(t, f, p)
		}

		// overflow edge: midpoint between max finite and infinity.
		checkOverflowEdge(t, f)
	}
}

func pairValues(f *Format, p struct {
	lo, hi *big.Int
	eLo    int
	bound  bool
}) (loV, hiV Exact) {
	loV = exactPow2(false, p.lo, p.eLo)
	if p.bound {
		hiV = exactPow2(false, p.hi, p.eLo+1)
	} else {
		hiV = exactPow2(false, p.hi, p.eLo)
	}
	return
}

func checkPair(t *testing.T, f *Format, p struct {
	lo, hi *big.Int
	eLo    int
	bound  bool
}) {
	loV, hiV := pairValues(f, p)
	sum := new(big.Int).Add(new(big.Int).Mul(loV.Num, hiV.Den), new(big.Int).Mul(hiV.Num, loV.Den))
	den := new(big.Int).Mul(loV.Den, hiV.Den)
	den.Lsh(den, 1) // midpoint = (lo+hi)/2
	mid := Exact{Kind: KindFinite, Num: sum, Den: den}
	gapNum := new(big.Int).Sub(new(big.Int).Mul(hiV.Num, loV.Den), new(big.Int).Mul(loV.Num, hiV.Den))
	epsNum := new(big.Int).Set(gapNum)
	epsDen := new(big.Int).Mul(new(big.Int).Mul(loV.Den, hiV.Den), big.NewInt(16))
	plus := Exact{Kind: KindFinite, Num: new(big.Int).Add(new(big.Int).Mul(mid.Num, epsDen), new(big.Int).Mul(epsNum, mid.Den)), Den: new(big.Int).Mul(mid.Den, epsDen)}
	minus := Exact{Kind: KindFinite, Num: new(big.Int).Sub(new(big.Int).Mul(mid.Num, epsDen), new(big.Int).Mul(epsNum, mid.Den)), Den: new(big.Int).Mul(mid.Den, epsDen)}

	for _, x := range []Exact{mid, plus, minus, loV, hiV} {
		for _, neg := range []bool{false, true} {
			y := x
			y.Neg = neg
			for _, mode := range allModes() {
				got := ConvertValue(f, y, mode)
				ref := ReferenceConvert(f, y, mode)
				if got.Bits != ref.Bits || got.Flags != ref.Flags {
					t.Fatalf("f mismatch %s pair(lo=%v*2^%d) mode=%d neg=%v val=%v/%v got=%0*x/%v ref=%0*x/%v",
						f.Name(), p.lo, p.eLo, mode, neg, y.Num, y.Den,
						f.Width()*2, got.Bits, got.Flags, f.Width()*2, ref.Bits, ref.Flags)
				}
			}
		}
	}
}

func checkOverflowEdge(t *testing.T, f *Format) {
	// max finite and 2^(emax+1) midpoint: m = 3*2^(emax-1)
	mid := exactPow2(false, big.NewInt(3), f.emax-1)
	eps := exactPow2(false, big.NewInt(1), f.emax-1-6) // much smaller than gap/2
	plus := exactPow2(false, new(big.Int).Add(bigNum(mid), bigNum(eps)), expOf(eps))
	minus := exactPow2(false, new(big.Int).Sub(bigNum(mid), bigNum(eps)), expOf(eps))
	for _, x := range []Exact{mid, plus, minus} {
		for _, neg := range []bool{false, true} {
			y := x
			y.Neg = neg
			for _, mode := range allModes() {
				got := ConvertValue(f, y, mode)
				ref := ReferenceConvert(f, y, mode)
				if got.Bits != ref.Bits || got.Flags != ref.Flags {
					t.Fatalf("overflow edge mismatch got=%0*x/%v ref=%0*x/%v",
						f.Width()*2, got.Bits, got.Flags, f.Width()*2, ref.Bits, ref.Flags)
				}
			}
		}
	}
}

func bigNum(x Exact) *big.Int { return new(big.Int).Set(x.Num) }
func expOf(x Exact) int {
	// x is n*2^e with n odd after reduction; infer e from denominator.
	d := x.Den
	if d.Cmp(big.NewInt(1)) == 0 {
		return int(x.Num.TrailingZeroBits())
	}
	return -(d.BitLen() - 1)
}

// TestF32MidpointSamples covers representative f32 binades where enumeration
// is infeasible, including the subnormal range and overflow boundary.
func TestF32MidpointSamples(t *testing.T) {
	f := Binary32
	es := append([]int{f.emin, f.emin + 1, f.emin + 2, -100, -1, 0, 1, 10, 100}, f.emax-2, f.emax-1, f.emax)
	type sp struct {
		lo  *big.Int
		eLo int
		hi  *big.Int
		eHi int
	}
	var pairs []sp
	// subnormal pairs sampled by q
	for _, q := range []uint64{1, 2, 3, 17, 0x3fffff - 1} {
		pairs = append(pairs, sp{
			lo:  new(big.Int).SetUint64(q),
			eLo: f.emin - int(f.MantBits),
			hi:  new(big.Int).SetUint64(q + 1),
			eHi: f.emin - int(f.MantBits),
		})
	}
	pairs = append(pairs, sp{
		lo: new(big.Int).SetUint64(0x7fffff), eLo: f.emin - int(f.MantBits),
		hi: new(big.Int).SetUint64(uint64(1) << f.MantBits), eHi: f.emin - int(f.MantBits),
	})
	for _, e := range es {
		step := e - int(f.MantBits)
		for _, s := range []uint64{uint64(1) << f.MantBits, (uint64(1) << f.MantBits) + 1, (uint64(1) << (f.MantBits + 1)) - 2} {
			pairs = append(pairs, sp{
				lo: new(big.Int).SetUint64(s), eLo: step,
				hi: new(big.Int).SetUint64(s + 1), eHi: step,
			})
		}
		pairs = append(pairs, sp{
			lo: new(big.Int).SetUint64((uint64(1) << (f.MantBits + 1)) - 1), eLo: step,
			hi: new(big.Int).SetUint64(uint64(1) << f.MantBits), eHi: step + 1,
		})
	}
	for _, p := range pairs {
		loV := exactPow2(false, p.lo, p.eLo)
		hiV := exactPow2(false, p.hi, p.eHi)
		sum := new(big.Int).Add(new(big.Int).Mul(loV.Num, hiV.Den), new(big.Int).Mul(hiV.Num, loV.Den))
		den := new(big.Int).Lsh(new(big.Int).Mul(loV.Den, hiV.Den), 1)
		gapNum := new(big.Int).Sub(new(big.Int).Mul(hiV.Num, loV.Den), new(big.Int).Mul(loV.Num, hiV.Den))
		epsDen := new(big.Int).Mul(new(big.Int).Mul(loV.Den, hiV.Den), big.NewInt(16))
		mid := Exact{Kind: KindFinite, Num: sum, Den: den}
		plus := Exact{Kind: KindFinite, Num: new(big.Int).Add(new(big.Int).Mul(mid.Num, epsDen), new(big.Int).Mul(gapNum, mid.Den)), Den: new(big.Int).Mul(mid.Den, epsDen)}
		minus := Exact{Kind: KindFinite, Num: new(big.Int).Sub(new(big.Int).Mul(mid.Num, epsDen), new(big.Int).Mul(gapNum, mid.Den)), Den: new(big.Int).Mul(mid.Den, epsDen)}
		for _, x := range []Exact{loV, hiV, mid, plus, minus} {
			for _, mode := range allModes() {
				got := ConvertValue(f, x, mode)
				ref := ReferenceConvert(f, x, mode)
				if got.Bits != ref.Bits || got.Flags != ref.Flags {
					t.Fatalf("f32 sample mismatch mode=%d val=%v/%v got=%08x/%v ref=%08x/%v",
						mode, x.Num, x.Den, got.Bits, got.Flags, ref.Bits, ref.Flags)
				}
			}
		}
	}
	checkOverflowEdge(t, f)
}

func (f *Format) Name() string {
	switch f {
	case Binary16:
		return "binary16"
	case BFloat16:
		return "bfloat16"
	case Binary32:
		return "binary32"
	case Binary64:
		return "binary64"
	default:
		return fmt.Sprintf("exp%dman%db%d", f.ExpBits, f.MantBits, f.Bias)
	}
}
