package netsec

import (
	"errors"
	"math/big"
	"testing"
)

func TestGroupPrimeSanity(t *testing.T) {
	for _, id := range []DHGroupID{DHGroup1024, DHGroup2048} {
		g, err := Group(id)
		if err != nil {
			t.Fatalf("group %v not provisioned: %v", id, err)
		}
		wantBits := map[DHGroupID]int{DHGroup1024: 1024, DHGroup2048: 2048}[id]
		if g.BitLen() != wantBits {
			t.Fatalf("%v: got %d bits, want %d", id, g.BitLen(), wantBits)
		}
		if !g.P.ProbablyPrime(20) {
			t.Fatalf("%v: hard-coded prime failed primality check", id)
		}
		if g.G.Cmp(big.NewInt(2)) != 0 {
			t.Fatalf("%v: generator should be 2", id)
		}
	}

	if _, err := Group(DHGroupNull); err == nil {
		t.Fatal("null DH must have no MODP parameters")
	}
	if _, err := Group(DHGroup3072); err == nil {
		t.Fatal("unprovisioned group must error")
	}
}

func TestValidatePublicValueSmallSubgroup(t *testing.T) {
	g, err := Group(DHGroup2048)
	if err != nil {
		t.Fatal(err)
	}
	pMinus1 := new(big.Int).Sub(g.P, big.NewInt(1))
	pMinus2 := new(big.Int).Sub(g.P, big.NewInt(2))

	invalid := []*big.Int{
		nil,
		big.NewInt(0),                        // 0
		big.NewInt(1),                        // 1
		pMinus1,                              // p-1
		new(big.Int).Set(g.P),                // p (outside modulus)
		new(big.Int).Add(g.P, big.NewInt(5)), // above modulus
	}
	for _, y := range invalid {
		if err := g.ValidatePublicValue(y); !errors.Is(err, ErrInvalidDHPublicValue) {
			t.Fatalf("value %v should be rejected, got %v", y, err)
		}
	}

	valid := []*big.Int{
		big.NewInt(2), // lower bound
		pMinus2,       // upper bound
		big.NewInt(123456789),
	}
	for _, y := range valid {
		if err := g.ValidatePublicValue(y); err != nil {
			t.Fatalf("value %v should be accepted, got %v", y, err)
		}
	}
}

func TestComputeSharedSecretRejectsSubgroup(t *testing.T) {
	g, err := Group(DHGroup1024)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := g.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []*big.Int{big.NewInt(0), big.NewInt(1), new(big.Int).Sub(g.P, big.NewInt(1))} {
		if _, err := g.ComputeSharedSecret(keys.Private, bad); !errors.Is(err, ErrInvalidDHPublicValue) {
			t.Fatalf("expected subgroup rejection for %v, got %v", bad, err)
		}
	}
}

func TestDHAgreementSymmetry(t *testing.T) {
	g, err := Group(DHGroup2048)
	if err != nil {
		t.Fatal(err)
	}
	alice, err := g.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	bob, err := g.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	// Self-consistency: generated keys are in the accepted range.
	if err := g.ValidatePublicValue(alice.Public); err != nil {
		t.Fatal(err)
	}
	if err := g.ValidatePublicValue(bob.Public); err != nil {
		t.Fatal(err)
	}

	s1, err := g.ComputeSharedSecret(alice.Private, bob.Public)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := g.ComputeSharedSecret(bob.Private, alice.Public)
	if err != nil {
		t.Fatal(err)
	}
	if s1.Cmp(s2) != 0 {
		t.Fatal("shared secrets do not match")
	}
}

func TestGeneratedPrivateInRange(t *testing.T) {
	g, err := Group(DHGroup1024)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		keys, err := g.GenerateKeyPair()
		if err != nil {
			t.Fatal(err)
		}
		upper := new(big.Int).Sub(g.P, big.NewInt(2))
		if keys.Private.Cmp(big.NewInt(2)) < 0 || keys.Private.Cmp(upper) > 0 {
			t.Fatalf("private exponent %v outside [2, p-2]", keys.Private)
		}
	}
}
