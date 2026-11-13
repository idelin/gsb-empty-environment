package netsec

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"math/bits"
)

// MaxPrime is the largest caller-supplied modulus accepted by this package.
const MaxPrime uint32 = 65521

// Field is a prime field F_p with p <= MaxPrime.
type Field struct {
	P uint32
}

// NewField validates and constructs a prime field.
func NewField(p uint32) (Field, error) {
	if p < 2 || p > MaxPrime || !IsPrime(p) {
		return Field{}, fmt.Errorf("%w: p=%d", ErrInvalidField, p)
	}
	return Field{P: p}, nil
}

// IsPrime reports whether n is prime. It is deterministic for n <= MaxPrime.
func IsPrime(n uint32) bool {
	if n < 2 {
		return false
	}
	if n%2 == 0 {
		return n == 2
	}
	// Trial division is fast enough for the 16-bit range accepted here.
	for d := uint32(3); uint64(d)*uint64(d) <= uint64(n); d += 2 {
		if n%d == 0 {
			return false
		}
	}
	return true
}

// Valid reports whether v is a canonical field element.
func (f Field) Valid(v uint32) bool {
	return v < f.P
}

// Add returns a+b mod p.
func (f Field) Add(a, b uint32) uint32 {
	return uint32((uint64(a) + uint64(b)) % uint64(f.P))
}

// Sub returns a-b mod p.
func (f Field) Sub(a, b uint32) uint32 {
	return uint32((uint64(a) + uint64(f.P) - uint64(b)%uint64(f.P)) % uint64(f.P))
}

// Mul returns a*b mod p.
func (f Field) Mul(a, b uint32) uint32 {
	return uint32((uint64(a) * uint64(b)) % uint64(f.P))
}

// Neg returns -a mod p.
func (f Field) Neg(a uint32) uint32 {
	if a%f.P == 0 {
		return 0
	}
	return f.P - a%f.P
}

// Sum returns the field sum of vals.
func (f Field) Sum(vals ...uint32) uint32 {
	var s uint64
	for _, v := range vals {
		s += uint64(v % f.P)
	}
	return uint32(s % uint64(f.P))
}

// Inv returns a^-1 mod p. Zero is not invertible.
func (f Field) Inv(a uint32) (uint32, error) {
	a %= f.P
	if a == 0 {
		return 0, fmt.Errorf("%w: zero has no inverse", ErrInvalidShare)
	}
	// Extended Euclid over int64.
	t, newT := int64(0), int64(1)
	r, newR := int64(f.P), int64(a)
	for newR != 0 {
		q := r / newR
		t, newT = newT, t-q*newT
		r, newR = newR, r-q*newR
	}
	if r != 1 {
		return 0, fmt.Errorf("%w: %d is not invertible", ErrInvalidShare, a)
	}
	if t < 0 {
		t += int64(f.P)
	}
	return uint32(t), nil
}

// Random returns a uniformly sampled field element. Bias is removed by
// rejection sampling over the 32-bit source space.
func (f Field) Random(rng io.Reader) (uint32, error) {
	if rng == nil {
		rng = rand.Reader
	}
	limit := (uint64(1) << 32) - ((uint64(1) << 32) % uint64(f.P))
	for {
		var buf [4]byte
		if _, err := io.ReadFull(rng, buf[:]); err != nil {
			return 0, fmt.Errorf("%w: %v", ErrRandom, err)
		}
		v := binary.BigEndian.Uint32(buf[:])
		if uint64(v) < limit {
			return v % f.P, nil
		}
	}
}

// RandomNonZero returns a uniformly sampled element of F_p^*. For p=2 this is
// necessarily 1.
func (f Field) RandomNonZero(rng io.Reader) (uint32, error) {
	for {
		v, err := f.Random(rng)
		if err != nil {
			return 0, err
		}
		if v != 0 {
			return v, nil
		}
	}
}

// BitLen returns the bit length of p; useful to callers constructing proofs.
func (f Field) BitLen() int {
	return bits.Len32(f.P)
}
