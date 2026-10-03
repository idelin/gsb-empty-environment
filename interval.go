package algebraic

import (
	"fmt"
	"math/big"
)

// Interval is a closed rational interval guaranteed to contain a value.
type Interval struct {
	Lo *big.Rat
	Hi *big.Rat
}

func (i Interval) String() string {
	return fmt.Sprintf("[%s, %s]", i.Lo.RatString(), i.Hi.RatString())
}

func cloneInterval(i Interval) Interval {
	return Interval{Lo: cloneRat(i.Lo), Hi: cloneRat(i.Hi)}
}

func exactInterval(x *big.Rat) Interval {
	return Interval{Lo: cloneRat(x), Hi: cloneRat(x)}
}

func intervalContainsZero(i Interval) bool {
	return i.Lo.Sign() <= 0 && i.Hi.Sign() >= 0
}

func intervalIntersects(a, b Interval) bool {
	return a.Lo.Cmp(b.Hi) <= 0 && b.Lo.Cmp(a.Hi) <= 0
}

func intervalAdd(a, b Interval) Interval {
	return Interval{
		Lo: new(big.Rat).Add(a.Lo, b.Lo),
		Hi: new(big.Rat).Add(a.Hi, b.Hi),
	}
}

func intervalNeg(a Interval) Interval {
	return Interval{Lo: new(big.Rat).Neg(a.Hi), Hi: new(big.Rat).Neg(a.Lo)}
}

func intervalSub(a, b Interval) Interval {
	return intervalAdd(a, intervalNeg(b))
}

func intervalMul(a, b Interval) Interval {
	products := []*big.Rat{
		new(big.Rat).Mul(a.Lo, b.Lo),
		new(big.Rat).Mul(a.Lo, b.Hi),
		new(big.Rat).Mul(a.Hi, b.Lo),
		new(big.Rat).Mul(a.Hi, b.Hi),
	}
	lo := cloneRat(products[0])
	hi := cloneRat(products[0])
	for _, p := range products[1:] {
		if p.Cmp(lo) < 0 {
			lo = cloneRat(p)
		}
		if p.Cmp(hi) > 0 {
			hi = cloneRat(p)
		}
	}
	return Interval{Lo: lo, Hi: hi}
}

func intervalDiv(a, b Interval) (Interval, bool) {
	if intervalContainsZero(b) {
		return Interval{}, false
	}
	quotients := []*big.Rat{
		new(big.Rat).Quo(a.Lo, b.Lo),
		new(big.Rat).Quo(a.Lo, b.Hi),
		new(big.Rat).Quo(a.Hi, b.Lo),
		new(big.Rat).Quo(a.Hi, b.Hi),
	}
	lo := cloneRat(quotients[0])
	hi := cloneRat(quotients[0])
	for _, q := range quotients[1:] {
		if q.Cmp(lo) < 0 {
			lo = cloneRat(q)
		}
		if q.Cmp(hi) > 0 {
			hi = cloneRat(q)
		}
	}
	return Interval{Lo: lo, Hi: hi}, true
}

func intervalSqrt(a Interval, bits uint) (Interval, bool) {
	if a.Hi.Sign() < 0 {
		return Interval{}, false
	}
	lo := a.Lo
	if lo.Sign() < 0 {
		lo = ratInt(0)
	}
	sqrtLo, _ := sqrtRatBounds(lo, bits)
	_, sqrtHi := sqrtRatBounds(a.Hi, bits)
	return Interval{Lo: sqrtLo, Hi: sqrtHi}, true
}

func evalExprInterval(e *expr, bits uint) (Interval, bool) {
	switch e.kind {
	case exprRational:
		return exactInterval(e.rat), true
	case exprNeg:
		v, ok := evalExprInterval(e.left, bits)
		if !ok {
			return Interval{}, false
		}
		return intervalNeg(v), ok
	case exprAdd:
		a, ok := evalExprInterval(e.left, bits)
		if !ok {
			return Interval{}, false
		}
		b, ok := evalExprInterval(e.right, bits)
		if !ok {
			return Interval{}, false
		}
		return intervalAdd(a, b), true
	case exprSub:
		a, ok := evalExprInterval(e.left, bits)
		if !ok {
			return Interval{}, false
		}
		b, ok := evalExprInterval(e.right, bits)
		if !ok {
			return Interval{}, false
		}
		return intervalSub(a, b), true
	case exprMul:
		a, ok := evalExprInterval(e.left, bits)
		if !ok {
			return Interval{}, false
		}
		b, ok := evalExprInterval(e.right, bits)
		if !ok {
			return Interval{}, false
		}
		return intervalMul(a, b), true
	case exprDiv:
		a, ok := evalExprInterval(e.left, bits)
		if !ok {
			return Interval{}, false
		}
		b, ok := evalExprInterval(e.right, bits)
		if !ok {
			return Interval{}, false
		}
		return intervalDiv(a, b)
	case exprSqrt:
		v, ok := evalExprInterval(e.left, bits)
		if !ok {
			return Interval{}, false
		}
		return intervalSqrt(v, bits)
	default:
		panic("unknown expression kind")
	}
}

func evalNumberInterval(n *Number, bits uint) Interval {
	for {
		if interval, ok := evalExprInterval(n.expr, bits); ok {
			return interval
		}
		bits++
	}
}

func evalPolyInterval(p poly, x Interval) Interval {
	acc := exactInterval(polyLeading(p))
	for i := len(p) - 2; i >= 0; i-- {
		acc = intervalMul(acc, x)
		acc = intervalAdd(acc, exactInterval(p[i]))
	}
	return acc
}

func sqrtRatBounds(x *big.Rat, bits uint) (*big.Rat, *big.Rat) {
	if x.Sign() < 0 {
		panic("square root of a negative rational")
	}
	if x.Sign() == 0 {
		return ratInt(0), ratInt(0)
	}
	scale := new(big.Int).Lsh(big.NewInt(1), bits)
	scaledNum := new(big.Int).Lsh(x.Num(), 2*bits)
	quotient := new(big.Int).Quo(scaledNum, x.Denom())
	root := integerSqrt(quotient)
	lo := new(big.Rat).Quo(new(big.Rat).SetInt(root), new(big.Rat).SetInt(scale))
	square := new(big.Int).Mul(root, root)
	square.Mul(square, x.Denom())
	if square.Cmp(scaledNum) == 0 {
		return cloneRat(lo), cloneRat(lo)
	}
	hiNum := new(big.Int).Add(root, big.NewInt(1))
	hi := new(big.Rat).Quo(new(big.Rat).SetInt(hiNum), new(big.Rat).SetInt(scale))
	return lo, hi
}

func integerSqrt(n *big.Int) *big.Int {
	if n.Sign() < 0 {
		panic("integer square root of a negative number")
	}
	if n.Sign() == 0 {
		return big.NewInt(0)
	}
	x := new(big.Int).Lsh(big.NewInt(1), uint((n.BitLen()+1)/2))
	for {
		quotient := new(big.Int).Quo(n, x)
		y := new(big.Int).Add(x, quotient)
		y.Rsh(y, 1)
		if y.Cmp(x) >= 0 {
			break
		}
		x = y
	}
	square := new(big.Int).Mul(x, x)
	for square.Cmp(n) > 0 {
		x.Sub(x, big.NewInt(1))
		square.Mul(x, x)
	}
	for {
		next := new(big.Int).Add(x, big.NewInt(1))
		nextSquare := new(big.Int).Mul(next, next)
		if nextSquare.Cmp(n) > 0 {
			break
		}
		x = next
	}
	return x
}
