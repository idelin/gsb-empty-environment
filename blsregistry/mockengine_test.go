package blsregistry

import (
	"crypto/rand"
	"crypto/sha256"
	"io"
	"math/big"
)

// mockOrder is the BLS12-381 scalar field order r. Points are represented as
// scalar multiples of the fixed group generators, so G1, G2 and the scalar
// field share this modulus in the simulation.
var mockOrder = func() *big.Int {
	r, ok := new(big.Int).SetString(
		"73eda753299d7d483339d80809a1d80553bda402fffe5bfeffffffff00000001", 16)
	if !ok {
		panic("invalid mock scalar order")
	}
	return r
}()

type mockScalar struct{ n *big.Int }

func (s mockScalar) IsZero() bool { return s.n.Sign() == 0 }

// norm reduces n modulo the mock group order into a fresh Int.
func norm(n *big.Int) *big.Int { return new(big.Int).Mod(n, mockOrder) }

// mockG1 represents x * G1generator by the scalar x.
type mockG1 struct{ n *big.Int }

func (p mockG1) IsIdentity() bool { return p.n.Sign() == 0 }

func (p mockG1) Add(other G1Point) G1Point {
	return mockG1{norm(new(big.Int).Add(p.n, other.(mockG1).n))}
}

func (p mockG1) Neg() G1Point {
	if p.n.Sign() == 0 {
		return mockG1{new(big.Int)}
	}
	return mockG1{new(big.Int).Sub(mockOrder, p.n)}
}

func (p mockG1) Mul(s Scalar) G1Point {
	return mockG1{norm(new(big.Int).Mul(p.n, s.(mockScalar).n))}
}

func (p mockG1) Encode() []byte {
	return p.n.FillBytes(make([]byte, 32))
}

func (p mockG1) Equal(other G1Point) bool {
	return p.n.Cmp(other.(mockG1).n) == 0
}

// mockG2 represents y * G2generator by the scalar y.
type mockG2 struct{ n *big.Int }

func (q mockG2) IsIdentity() bool { return q.n.Sign() == 0 }

func (q mockG2) Add(other G2Point) G2Point {
	return mockG2{norm(new(big.Int).Add(q.n, other.(mockG2).n))}
}

func (q mockG2) Neg() G2Point {
	if q.n.Sign() == 0 {
		return mockG2{new(big.Int)}
	}
	return mockG2{new(big.Int).Sub(mockOrder, q.n)}
}

func (q mockG2) Mul(s Scalar) G2Point {
	return mockG2{norm(new(big.Int).Mul(q.n, s.(mockScalar).n))}
}

func (q mockG2) Equal(other G2Point) bool {
	return q.n.Cmp(other.(mockG2).n) == 0
}

// mockEngine simulates a minimal-pubkey-size BLS engine algebraically. It is
// correct for exercising protocol logic but has no cryptographic security:
// never use it outside tests.
type mockEngine struct{}

func (mockEngine) G1Generator() G1Point { return mockG1{big.NewInt(1)} }

func (mockEngine) G2Generator() G2Point { return mockG2{big.NewInt(1)} }

func (mockEngine) HashToG2(dst string, message []byte) G2Point {
	// The domain separation tag is part of the hash input, so distinct dst
	// values yield independent simulated hash points.
	h := sha256.New()
	_, _ = io.WriteString(h, dst)
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(message)
	scalar := norm(new(big.Int).SetBytes(h.Sum(nil)))
	for counter := uint64(1); scalar.Sign() == 0; counter++ {
		h2 := sha256.New()
		_, _ = io.WriteString(h2, dst)
		_, _ = h2.Write([]byte{byte(counter)})
		_, _ = h2.Write(message)
		scalar.Set(norm(new(big.Int).SetBytes(h2.Sum(nil))))
	}
	return mockG2{scalar}
}

// CheckPairing simulates e(P, Q) == g^(p*q) in a cyclic target group of
// order mockOrder. The multi-pairing holds iff the summed exponent is zero.
func (mockEngine) CheckPairing(g1 []G1Point, g2 []G2Point) bool {
	if len(g1) != len(g2) {
		return false
	}
	exponent := new(big.Int)
	for i := range g1 {
		term := new(big.Int).Mul(g1[i].(mockG1).n, g2[i].(mockG2).n)
		exponent.Add(exponent, term)
	}
	exponent.Mod(exponent, mockOrder)
	return exponent.Sign() == 0
}

type mockKeyPair struct {
	secret mockScalar
	public mockG1
}

func newMockKeyPair() mockKeyPair {
	buf := make([]byte, 32)
	for {
		if _, err := io.ReadFull(rand.Reader, buf); err != nil {
			panic(err)
		}
		scalar := norm(new(big.Int).SetBytes(buf))
		if scalar.Sign() != 0 {
			return mockKeyPair{
				secret: mockScalar{scalar},
				public: mockG1{new(big.Int).Set(scalar)},
			}
		}
	}
}
