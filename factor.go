package binom

import (
	"errors"
	"math/bits"
	"sort"
)

// ErrBudgetExceeded is returned when the factorization budget is exhausted
// before a modulus could be fully factored.
var ErrBudgetExceeded = errors.New("binom: factorization budget exceeded")

// PrimePower is one prime-power factor p^exp of a modulus; Mod is p^exp.
type PrimePower struct {
	Prime uint64
	Exp   uint64
	Mod   uint64
}

// Factorizer factors uint64 moduli and caches the results so that batch
// queries sharing a modulus only pay the factorization cost once.
//
// Budget bounds the total amount of factorization work (trial-division
// steps and Pollard-rho iterations) over the Factorizer's lifetime.
// A zero budget means no work may be performed; cached results are
// always free. Primality tests do not consume budget.
type Factorizer struct {
	budget uint64
	cache  map[uint64][]PrimePower
	// Factorizations counts cache misses, i.e. moduli actually factored.
	Factorizations int
}

// NewFactorizer returns a Factorizer with the given work budget.
func NewFactorizer(budget uint64) *Factorizer {
	return &Factorizer{budget: budget, cache: make(map[uint64][]PrimePower)}
}

// RemainingBudget reports how much factorization work is still allowed.
func (f *Factorizer) RemainingBudget() uint64 { return f.budget }

func (f *Factorizer) spend() bool {
	if f.budget == 0 {
		return false
	}
	f.budget--
	return true
}

// Factorize returns the prime-power factorization of m, consulting the
// shared cache first. m == 1 yields an empty slice.
func (f *Factorizer) Factorize(m uint64) ([]PrimePower, error) {
	if pps, ok := f.cache[m]; ok {
		return pps, nil
	}
	if m == 0 {
		return nil, errors.New("binom: cannot factor 0")
	}
	counts := make(map[uint64]uint64)
	rest := m
	if isPrime(rest) {
		pps := []PrimePower{{Prime: rest, Exp: 1, Mod: rest}}
		f.cache[m] = pps
		f.Factorizations++
		return pps, nil
	}
	for d := uint64(2); d <= 100 && d*d <= rest; d++ {
		if !f.spend() {
			return nil, ErrBudgetExceeded
		}
		for rest%d == 0 {
			counts[d]++
			rest /= d
		}
	}
	if rest > 1 {
		if err := f.factorRecursive(rest, counts); err != nil {
			return nil, err
		}
	}
	primes := make([]uint64, 0, len(counts))
	for p := range counts {
		primes = append(primes, p)
	}
	sort.Slice(primes, func(i, j int) bool { return primes[i] < primes[j] })
	pps := make([]PrimePower, 0, len(primes))
	for _, p := range primes {
		pe := uint64(1)
		for e := uint64(0); e < counts[p]; e++ {
			pe *= p
		}
		pps = append(pps, PrimePower{Prime: p, Exp: counts[p], Mod: pe})
	}
	f.cache[m] = pps
	f.Factorizations++
	return pps, nil
}

func (f *Factorizer) factorRecursive(n uint64, counts map[uint64]uint64) error {
	if n == 1 {
		return nil
	}
	if isPrime(n) {
		counts[n]++
		return nil
	}
	d, ok := f.rho(n)
	if !ok {
		return ErrBudgetExceeded
	}
	if err := f.factorRecursive(d, counts); err != nil {
		return err
	}
	return f.factorRecursive(n/d, counts)
}

// rho finds a non-trivial factor of n using Pollard's rho method with
// Brent's cycle detection. The second result is false if the budget ran
// out before a factor was found.
func (f *Factorizer) rho(n uint64) (uint64, bool) {
	if n%2 == 0 {
		return 2, true
	}
	for c := uint64(1); ; c++ {
		y := c + 1
		var r, q, g uint64 = 1, 1, 1
		var x, ys uint64
		for g == 1 {
			x = y
			for i := uint64(0); i < r; i++ {
				y = rhoStep(y, c, n)
			}
			k := uint64(0)
			for k < r && g == 1 {
				ys = y
				limit := uint64(128)
				if r-k < limit {
					limit = r - k
				}
				for i := uint64(0); i < limit; i++ {
					y = rhoStep(y, c, n)
					q = mulmod(q, absDiff(x, y), n)
					if !f.spend() {
						return 0, false
					}
				}
				g = gcd(q, n)
				k += limit
			}
			r *= 2
		}
		if g == n {
			for {
				ys = rhoStep(ys, c, n)
				g = gcd(absDiff(x, ys), n)
				if !f.spend() {
					return 0, false
				}
				if g > 1 {
					break
				}
			}
		}
		if g != n {
			return g, true
		}
	}
}

func rhoStep(x, c, n uint64) uint64 {
	return addmod(mulmod(x, x, n), c, n)
}

func addmod(a, b, m uint64) uint64 {
	s := a + b
	if s < a || s >= m {
		s -= m
	}
	return s
}

func absDiff(a, b uint64) uint64 {
	if a >= b {
		return a - b
	}
	return b - a
}

func gcd(a, b uint64) uint64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// mulmod returns a*b mod m for a, b < m using 128-bit intermediate
// arithmetic, so it never overflows.
func mulmod(a, b, m uint64) uint64 {
	hi, lo := bits.Mul64(a, b)
	_, rem := bits.Div64(hi, lo, m)
	return rem
}

func powmod(base, exp, m uint64) uint64 {
	if m == 1 {
		return 0
	}
	result := uint64(1 % m)
	base %= m
	for exp > 0 {
		if exp&1 == 1 {
			result = mulmod(result, base, m)
		}
		base = mulmod(base, base, m)
		exp >>= 1
	}
	return result
}

// mrBases are deterministic Miller-Rabin witnesses for all n < 2^64.
var mrBases = [...]uint64{2, 325, 9375, 28178, 450775, 9780504, 1795265022}

func isPrime(n uint64) bool {
	if n < 2 {
		return false
	}
	for _, p := range [...]uint64{2, 3, 5, 7, 11, 13, 17, 19, 23, 29, 31, 37} {
		if n%p == 0 {
			return n == p
		}
	}
	d := n - 1
	s := bits.TrailingZeros64(d)
	d >>= s
	for _, a := range mrBases {
		if a%n == 0 {
			continue
		}
		x := powmod(a, d, n)
		if x == 1 || x == n-1 {
			continue
		}
		composite := true
		for r := 1; r < s; r++ {
			x = mulmod(x, x, n)
			if x == n-1 {
				composite = false
				break
			}
		}
		if composite {
			return false
		}
	}
	return true
}
