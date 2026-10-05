// Package conv computes exact products of polynomials with
// arbitrary-precision integer coefficients, restricted to a
// caller-specified coefficient interval [lo, hi).
//
// Algorithm: multi-modulus convolution. The product is evaluated modulo
// several machine-word primes (schoolbook, but only over index pairs
// that land inside the requested interval) and reconstructed with
// Garner's CRT algorithm using symmetric residues.
//
// Exactness proof (no silent wraparound): let c_k be a coefficient of
// the product and B the a priori bound from coeffBound, so |c_k| <= B.
// The modulus chain is chosen so that its product M satisfies
// M > 2*B (enforced by newChain, which errors out instead of
// under-provisioning). The reconstructed value r is the unique integer
// in (-M/2, M/2] congruent to c_k mod M. Since |c_k| <= B < M/2, c_k
// itself lies in that interval, and two integers inside an interval of
// length M that are congruent mod M must be equal. Hence r == c_k.
package conv

import (
	"context"
	"fmt"
	"math/big"
)

// Poly is a polynomial with arbitrary-precision integer coefficients.
// Coeffs[i] is the coefficient of x^i; nil entries and trailing zeros
// are treated as zero.
type Poly struct {
	Coeffs []*big.Int
}

// NewPoly returns a polynomial with the given coefficients (copied).
func NewPoly(coeffs ...*big.Int) *Poly {
	c := make([]*big.Int, len(coeffs))
	for i, x := range coeffs {
		c[i] = new(big.Int)
		if x != nil {
			c[i].Set(x)
		}
	}
	return &Poly{Coeffs: c}
}

// deg returns the degree, or -1 for the zero polynomial.
func (p *Poly) deg() int {
	for i := len(p.Coeffs) - 1; i >= 0; i-- {
		if p.Coeffs[i] != nil && p.Coeffs[i].Sign() != 0 {
			return i
		}
	}
	return -1
}

// nnz counts the nonzero coefficients.
func (p *Poly) nnz() int {
	n := 0
	for _, c := range p.Coeffs {
		if c != nil && c.Sign() != 0 {
			n++
		}
	}
	return n
}

// maxAbs returns max |coeff|, or 0 for the zero polynomial.
func (p *Poly) maxAbs() *big.Int {
	m := new(big.Int)
	for _, c := range p.Coeffs {
		if c == nil {
			continue
		}
		if t := new(big.Int).Abs(c); t.Cmp(m) > 0 {
			m.Set(t)
		}
	}
	return m
}

// coeffBound returns B such that every coefficient c_k of a*b satisfies
// |c_k| <= B.
//
// Proof: c_k = sum_{i+j=k} a_i*b_j. Distinct summands use distinct
// indices of a (and of b), so the number of nonzero summands is at most
// min(nnz(a), nnz(b)). Each nonzero summand has magnitude at most
// maxAbs(a)*maxAbs(b). The bound follows. QED.
func coeffBound(a, b *Poly) *big.Int {
	n := minInt(a.nnz(), b.nnz())
	bound := new(big.Int).Mul(a.maxAbs(), b.maxAbs())
	return bound.Mul(bound, big.NewInt(int64(n)))
}

// Interval is a half-open coefficient range [Lo, Hi).
type Interval struct {
	Lo, Hi int
}

// Result holds the coefficients of the product over [Lo, Hi).
// Coeffs[i] is the coefficient of x^(Lo+i); it is nil when its block
// was not completed (e.g. the context was cancelled). Done lists the
// completed sub-intervals in completion order.
type Result struct {
	Lo, Hi int
	Coeffs []*big.Int
	Done   []Interval
}

// At returns the coefficient of x^k, and whether it has been computed.
func (r *Result) At(k int) (*big.Int, bool) {
	if k < r.Lo || k >= r.Hi {
		return nil, false
	}
	c := r.Coeffs[k-r.Lo]
	if c == nil {
		return nil, false
	}
	return c, true
}

// Options tunes Product.
type Options struct {
	// BlockSize is the number of coefficients computed per block.
	// Cancellation is checked between blocks. Default 1<<16.
	BlockSize int
	// OnBlockDone, if set, is called after each completed block.
	OnBlockDone func(Interval)
}

const defaultBlockSize = 1 << 16

// Product computes the coefficients of a*b over the interval [lo, hi),
// exactly. Degrees beyond deg(a)+deg(b) are zero-filled. If ctx is
// cancelled, it returns the partial result (with Done recording the
// completed intervals) together with the context error.
func Product(ctx context.Context, a, b *Poly, lo, hi int, opts *Options) (*Result, error) {
	if lo < 0 || hi < lo {
		return nil, fmt.Errorf("conv: invalid interval [%d, %d)", lo, hi)
	}
	res := &Result{Lo: lo, Hi: hi, Coeffs: make([]*big.Int, hi-lo)}
	if lo == hi {
		return res, nil
	}

	da, db := a.deg(), b.deg()

	// Coefficients at degrees > da+db (or all of them, if a or b is
	// the zero polynomial) are exactly zero.
	zeroFrom := hi
	switch {
	case da < 0 || db < 0:
		zeroFrom = lo
	case da+db+1 < hi:
		zeroFrom = maxInt(da+db+1, lo)
	}
	for k := zeroFrom; k < hi; k++ {
		res.Coeffs[k-lo] = new(big.Int)
	}
	if zeroFrom < hi {
		// The zero tail is exact and complete from the start.
		res.Done = append(res.Done, Interval{zeroFrom, hi})
	}
	hiEff := zeroFrom // compute only [lo, hiEff); hiEff <= hi
	if hiEff <= lo {
		return res, nil
	}

	// A priori coefficient bound; the chain guarantees M > 2*bound.
	bound := coeffBound(a, b)
	chain, err := newChain(bound)
	if err != nil {
		return nil, err
	}
	plan := newPlan(chain, a, b, da, db)

	bs := defaultBlockSize
	if opts != nil && opts.BlockSize > 0 {
		bs = opts.BlockSize
	}
	for blo := lo; blo < hiEff; blo += bs {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		bhi := minInt(blo+bs, hiEff)
		plan.convolveBlock(db, blo, bhi, res.Coeffs[blo-lo:bhi-lo])
		res.Done = append(res.Done, Interval{blo, bhi})
		if opts != nil && opts.OnBlockDone != nil {
			opts.OnBlockDone(Interval{blo, bhi})
		}
	}
	return res, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
