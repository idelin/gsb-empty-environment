package netsec

import (
	"errors"
	"testing"
	"time"
)

func aRR(name Name, addr [4]byte) RR {
	return RR{Name: name, Type: TypeA, Class: ClassIN, TTL: 3600, A: addr}
}

func addr(b ...byte) [4]byte {
	var a [4]byte
	copy(a[:], b)
	return a
}

// TestVerifyMultiLabelExpansion is the end-to-end happy path: a wildcard
// signature rebuilt as "*.sub.example.com" validates the expanded owner
// "a.b.sub.example.com" once the NSEC denial of the next-closer name is present.
func TestVerifyMultiLabelExpansion(t *testing.T) {
	priv, key := newTestKey(t)
	encloser := MustParseName("sub.example.com.")
	owner := MustParseName("a.b.sub.example.com.")

	rrs := []RR{
		aRR(owner, addr(203, 0, 113, 7)),
		aRR(owner, addr(203, 0, 113, 8)),
	}
	rrset := signWildcard(t, priv, key, owner, encloser, rrs)
	rrset.Proof = nsecProof(t, encloser)

	res, err := Verify(verifierWith(key), rrset)
	if err != nil {
		t.Fatalf("verify wildcard expansion: %v", err)
	}
	if !res.Classification.Wildcard {
		t.Fatal("expected wildcard result")
	}
	wantNext := MustParseName("*.sub.example.com.")
	if !res.NextCloser.Equal(wantNext) {
		t.Fatalf("next closer = %s, want %s", res.NextCloser, wantNext)
	}
	if !res.Classification.SignedName.Equal(wantNext) {
		t.Fatalf("signed name = %s, want %s", res.Classification.SignedName, wantNext)
	}
}

func TestVerifySingleLabelExpansion(t *testing.T) {
	priv, key := newTestKey(t)
	encloser := MustParseName("example.com.")
	owner := MustParseName("deep.host.example.com.")

	rrset := signWildcard(t, priv, key, owner, encloser, []RR{aRR(owner, addr(192, 0, 2, 1))})
	rrset.Proof = nsecProof(t, encloser)

	if _, err := Verify(verifierWith(key), rrset); err != nil {
		t.Fatalf("verify single-label expansion: %v", err)
	}
}

func TestVerifyMissingProofRejected(t *testing.T) {
	priv, key := newTestKey(t)
	encloser := MustParseName("sub.example.com.")
	owner := MustParseName("a.b.sub.example.com.")

	rrset := signWildcard(t, priv, key, owner, encloser, []RR{aRR(owner, addr(203, 0, 113, 7))})
	// Deliberately leave Proof nil.

	_, err := Verify(verifierWith(key), rrset)
	if !errors.Is(err, ErrProofMissing) {
		t.Fatalf("want ErrProofMissing, got %v", err)
	}
}

func TestVerifyEmptyProofRejected(t *testing.T) {
	priv, key := newTestKey(t)
	encloser := MustParseName("sub.example.com.")
	owner := MustParseName("a.b.sub.example.com.")

	rrset := signWildcard(t, priv, key, owner, encloser, []RR{aRR(owner, addr(203, 0, 113, 7))})
	rrset.Proof = &NSECProof{}

	_, err := Verify(verifierWith(key), rrset)
	if !errors.Is(err, ErrProofMissing) {
		t.Fatalf("want ErrProofMissing for empty proof, got %v", err)
	}
}

func TestVerifyForgedOutOfRangeLabels(t *testing.T) {
	priv, key := newTestKey(t)
	encloser := MustParseName("example.com.")
	owner := MustParseName("host.example.com.") // 3 labels

	rrset := signWildcard(t, priv, key, owner, encloser, []RR{aRR(owner, addr(192, 0, 2, 1))})
	// Inflate Labels beyond the owner label count: classic forgery attempt.
	rrset.Signature.Labels = uint8(owner.LabelCount() + 1)
	rrset.Proof = nsecProof(t, encloser)

	_, err := Verify(verifierWith(key), rrset)
	if !errors.Is(err, ErrLabelsExceedOwner) {
		t.Fatalf("want ErrLabelsExceedOwner, got %v", err)
	}
}

