package netsec_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"testing"

	netsec "example.org/netsec/q0044"
	"example.org/netsec/q0044/adapters/p256"
	"example.org/netsec/q0044/adapters/smallprime"
)

// suite bundles a group adapter with a deterministic source of
// host-fixed, non-identity generators.
type suite struct {
	name string
	g    netsec.Group
	gen  func(label string, i int) netsec.Point
}

func deriveSeed(label string, i int) []byte {
	h := sha256.Sum256([]byte(fmt.Sprintf("netsec-test-gen/%s/%d", label, i)))
	return h[:]
}

func suites() []suite {
	p256g := p256.New()
	toy := smallprime.New()
	return []suite{
		{
			name: "p256",
			g:    p256g,
			gen: func(label string, i int) netsec.Point {
				s := p256g.ScalarFromBig(new(big.Int).SetBytes(deriveSeed(label, i)))
				if s.IsZero() {
					s = p256g.ScalarFromBig(big.NewInt(1))
				}
				return p256g.Base().ScalarMul(s)
			},
		},
		{
			name: "smallprime",
			g:    toy,
			gen: func(label string, i int) netsec.Point {
				v := binary.BigEndian.Uint64(deriveSeed(label, i)) % smallprime.P
				if v == 0 {
					v = 1
				}
				return toy.PointFromUint64(v)
			},
		},
	}
}

func makeParams(s suite, n int, context string) *netsec.Params {
	p := &netsec.Params{
		G:       make([]netsec.Point, n),
		H:       make([]netsec.Point, n),
		U:       s.gen("U", 0),
		Context: []byte(context),
	}
	for i := 0; i < n; i++ {
		p.G[i] = s.gen("G", i)
		p.H[i] = s.gen("H", i)
	}
	return p
}

func makeWitness(s suite, n int, seedLabel string) (a, b []netsec.Scalar) {
	a = make([]netsec.Scalar, n)
	b = make([]netsec.Scalar, n)
	for i := 0; i < n; i++ {
		a[i] = s.g.ScalarFromBig(new(big.Int).SetBytes(deriveSeed(seedLabel+"/a", i)))
		b[i] = s.g.ScalarFromBig(new(big.Int).SetBytes(deriveSeed(seedLabel+"/b", i)))
	}
	return a, b
}

func TestRoundTrip(t *testing.T) {
	for _, s := range suites() {
		for _, n := range []int{1, 2, 4, 8, 64, 256, 1024} {
			t.Run(fmt.Sprintf("%s/n=%d", s.name, n), func(t *testing.T) {
				params := makeParams(s, n, "roundtrip")
				a, b := makeWitness(s, n, "w")
				P, proof, err := netsec.Prove(s.g, params, a, b)
				if err != nil {
					t.Fatalf("Prove: %v", err)
				}
				if err := netsec.Verify(s.g, params, P, proof); err != nil {
					t.Fatalf("Verify: %v", err)
				}
				if err := netsec.VerifyBytes(s.g, params, P, proof.Marshal(s.g)); err != nil {
					t.Fatalf("VerifyBytes: %v", err)
				}
			})
		}
	}
}

func TestZeroWitnessIdentityCommitment(t *testing.T) {
	for _, s := range suites() {
		t.Run(s.name, func(t *testing.T) {
			n := 8
			params := makeParams(s, n, "zero-witness")
			zero := s.g.ScalarFromBig(new(big.Int))
			a := make([]netsec.Scalar, n)
			b := make([]netsec.Scalar, n)
			for i := range a {
				a[i], b[i] = zero, zero
			}
			P, proof, err := netsec.Prove(s.g, params, a, b)
			if err != nil {
				t.Fatalf("Prove: %v", err)
			}
			if !P.IsIdentity() {
				t.Fatal("zero witness must commit to the identity")
			}
			for i := range proof.L {
				if !proof.L[i].IsIdentity() || !proof.R[i].IsIdentity() {
					t.Fatalf("round %d: zero witness must yield identity L/R", i)
				}
			}
			if err := netsec.Verify(s.g, params, P, proof); err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if err := netsec.VerifyBytes(s.g, params, P, proof.Marshal(s.g)); err != nil {
				t.Fatalf("VerifyBytes with identity P/L/R: %v", err)
			}
		})
	}
}

func TestDeterministicProof(t *testing.T) {
	for _, s := range suites() {
		t.Run(s.name, func(t *testing.T) {
			params := makeParams(s, 16, "det")
			a, b := makeWitness(s, 16, "w")
			_, p1, err := netsec.Prove(s.g, params, a, b)
			if err != nil {
				t.Fatalf("Prove: %v", err)
			}
			_, p2, err := netsec.Prove(s.g, params, a, b)
			if err != nil {
				t.Fatalf("Prove: %v", err)
			}
			if !bytes.Equal(p1.Marshal(s.g), p2.Marshal(s.g)) {
				t.Fatal("proofs must be deterministic")
			}
		})
	}
}

