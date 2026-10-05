package conv

import "math/big"

// Naive computes the full product a*b with plain big.Int arithmetic.
// It is the reference implementation used to cross-check Product.
// The result has length deg(a)+deg(b)+1, or nil if a or b is zero.
func Naive(a, b *Poly) []*big.Int {
	da, db := a.deg(), b.deg()
	if da < 0 || db < 0 {
		return nil
	}
	out := make([]*big.Int, da+db+1)
	for k := range out {
		out[k] = new(big.Int)
	}
	t := new(big.Int)
	for i := 0; i <= da; i++ {
		ai := a.Coeffs[i]
		if ai == nil || ai.Sign() == 0 {
			continue
		}
		for j := 0; j <= db; j++ {
			bj := b.Coeffs[j]
			if bj == nil || bj.Sign() == 0 {
				continue
			}
			out[i+j].Add(out[i+j], t.Mul(ai, bj))
		}
	}
	return out
}
