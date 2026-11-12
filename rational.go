package taskpkg

import "math/big"

func newRat() *big.Rat {
	return new(big.Rat)
}

func cloneRat(r *big.Rat) *big.Rat {
	if r == nil {
		return nil
	}
	return new(big.Rat).Set(r)
}

func ratAdd(a, b *big.Rat) *big.Rat {
	z := new(big.Rat)
	z.Add(a, b)
	return z
}

func ratSub(a, b *big.Rat) *big.Rat {
	z := new(big.Rat)
	z.Sub(a, b)
	return z
}

func ratMul(a, b *big.Rat) *big.Rat {
	z := new(big.Rat)
	z.Mul(a, b)
	return z
}

func ratQuo(a, b *big.Rat) *big.Rat {
	z := new(big.Rat)
	z.Quo(a, b)
	return z
}

func ratNeg(a *big.Rat) *big.Rat {
	z := new(big.Rat)
	z.Neg(a)
	return z
}

func ratAbs(a *big.Rat) *big.Rat {
	z := new(big.Rat)
	z.Abs(a)
	return z
}

func ratHalf(a *big.Rat) *big.Rat {
	return ratQuo(a, big.NewRat(2, 1))
}

func ratSign(a *big.Rat) int {
	return a.Sign()
}

func crossRat(ax, ay, bx, by *big.Rat) *big.Rat {
	z := ratMul(ax, by)
	z.Sub(z, ratMul(ay, bx))
	return z
}