func TestVerifyProofWithoutEncloserNSECRejected(t *testing.T) {
	priv, key := newTestKey(t)
	encloser := MustParseName("sub.example.com.")
	owner := MustParseName("a.b.sub.example.com.")

	rrset := signWildcard(t, priv, key, owner, encloser, []RR{aRR(owner, addr(203, 0, 113, 7))})

	// NSEC records exist but none is owned by the closest encloser, so the
	// wildcard denial cannot be authenticated.
	rrset.Proof = &NSECProof{Records: []NSEC{{
		OwnerName:      MustParseName("other.example.com."),
		NextDomainName: MustParseName("zzz.example.com."),
		Types:          []uint16{TypeRRSIG, TypeNSEC},
	}}}

	_, err := Verify(verifierWith(key), rrset)
	if !errors.Is(err, ErrNoClosestEncloserNSEC) {
		t.Fatalf("want ErrNoClosestEncloserNSEC, got %v", err)
	}
}

func TestVerifyExactMatchRoundTrip(t *testing.T) {
	priv, key := newTestKey(t)
	owner := MustParseName("host.example.com.")
	now := time.Now()
	inception, expiration := fixedValidity(now)
	keyTag, _ := key.KeyTag()

	sig := RRSIG{
		TypeCovered:   TypeA,
		Algorithm:     AlgRSASHA256,
		Labels:        uint8(owner.LabelCount()),
		OriginalTTL:   3600,
		SigExpiration: expiration,
		SigInception:  inception,
		KeyTag:        keyTag,
		SignerName:    keySigner,
	}
	rrset := SignedRRset{
		RRs:       []RR{aRR(owner, addr(198, 51, 100, 4))},
		Signature: sig,
		Now:       now,
	}
	signature, err := Sign(priv, rrset, owner)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	rrset.Signature.Signature = signature

	res, err := Verify(verifierWith(key), rrset)
	if err != nil {
		t.Fatalf("verify exact: %v", err)
	}
	if res.Classification.Wildcard {
		t.Fatal("exact match must not be wildcard")
	}
}

func TestVerifyTamperedSignatureRejected(t *testing.T) {
	priv, key := newTestKey(t)
	encloser := MustParseName("sub.example.com.")
	owner := MustParseName("a.b.sub.example.com.")

	rrset := signWildcard(t, priv, key, owner, encloser, []RR{aRR(owner, addr(203, 0, 113, 7))})
	rrset.Proof = nsecProof(t, encloser)

	// Flip a byte in the RDATA so the rebuilt signed data no longer matches.
	rrset.RRs[0].A[0] ^= 0xff

	if _, err := Verify(verifierWith(key), rrset); err == nil {
		t.Fatal("expected signature verification to fail after tampering")
	}
}

func TestVerifyUnexpectedProofOnExactMatch(t *testing.T) {
	priv, key := newTestKey(t)
	owner := MustParseName("host.example.com.")
	now := time.Now()
	inception, expiration := fixedValidity(now)
	keyTag, _ := key.KeyTag()

	sig := RRSIG{
		TypeCovered: TypeA, Algorithm: AlgRSASHA256,
		Labels: uint8(owner.LabelCount()), OriginalTTL: 3600,
		SigExpiration: expiration, SigInception: inception, KeyTag: keyTag, SignerName: keySigner,
	}
	rrset := SignedRRset{
		RRs:       []RR{aRR(owner, addr(198, 51, 100, 4))},
		Signature: sig, Now: now,
		Proof: &NSECProof{Records: []NSEC{{OwnerName: owner, NextDomainName: owner}}},
	}
	signature, err := Sign(priv, rrset, owner)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	rrset.Signature.Signature = signature

	_, err = Verify(verifierWith(key), rrset)
	if !errors.Is(err, ErrUnexpectedProof) {
		t.Fatalf("want ErrUnexpectedProof, got %v", err)
	}
}

func TestVerifyExpiredSignatureRejected(t *testing.T) {
	priv, key := newTestKey(t)
	encloser := MustParseName("sub.example.com.")
	owner := MustParseName("a.b.sub.example.com.")

	rrset := signWildcard(t, priv, key, owner, encloser, []RR{aRR(owner, addr(203, 0, 113, 7))})
	rrset.Proof = nsecProof(t, encloser)
	now := time.Now()
	rrset.Now = now
	rrset.Signature.SigExpiration = uint32(now.Add(-time.Second).Unix())

	_, err := Verify(verifierWith(key), rrset)
	if !errors.Is(err, ErrSignatureExpired) {
		t.Fatalf("want ErrSignatureExpired, got %v", err)
	}
}
