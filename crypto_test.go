package rpki

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"testing"
	"time"
)

func TestCMSWrongSignerKeyRejected(t *testing.T) {
	h := newTestHarness(t)
	ee := h.createEE("cms-ee", h.root, inheritClaims())
	wrongKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manifestDER, err := MarshalManifest(&Manifest{
		ManifestNumber: big.NewInt(1),
		ThisUpdate:     h.now.Add(-time.Hour),
		NextUpdate:     h.now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	badCMS := createCMSWithKeyTest(t, oidManifest, manifestDER, ee, wrongKey)
	_, err = ParseAndVerifyCMS(badCMS)
	if !errors.Is(err, ErrCMSSignature) {
		t.Fatalf("expected ErrCMSSignature, got %v", err)
	}
}

func TestTALPublicKeyMismatchRejected(t *testing.T) {
	h := newTestHarness(t)
	other := h.createCA("other-trust-anchor", "other", nil, mustClaims(t, []string{"172.16.0.0/12"}, nil, []string{"65000"}))
	h.tal.SubjectPublicKeyInfo = other.Cert.X509.RawSubjectPublicKeyInfo

	ee := h.createEE("mft-ee", h.root, inheritClaims())
	crlName := h.writeCRL(h.root)
	h.writeManifest(h.root, ee, 1, h.now.Add(-time.Hour), []string{"ta.cer", crlName})
	_, err := NewValidator(h.repository(), Options{Now: fixedNow(h.now)}).ValidateTAL(context.Background(), h.tal)
	if !errors.Is(err, ErrTALKeyMismatch) {
		t.Fatalf("expected ErrTALKeyMismatch, got %v", err)
	}
}

func TestArbitraryRFC3779RangesRoundTripAndContainment(t *testing.T) {
	v4Range, err := ParseIPRange("192.168.1.128", "192.168.1.200")
	if err != nil {
		t.Fatal(err)
	}
	v6Range, err := ParseIPRange("2001:db8:1::10", "2001:db8:1::20")
	if err != nil {
		t.Fatal(err)
	}
	claims := ResourceClaims{
		IPv4: &IPChoice{Ranges: []IPRange{v4Range}},
		IPv6: &IPChoice{Ranges: []IPRange{v6Range}},
		ASNs: &ASChoice{Ranges: []ASRange{{Start: 64510, End: 64520}}},
	}
	ipDER, asDER, err := MarshalResourceExtensions(claims)
	if err != nil {
		t.Fatal(err)
	}
	parsed, found, err := ParseResourceExtensions([]pkix.Extension{{Id: oidPEIPAddrBlocks, Value: ipDER}, {Id: oidPEASIdentifiers, Value: asDER}})
	if err != nil || !found {
		t.Fatalf("ParseResourceExtensions found=%v err=%v", found, err)
	}
	effective, err := parsed.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !effective.ContainsIPRange(mustParseIPRange(t, "192.168.1.128", "192.168.1.200")) {
		t.Fatal("IPv4 arbitrary range was not preserved")
	}
	if effective.ContainsIPRange(mustParseIPRange(t, "192.168.1.128", "192.168.1.201")) {
		t.Fatal("IPv4 range endpoint expanded")
	}
	if !effective.ContainsIPRange(mustParseIPRange(t, "2001:db8:1::10", "2001:db8:1::20")) {
		t.Fatal("IPv6 arbitrary range was not preserved")
	}
	if !effective.ContainsASRange(ASRange{Start: 64515, End: 64515}) {
		t.Fatal("AS range was not preserved")
	}
	if effective.ContainsASRange(ASRange{Start: 64521, End: 64521}) {
		t.Fatal("AS range endpoint expanded")
	}
}

func mustParseIPRange(t *testing.T, start, end string) IPRange {
	t.Helper()
	r, err := ParseIPRange(start, end)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
