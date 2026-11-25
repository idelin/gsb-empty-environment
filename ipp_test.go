package ipp_test

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"testing"

	ipp "prescreen.local/ps-bad42ea1f6"
)

func ints(values ...int64) []*big.Int {
	out := make([]*big.Int, len(values))
	for i, value := range values {
		out[i] = big.NewInt(value)
	}
	return out
}

func modInts(t *testing.T, q *big.Int, values ...int64) []*big.Int {
	t.Helper()
	out := make([]*big.Int, len(values))
	for i, value := range values {
		out[i] = new(big.Int).Mod(big.NewInt(value), q)
		if out[i].Sign() < 0 {
			out[i].Add(out[i], q)
		}
	}
	return out
}

func requireProofOK(t *testing.T, ad ipp.Adapter, context []byte, gens ipp.Generators, p ipp.Point, proof *ipp.Proof) {
	t.Helper()
	if err := ipp.Verify(ad, context, gens, p, proof); err != nil {
		t.Fatalf("Verify failed: %v", err)
	}
	raw, err := ipp.EncodeProof(ad, proof)
	if err != nil {
		t.Fatalf("EncodeProof: %v", err)
	}
	decoded, err := ipp.DecodeProof(ad, raw)
	if err != nil {
		t.Fatalf("DecodeProof: %v", err)
	}
	if err := ipp.Verify(ad, context, gens, p, decoded); err != nil {
		t.Fatalf("Verify decoded proof: %v", err)
	}
}

func TestSmallGroupHonestProofs(t *testing.T) {
	for _, n := range []int{1, 2, 4, 8, 16, 32} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			ad := ipp.NewSmallGroup()
			gens := ipp.SmallGenerators(ad, n)
			a := make([]*big.Int, n)
			b := make([]*big.Int, n)
			for i := 0; i < n; i++ {
				a[i] = big.NewInt(int64(1 + (i*7+3)%10))
				b[i] = big.NewInt(int64(1 + (i*5+2)%10))
			}
			p, proof, err := ipp.Prove(ad, []byte("host-context"), gens, a, b)
			if err != nil {
				t.Fatalf("Prove: %v", err)
			}
			if proof.N != n || len(proof.L) != proofRounds(n) {
				t.Fatalf("bad proof shape: n=%d L=%d", proof.N, len(proof.L))
			}
			requireProofOK(t, ad, []byte("host-context"), gens, p, proof)

			challenges, err := ipp.Challenges(ad, []byte("host-context"), gens, p, proof)
			if err != nil {
				t.Fatalf("Challenges: %v", err)
			}
			if len(challenges) != proofRounds(n) {
				t.Fatalf("got %d challenges", len(challenges))
			}
			for _, x := range challenges {
				if x.Sign() == 0 || x.Cmp(ad.Order()) >= 0 {
					t.Fatalf("invalid challenge %v", x)
				}
			}
		})
	}
}

func proofRounds(n int) int {
	rounds := 0
	for n > 1 {
		n >>= 1
		rounds++
	}
	return rounds
}

func TestSmallGroupZeroWitnessIsValid(t *testing.T) {
	for _, n := range []int{1, 2, 4, 8} {
		ad := ipp.NewSmallGroup()
		gens := ipp.SmallGenerators(ad, n)
		a := make([]*big.Int, n)
		b := make([]*big.Int, n)
		for i := range a {
			a[i] = big.NewInt(0)
			b[i] = big.NewInt(0)
		}
		p, proof, err := ipp.Prove(ad, []byte("zero-context"), gens, a, b)
		if err != nil {
			t.Fatalf("Prove zero witness: %v", err)
		}
		if !bytes.Equal(p, ad.Identity()) {
			t.Fatalf("zero commitment not identity: %x", p)
		}
		for i := range proof.L {
			if !bytes.Equal(proof.L[i], ad.Identity()) || !bytes.Equal(proof.R[i], ad.Identity()) {
				t.Fatalf("round %d L/R not identity", i)
			}
		}
		if proof.A.Sign() != 0 || proof.B.Sign() != 0 {
			t.Fatalf("folded zero witness became %v,%v", proof.A, proof.B)
		}
		requireProofOK(t, ad, []byte("zero-context"), gens, p, proof)
	}
}

func TestSmallGroupSwapLR(t *testing.T) {
	ad := ipp.NewSmallGroup()
	const n = 8
	gens := ipp.SmallGenerators(ad, n)
	a := modInts(t, ad.Order(), 1, 2, 3, 4, 5, 6, 7, 8)
	b := modInts(t, ad.Order(), 8, 7, 6, 5, 4, 3, 2, 1)
	p, proof, err := ipp.Prove(ad, []byte("swap-context"), gens, a, b)
	if err != nil {
		t.Fatal(err)
	}

	swapped := *proof
	swapped.L = append([]ipp.Point(nil), proof.L...)
	swapped.R = append([]ipp.Point(nil), proof.R...)
	for i := range swapped.L {
		swapped.L[i], swapped.R[i] = swapped.R[i], swapped.L[i]
	}
	if err := ipp.Verify(ad, []byte("swap-context"), gens, p, &swapped); err == nil {
		t.Fatal("swapped L/R verified")
	}

	raw, err := ipp.EncodeProof(ad, proof)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := ipp.DecodeProof(ad, raw)
	if err != nil {
		t.Fatal(err)
	}
	decoded.L[0], decoded.R[0] = decoded.R[0], decoded.L[0]
	if err := ipp.Verify(ad, []byte("swap-context"), gens, p, decoded); err == nil {
		t.Fatal("first-round L/R swap verified after decode")
	}
}

