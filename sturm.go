package algebraic

import "math/big"

func sturmSequence(p poly) []poly {
	p = polyMonic(p)
	if len(p) == 0 {
		return nil
	}
	seq := []poly{p}
	derivative := polyDerivative(p)
	if len(derivative) == 0 {
		return seq
	}
	seq = append(seq, derivative)
	for len(seq[len(seq)-1]) != 0 {
		_, remainder := polyDivMod(seq[len(seq)-2], seq[len(seq)-1])
		next := polyNeg(remainder)
		if len(next) == 0 {
			break
		}
		absLeading := new(big.Rat).Abs(polyLeading(next))
		next = polyScale(next, new(big.Rat).Inv(absLeading))
		seq = append(seq, next)
	}
	return seq
}

func sturmSignVariations(seq []poly, x *big.Rat) int {
	variations := 0
	previous := 0
	for _, p := range seq {
		sign := polyEval(p, x).Sign()
		if sign == 0 {
			continue
		}
		if previous != 0 && sign != previous {
			variations++
		}
		previous = sign
	}
	return variations
}

func sturmRootCount(seq []poly, lo, hi *big.Rat) int {
	return sturmSignVariations(seq, lo) - sturmSignVariations(seq, hi)
}

func cauchyBound(p poly) *big.Rat {
	lead := new(big.Rat).Abs(polyLeading(p))
	max := ratInt(0)
	for i := 0; i < len(p)-1; i++ {
		ratio := new(big.Rat).Quo(new(big.Rat).Abs(p[i]), lead)
		if ratio.Cmp(max) > 0 {
			max = ratio
		}
	}
	bound := new(big.Rat).Add(max, ratInt(1))
	ceil := ceilRat(bound)
	ceil.Add(ceil, ratInt(1))
	return ceil
}

func ceilRat(x *big.Rat) *big.Rat {
	quotient := new(big.Int).Quo(x.Num(), x.Denom())
	remainder := new(big.Int).Rem(x.Num(), x.Denom())
	if remainder.Sign() != 0 && x.Sign() > 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	return new(big.Rat).SetInt(quotient)
}

func isolateRealRoots(p poly) []Interval {
	p = polyMonic(p)
	if len(p) <= 1 {
		return nil
	}
	if len(p) == 2 {
		root := new(big.Rat).Quo(new(big.Rat).Neg(p[0]), p[1])
		return []Interval{exactInterval(root)}
	}
	seq := sturmSequence(p)
	bound := cauchyBound(p)
	lo := new(big.Rat).Neg(bound)
	hi := bound
	return isolateRootsRecursive(seq, lo, hi)
}

func isolateRootsRecursive(seq []poly, lo, hi *big.Rat) []Interval {
	count := sturmRootCount(seq, lo, hi)
	if count == 0 {
		return nil
	}
	if count == 1 {
		return []Interval{{Lo: cloneRat(lo), Hi: cloneRat(hi)}}
	}
	mid := new(big.Rat).Add(lo, hi)
	mid.Quo(mid, ratInt(2))
	left := isolateRootsRecursive(seq, lo, mid)
	right := isolateRootsRecursive(seq, mid, hi)
	return append(left, right...)
}

func refineRootInterval(p poly, interval Interval) Interval {
	p = polyMonic(p)
	if len(p) <= 2 {
		return cloneInterval(interval)
	}
	seq := sturmSequence(p)
	mid := new(big.Rat).Add(interval.Lo, interval.Hi)
	mid.Quo(mid, ratInt(2))
	if sturmRootCount(seq, interval.Lo, mid) == 1 {
		return Interval{Lo: cloneRat(interval.Lo), Hi: mid}
	}
	return Interval{Lo: mid, Hi: cloneRat(interval.Hi)}
}

func intervalEqual(a, b Interval) bool {
	return a.Lo.Cmp(b.Lo) == 0 && a.Hi.Cmp(b.Hi) == 0
}
