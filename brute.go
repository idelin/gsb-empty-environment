package quadopt

import "math/big"

// BruteForce minimizes f over the integer box by full enumeration.
// It returns the lexicographically smallest minimizer and its value.
// Intended for cross-checking Solve on small boxes.
func BruteForce(f *Quadratic, lo, hi []*big.Int) (point []*big.Int, value *big.Int) {
	n := f.N
	x := cloneVec(lo)
	var bestVal *big.Int
	var bestPoint []*big.Int
	for {
		v := f.Eval(x)
		// Enumeration is lexicographic, so replacing only on strict
		// improvement keeps the lexicographically smallest minimizer.
		if bestVal == nil || v.Cmp(bestVal) < 0 {
			bestVal = v
			bestPoint = cloneVec(x)
		}
		i := n - 1
		for ; i >= 0; i-- {
			if x[i].Cmp(hi[i]) < 0 {
				x[i].Add(x[i], big.NewInt(1))
				break
			}
			x[i].Set(lo[i])
		}
		if i < 0 {
			return bestPoint, bestVal
		}
	}
}
