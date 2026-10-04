package binom

import "math/big"

// primePower is a prime factor p of the modulus together with p^exp.
type primePower struct {
	prime *big.Int
	exp   int
	power *big.Int // prime^exp
}

var (
	bigZero = big.NewInt(0)
	bigOne  = big.NewInt(1)
	bigTwo  = big.NewInt(2)
)

// factorize splits m (> 1) into its prime-power factors.
//
// budget caps the number of trial-division candidates examined; a negative
// budget means unlimited. Primality proofs (ProbablyPrime) are free, so a
// prime modulus or a large prime cofactor is detected without consuming
// budget. ErrFactorBudgetExceeded is returned when the budget runs out
// before the factorization is complete.
func factorize(m *big.Int, budget int64) ([]primePower, error) {
	rem := new(big.Int).Set(m)
	var factors []primePower

	d := new(big.Int).Set(bigTwo)
	step := big.NewInt(2)
	q, r, sq := new(big.Int), new(big.Int), new(big.Int)

	for rem.Cmp(bigOne) > 0 {
		if rem.ProbablyPrime(20) || sq.Mul(d, d).Cmp(rem) > 0 {
			// rem is prime.
			p := new(big.Int).Set(rem)
			factors = append(factors, primePower{p, 1, new(big.Int).Set(p)})
			break
		}
		if budget == 0 {
			return nil, ErrFactorBudgetExceeded
		}
		if budget > 0 {
			budget--
		}
		exp := 0
		for {
			q.QuoRem(rem, d, r)
			if r.Sign() != 0 {
				break
			}
			rem.Set(q)
			exp++
		}
		if exp > 0 {
			p := new(big.Int).Set(d)
			factors = append(factors, primePower{
				prime: p,
				exp:   exp,
				power: new(big.Int).Exp(p, big.NewInt(int64(exp)), nil),
			})
		}
		if d.Cmp(bigTwo) == 0 {
			d.SetInt64(3)
		} else {
			d.Add(d, step)
		}
	}
	return factors, nil
}
