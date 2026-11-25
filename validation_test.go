package ipp_test

import (
	"errors"
	"math/big"
	"testing"

	ipp "prescreen.local/ps-bad42ea1f6"
)

func TestInvalidNAndLengths(t *testing.T) {
	ad := ipp.NewSmallGroup()
	for _, n := range []int{0, 3, 5, 1023, 2048} {
		a := make([]*big.Int, n)
		b := make([]*big.Int, n)
		for i := range a {
			a[i] = big.NewInt(1)
			b[i] = big.NewInt(1)
		}
		gens := ipp.SmallGenerators(ad, max1(n))
		_, _, err := ipp.Prove(ad, nil, gens, a, b)
		if !errors.Is(err, ipp.ErrN) && !errors.Is(err, ipp.ErrGeneratorLength) {
			t.Fatalf("n=%d Prove error = %v", n, err)
		}
	}

	gens := ipp.SmallGenerators(ad, 4)
	a := []*big.Int{big.NewInt(1), big.NewInt(2)}
	b := []*big.Int{big.NewInt(1), big.NewInt(2)}
	if _, err := ipp.Commit(ad, gens, a, b); err == nil {
		t.Fatal("length mismatch commitment accepted")
	}
}

func max1(n int) int {
	if n <= 0 {
		return 1
	}
	if n > 32 {
		return 32
	}
	return n
}

func TestTranscriptMutationChangesChallenge(t *testing.T) {
	ad := ipp.NewSmallGroup()
	gens := ipp.SmallGenerators(ad, 8)
	a := modInts(t, ad.Order(), 1, 2, 3, 4, 5, 6, 7, 8)
	b := modInts(t, ad.Order(), 8, 7, 6, 5, 4, 3, 2, 1)
	p, proof, err := ipp.Prove(ad, []byte("transcript"), gens, a, b)
	if err != nil {
		t.Fatal(err)
	}
	before, err := ipp.Challenges(ad, []byte("transcript"), gens, p, proof)
	if err != nil {
		t.Fatal(err)
	}

	mutated := *proof
	mutated.L = append([]ipp.Point(nil), proof.L...)
	mutated.R = append([]ipp.Point(nil), proof.R...)
	nextPoint := byte((int(proof.L[0][0]) + 1) % int(ad.Order().Int64()))
	mutated.L[0] = ipp.Point{nextPoint}
	after, err := ipp.Challenges(ad, []byte("transcript"), gens, p, &mutated)
	if err != nil {
		t.Fatal(err)
	}
	if before[0].Cmp(after[0]) == 0 {
		t.Fatal("first challenge did not change after L[0] mutation")
	}
	if err := ipp.Verify(ad, []byte("transcript"), gens, p, &mutated); err == nil {
		t.Fatal("mutated L[0] verified")
	}
}

func TestCanonicalScalarRejection(t *testing.T) {
	q := big.NewInt(11)
	if _, err := ipp.ParseScalar([]byte{}, q); !errors.Is(err, ipp.ErrInvalidScalar) {
		t.Fatalf("empty scalar error = %v", err)
	}
	if _, err := ipp.ParseScalar([]byte{0, 1}, q); !errors.Is(err, ipp.ErrInvalidScalar) {
		t.Fatalf("wrong-width scalar error = %v", err)
	}
	if s, err := ipp.ParseScalar([]byte{0}, q); err != nil || s.Sign() != 0 {
		t.Fatalf("canonical zero scalar: %v %v", s, err)
	}
	if _, err := ipp.ParseNonZeroScalar([]byte{0}, q); !errors.Is(err, ipp.ErrZeroScalar) {
		t.Fatalf("non-zero parser zero error = %v", err)
	}
}
