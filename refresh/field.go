package refresh

import (
	"errors"
	"math/big"
)

// Field is the prime field over which all custodian scalars (coordinates,
// polynomial coefficients and share increments) live. The prime is supplied by
// the caller: this package never invents domain parameters.
type Field struct {
	p *big.Int
}

var errZeroModulus = errors.New("refresh: field modulus must be a positive prime")

// NewField builds a prime field. It rejects non-prime or non-positive moduli so
// that a misconfigured host fails closed rather than running the protocol over
// a composite ring.
func NewField(p *big.Int) (*Field, error) {
	if p == nil || p.Sign() <= 0 {
		return nil, errZeroModulus
	}
	if !p.ProbablyPrime(32) {
		return nil, errors.New("refresh: field modulus is not prime")
	}
	return &Field{p: new(big.Int).Set(p)}, nil
}

// P returns the field modulus as a value that must not be mutated by callers.
func (f *Field) P() *big.Int { return f.p }

// Mod reduces x into [0, p).
func (f *Field) Mod(x *big.Int) *big.Int { return new(big.Int).Mod(x, f.p) }

// Add returns a+b mod p.
func (f *Field) Add(a, b *big.Int) *big.Int {
	return new(big.Int).Mod(new(big.Int).Add(a, b), f.p)
}

// Sub returns a-b mod p.
func (f *Field) Sub(a, b *big.Int) *big.Int {
	return new(big.Int).Mod(new(big.Int).Sub(a, b), f.p)
}

// Mul returns a*b mod p.
func (f *Field) Mul(a, b *big.Int) *big.Int {
	return new(big.Int).Mod(new(big.Int).Mul(a, b), f.p)
}

// Neg returns -a mod p.
func (f *Field) Neg(a *big.Int) *big.Int { return new(big.Int).Mod(new(big.Int).Neg(a), f.p) }

// Sum returns the field sum of vals (zero when empty).
func (f *Field) Sum(vals ...*big.Int) *big.Int {
	acc := big.NewInt(0)
	for _, v := range vals {
		if v == nil {
			continue
		}
		acc.Add(acc, v)
	}
	return f.Mod(acc)
}

// Eval evaluates polynomial coefficients (low degree first) at x using Horner.
func (f *Field) Eval(coeffs []*big.Int, x *big.Int) *big.Int {
	acc := big.NewInt(0)
	for i := len(coeffs) - 1; i >= 0; i-- {
		acc = f.Mul(acc, x)
		acc = f.Add(acc, coeffs[i])
	}
	return acc
}

// EqualScalar reports whether a and b denote the same field element.
func (f *Field) EqualScalar(a, b *big.Int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return f.Mod(a).Cmp(f.Mod(b)) == 0
}
