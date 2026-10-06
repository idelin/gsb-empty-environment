package convlib

import (
	"errors"
	"math/big"
)

// ErrModulusProductTooSmall is returned when the product of every available
// NTT prime is still <= 2*B for the proven coefficient bound B. The library
// refuses to return coefficients in that case instead of silently
// truncating them modulo the product.
var ErrModulusProductTooSmall = errors.New("convlib: modulus product insufficient to recover signed coefficients exactly")

// CoeffBound returns the proven bound B on the absolute value of every
// convolution coefficient of a and b:
//
//	|c_k| = |sum_i a_i b_{k-i}| <= sum_i |a_i| |b_{k-i}|
//	      <= min(len(a), len(b)) * max|a_i| * max|b_j| = B.
//
// A modulus product M with M > 2*B therefore guarantees that every
// coefficient lies in (-M/2, M/2] and is uniquely recovered by the balanced
// (symmetric) CRT representative, sign included.
func CoeffBound(a, b []int64) *big.Int {
	if len(a) == 0 || len(b) == 0 {
		return big.NewInt(0)
	}
	maxAbs := func(s []int64) *big.Int {
		m := big.NewInt(0)
		for _, v := range s {
			abs := new(big.Int).SetInt64(v)
			abs.Abs(abs)
			if abs.Cmp(m) > 0 {
				m = abs
			}
		}
		return m
	}
	minLen := len(a)
	if len(b) < minLen {
		minLen = len(b)
	}
	bound := new(big.Int).Mul(maxAbs(a), maxAbs(b))
	return bound.Mul(bound, big.NewInt(int64(minLen)))
}

// selectPrimes returns the shortest prefix of the prime table whose product
// M satisfies M > 2*bound, together with M. It fails when even the full
// table is insufficient.
func selectPrimes(bound *big.Int) ([]nttPrime, *big.Int, error) {
	twoB := new(big.Int).Lsh(bound, 1)
	m := big.NewInt(1)
	for i, pr := range nttPrimes {
		m.Mul(m, new(big.Int).SetUint64(pr.p))
		if m.Cmp(twoB) > 0 {
			return nttPrimes[:i+1], m, nil
		}
	}
	return nil, m, ErrModulusProductTooSmall
}

// Convolve returns the exact integer convolution of a and b as big integers.
// Negative entries are supported: residues are reconstructed with CRT and
// mapped to the balanced representative in (-M/2, M/2], which is exact
// because M > 2*B. It also returns the coefficient bound B that was used
// and the modulus product M. If no prefix of the prime table satisfies
// M > 2*B, it returns ErrModulusProductTooSmall rather than truncated
// values.
func Convolve(a, b []int64) (coeffs []*big.Int, bound, modulusProduct *big.Int, err error) {
	if len(a) == 0 || len(b) == 0 {
		return nil, big.NewInt(0), big.NewInt(1), nil
	}
	bound = CoeffBound(a, b)
	primes, m, err := selectPrimes(bound)
	if err != nil {
		return nil, bound, m, err
	}

	need := len(a) + len(b) - 1
	n := 1
	for n < need {
		n <<= 1
	}
	for _, pr := range primes {
		if uint(n) > 1<<pr.maxLog {
			return nil, bound, m, errors.New("convlib: transform length exceeds NTT capacity of selected primes")
		}
	}

	residues := make([][]uint64, len(primes))
	for i, pr := range primes {
		p := int64(pr.p)
		ra := make([]uint64, len(a))
		for j, v := range a {
			ra[j] = uint64(signedMod(v, p))
		}
		rb := make([]uint64, len(b))
		for j, v := range b {
			rb[j] = uint64(signedMod(v, p))
		}
		residues[i] = convMod(ra, rb, need, pr)
	}

	coeffs = make([]*big.Int, need)
	for k := 0; k < need; k++ {
		rs := make([]uint64, len(primes))
		for i := range primes {
			rs[i] = residues[i][k]
		}
		x := garner(rs, primes)
		// Balanced representative: map [0, M) to (-M/2, M/2].
		if new(big.Int).Lsh(x, 1).Cmp(m) > 0 {
			x.Sub(x, m)
		}
		coeffs[k] = x
	}
	return coeffs, bound, m, nil
}

// signedMod returns v mod p in [0, p) for a signed v. A plain uint64 cast of
// a negative v would be wrong because 2^64 mod p != 0.
func signedMod(v, p int64) int64 {
	r := v % p
	if r < 0 {
		r += p
	}
	return r
}

// garner combines residues modulo the selected primes into the unique
// representative in [0, M) using Garner's algorithm with big integers.
func garner(residues []uint64, primes []nttPrime) *big.Int {
	x := big.NewInt(0)
	m := big.NewInt(1)
	for i, pr := range primes {
		p := new(big.Int).SetUint64(pr.p)
		inv := new(big.Int).ModInverse(new(big.Int).Mod(m, p), p)
		t := new(big.Int).Sub(new(big.Int).SetUint64(residues[i]), x)
		t.Mod(t, p)
		t.Mul(t, inv)
		t.Mod(t, p)
		x.Add(x, new(big.Int).Mul(m, t))
		m.Mul(m, p)
	}
	return x
}

// ConvolveNaive is the reference O(n*m) big-integer convolution used for
// cross-checking.
func ConvolveNaive(a, b []int64) []*big.Int {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	out := make([]*big.Int, len(a)+len(b)-1)
	for i := range out {
		out[i] = big.NewInt(0)
	}
	for i, av := range a {
		ai := big.NewInt(av)
		for j, bv := range b {
			out[i+j].Add(out[i+j], new(big.Int).Mul(ai, big.NewInt(bv)))
		}
	}
	return out
}
