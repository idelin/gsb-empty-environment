package algebraic

import (
	"math/big"
	"sort"
)

type intPoly []*big.Int

func cloneInt(x *big.Int) *big.Int {
	return new(big.Int).Set(x)
}

func normIntPoly(p intPoly) intPoly {
	n := len(p)
	for n > 0 && p[n-1].Sign() == 0 {
		n--
	}
	return p[:n]
}

func intPolyFromPoly(p poly) intPoly {
	p = normPoly(p)
	if len(p) == 0 {
		return intPoly{}
	}
	lcm := big.NewInt(1)
	for _, c := range p {
		d := c.Denom()
		g := new(big.Int).GCD(nil, nil, lcm, d)
		lcm.Div(lcm, g)
		lcm.Mul(lcm, d)
	}
	out := make(intPoly, len(p))
	for i, c := range p {
		multiplier := new(big.Int).Quo(lcm, c.Denom())
		out[i] = new(big.Int).Mul(c.Num(), multiplier)
	}
	return primitiveIntPoly(out)
}

func primitiveIntPoly(p intPoly) intPoly {
	p = normIntPoly(p)
	if len(p) == 0 {
		return intPoly{}
	}
	content := big.NewInt(0)
	for _, c := range p {
		abs := new(big.Int).Abs(c)
		content = gcdInt(content, abs)
	}
	out := make(intPoly, len(p))
	for i, c := range p {
		out[i] = new(big.Int).Quo(c, content)
	}
	if out[len(out)-1].Sign() < 0 {
		for i := range out {
			out[i].Neg(out[i])
		}
	}
	return out
}

func gcdInt(a, b *big.Int) *big.Int {
	if a.Sign() == 0 {
		return new(big.Int).Abs(b)
	}
	if b.Sign() == 0 {
		return new(big.Int).Abs(a)
	}
	aa := new(big.Int).Abs(a)
	bb := new(big.Int).Abs(b)
	return new(big.Int).GCD(nil, nil, aa, bb)
}

func intPolyToPoly(p intPoly) poly {
	out := make(poly, len(p))
	for i, c := range p {
		out[i] = new(big.Rat).SetInt(c)
	}
	return normPoly(out)
}

func intPolyFromRationalPoly(p poly) intPoly {
	p = normPoly(p)
	out := make(intPoly, len(p))
	for i, c := range p {
		if c.Denom().Cmp(big.NewInt(1)) != 0 {
			panic("expected an integer polynomial")
		}
		out[i] = cloneInt(c.Num())
	}
	return normIntPoly(out)
}

func intPolyDivExact(a, b intPoly) intPoly {
	q, r := polyDivMod(intPolyToPoly(a), intPolyToPoly(b))
	if len(r) != 0 {
		panic("integer polynomial division is not exact")
	}
	return primitiveIntPoly(intPolyFromRationalPoly(q))
}

func intPolyEval(p intPoly, x int64) *big.Int {
	acc := big.NewInt(0)
	xx := big.NewInt(x)
	for i := len(p) - 1; i >= 0; i-- {
		acc.Mul(acc, xx)
		acc.Add(acc, p[i])
	}
	return acc
}

func factorPolynomial(p poly) []poly {
	p = polyMonic(p)
	if len(p) <= 2 {
		return []poly{p}
	}
	gcd := polyGCD(p, polyDerivative(p))
	squarefree := p
	if len(gcd) != 0 {
		squarefree = polyDivExact(p, gcd)
	}
	if len(squarefree) <= 2 {
		return []poly{polyMonic(squarefree)}
	}
	integer := intPolyFromPoly(squarefree)
	integerFactors := factorSquarefreeIntPoly(integer)
	out := make([]poly, 0, len(integerFactors))
	for _, f := range integerFactors {
		rp := intPolyToPoly(f)
		out = append(out, polyMonic(rp))
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i]) != len(out[j]) {
			return len(out[i]) < len(out[j])
		}
		for k := len(out[i]) - 1; k >= 0; k-- {
			if out[i][k].Cmp(out[j][k]) != 0 {
				return out[i][k].Cmp(out[j][k]) < 0
			}
		}
		return false
	})
	return out
}

func factorSquarefreeIntPoly(p intPoly) []intPoly {
	p = primitiveIntPoly(p)
	degree := len(p) - 1
	if degree <= 1 {
		return []intPoly{p}
	}
	for d := 1; d <= degree/2; d++ {
		if factor := findIntPolyFactor(p, d); factor != nil {
			quotient := intPolyDivExact(p, factor)
			left := factorSquarefreeIntPoly(factor)
			right := factorSquarefreeIntPoly(quotient)
			return append(left, right...)
		}
	}
	return []intPoly{p}
}