func TestSwapLRRejected(t *testing.T) {
	for _, s := range suites() {
		t.Run(s.name, func(t *testing.T) {
			params := makeParams(s, 8, "swap")
			a, b := makeWitness(s, 8, "w")
			P, proof, err := netsec.Prove(s.g, params, a, b)
			if err != nil {
				t.Fatalf("Prove: %v", err)
			}
			proof.L[0], proof.R[0] = proof.R[0], proof.L[0]
			if err := netsec.Verify(s.g, params, P, proof); !errors.Is(err, netsec.ErrVerification) {
				t.Fatalf("swapped L/R: got %v, want ErrVerification", err)
			}
			if err := netsec.VerifyBytes(s.g, params, P, proof.Marshal(s.g)); !errors.Is(err, netsec.ErrVerification) {
				t.Fatalf("swapped L/R bytes: got %v, want ErrVerification", err)
			}
		})
	}
}

func TestChangedNRejected(t *testing.T) {
	for _, s := range suites() {
		t.Run(s.name, func(t *testing.T) {
			params := makeParams(s, 8, "n-change")
			a, b := makeWitness(s, 8, "w")
			P, proof, err := netsec.Prove(s.g, params, a, b)
			if err != nil {
				t.Fatalf("Prove: %v", err)
			}
			bigger := makeParams(s, 16, "n-change")
			if err := netsec.Verify(s.g, bigger, P, proof); !errors.Is(err, netsec.ErrRoundCount) {
				t.Fatalf("n=16 params: got %v, want ErrRoundCount", err)
			}
			smaller := makeParams(s, 4, "n-change")
			if err := netsec.Verify(s.g, smaller, P, proof); !errors.Is(err, netsec.ErrRoundCount) {
				t.Fatalf("n=4 params: got %v, want ErrRoundCount", err)
			}
		})
	}
}

func TestWrongRoundCountRejected(t *testing.T) {
	for _, s := range suites() {
		t.Run(s.name, func(t *testing.T) {
			params := makeParams(s, 8, "rounds")
			a, b := makeWitness(s, 8, "w")
			P, proof, err := netsec.Prove(s.g, params, a, b)
			if err != nil {
				t.Fatalf("Prove: %v", err)
			}
			extra := *proof
			extra.L = append(append([]netsec.Point{}, proof.L...), s.g.Identity())
			extra.R = append(append([]netsec.Point{}, proof.R...), s.g.Identity())
			if err := netsec.Verify(s.g, params, P, &extra); !errors.Is(err, netsec.ErrRoundCount) {
				t.Fatalf("extra round: got %v, want ErrRoundCount", err)
			}
			dropped := *proof
			dropped.L = dropped.L[:len(dropped.L)-1]
			dropped.R = dropped.R[:len(dropped.R)-1]
			if err := netsec.Verify(s.g, params, P, &dropped); !errors.Is(err, netsec.ErrRoundCount) {
				t.Fatalf("dropped round: got %v, want ErrRoundCount", err)
			}
		})
	}
}

func TestChangedContextRejected(t *testing.T) {
	for _, s := range suites() {
		t.Run(s.name, func(t *testing.T) {
			params := makeParams(s, 8, "context-A")
			a, b := makeWitness(s, 8, "w")
			P, proof, err := netsec.Prove(s.g, params, a, b)
			if err != nil {
				t.Fatalf("Prove: %v", err)
			}
			other := makeParams(s, 8, "context-B")
			if err := netsec.Verify(s.g, other, P, proof); !errors.Is(err, netsec.ErrVerification) {
				t.Fatalf("foreign context: got %v, want ErrVerification", err)
			}
		})
	}
}

func TestChangedGeneratorsRejected(t *testing.T) {
	for _, s := range suites() {
		t.Run(s.name, func(t *testing.T) {
			params := makeParams(s, 8, "gens")
			a, b := makeWitness(s, 8, "w")
			P, proof, err := netsec.Prove(s.g, params, a, b)
			if err != nil {
				t.Fatalf("Prove: %v", err)
			}
			other := makeParams(s, 8, "gens")
			other.U = s.gen("U", 1)
			if err := netsec.Verify(s.g, other, P, proof); !errors.Is(err, netsec.ErrVerification) {
				t.Fatalf("foreign U: got %v, want ErrVerification", err)
			}
		})
	}
}

