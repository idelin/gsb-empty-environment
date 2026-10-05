// Package binom evaluates binomial coefficients C(n, k) exactly as big
// integers and modulo arbitrary positive integers, including composite
// moduli for which factorials are not invertible.
//
// Modular evaluation factors the modulus into prime powers, computes
// C(n, k) mod p^a via the p-adic valuation (Kummer/Legendre) and
// factorials with all factors of p stripped, then recombines the
// residues with the Chinese remainder theorem. A shared Factorizer
// caches prime factorizations across batch queries and bounds the
// factorization work with a budget.
package binom

import (
	"errors"
	"math/big"
)

// ErrNonPositiveModulus is returned for a zero modulus.
var ErrNonPositiveModulus = errors.New("binom: modulus must be a positive integer")

// Exact returns the exact value of C(n, k) as a big integer.
// It returns 0 when k is out of range (k < 0 or k > n) or n < 0.
func Exact(n, k int64) *big.Int {
	if n < 0 || k < 0 || k > n {
		return big.NewInt(0)
	}
	if k > n-k {
		k = n - k
	}
	result := big.NewInt(1)
	term := big.NewInt(0)
	for i := int64(1); i <= k; i++ {
		term.SetInt64(n - k + i)
		result.Mul(result, term)
		result.Quo(result, term.SetInt64(i))
	}
	return result
}

// Query is a single binomial-modulo request in a batch.
type Query struct {
	N, K int64
	M    uint64
}

// Calculator evaluates binomial coefficients modulo positive integers,
// sharing one Factorizer (cache and budget) across all queries.
type Calculator struct {
	fz *Factorizer
}

// NewCalculator returns a Calculator whose factorization work is bounded
// by the given budget. The budget is shared by every query issued
// through this calculator; cached factorizations are reused for free.
func NewCalculator(budget uint64) *Calculator {
	return &Calculator{fz: NewFactorizer(budget)}
}

// Factorizer exposes the underlying Factorizer, e.g. to inspect the
// remaining budget or the number of factorizations performed.
func (c *Calculator) Factorizer() *Factorizer { return c.fz }

// Mod returns C(n, k) mod m. Out-of-range k (k < 0 or k > n) yields 0
// without consuming factorization budget, as does m == 1.
func (c *Calculator) Mod(n, k int64, m uint64) (uint64, error) {
	if n < 0 || k < 0 || k > n {
		return 0, nil
	}
	if m == 0 {
		return 0, ErrNonPositiveModulus
	}
	if m == 1 {
		return 0, nil
	}
	if k > n-k {
		k = n - k
	}
	pps, err := c.fz.Factorize(m)
	if err != nil {
		return 0, err
	}
	un, uk := uint64(n), uint64(k)
	residues := make([]uint64, len(pps))
	mods := make([]uint64, len(pps))
	for i, pp := range pps {
		residues[i] = binomModPrimePower(un, uk, pp)
		mods[i] = pp.Mod
	}
	return crt(residues, mods), nil
}

// ModBatch evaluates many queries while sharing cached factorizations:
// repeated moduli across the batch are factored only once, and the
// calculator's budget bounds the total factorization work.
func (c *Calculator) ModBatch(queries []Query) ([]uint64, error) {
	results := make([]uint64, len(queries))
	for i, q := range queries {
		r, err := c.Mod(q.N, q.K, q.M)
		if err != nil {
			return nil, err
		}
		results[i] = r
	}
	return results, nil
}

// binomModPrimePower returns C(n, k) mod p^a without assuming any
// factorial is invertible: the p-adic valuation is handled separately
// and only p-coprime (hence invertible) factors are divided.
func binomModPrimePower(n, k uint64, pp PrimePower) uint64 {
	p, a, pe := pp.Prime, pp.Exp, pp.Mod
	e := legendre(n, p) - legendre(k, p) - legendre(n-k, p)
	if e >= a {
		return 0
	}
	ctx := primePowerCtx{p: p, pe: pe, block: unitBlockProduct(p, a, pe)}
	num := ctx.factNoP(n)
	den := mulmod(ctx.factNoP(k), ctx.factNoP(n-k), pe)
	denInv := new(big.Int).ModInverse(
		new(big.Int).SetUint64(den),
		new(big.Int).SetUint64(pe),
	).Uint64()
	result := mulmod(powmod(p, e, pe), num, pe)
	return mulmod(result, denInv, pe)
}

// legendre returns the p-adic valuation of n!.
func legendre(n, p uint64) uint64 {
	var v uint64
	for n > 0 {
		n /= p
		v += n
	}
	return v
}

// unitBlockProduct returns the product of all units mod p^a, which by
// Gauss's generalization of Wilson's theorem is -1 unless p^a is a power
// of two with exponent >= 3, in which case it is 1.
func unitBlockProduct(p, a, pe uint64) uint64 {
	if p == 2 && a >= 3 {
		return 1
	}
	return pe - 1
}

type primePowerCtx struct {
	p, pe, block uint64
}

// factNoP returns n! with every factor of p removed, reduced mod pe.
// With G(n) denoting that stripped factorial and U(m) the product of the
// p-coprime integers in [1, m], n! = p^v * G(n) where
// G(n) = G(n/p) * U(pe)^(n/pe) * U(n mod pe) (mod pe).
func (c *primePowerCtx) factNoP(n uint64) uint64 {
	if n == 0 {
		return 1
	}
	result := c.factNoP(n / c.p)
	result = mulmod(result, powmod(c.block, n/c.pe, c.pe), c.pe)
	rem := n % c.pe
	for i := uint64(2); i <= rem; i++ {
		if i%c.p != 0 {
			result = mulmod(result, i, c.pe)
		}
	}
	return result
}

// crt combines residues modulo pairwise-coprime moduli into a single
// residue modulo their product.
func crt(residues, mods []uint64) uint64 {
	x := new(big.Int).SetUint64(residues[0])
	m := new(big.Int).SetUint64(mods[0])
	t := new(big.Int)
	bi := new(big.Int)
	bm := new(big.Int)
	for i := 1; i < len(residues); i++ {
		bi.SetUint64(residues[i])
		bm.SetUint64(mods[i])
		inv := new(big.Int).ModInverse(new(big.Int).Mod(m, bm), bm)
		t.Sub(bi, x).Mod(t, bm).Mul(t, inv).Mod(t, bm)
		x.Add(x, t.Mul(t, m))
		m.Mul(m, bm)
		x.Mod(x, m)
	}
	return x.Uint64()
}
