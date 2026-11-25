package ipp_test

import (
	"crypto/elliptic"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
	"testing"

	ipp "prescreen.local/ps-bad42ea1f6"
)

func p256Scalars(t *testing.T, seed string, n int) []*big.Int {
	t.Helper()
	q := elliptic.P256().Params().N
	out := make([]*big.Int, n)
	for i := range out {
		h := sha256.Sum256([]byte(fmt.Sprintf("%s-%d", seed, i)))
		s := new(big.Int).SetBytes(h[:])
		s.Mod(s, new(big.Int).Sub(q, big.NewInt(1)))
		out[i] = s.Add(s, big.NewInt(1))
	}
	return out
}

func TestP256HonestProofs(t *testing.T) {
	for _, n := range []int{1, 2, 4, 16} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			ad := ipp.NewP256Adapter()
			gens, err := ipp.DeterministicP256TestGenerators(n, []byte("unit-test"))
			if err != nil {
				t.Fatal(err)
			}
			a := p256Scalars(t, "a", n)
			b := p256Scalars(t, "b", n)
			p, proof, err := ipp.Prove(ad, []byte("p256-context"), gens, a, b)
			if err != nil {
				t.Fatalf("Prove: %v", err)
			}
			requireProofOK(t, ad, []byte("p256-context"), gens, p, proof)

			err = ipp.Verify(ad, []byte("wrong-context"), gens, p, proof)
			if !errors.Is(err, ipp.ErrContextMismatch) {
				t.Fatalf("wrong host context error = %v", err)
			}

			if n > 1 {
				swapped := *proof
				swapped.L = append([]ipp.Point(nil), proof.L...)
				swapped.R = append([]ipp.Point(nil), proof.R...)
				for i := range swapped.L {
					swapped.L[i], swapped.R[i] = swapped.R[i], swapped.L[i]
				}
				if err := ipp.Verify(ad, []byte("p256-context"), gens, p, &swapped); err == nil {
					t.Fatal("swapped L/R verified on P-256")
				}
			}
		})
	}
}

func TestP256MaxN1024(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping max-n P-256 algebra in short mode")
	}
	ad := ipp.NewP256Adapter()
	gens, err := ipp.DeterministicP256TestGenerators(1024, []byte("max-n"))
	if err != nil {
		t.Fatal(err)
	}
	a := p256Scalars(t, "max-a", 1024)
	b := p256Scalars(t, "max-b", 1024)
	p, proof, err := ipp.Prove(ad, []byte("max-context"), gens, a, b)
	if err != nil {
		t.Fatalf("Prove n=1024: %v", err)
	}
	if len(proof.L) != 10 || len(proof.R) != 10 {
		t.Fatalf("rounds = %d/%d", len(proof.L), len(proof.R))
	}
	requireProofOK(t, ad, []byte("max-context"), gens, p, proof)
}

func TestP256CanonicalEncodingAndGeneratorChecks(t *testing.T) {
	ad := ipp.NewP256Adapter()
	gens, err := ipp.DeterministicP256TestGenerators(2, []byte("encoding"))
	if err != nil {
		t.Fatal(err)
	}

	x, y := elliptic.P256().Params().Gx, elliptic.P256().Params().Gy
	uncompressed := elliptic.Marshal(elliptic.P256(), x, y)
	if _, err := ad.ParsePoint(uncompressed); !errors.Is(err, ipp.ErrNonCanonicalPoint) {
		t.Fatalf("uncompressed point error = %v", err)
	}

	bad := make([]byte, 33)
	bad[0] = 0x02
	for i := 1; i < 33; i++ {
		bad[i] = 0xff
	}
	if _, err := ad.ParsePoint(bad); !errors.Is(err, ipp.ErrInvalidPoint) && !errors.Is(err, ipp.ErrNonCanonicalPoint) {
		t.Fatalf("invalid curve point error = %v", err)
	}

	id := ad.Identity()
	parsedID, err := ad.ParsePoint(id)
	if err != nil {
		t.Fatalf("canonical identity: %v", err)
	}
	if string(parsedID) != string(id) {
		t.Fatal("identity encoding changed")
	}

	badGens := gens
	badGens.G = append([]ipp.Point(nil), gens.G...)
	badGens.G[0] = ad.Identity()
	a := p256Scalars(t, "a", 2)
	b := p256Scalars(t, "b", 2)
	if _, _, err := ipp.Prove(ad, nil, badGens, a, b); !errors.Is(err, ipp.ErrIdentityGenerator) {
		t.Fatalf("identity G prove error = %v", err)
	}
	p, proof, err := ipp.Prove(ad, nil, gens, a, b)
	if err != nil {
		t.Fatal(err)
	}
	if err := ipp.Verify(ad, nil, badGens, p, proof); !errors.Is(err, ipp.ErrIdentityGenerator) {
		t.Fatalf("identity G verify error = %v", err)
	}
}

func TestP256ZeroWitnessUsesIdentityPoints(t *testing.T) {
	ad := ipp.NewP256Adapter()
	gens, err := ipp.DeterministicP256TestGenerators(4, []byte("zero"))
	if err != nil {
		t.Fatal(err)
	}
	a := make([]*big.Int, 4)
	b := make([]*big.Int, 4)
	for i := range a {
		a[i] = big.NewInt(0)
		b[i] = big.NewInt(0)
	}
	p, proof, err := ipp.Prove(ad, []byte("zero-p256"), gens, a, b)
	if err != nil {
		t.Fatal(err)
	}
	if !bytesEqual(p, ad.Identity()) {
		t.Fatal("zero witness commitment is not canonical identity")
	}
	requireProofOK(t, ad, []byte("zero-p256"), gens, p, proof)
}

func bytesEqual(a, b []byte) bool {
	return string(a) == string(b)
}