func TestTruncatedProofRejected(t *testing.T) {
	for _, s := range suites() {
		t.Run(s.name, func(t *testing.T) {
			params := makeParams(s, 8, "trunc")
			a, b := makeWitness(s, 8, "w")
			P, proof, err := netsec.Prove(s.g, params, a, b)
			if err != nil {
				t.Fatalf("Prove: %v", err)
			}
			full := proof.Marshal(s.g)
			for _, cut := range []int{1, s.g.PointSize(), len(full) / 2, len(full) - 1} {
				if err := netsec.VerifyBytes(s.g, params, P, full[:len(full)-cut]); err == nil {
					t.Fatalf("truncated by %d bytes: want error, got nil", cut)
				}
			}
			trailing := append(append([]byte{}, full...), 0)
			if err := netsec.VerifyBytes(s.g, params, P, trailing); err == nil {
				t.Fatal("trailing byte: want error, got nil")
			}
		})
	}
}

func TestNonCanonicalScalarRejected(t *testing.T) {
	for _, s := range suites() {
		t.Run(s.name, func(t *testing.T) {
			params := makeParams(s, 4, "canon")
			a, b := makeWitness(s, 4, "w")
			_, proof, err := netsec.Prove(s.g, params, a, b)
			if err != nil {
				t.Fatalf("Prove: %v", err)
			}
			full := proof.Marshal(s.g)
			ss := s.g.ScalarSize()
			bad := append([]byte{}, full...)
			for i := len(bad) - ss; i < len(bad); i++ {
				bad[i] = 0xFF
			}
			if _, err := netsec.UnmarshalProof(s.g, bad); err == nil {
				t.Fatal("non-canonical scalar: want decode error, got nil")
			}
		})
	}
}

func TestNonCanonicalPointRejected(t *testing.T) {
	for _, s := range suites() {
		t.Run(s.name, func(t *testing.T) {
			params := makeParams(s, 4, "canon")
			a, b := makeWitness(s, 4, "w")
			_, proof, err := netsec.Prove(s.g, params, a, b)
			if err != nil {
				t.Fatalf("Prove: %v", err)
			}
			full := proof.Marshal(s.g)
			bad := append([]byte{}, full...)
			for i := 0; i < s.g.PointSize(); i++ {
				bad[i] = 0xFF
			}
			if _, err := netsec.UnmarshalProof(s.g, bad); err == nil {
				t.Fatal("non-canonical point: want decode error, got nil")
			}
		})
	}
}

func TestTamperedFinalScalarRejected(t *testing.T) {
	for _, s := range suites() {
		t.Run(s.name, func(t *testing.T) {
			params := makeParams(s, 8, "tamper")
			a, b := makeWitness(s, 8, "w")
			P, proof, err := netsec.Prove(s.g, params, a, b)
			if err != nil {
				t.Fatalf("Prove: %v", err)
			}
			one := s.g.ScalarFromBig(big.NewInt(1))
			tampered := *proof
			tampered.A = tampered.A.Add(one)
			if err := netsec.Verify(s.g, params, P, &tampered); !errors.Is(err, netsec.ErrVerification) {
				t.Fatalf("tampered A: got %v, want ErrVerification", err)
			}
		})
	}
}

func TestInvalidNRejected(t *testing.T) {
	for _, s := range suites() {
		t.Run(s.name, func(t *testing.T) {
			for _, n := range []int{0, 3, 6, 1025, 2048} {
				params := makeParams(s, n, "bad-n")
				a, b := makeWitness(s, n, "w")
				if _, _, err := netsec.Prove(s.g, params, a, b); !errors.Is(err, netsec.ErrInvalidN) {
					t.Fatalf("n=%d: got %v, want ErrInvalidN", n, err)
				}
			}
		})
	}
}

func TestIdentityGeneratorRejected(t *testing.T) {
	for _, s := range suites() {
		t.Run(s.name, func(t *testing.T) {
			params := makeParams(s, 4, "id-gen")
			a, b := makeWitness(s, 4, "w")
			params.G[0] = s.g.Identity()
			if _, _, err := netsec.Prove(s.g, params, a, b); !errors.Is(err, netsec.ErrIdentityGenerator) {
				t.Fatalf("identity G[0]: got %v, want ErrIdentityGenerator", err)
			}
			params = makeParams(s, 4, "id-gen")
			params.U = s.g.Identity()
			if _, _, err := netsec.Prove(s.g, params, a, b); !errors.Is(err, netsec.ErrIdentityGenerator) {
				t.Fatalf("identity U: got %v, want ErrIdentityGenerator", err)
			}
		})
	}
}
