// Package convrange computes exact big-integer polynomial products while
// emitting only a caller-specified coefficient interval [lo, hi).
//
// Method: multi-modulus convolution. The product coefficients are computed
// modulo several 31-bit primes and reconstructed with Garner's CRT, followed
// by a centered lift into [-(M-1)/2, (M-1)/2] where M is the modulus product.
//
// Exactness proof (no silent wraparound):
//
//	For k in [lo,hi), c_k = sum_{i+j=k} a_i*b_j over t_k index pairs, where
//	t_k = min(k, n-1) - max(0, k-(m-1)) + 1. Let T = max_{k in [lo,hi)} t_k,
//	A = max_i |a_i|, B = max_j |b_j|. Then |c_k| <= T*A*B =: Bound.
//
//	Primes are selected so that M = prod p_i > 2*Bound. CRT yields the unique
//	x in [0, M) with x == c_k (mod M); the centered lift c' = x if 2x <= M
//	else x-M lies in [-(M-1)/2, (M-1)/2]. Since M > 2*Bound and M is odd,
//	|c_k| <= Bound <= (M-1)/2, so both c_k and c' lie in that interval and
//	are congruent mod M; their difference is a multiple of M with absolute
//	value < M, hence zero. Therefore c' == c_k exactly.
package convrange

import (
	"context"
	"fmt"
	"math/big"
)

// Range is a half-open coefficient interval [Lo, Hi).
type Range struct {
	Lo, Hi int
}

// Options tunes MulRange. The zero value is valid.
type Options struct {
	// BlockSize is the number of coefficients reconstructed per block.
	// Smaller values give finer cancellation granularity. Default 4096.
	BlockSize int
	// OnBlockDone, if set, is called after each block is fully
	// reconstructed. It may cancel the context to stop early.
	OnBlockDone func(lo, hi int)
}

// Result holds the coefficients of a*b over [Lo, Hi).
type Result struct {
	Lo, Hi int
	// Coeffs[i] is the coefficient of x^(Lo+i). On cancellation, entries
	// outside Completed are nil.
	Coeffs []*big.Int
	// Completed lists the finished sub-intervals, in order. Every entry in
	// Coeffs covered by Completed is exact, even when err != nil.
	Completed []Range
}

// Coeff returns the exact coefficient of x^k, or nil if k is outside
// [Lo, Hi) or belongs to an unfinished block.
func (r *Result) Coeff(k int) *big.Int {
	if k < r.Lo || k >= r.Hi {
		return nil
	}
	return r.Coeffs[k-r.Lo]
}

