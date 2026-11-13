package netsec

import (
	"crypto/rsa"
	"fmt"
	"math/big"
)

// PublicKey is the local RSA public key used by the recovery package.
//
// N is the modulus, E the public exponent, K the byte length of N, and B is
// 2^(8*(K-2)).  All values are treated as exact integers.
type PublicKey struct {
	N *big.Int
	E *big.Int
	K int
	B *big.Int
}

func coercePublicKey(v any) (*PublicKey, error) {
	switch k := v.(type) {
	case *PublicKey:
		if k == nil {
			return nil, ErrInvalidKey
		}
		return NewPublicKey(k.N, k.E)
	case PublicKey:
		return NewPublicKey(k.N, k.E)
	case *rsa.PublicKey:
		return NewFromRSA(k)
	case rsa.PublicKey:
		return NewFromRSA(&k)
	default:
		return nil, ErrInvalidKey
	}
}

// NewPublicKey validates and copies a local RSA public key.  The prefix game
// requires at least a three-byte modulus.
func NewPublicKey(n, e *big.Int) (*PublicKey, error) {
	if n == nil || e == nil || n.Sign() <= 0 || e.Sign() <= 0 {
		return nil, ErrInvalidKey
	}
	if n.Bit(0) == 0 {
		return nil, fmt.Errorf("%w: modulus must be odd", ErrInvalidKey)
	}
	if e.Cmp(big.NewInt(3)) < 0 || e.Bit(0) == 0 {
		return nil, fmt.Errorf("%w: public exponent must be odd and at least 3", ErrInvalidKey)
	}

	k := len(n.Bytes())
	if k < 3 {
		return nil, fmt.Errorf("%w: modulus is only %d bytes", ErrInvalidKey, k)
	}
	b := new(big.Int).Lsh(big.NewInt(1), uint(8*(k-2)))
	if n.Cmp(new(big.Int).Mul(b, big.NewInt(3))) <= 0 {
		return nil, fmt.Errorf("%w: modulus too small for the 2B..3B range", ErrInvalidKey)
	}

	return &PublicKey{
		N: new(big.Int).Set(n),
		E: new(big.Int).Set(e),
		K: k,
		B: b,
	}, nil
}

// NewFromRSA converts a crypto/rsa public key.
func NewFromRSA(pub *rsa.PublicKey) (*PublicKey, error) {
	if pub == nil {
		return nil, ErrInvalidKey
	}
	return NewPublicKey(pub.N, big.NewInt(int64(pub.E)))
}

// Clone returns a deep copy.
func (k *PublicKey) Clone() *PublicKey {
	if k == nil {
		return nil
	}
	return &PublicKey{
		N: new(big.Int).Set(k.N),
		E: new(big.Int).Set(k.E),
		K: k.K,
		B: new(big.Int).Set(k.B),
	}
}

// PrefixInterval returns the known integer range [2B, 3B-1].
func (k *PublicKey) PrefixInterval() Interval {
	lo := new(big.Int).Lsh(new(big.Int).Set(k.B), 1)
	hi := new(big.Int).Sub(new(big.Int).Mul(new(big.Int).Set(k.B), big.NewInt(3)), big.NewInt(1))
	return Interval{Lo: lo, Hi: hi}
}

// VerifyReencryption reports whether m^E mod N equals the target ciphertext.
func (k *PublicKey) VerifyReencryption(ciphertext, m *big.Int) bool {
	if k == nil || ciphertext == nil || m == nil {
		return false
	}
	if m.Sign() < 0 || m.Cmp(k.N) >= 0 {
		return false
	}
	re := new(big.Int).Exp(m, k.E, k.N)
	return re.Cmp(ciphertext) == 0
}

// InitialIntervals returns the full legal plaintext range for this key.
func InitialIntervals(n *big.Int) ([]Interval, error) {
	k, err := NewPublicKey(n, big.NewInt(3))
	if err != nil {
		return nil, err
	}
	return []Interval{k.PrefixInterval()}, nil
}

// BForModulus returns B = 2^(8*(k-2)) for a valid modulus byte length.
func BForModulus(n *big.Int) (*big.Int, error) {
	k, err := NewPublicKey(n, big.NewInt(3))
	if err != nil {
		return nil, err
	}
	return new(big.Int).Set(k.B), nil
}

// PrefixRange returns the inclusive integer interval [2B, 3B-1].
func PrefixRange(n *big.Int) (Interval, error) {
	k, err := NewPublicKey(n, big.NewInt(3))
	if err != nil {
		return Interval{}, err
	}
	return k.PrefixInterval(), nil
}
