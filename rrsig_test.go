package dnssec

import (
	"bytes"
	"testing"
	"time"
)

func TestVerifyRRSIGECDSA(t *testing.T) {
	signer := newTestSigner(t, "example.com.")
	owner := MustParseName("www.example.com.")
	rrset := []RR{
		{Name: owner, Type: TypeA, Class: ClassINET, TTL: 300, Data: makeA(3)},
		{Name: owner, Type: TypeA, Class: ClassINET, TTL: 300, Data: makeA(1)},
		{Name: owner, Type: TypeA, Class: ClassINET, TTL: 300, Data: makeA(2)},
	}
	sig := signer.sign(t, rrset, uint8(owner.LabelCount()))
	if err := VerifyRRSIG(rrset, sig, signer.key, signer.now, signer.zone); err != nil {
		t.Fatalf("valid RRSIG rejected: %v", err)
	}

	tampered := append([]RR(nil), rrset...)
	tampered[1] = RR{Name: owner, Type: TypeA, Class: ClassINET, TTL: 300, Data: makeA(9)}
	if err := VerifyRRSIG(tampered, sig, signer.key, signer.now, signer.zone); err == nil {
		t.Fatal("tampered RRset accepted")
	}

	other := newTestSigner(t, "example.com.")
	if err := VerifyRRSIG(rrset, sig, other.key, signer.now, signer.zone); err == nil {
		t.Fatal("signature verified with wrong DNSKEY")
	}

	sig.KeyTag++
	if err := VerifyRRSIG(rrset, sig, signer.key, signer.now, signer.zone); err == nil {
		t.Fatal("mismatched key tag accepted")
	}
}

func TestRRSIGTimeWindow(t *testing.T) {
	signer := newTestSigner(t, "example.com.")
	owner := MustParseName("www.example.com.")
	rrset := []RR{{Name: owner, Type: TypeA, Class: ClassINET, TTL: 300, Data: makeA(1)}}
	sig := signer.sign(t, rrset, uint8(owner.LabelCount()))

	tooEarly := time.Unix(int64(sig.Inception)-1, 0)
	if err := VerifyRRSIG(rrset, sig, signer.key, tooEarly, signer.zone); err == nil {
		t.Fatal("inception window was not enforced")
	}
	tooLate := time.Unix(int64(sig.Expiration)+1, 0)
	if err := VerifyRRSIG(rrset, sig, signer.key, tooLate, signer.zone); err == nil {
		t.Fatal("expiration window was not enforced")
	}
}

func TestRRSIGSignerBelongsToCurrentZone(t *testing.T) {
	signer := newTestSigner(t, "example.com.")
	owner := MustParseName("www.example.com.")
	rrset := []RR{{Name: owner, Type: TypeA, Class: ClassINET, TTL: 300, Data: makeA(1)}}
	sig := signer.sign(t, rrset, uint8(owner.LabelCount()))

	if err := VerifyRRSIG(rrset, sig, signer.key, signer.now, MustParseName("www.example.com.")); err == nil {
		t.Fatal("signer outside the supplied current zone was accepted")
	}
	if err := VerifyRRSIG(rrset, sig, signer.key, signer.now, MustParseName("other.example.")); err == nil {
		t.Fatal("cross-zone signature was accepted")
	}
}

func TestWildcardLabelsReconstruction(t *testing.T) {
	signer := newTestSigner(t, "example.com.")
	qname := MustParseName("anything.example.com.")
	rrset := []RR{{Name: qname, Type: TypeA, Class: ClassINET, TTL: 300, Data: makeA(1)}}
	sig := signer.sign(t, rrset, uint8(signer.zone.LabelCount()))
	owner, err := sig.SignedOwner(qname)
	if err != nil {
		t.Fatal(err)
	}
	if !EqualNames(owner, MustParseName("*.example.com.")) {
		t.Fatalf("reconstructed owner = %s", owner)
	}
	if !sig.IsWildcardExpansion(qname) {
		t.Fatal("wildcard expansion was not detected")
	}
	if err := VerifyRRSIG(rrset, sig, signer.key, signer.now, signer.zone); err != nil {
		t.Fatalf("wildcard-expanded signature rejected: %v", err)
	}

	bad := sig
	bad.Labels = uint8(qname.LabelCount())
	if _, err := DataToSign(rrset, bad); err != nil {
		t.Fatalf("labels equal to owner count should describe an exact owner: %v", err)
	}

	bad.Labels = uint8(qname.LabelCount() + 1)
	if _, err := DataToSign(rrset, bad); err == nil {
		t.Fatal("labels exceeding owner label count were accepted")
	}
}

func TestRRSIGCanonicalRRsetOrdering(t *testing.T) {
	signer := newTestSigner(t, "example.com.")
	owner := MustParseName("multi.example.com.")
	rrset := []RR{
		{Name: owner, Type: TypeA, Class: ClassINET, TTL: 300, Data: makeA(4)},
		{Name: owner, Type: TypeA, Class: ClassINET, TTL: 300, Data: makeA(2)},
	}
	sig := signer.sign(t, rrset, uint8(owner.LabelCount()))
	reordered := []RR{rrset[1], rrset[0]}
	if err := VerifyRRSIG(reordered, sig, signer.key, signer.now, signer.zone); err != nil {
		t.Fatalf("canonical RRset ordering was not applied: %v", err)
	}

	data1, err := DataToSign(rrset, sig)
	if err != nil {
		t.Fatal(err)
	}
	data2, err := DataToSign(reordered, sig)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data1, data2) {
		t.Fatal("RRset ordering changed signed data")
	}
}

func TestParseDNSKEYAndRRSIGWireData(t *testing.T) {
	signer := newTestSigner(t, "example.com.")
	parsedKey, err := ParseDNSKEY(signer.key.RData())
	if err != nil {
		t.Fatal(err)
	}
	if parsedKey.KeyTag() != signer.key.KeyTag() || parsedKey.Algorithm != AlgECDSAP256 {
		t.Fatalf("parsed DNSKEY = %+v", parsedKey)
	}

	owner := MustParseName("www.example.com.")
	rrset := []RR{{Name: owner, Type: TypeA, Class: ClassINET, TTL: 300, Data: makeA(1)}}
	sig := signer.sign(t, rrset, uint8(owner.LabelCount()))
	parsedSig, err := ParseRRSIG(sig.RData())
	if err != nil {
		t.Fatal(err)
	}
	if !EqualNames(parsedSig.SignerName, signer.zone) || len(parsedSig.Signature) != 64 {
		t.Fatalf("parsed RRSIG = %+v", parsedSig)
	}
	if err := VerifyRRSIG(rrset, parsedSig, parsedKey, signer.now, signer.zone); err != nil {
		t.Fatalf("parsed RRSIG failed verification: %v", err)
	}
}