// MulRange computes coefficients lo..hi-1 of the product a*b exactly.
// a and b are coefficient slices (a[i] is the coefficient of x^i); negative
// coefficients are supported. hi is clamped to deg(a*b)+1; coefficients at
// or beyond that index are zero. If ctx is cancelled, MulRange returns the
// partial Result (with Completed recording finished blocks) and ctx.Err().
func MulRange(ctx context.Context, a, b []*big.Int, lo, hi int, opts *Options) (*Result, error) {
	if lo < 0 {
		return nil, fmt.Errorf("convrange: lo must be >= 0, got %d", lo)
	}
	if lo > hi {
		return nil, fmt.Errorf("convrange: lo (%d) > hi (%d)", lo, hi)
	}
	if opts == nil {
		opts = &Options{}
	}
	blockSize := opts.BlockSize
	if blockSize <= 0 {
		blockSize = 4096
	}

	res := &Result{Lo: lo, Hi: hi, Coeffs: make([]*big.Int, hi-lo)}

	deg := len(a) + len(b) - 1 // degree+1 of the full product
	if lo >= deg || len(a) == 0 || len(b) == 0 {
		// Product is zero over the requested interval (or entirely).
		for i := range res.Coeffs {
			res.Coeffs[i] = new(big.Int)
		}
		res.Completed = append(res.Completed, Range{lo, hi})
		return res, nil
	}
	// Coefficients at indices >= deg are zero; compute only [lo, calcHi)
	// and zero-fill the tail afterwards.
	calcHi := hi
	if calcHi > deg {
		calcHi = deg
	}

	maxA := maxAbs(a)
	maxB := maxAbs(b)
	if maxA.Sign() == 0 || maxB.Sign() == 0 {
		for i := range res.Coeffs {
			res.Coeffs[i] = new(big.Int)
		}
		res.Completed = append(res.Completed, Range{lo, hi})
		return res, nil
	}

	// Coefficient bound: |c_k| <= maxTerms * maxA * maxB for k in [lo,hi).
	bound := new(big.Int).Mul(maxA, maxB)
	bound.Mul(bound, big.NewInt(int64(maxTerms(lo, calcHi, len(a), len(b)))))

	primes := selectPrimes(bound) // guarantees prod(primes) > 2*bound
	crt := newCRT(primes)

	nza := nonzeros(a)
	nzb := nonzeros(b)

	for blo := lo; blo < calcHi; blo += blockSize {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		bhi := blo + blockSize
		if bhi > calcHi {
			bhi = calcHi
		}
		// residues[p][k-blo] = c_k mod primes[p] for k in [blo,bhi)
		residues := make([][]uint64, len(primes))
		for pi, p := range primes {
			residues[pi] = blockResidues(a, b, nza, nzb, p, blo, bhi)
		}
		for k := blo; k < bhi; k++ {
			r := make([]uint64, len(primes))
			for pi := range primes {
				r[pi] = residues[pi][k-blo]
			}
			res.Coeffs[k-lo] = crt.lift(r)
		}
		res.Completed = append(res.Completed, Range{blo, bhi})
		if opts.OnBlockDone != nil {
			opts.OnBlockDone(blo, bhi)
		}
	}
	if calcHi < hi {
		for k := calcHi; k < hi; k++ {
			res.Coeffs[k-lo] = new(big.Int)
		}
		res.Completed = append(res.Completed, Range{calcHi, hi})
	}
	return res, nil
}

// maxTerms returns max over k in [lo,hi) of the number of index pairs
// (i,j) with i+j=k, 0<=i<n, 0<=j<m. t(k) is a tent function: it rises
// linearly to a plateau on [min(n,m)-1, max(n,m)-1] then falls, so the
// maximum on the interval is attained at an endpoint or a plateau edge.
func maxTerms(lo, hi, n, m int) int {
	best := 0
	for _, k := range []int{lo, hi - 1, clamp(min(n, m)-1, lo, hi-1), clamp(max(n, m)-1, lo, hi-1)} {
		if t := termsAt(k, n, m); t > best {
			best = t
		}
	}
	return best
}

func termsAt(k, n, m int) int {
	lo := k - (m - 1)
	if lo < 0 {
		lo = 0
	}
	hi := k
	if hi > n-1 {
		hi = n - 1
	}
	if hi < lo {
		return 0
	}
	return hi - lo + 1
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func maxAbs(p []*big.Int) *big.Int {
	m := new(big.Int)
	for _, c := range p {
		if c == nil {
			continue
		}
		if abs := new(big.Int).Abs(c); abs.Cmp(m) > 0 {
			m.Set(abs)
		}
	}
	return m
}

// nonzeroTerm is a nonzero coefficient with its index.
type nonzeroTerm struct {
	idx int
	val *big.Int
}

func nonzeros(p []*big.Int) []nonzeroTerm {
	var out []nonzeroTerm
	for i, c := range p {
		if c != nil && c.Sign() != 0 {
			out = append(out, nonzeroTerm{i, c})
		}
	}
	return out
}

// selectPrimes returns the fewest 31-bit primes whose product M satisfies
// M > 2*bound, which is exactly the exactness precondition proved above.
func selectPrimes(bound *big.Int) []uint64 {
	need := new(big.Int).Lsh(bound, 1) // 2*bound
	M := big.NewInt(1)
	var primes []uint64
	next := uint64(1)<<31 - 1 // 2^31-1, prime
	for M.Cmp(need) <= 0 {
		primes = append(primes, next)
		M.Mul(M, big.NewInt(int64(next)))
		next = prevPrime(next - 1)
	}
	return primes
}
