package binom

import (
	"math/big"
	"sync"
)

// Query is a single binomial-coefficient request in a batch.
type Query struct {
	N, K int64
}

// Calculator evaluates binomial coefficients modulo arbitrary positive
// integers. Factorizations of moduli are cached, so repeated or batched
// queries against the same modulus share the factorization work.
//
// Budget caps the number of trial-division candidates examined while
// factoring one modulus; a negative value means unlimited. It may be
// changed between queries; cached factorizations remain valid.
type Calculator struct {
	Budget int64

	mu    sync.Mutex
	cache map[string][]primePower
}

// NewCalculator returns a Calculator with an unlimited factorization budget.
func NewCalculator() *Calculator {
	return &Calculator{Budget: -1, cache: make(map[string][]primePower)}
}

var defaultCalculator = NewCalculator()

// Mod returns C(n, k) mod m using a shared default Calculator.
// See Calculator.Mod for the exact contract.
func Mod(n, k int64, m *big.Int) (*big.Int, error) {
	return defaultCalculator.Mod(n, k, m)
}

// Mod returns C(n, k) mod m for any positive integer m.
//
// By convention C(n, k) = 0 when k < 0 or k > n, and the result is 0 when
// m == 1. n must be non-negative and m must be positive.
func (c *Calculator) Mod(n, k int64, m *big.Int) (*big.Int, error) {
	if n < 0 {
		return nil, ErrNegativeN
	}
	if m.Sign() <= 0 {
		return nil, ErrNonPositiveModulus
	}
	if m.Cmp(bigOne) == 0 || k < 0 || k > n {
		return big.NewInt(0), nil
	}
	factors, err := c.factorize(m)
	if err != nil {
		return nil, err
	}
	return modWithFactors(n, k, factors), nil
}

// ModBatch evaluates C(q.N, q.K) mod m for every query, factoring m once
// and sharing the cached factorization across all queries. The returned
// slice is aligned with qs. All queries are validated before any work is
// done; a single invalid query fails the whole batch.
func (c *Calculator) ModBatch(qs []Query, m *big.Int) ([]*big.Int, error) {
	if m.Sign() <= 0 {
		return nil, ErrNonPositiveModulus
	}
	for _, q := range qs {
		if q.N < 0 {
			return nil, ErrNegativeN
		}
	}
	res := make([]*big.Int, len(qs))
	if m.Cmp(bigOne) == 0 {
		for i := range res {
			res[i] = big.NewInt(0)
		}
		return res, nil
	}
	factors, err := c.factorize(m)
	if err != nil {
		return nil, err
	}
	for i, q := range qs {
		if q.K < 0 || q.K > q.N {
			res[i] = big.NewInt(0)
			continue
		}
		res[i] = modWithFactors(q.N, q.K, factors)
	}
	return res, nil
}

// factorize returns the cached prime-power factorization of m (> 1),
// computing it under the calculator's budget on first use.
func (c *Calculator) factorize(m *big.Int) ([]primePower, error) {
	key := m.String()
	c.mu.Lock()
	defer c.mu.Unlock()
	if f, ok := c.cache[key]; ok {
		return f, nil
	}
	f, err := factorize(m, c.Budget)
	if err != nil {
		return nil, err
	}
	c.cache[key] = f
	return f, nil
}

// modWithFactors evaluates C(n, k) modulo the product of the given pairwise
// coprime prime powers and recombines the residues via CRT.
func modWithFactors(n, k int64, factors []primePower) *big.Int {
	if len(factors) == 1 {
		return binomModPrimePower(n, k, factors[0])
	}
	residues := make([]*big.Int, len(factors))
	moduli := make([]*big.Int, len(factors))
	for i, pp := range factors {
		residues[i] = binomModPrimePower(n, k, pp)
		moduli[i] = pp.power
	}
	return crtCombine(residues, moduli)
}

// binomModPrimePower computes C(n, k) mod p^a using the prime-power
// generalization of Lucas' theorem: strip all factors of p from the
// factorials (leaving p-coprime residues, which are invertible mod p^a)
// and multiply back the p-adic valuation separately.
func binomModPrimePower(n, k int64, pp primePower) *big.Int {
	mod := pp.power
	e := vpBinom(n, k, pp.prime)
	if e >= pp.exp {
		return big.NewInt(0)
	}
	fn := factNoP(n, pp.prime, mod)
	fk := factNoP(k, pp.prime, mod)
	fnk := factNoP(n-k, pp.prime, mod)

	res := new(big.Int).Mul(fn, new(big.Int).ModInverse(fk, mod))
	res.Mul(res, new(big.Int).ModInverse(fnk, mod))
	res.Mod(res, mod)
	res.Mul(res, new(big.Int).Exp(pp.prime, big.NewInt(int64(e)), mod))
	return res.Mod(res, mod)
}

// vpBinom returns the p-adic valuation of C(n, k) via Legendre's formula
// v_p(x!) = (x - s_p(x)) / (p - 1), where s_p is the base-p digit sum.
func vpBinom(n, k int64, p *big.Int) int {
	if !p.IsInt64() || p.Int64() > n {
		return 0
	}
	pi := p.Int64()
	vp := func(x int64) int64 {
		var s int64
		for y := x; y > 0; y /= pi {
			s += y % pi
		}
		return (x - s) / (pi - 1)
	}
	return int(vp(n) - vp(k) - vp(n-k))
}

// factNoP returns n! with every factor of p removed, reduced mod pa.
// The p-coprime residues repeat with period pa, giving the recursion
// F(n) = F(n/p) * G(pa)^(n/pa) * G(n mod pa) (mod pa), where
// G(x) = product of the p-coprime integers in [1, x].
func factNoP(n int64, p, pa *big.Int) *big.Int {
	res := big.NewInt(1)

	p64 := int64(0) // 0 means "p larger than any int64"
	if p.IsInt64() {
		p64 = p.Int64()
	}
	pa64 := int64(-1) // negative means "pa does not fit in int64"
	if pa.IsInt64() {
		pa64 = pa.Int64()
	}

	coprimeProduct := func(x int64) *big.Int {
		prod := big.NewInt(1)
		t := new(big.Int)
		for i := int64(1); i <= x; i++ {
			if p64 != 0 && i%p64 == 0 {
				continue
			}
			prod.Mul(prod, t.SetInt64(i))
			prod.Mod(prod, pa)
		}
		return prod
	}

	for n > 0 {
		var cycles, rem int64
		if pa64 >= 0 && pa64 <= n {
			cycles, rem = n/pa64, n%pa64
		} else {
			cycles, rem = 0, n
		}
		if cycles > 0 {
			g := coprimeProduct(pa64)
			res.Mul(res, new(big.Int).Exp(g, big.NewInt(cycles), pa))
			res.Mod(res, pa)
		}
		if rem > 0 {
			res.Mul(res, coprimeProduct(rem))
			res.Mod(res, pa)
		}
		if p64 == 0 {
			break
		}
		n /= p64
	}
	return res
}

// crtCombine merges residues modulo pairwise coprime moduli into a single
// residue modulo their product.
func crtCombine(residues, moduli []*big.Int) *big.Int {
	x := new(big.Int).Set(residues[0])
	m := new(big.Int).Set(moduli[0])
	t := new(big.Int)
	for i := 1; i < len(residues); i++ {
		inv := new(big.Int).ModInverse(t.Mod(m, moduli[i]), moduli[i])
		t.Sub(residues[i], x)
		t.Mul(t, inv)
		t.Mod(t, moduli[i])
		x.Add(x, t.Mul(t, m))
		m.Mul(m, moduli[i])
		x.Mod(x, m)
	}
	return x
}
