package netsec

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"testing"
	"time"
)

func ecKey(t *testing.T, curve elliptic.Curve, alg uint8) (*ecdsa.PrivateKey, DNSKEY) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		t.Fatalf("generate ecdsa key: %v", err)
	}
	return priv, DNSKEY{Flags: FlagZoneKey, Protocol: 3, Algorithm: alg, PublicKey: &priv.PublicKey}
}

func signGeneric(t *testing.T, alg uint8, priv any, key DNSKEY, owner, encloser Name) SignedRRset {
	t.Helper()
	wildcard := encloser.PrependWildcard()
	now := time.Now()
	inception, expiration := fixedValidity(now)
	keyTag, err := key.KeyTag()
	if err != nil {
		t.Fatalf("key tag: %v", err)
	}
	sig := RRSIG{
		TypeCovered: TypeA, Algorithm: alg,
		Labels: uint8(wildcard.LabelCount()), OriginalTTL: 3600,
		SigExpiration: expiration, SigInception: inception, KeyTag: keyTag,
		SignerName: keySigner,
	}
	rrset := SignedRRset{
		RRs:       []RR{aRR(owner, addr(203, 0, 113, 9))},
		Signature: sig, Now: now,
		Proof: nsecProof(t, encloser),
	}
	signature, err := Sign(priv, rrset, wildcard)
	if err != nil {
		t.Fatalf("sign alg %d: %v", alg, err)
	}
	rrset.Signature.Signature = signature
	return rrset
}

func TestVerifyECDSAP256Wildcard(t *testing.T) {
	priv, key := ecKey(t, elliptic.P256(), AlgECDSAP256)
	encloser := MustParseName("sub.example.com.")
	owner := MustParseName("a.b.sub.example.com.")
	rrset := signGeneric(t, AlgECDSAP256, priv, key, owner, encloser)

	res, err := Verify(verifierWith(key), rrset)
	if err != nil {
		t.Fatalf("ecdsa verify: %v", err)
	}
	if !res.Classification.Wildcard {
		t.Fatal("expected wildcard")
	}
}

func TestVerifyECDSAP384Wildcard(t *testing.T) {
	priv, key := ecKey(t, elliptic.P384(), AlgECDSAP384)
	encloser := MustParseName("sub.example.com.")
	owner := MustParseName("a.b.sub.example.com.")
	rrset := signGeneric(t, AlgECDSAP384, priv, key, owner, encloser)

	if _, err := Verify(verifierWith(key), rrset); err != nil {
		t.Fatalf("ecdsa p384 verify: %v", err)
	}
}

func TestVerifyEd25519Wildcard(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519: %v", err)
	}
	key := DNSKEY{Flags: FlagZoneKey, Protocol: 3, Algorithm: AlgEd25519, PublicKey: pub}
	encloser := MustParseName("sub.example.com.")
	owner := MustParseName("a.b.sub.example.com.")
	rrset := signGeneric(t, AlgEd25519, priv, key, owner, encloser)

	if _, err := Verify(verifierWith(key), rrset); err != nil {
		t.Fatalf("ed25519 verify: %v", err)
	}
}

func TestVerifyUnsupportedAlgorithmRejected(t *testing.T) {
	priv, key := newTestKey(t)
	encloser := MustParseName("sub.example.com.")
	owner := MustParseName("a.b.sub.example.com.")
	rrset := signWildcard(t, priv, key, owner, encloser, []RR{aRR(owner, addr(203, 0, 113, 7))})
	rrset.Proof = nsecProof(t, encloser)
	rrset.Signature.Algorithm = 99
	badKey := DNSKEY{
		Flags: FlagZoneKey, Protocol: 3, Algorithm: 99, PublicKey: key.PublicKey,
	}
	v := VerifierFunc(func(signer Name, keyTag uint16, algorithm uint8) (DNSKEY, bool) {
		if algorithm == 99 && signer.Equal(keySigner) {
			return badKey, true
		}
		return DNSKEY{}, false
	})

	_, err := Verify(v, rrset)
	if !errors.Is(err, ErrUnsupportedAlgorithm) {
		t.Fatalf("want ErrUnsupportedAlgorithm, got %v", err)
	}
}

func TestVerifyNotYetValidRejected(t *testing.T) {
	priv, key := newTestKey(t)
	encloser := MustParseName("sub.example.com.")
	owner := MustParseName("a.b.sub.example.com.")
	rrset := signWildcard(t, priv, key, owner, encloser, []RR{aRR(owner, addr(203, 0, 113, 7))})
	rrset.Proof = nsecProof(t, encloser)
	now := time.Now()
	rrset.Now = now
	rrset.Signature.SigInception = uint32(now.Add(time.Hour).Unix())

	_, err := Verify(verifierWith(key), rrset)
	if !errors.Is(err, ErrSignatureNotValidYet) {
		t.Fatalf("want ErrSignatureNotValidYet, got %v", err)
	}
}
