package netsec

import (
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"testing"
	"time"
)

func newTestKey(t *testing.T) (*rsa.PrivateKey, DNSKEY) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	key := DNSKEY{
		Flags:     FlagZoneKey,
		Protocol:  3,
		Algorithm: AlgRSASHA256,
		PublicKey: &priv.PublicKey,
	}
	return priv, key
}

func fixedValidity(now time.Time) (uint32, uint32) {
	return uint32(now.Add(-time.Hour).Unix()), uint32(now.Add(time.Hour).Unix())
}

// signWildcard builds a valid RRSIG for an RRset that was produced by
// expanding wildcard "*.<encloser>" into expandedOwner.
func signWildcard(t *testing.T, priv *rsa.PrivateKey, key DNSKEY, expandedOwner, encloser Name, rrs []RR) SignedRRset {
	t.Helper()
	wildcard := encloser.PrependWildcard()
	now := time.Now()
	inception, expiration := fixedValidity(now)
	keyTag, err := key.KeyTag()
	if err != nil {
		t.Fatalf("key tag: %v", err)
	}

	sig := RRSIG{
		TypeCovered:   TypeA,
		Algorithm:     AlgRSASHA256,
		Labels:        uint8(wildcard.LabelCount()),
		OriginalTTL:   3600,
		SigExpiration: expiration,
		SigInception:  inception,
		KeyTag:        keyTag,
		SignerName:    MustParseName("example.com."),
	}
	rrset := SignedRRset{RRs: rrs, Signature: sig, Now: now}
	signature, err := Sign(priv, rrset, wildcard)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	rrset.Signature.Signature = signature
	return rrset
}

func nsecProof(t *testing.T, encloser Name) *NSECProof {
	t.Helper()
	wildcard := encloser.PrependWildcard()
	// One NSEC owned by the closest encloser; its interval covers the
	// next-closer wildcard name and loops back inside the zone.
	encloserNSEC := NSEC{
		OwnerName:      encloser,
		NextDomainName: MustParseName("zzz.example.com."),
		Types:          []uint16{TypeRRSIG, TypeNSEC},
	}
	// A second record establishes a wrapping interval so canonical ordering is
	// exercised; it does not itself need to cover the wildcard.
	lastNSEC := NSEC{
		OwnerName:      MustParseName("zzz.example.com."),
		NextDomainName: MustParseName("example.com."),
		Types:          []uint16{TypeNS, TypeSOA, TypeRRSIG, TypeNSEC},
	}
	if !intervalContains(encloserNSEC, wildcard) {
		t.Fatalf("test fixture: interval does not cover %s", wildcard)
	}
	return &NSECProof{Records: []NSEC{encloserNSEC, lastNSEC}}
}

func verifierWith(key DNSKEY) Verifier {
	tag, _ := key.KeyTag()
	return VerifierFunc(func(signer Name, keyTag uint16, algorithm uint8) (DNSKEY, bool) {
		if keyTag == tag && algorithm == key.Algorithm && signer.Equal(keySigner) {
			return key, true
		}
		return DNSKEY{}, false
	})
}

var keySigner = MustParseName("example.com.")

func TestClassifyExactMatch(t *testing.T) {
	owner := MustParseName("host.example.com.")
	cls, err := Classify(owner, uint8(owner.LabelCount()))
	if err != nil {
		t.Fatalf("exact match: %v", err)
	}
	if cls.Wildcard {
		t.Fatal("exact match misclassified as wildcard")
	}
	if !cls.SignedName.Equal(owner) {
		t.Fatalf("signed name = %s, want %s", cls.SignedName, owner)
	}
}

func TestClassifyExactRejectsLeadingStar(t *testing.T) {
	owner := MustParseName("*.example.com.")
	// The wildcard owner has an effective count of 2 (the "*" is not counted);
	// Labels == 2 with a leading '*' owner is an impossible exact-match form.
	_, err := Classify(owner, 2)
	if !errors.Is(err, ErrWildcardOnExactName) {
		t.Fatalf("want ErrWildcardOnExactName, got %v", err)
	}
}

func TestClassifyLabelsExceedOwner(t *testing.T) {
	owner := MustParseName("host.example.com.")
	_, err := Classify(owner, uint8(owner.LabelCount()+1))
	if !errors.Is(err, ErrLabelsExceedOwner) {
		t.Fatalf("want ErrLabelsExceedOwner, got %v", err)
	}
}

func TestClassifySingleLabelWildcardExpansion(t *testing.T) {
	// A one-label expansion where the owner carries an extra concrete label:
	// *.example.com (Labels=3 including "*") expands to deep.host.example.com,
	// whose effective count is 4, so Labels(3) < owner(4) flags an expansion.
	encloser := MustParseName("example.com.")
	owner := MustParseName("deep.host.example.com.")
	wildcard := encloser.PrependWildcard() // *.example.com, Labels = 3

	cls, err := Classify(owner, uint8(wildcard.LabelCount()))
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if !cls.Wildcard {
		t.Fatal("expected wildcard classification")
	}
	if !cls.ClosestEncloser.Equal(encloser) {
		t.Fatalf("encloser = %s, want %s", cls.ClosestEncloser, encloser)
	}
	if !cls.SignedName.Equal(wildcard) {
		t.Fatalf("signed name = %s, want %s", cls.SignedName, wildcard)
	}
}

func TestClassifyMultiLabelWildcardExpansion(t *testing.T) {
	// *.sub.example.com expands to a.b.sub.example.com.
	encloser := MustParseName("sub.example.com.")
	owner := MustParseName("a.b.sub.example.com.")
	wildcard := encloser.PrependWildcard() // *.sub.example.com -> 4 labels

	cls, err := Classify(owner, uint8(wildcard.LabelCount()))
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if !cls.Wildcard {
		t.Fatal("expected wildcard classification")
	}
	if !cls.ClosestEncloser.Equal(encloser) {
		t.Fatalf("encloser = %s, want %s", cls.ClosestEncloser, encloser)
	}
	if !cls.SignedName.Equal(wildcard) {
		t.Fatalf("signed name = %s, want %s", cls.SignedName, wildcard)
	}
}
