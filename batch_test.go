package ipp_test

import (
	"crypto/rand"
	"math/big"
	"testing"

	ipp "prescreen.local/ps-bad42ea1f6"
)

func TestP256BatchVerification(t *testing.T) {
	ad := ipp.NewP256Adapter()
	items := make([]ipp.BatchItem, 0, 3)
	for i, n := range []int{2, 4, 8} {
		gens, err := ipp.DeterministicP256TestGenerators(n, []byte("batch"))
		if err != nil {
			t.Fatal(err)
		}
		a := p256Scalars(t, "batch-a", n)
		b := p256Scalars(t, "batch-b", n)
		context := []byte{byte('c'), byte('0' + i)}
		p, proof, err := ipp.Prove(ad, context, gens, a, b)
		if err != nil {
			t.Fatal(err)
		}
		items = append(items, ipp.BatchItem{
			Context:    context,
			Generators: gens,
			Commitment: p,
			Proof:      proof,
		})
	}

	if err := ipp.VerifyBatch(ad, items); err != nil {
		t.Fatalf("VerifyBatch: %v", err)
	}
	if err := ipp.VerifyLinearBatch(ad, rand.Reader, items); err != nil {
		t.Fatalf("VerifyLinearBatch: %v", err)
	}

	badItems := append([]ipp.BatchItem(nil), items...)
	badProof := *badItems[1].Proof
	badProof.A = new(big.Int).Add(badProof.A, big.NewInt(1))
	badItems[1].Proof = &badProof
	if err := ipp.VerifyBatch(ad, badItems); err == nil {
		t.Fatal("sequential batch accepted tampered proof")
	}
	if err := ipp.VerifyLinearBatch(ad, rand.Reader, badItems); err == nil {
		t.Fatal("linear batch accepted tampered proof")
	}
}

func TestSmallBatchRejectsWrongContext(t *testing.T) {
	ad := ipp.NewSmallGroup()
	gens := ipp.SmallGenerators(ad, 4)
	a := modInts(t, ad.Order(), 1, 2, 3, 4)
	b := modInts(t, ad.Order(), 4, 3, 2, 1)
	p, proof, err := ipp.Prove(ad, []byte("batch-ctx"), gens, a, b)
	if err != nil {
		t.Fatal(err)
	}
	item := ipp.BatchItem{Context: []byte("wrong"), Generators: gens, Commitment: p, Proof: proof}
	if err := ipp.BatchVerify(ad, item); err == nil {
		t.Fatal("batch accepted context mismatch")
	}
}