type factorPoint struct {
	x        int64
	value    *big.Int
	divisors []*big.Int
}

func findIntPolyFactor(p intPoly, degree int) intPoly {
	points := chooseFactorPoints(p, degree+1)
	values := make([]*big.Int, len(points))
	xs := make([]int64, len(points))
	for i, point := range points {
		xs[i] = point.x
	}
	var search func(int) (intPoly, bool)
	search = func(pos int) (intPoly, bool) {
		if pos == len(points) {
			candidate := interpolateFactorPolynomial(xs, values)
			if candidate == nil || len(candidate) <= 1 {
				return nil, false
			}
			candidate = primitiveIntPoly(candidate)
			if len(candidate)-1 > degree {
				return nil, false
			}
			if intPolyDivides(p, candidate) {
				return candidate, true
			}
			return nil, false
		}
		for _, divisor := range points[pos].divisors {
			values[pos] = divisor
			if found, ok := search(pos + 1); ok {
				return found, true
			}
		}
		return nil, false
	}
	if found, ok := search(0); ok {
		return found
	}
	return nil
}

func chooseFactorPoints(p intPoly, count int) []factorPoint {
	points := make([]factorPoint, 0, count*4)
	for step := 0; len(points) < count*4+4; step++ {
		candidates := []int64{int64(step)}
		if step > 0 {
			candidates = append(candidates, int64(-step))
		}
		for _, x := range candidates {
			value := intPolyEval(p, x)
			if value.Sign() == 0 {
				continue
			}
			points = append(points, factorPoint{
				x:        x,
				value:    value,
				divisors: signedDivisors(value),
			})
			if len(points) >= count*4+4 {
				break
			}
		}
	}
	sort.Slice(points, func(i, j int) bool {
		if len(points[i].divisors) != len(points[j].divisors) {
			return len(points[i].divisors) < len(points[j].divisors)
		}
		ai := points[i].x
		aj := points[j].x
		if ai < 0 {
			ai = -ai
		}
		if aj < 0 {
			aj = -aj
		}
		if ai != aj {
			return ai < aj
		}
		return points[i].x < points[j].x
	})
	return points[:count]
}

func signedDivisors(n *big.Int) []*big.Int {
	positive := positiveDivisors(n)
	out := make([]*big.Int, 0, 2*len(positive))
	for _, d := range positive {
		out = append(out, d)
		out = append(out, new(big.Int).Neg(d))
	}
	return out
}

func positiveDivisors(n *big.Int) []*big.Int {
	n = new(big.Int).Abs(n)
	if n.Sign() == 0 {
		return nil
	}
	out := make([]*big.Int, 0)
	if n.BitLen() < 63 {
		v := n.Int64()
		for d := int64(1); d <= v/d; d++ {
			if v%d == 0 {
				out = append(out, big.NewInt(d))
				other := v / d
				if other != d {
					out = append(out, big.NewInt(other))
				}
			}
		}
	} else {
		one := big.NewInt(1)
		d := big.NewInt(1)
		square := big.NewInt(1)
		for square.Cmp(n) <= 0 {
			if new(big.Int).Mod(n, d).Sign() == 0 {
				out = append(out, cloneInt(d))
				other := new(big.Int).Quo(n, d)
				if other.Cmp(d) != 0 {
					out = append(out, other)
				}
			}
			d.Add(d, one)
			square.Mul(d, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Cmp(out[j]) < 0 })
	return out
}

func interpolateFactorPolynomial(xs []int64, ys []*big.Int) intPoly {
	ratValues := make([]*big.Rat, len(ys))
	for i, y := range ys {
		ratValues[i] = new(big.Rat).SetInt(y)
	}
	p := interpolate(xs, ratValues)
	out := make(intPoly, len(p))
	for i, c := range p {
		if c.Denom().Cmp(big.NewInt(1)) != 0 {
			return nil
		}
		out[i] = cloneInt(c.Num())
	}
	return normIntPoly(out)
}

func intPolyDivides(p, divisor intPoly) bool {
	if len(divisor) == 0 {
		return false
	}
	_, remainder := polyDivMod(intPolyToPoly(p), intPolyToPoly(divisor))
	return len(remainder) == 0
}
