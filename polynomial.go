package netsec

import (
	"fmt"
	"math/big"
)

// polynomial is a coefficient slice in ascending degree order. Every
// coefficient returned by these helpers is reduced into [0, q).

func zero() *big.Int {
	return new(big.Int)
}

func one() *big.Int {
	return big.NewInt(1)
}

func modValue(v, q *big.Int) *big.Int {
	z := new(big.Int).Mod(v, q)
	if z.Sign() < 0 {
		z.Add(z, q)
	}
	return z
}

func addMod(a, b, q *big.Int) *big.Int {
	return new(big.Int).Mod(new(big.Int).Add(a, b), q)
}

func subMod(a, b, q *big.Int) *big.Int {
	return new(big.Int).Mod(new(big.Int).Sub(a, b), q)
}

func mulMod(a, b, q *big.Int) *big.Int {
	return new(big.Int).Mod(new(big.Int).Mul(a, b), q)
}

func negMod(a, q *big.Int) *big.Int {
	z := new(big.Int).Neg(a)
	return modValue(z, q)
}

func polyAdd(a, b []*big.Int, q *big.Int) []*big.Int {
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	out := make([]*big.Int, n)
	for i := 0; i < n; i++ {
		var av, bv *big.Int
		if i < len(a) {
			av = a[i]
		} else {
			av = zero()
		}
		if i < len(b) {
			bv = b[i]
		} else {
			bv = zero()
		}
		out[i] = addMod(av, bv, q)
	}
	return out
}

func polySub(a, b []*big.Int, q *big.Int) []*big.Int {
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	out := make([]*big.Int, n)
	for i := 0; i < n; i++ {
		var av, bv *big.Int
		if i < len(a) {
			av = a[i]
		} else {
			av = zero()
		}
		if i < len(b) {
			bv = b[i]
		} else {
			bv = zero()
		}
		out[i] = subMod(av, bv, q)
	}
	return out
}

func polyScale(a []*big.Int, scalar, q *big.Int) []*big.Int {
	out := make([]*big.Int, len(a))
	for i := range a {
		out[i] = mulMod(a[i], scalar, q)
	}
	return out
}

func polyMul(a, b []*big.Int, q *big.Int) []*big.Int {
	if len(a) == 0 || len(b) == 0 {
		return []*big.Int{zero()}
	}
	out := make([]*big.Int, len(a)+len(b)-1)
	for i := range out {
		out[i] = zero()
	}
	for i := range a {
		if a[i].Sign() == 0 {
			continue
		}
		for j := range b {
			if b[j].Sign() == 0 {
				continue
			}
			out[i+j] = addMod(out[i+j], mulMod(a[i], b[j], q), q)
		}
	}
	return out
}

func polyEval(a []*big.Int, x, q *big.Int) *big.Int {
	result := zero()
	for i := len(a) - 1; i >= 0; i-- {
		result = addMod(mulMod(result, x, q), a[i], q)
	}
	return result
}

func polyIsZero(a []*big.Int) bool {
	for _, c := range a {
		if c.Sign() != 0 {
			return false
		}
	}
	return true
}

func polyTrim(a []*big.Int) []*big.Int {
	n := len(a)
	for n > 1 && a[n-1].Sign() == 0 {
		n--
	}
	return a[:n]
}

// interpolate returns the unique polynomial of degree at most len(points)-1
// that evaluates to y_i at z_i. points must already be normalized: distinct z
// values, sorted ascending.
func interpolate(points []Evaluation, q *big.Int) ([]*big.Int, error) {
	k := len(points)
	if k == 0 {
		return nil, fmt.Errorf("%w: no interpolation points", ErrField)
	}

	out := make([]*big.Int, k)
	for i := range out {
		out[i] = zero()
	}

	for i := range points {
		basis := []*big.Int{one()}
		denominator := one()
		for j := range points {
			if i == j {
				continue
			}
			linear := []*big.Int{negMod(points[j].Z, q), one()}
			basis = polyMul(basis, linear, q)

			diff := subMod(points[i].Z, points[j].Z, q)
			inverse := new(big.Int).ModInverse(diff, q)
			if inverse == nil {
				return nil, fmt.Errorf("%w: z values are not distinct mod q", ErrField)
			}
			denominator = mulMod(denominator, inverse, q)
			linear = nil
		}
		scale := mulMod(points[i].Y, denominator, q)
		out = polyAdd(out, polyScale(basis, scale, q), q)
		basis = nil
	}

	return out, nil
}

// vanishing returns Z(X) = product_i (X - z_i).
func vanishing(points []Evaluation, q *big.Int) []*big.Int {
	z := []*big.Int{one()}
	for i := range points {
		linear := []*big.Int{negMod(points[i].Z, q), one()}
		z = polyMul(z, linear, q)
	}
	return z
}

// divideExact divides numerator by denominator over F_q and requires a zero
// remainder. It is used by tests that generate honest KZG openings.
func divideExact(numerator, denominator []*big.Int, q *big.Int) ([]*big.Int, error) {
	den := polyTrim(denominator)
	if polyIsZero(den) {
		return nil, fmt.Errorf("%w: division by zero polynomial", ErrField)
	}
	if len(den) == 1 {
		inverse := new(big.Int).ModInverse(den[0], q)
		if inverse == nil {
			return nil, fmt.Errorf("%w: non-invertible divisor", ErrField)
		}
		return polyScale(numerator, inverse, q), nil
	}

	rem := make([]*big.Int, len(numerator))
	for i := range numerator {
		rem[i] = modValue(numerator[i], q)
	}
	rem = polyTrim(rem)

	degreeDiff := len(rem) - len(den)
	if degreeDiff < 0 {
		if polyIsZero(rem) {
			return []*big.Int{zero()}, nil
		}
		return nil, fmt.Errorf("%w: non-zero remainder", ErrField)
	}

	quotient := make([]*big.Int, degreeDiff+1)
	for i := range quotient {
		quotient[i] = zero()
	}

	leadInverse := new(big.Int).ModInverse(den[len(den)-1], q)
	if leadInverse == nil {
		return nil, fmt.Errorf("%w: non-invertible leading coefficient", ErrField)
	}

	for len(rem) >= len(den) && !polyIsZero(rem) {
		shift := len(rem) - len(den)
		coef := mulMod(rem[len(rem)-1], leadInverse, q)
		quotient[shift] = addMod(quotient[shift], coef, q)
		for i := 0; i < len(den); i++ {
			idx := shift + i
			rem[idx] = subMod(rem[idx], mulMod(coef, den[i], q), q)
		}
		rem = polyTrim(rem)
	}

	if !polyIsZero(rem) {
		return nil, fmt.Errorf("%w: non-zero remainder", ErrField)
	}
	return polyTrim(quotient), nil
}