func TestChangeNAndContextRejected(t *testing.T) {
	ad := ipp.NewSmallGroup()
	gens4 := ipp.SmallGenerators(ad, 4)
	a4 := modInts(t, ad.Order(), 1, 2, 3, 4)
	b4 := modInts(t, ad.Order(), 4, 3, 2, 1)
	p4, proof4, err := ipp.Prove(ad, []byte("ctx"), gens4, a4, b4)
	if err != nil {
		t.Fatal(err)
	}

	wrongGens := gens4
	wrongGens.G = gens4.G[:2]
	wrongGens.H = gens4.H[:2]
	if err := ipp.Verify(ad, []byte("ctx"), wrongGens, p4, proof4); err == nil {
		t.Fatal("proof for n=4 verified with n=2 generators")
	}
	if err := ipp.Verify(ad, []byte("other-context"), gens4, p4, proof4); !errors.Is(err, ipp.ErrContextMismatch) {
		t.Fatalf("context mismatch error = %v", err)
	}

	raw, err := ipp.EncodeProof(ad, proof4)
	if err != nil {
		t.Fatal(err)
	}
	changedN := bytes.Clone(raw)
	copy(changedN[4:6], []byte{0x00, 0x02})
	if _, err := ipp.DecodeProof(ad, changedN); err == nil {
		t.Fatal("changed n decoded")
	}
}

func TestZeroAndOutOfRangeScalarInjection(t *testing.T) {
	ad := ipp.NewSmallGroup()
	const n = 4
	gens := ipp.SmallGenerators(ad, n)
	a := modInts(t, ad.Order(), 2, 4, 6, 8)
	b := modInts(t, ad.Order(), 9, 7, 5, 3)
	p, proof, err := ipp.Prove(ad, []byte("scalar"), gens, a, b)
	if err != nil {
		t.Fatal(err)
	}

	zeroA := *proof
	zeroA.A = big.NewInt(0)
	err = ipp.Verify(ad, []byte("scalar"), gens, p, &zeroA)
	if !errors.Is(err, ipp.ErrZeroScalar) || !errors.Is(err, ipp.ErrVerificationFailed) {
		t.Fatal("zero final a injection verified")
	}

	if _, err := ipp.ParseNonZeroScalar([]byte{0}, ad.Order()); !errors.Is(err, ipp.ErrZeroScalar) {
		t.Fatalf("ParseNonZeroScalar zero error = %v", err)
	}
	if _, err := ipp.ParseScalar([]byte{byte(ad.Order().Int64())}, ad.Order()); !errors.Is(err, ipp.ErrInvalidScalar) {
		t.Fatalf("q scalar error = %v", err)
	}

	raw, err := ipp.EncodeProof(ad, proof)
	if err != nil {
		t.Fatal(err)
	}
	qByte := byte(ad.Order().Int64())
	scalarByteLen := (ad.Order().BitLen() + 7) / 8
	scalarOffset := len(raw) - 4 - 32 - 2*scalarByteLen
	raw[scalarOffset] = qByte
	if _, err := ipp.DecodeProof(ad, raw); !errors.Is(err, ipp.ErrInvalidScalar) {
		t.Fatalf("out-of-range injected scalar decode error = %v", err)
	}
}

func TestTruncatedProofsRejected(t *testing.T) {
	ad := ipp.NewSmallGroup()
	gens := ipp.SmallGenerators(ad, 8)
	a := modInts(t, ad.Order(), 1, 2, 3, 4, 5, 6, 7, 8)
	b := modInts(t, ad.Order(), 1, 3, 5, 7, 9, 2, 4, 6)
	_, proof, err := ipp.Prove(ad, []byte("truncate"), gens, a, b)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := ipp.EncodeProof(ad, proof)
	if err != nil {
		t.Fatal(err)
	}
	for length := 0; length < len(raw); length++ {
		if _, err := ipp.DecodeProof(ad, raw[:length]); err == nil {
			t.Fatalf("truncated proof of length %d decoded", length)
		}
	}
}

func TestWrongRoundCountRejected(t *testing.T) {
	ad := ipp.NewSmallGroup()
	gens := ipp.SmallGenerators(ad, 4)
	a := modInts(t, ad.Order(), 1, 2, 3, 4)
	b := modInts(t, ad.Order(), 4, 3, 2, 1)
	p, proof, err := ipp.Prove(ad, []byte("rounds"), gens, a, b)
	if err != nil {
		t.Fatal(err)
	}

	missing := *proof
	missing.L = proof.L[:1]
	missing.R = proof.R[:1]
	if err := ipp.Verify(ad, []byte("rounds"), gens, p, &missing); !errors.Is(err, ipp.ErrWrongRoundCount) {
		t.Fatalf("missing round error = %v", err)
	}

	extra := *proof
	extra.L = append(append([]ipp.Point(nil), proof.L...), ad.Identity())
	extra.R = append(append([]ipp.Point(nil), proof.R...), ad.Identity())
	if err := ipp.Verify(ad, []byte("rounds"), gens, p, &extra); !errors.Is(err, ipp.ErrWrongRoundCount) {
		t.Fatalf("extra round error = %v", err)
	}
}
