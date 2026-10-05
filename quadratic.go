// Package quadopt minimizes integer quadratic functions over integer boxes.
//
// The objective is f(x) = C + G·x + xᵀQx with arbitrary integer coefficients
// (math/big). Q may be indefinite: the branch-and-bound lower bounds are
// interval-arithmetic bounds that stay valid for any symmetric Q.
package quadopt

import (
	"errors"
	"math/big"
)

// Quadratic represents f(x) = C + G·x + xᵀQx over Z^n.
type Quadratic struct {
	N int
	C *big.Int
	G []*big.Int
	Q [][]*big.Int // symmetric n×n matrix
}

// NewQuadratic builds a Quadratic, validating dimensions and symmetry.
// Inputs are deep-copied.
func NewQuadratic(c *big.Int, g []*big.Int, q [][]*big.Int) (*Quadratic, error) {
	n := len(g)
	if n == 0 {
		return nil, errors.New("quadopt: need at least one variable")
	}
	if c == nil {
		return nil, errors.New("quadopt: nil constant term")
	}
	if len(q) != n {
		return nil, errors.New("quadopt: Q row count mismatch")
	}
	f := &Quadratic{N: n, C: new(big.Int).Set(c), G: make([]*big.Int, n), Q: make([][]*big.Int, n)}
	for i := 0; i < n; i++ {
		if g[i] == nil {
			return nil, errors.New("quadopt: nil linear coefficient")
		}
		if len(q[i]) != n {
			return nil, errors.New("quadopt: Q column count mismatch")
		}
		f.G[i] = new(big.Int).Set(g[i])
		f.Q[i] = make([]*big.Int, n)
		for j := 0; j < n; j++ {
			if q[i][j] == nil {
				return nil, errors.New("quadopt: nil quadratic coefficient")
			}
			if q[i][j].Cmp(q[j][i]) != 0 {
				return nil, errors.New("quadopt: Q must be symmetric")
			}
			f.Q[i][j] = new(big.Int).Set(q[i][j])
		}
	}
	return f, nil
}

// Eval computes f(x) exactly.
func (f *Quadratic) Eval(x []*big.Int) *big.Int {
	acc := new(big.Int).Set(f.C)
	tmp := new(big.Int)
	for i := 0; i < f.N; i++ {
		acc.Add(acc, tmp.Mul(f.G[i], x[i]))
	}
	for i := 0; i < f.N; i++ {
		for j := 0; j < f.N; j++ {
			tmp.Mul(f.Q[i][j], x[i])
			tmp.Mul(tmp, x[j])
			acc.Add(acc, tmp)
		}
	}
	return acc
}

// lowerBound returns a lower bound of f over the integer box [lo, hi].
//
// It uses plain interval arithmetic term by term: for each monomial
// coeff·x_i·x_j the minimum of x_i·x_j over the box is found from the four
// endpoint products, then multiplied by the sign-aware coefficient. Unlike
// eigenvalue/SDP relaxations this is valid for indefinite Q as well.
func (f *Quadratic) lowerBound(lo, hi []*big.Int) *big.Int {
	lb := new(big.Int).Set(f.C)
	for i := 0; i < f.N; i++ {
		a := new(big.Int).Mul(f.G[i], lo[i])
		b := new(big.Int).Mul(f.G[i], hi[i])
		if b.Cmp(a) < 0 {
			a = b
		}
		lb.Add(lb, a)
	}
	for i := 0; i < f.N; i++ {
		for j := 0; j < f.N; j++ {
			coeff := f.Q[i][j]
			if coeff.Sign() == 0 {
				continue
			}
			mn, mx := intervalProductMinMax(lo[i], hi[i], lo[j], hi[j])
			if coeff.Sign() > 0 {
				lb.Add(lb, mn.Mul(mn, coeff))
			} else {
				lb.Add(lb, mx.Mul(mx, coeff))
			}
		}
	}
	return lb
}

// intervalProductMinMax returns the min and max of u·v over
// u ∈ [alo, ahi], v ∈ [blo, bhi]. The extreme values of a bilinear form over
// a box are attained at corners.
func intervalProductMinMax(alo, ahi, blo, bhi *big.Int) (mn, mx *big.Int) {
	corners := [4]*big.Int{
		new(big.Int).Mul(alo, blo),
		new(big.Int).Mul(alo, bhi),
		new(big.Int).Mul(ahi, blo),
		new(big.Int).Mul(ahi, bhi),
	}
	mn, mx = corners[0], corners[0]
	for _, p := range corners[1:] {
		if p.Cmp(mn) < 0 {
			mn = p
		}
		if p.Cmp(mx) > 0 {
			mx = p
		}
	}
	return mn, mx
}
