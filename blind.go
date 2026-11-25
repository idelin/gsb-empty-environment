package parity

import (
	crand "crypto/rand"
	"io"
	"math/big"
)

// BlindQuery returns x * r^e mod N, the rerandomized wire query.
//
// Decryption of the result is (r * Dec(x)) mod N. When x itself corresponds to
// a known plaintext multiplier T, the new exact multiplier is T*r.
func BlindQuery(x, r, e, N *big.Int) *big.Int {
	rE := new(big.Int).Exp(r, e, N)
	out := new(big.Int).Mul(x, rE)
	return out.Mod(out, N)
}

// PowerTwoE returns 2^e mod N, the per-step ciphertext multiplier.
func PowerTwoE(e, N *big.Int) *big.Int {
	return new(big.Int).Exp(big.NewInt(2), e, N)
}

// BlindPolicy controls when per-query rerandomization with r is used.
type BlindPolicy int

const (
	// BlindRetries rerandomizes retry attempts with a fresh known-factor r.
	// It is the zero value and default. It bypasses single-point duplicate
	// detection while leaving smooth runs on the exact power-of-two dyadic path.
	BlindRetries BlindPolicy = iota
	// BlindNone repeats the identical logical query after unavailability.
	BlindNone
	// BlindEveryQuery rerandomizes every wire attempt, including first tries.
	BlindEveryQuery
)

// Blind carries a generated blinding factor and its exact known factorization.
type Blind struct {
	R       *big.Int
	Factors []*big.Int
}

// BlindFunc produces a fresh blinding factor for modulus N.
type BlindFunc func(N *big.Int) (Blind, error)

// FactorSmall returns the prime factors of v with multiplicity, ascending.
// It is intended for the small known-factor blinders used by the exact strip
// engine. v must be >= 2.
func FactorSmall(v *big.Int) []*big.Int {
	n := new(big.Int).Set(v)
	var factors []*big.Int
	two := big.NewInt(2)
	for new(big.Int).Mod(n, two).Sign() == 0 {
		factors = append(factors, big.NewInt(2))
		n.Div(n, two)
	}
	d := big.NewInt(3)
	one := big.NewInt(1)
	for {
		d2 := new(big.Int).Mul(d, d)
		if d2.Cmp(n) > 0 {
			break
		}
		for new(big.Int).Mod(n, d).Sign() == 0 {
			factors = append(factors, new(big.Int).Set(d))
			n.Div(n, d)
		}
		d.Add(d, big.NewInt(2))
	}
	if n.Cmp(one) > 0 {
		factors = append(factors, n)
	}
	return factors
}

// NewSmallFactorBlind returns a BlindFunc that chooses a random ODD r in
// [3, maxR] with gcd(r,N)==1 and records its exact factorization.
//
// Odd r preserves the 2-adic bit depth of the pending query (it does not skip
// dyadic bits), and the bound keeps exact rational strip expansion O(r). If
// random is nil, crypto/rand.Reader is used.
func NewSmallFactorBlind(maxR int64, random io.Reader) BlindFunc {
	if maxR < 3 {
		maxR = 3
	}
	if random == nil {
		random = crand.Reader
	}
	return func(N *big.Int) (Blind, error) {
		if N == nil || N.Sign() <= 0 {
			return Blind{}, ErrInvalidInput
		}
		var odds []int64
		for cand := int64(3); cand <= maxR; cand += 2 {
			r := big.NewInt(cand)
			if r.Cmp(N) >= 0 {
				break
			}
			if new(big.Int).GCD(nil, nil, r, N).Cmp(bigOne) == 0 {
				odds = append(odds, cand)
			}
		}
		if len(odds) == 0 {
			// N too small for an odd candidate; r=2 is safe for odd N.
			r := big.NewInt(2)
			if new(big.Int).GCD(nil, nil, r, N).Cmp(bigOne) == 0 {
				return Blind{R: r, Factors: FactorSmall(r)}, nil
			}
			return Blind{}, ErrInvalidInput
		}
		idx, err := crand.Int(random, big.NewInt(int64(len(odds))))
		if err != nil {
			return Blind{}, err
		}
		r := big.NewInt(odds[idx.Int64()])
		return Blind{R: new(big.Int).Set(r), Factors: FactorSmall(r)}, nil
	}
}

// NewBaseBlindR returns a full-size random coprime r for classic base
// blinding. The caller knows r exactly (and may record its factors); r need not
// be small because base blinding keeps a single exact dyadic interval.
func NewBaseBlindR(random io.Reader) BlindFunc {
	if random == nil {
		random = crand.Reader
	}
	return func(N *big.Int) (Blind, error) {
		if N == nil || N.Sign() <= 0 {
			return Blind{}, ErrInvalidInput
		}
		// Draw a random integer below N and force coprimality. Its exact value
		// is recorded; factorization is unnecessary for classic base blinding.
		for attempt := 0; attempt < 128; attempt++ {
			r, err := crand.Int(random, N)
			if err != nil {
				return Blind{}, err
			}
			if r.Sign() <= 0 || r.Cmp(bigOne) == 0 {
				continue
			}
			if new(big.Int).GCD(nil, nil, r, N).Cmp(bigOne) != 0 {
				continue
			}
			// r itself is the known blinding scalar; factoring a full-size
			// random integer is unnecessary (and would be prohibitively slow).
			return Blind{R: r}, nil
		}
		return Blind{}, ErrInvalidInput
	}
}

// NewPrimeBaseBlindR returns a random prime r whose single known factor is
// itself. bits should be below N.BitLen so r < N in the usual case.
func NewPrimeBaseBlindR(bits int, random io.Reader) BlindFunc {
	if random == nil {
		random = crand.Reader
	}
	if bits < 8 {
		bits = 8
	}
	return func(N *big.Int) (Blind, error) {
		r, err := crand.Prime(random, bits)
		if err != nil {
			return Blind{}, err
		}
		if new(big.Int).GCD(nil, nil, r, N).Cmp(bigOne) != 0 {
			return Blind{}, ErrInvalidInput
		}
		return Blind{R: r, Factors: []*big.Int{new(big.Int).Set(r)}}, nil
	}
}

// fixedBlind returns a BlindFunc that always uses r (and factors it once).
// Useful for deterministic host regression vectors. r should be coprime to N
// for the non-integer-boundary invariant.
func fixedBlind(r *big.Int) BlindFunc {
	factors := FactorSmall(r)
	return func(*big.Int) (Blind, error) {
		return Blind{R: new(big.Int).Set(r), Factors: append([]*big.Int(nil), factors...)}, nil
	}
}
