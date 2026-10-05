package dtri

import "math/big"

// orient returns the sign of the oriented area of triangle (a,b,c):
// +1 if c is strictly left of a->b (CCW), -1 if strictly right (CW), 0 if collinear.
// Computed exactly with big.Int; never overflows.
func orient(a, b, c Point) int {
	bax := new(big.Int).Sub(b.X, a.X)
	bay := new(big.Int).Sub(b.Y, a.Y)
	cax := new(big.Int).Sub(c.X, a.X)
	cay := new(big.Int).Sub(c.Y, a.Y)
	t1 := new(big.Int).Mul(bax, cay)
	t2 := new(big.Int).Mul(bay, cax)
	return t1.Sub(t1, t2).Sign()
}

// incircle assumes (a,b,c) is counter-clockwise. It returns the sign of the
// exact incircle determinant:
// >0: d is strictly inside the circumcircle of (a,b,c)
//  0: d is exactly on the circumcircle (cocircular)
// <0: d is strictly outside
// Computed exactly with big.Int; never overflows.
func incircle(a, b, c, d Point) int {
	adx := new(big.Int).Sub(a.X, d.X)
	ady := new(big.Int).Sub(a.Y, d.Y)
	bdx := new(big.Int).Sub(b.X, d.X)
	bdy := new(big.Int).Sub(b.Y, d.Y)
	cdx := new(big.Int).Sub(c.X, d.X)
	cdy := new(big.Int).Sub(c.Y, d.Y)

	alift := new(big.Int).Add(new(big.Int).Mul(adx, adx), new(big.Int).Mul(ady, ady))
	blift := new(big.Int).Add(new(big.Int).Mul(bdx, bdx), new(big.Int).Mul(bdy, bdy))
	clift := new(big.Int).Add(new(big.Int).Mul(cdx, cdx), new(big.Int).Mul(cdy, cdy))

	// det = adx*(bdy*clift - cdy*blift)
	//     - ady*(bdx*clift - cdx*blift)
	//     + alift*(bdx*cdy - cdx*bdy)
	t1 := new(big.Int).Sub(new(big.Int).Mul(bdy, clift), new(big.Int).Mul(cdy, blift))
	t1.Mul(adx, t1)

	t2 := new(big.Int).Sub(new(big.Int).Mul(bdx, clift), new(big.Int).Mul(cdx, blift))
	t2.Mul(ady, t2)

	t3 := new(big.Int).Sub(new(big.Int).Mul(bdx, cdy), new(big.Int).Mul(cdx, bdy))
	t3.Mul(alift, t3)

	det := new(big.Int).Sub(t1, t2)
	det.Add(det, t3)
	return det.Sign()
}
