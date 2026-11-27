package dnssec

import (
	"testing"
	"time"
)

func TestVerifyRRSIGECDSA(t *testing.T) {
	priv, keyRR := mustKey(t)
	zone := MustParseName("example.")
	keyRR.Name = zone
	owner := MustParseName("host.example.")
	rrset := []RR{
		{Name: owner, Type: TypeA, Class: ClassIN, TTL: testTTL, Rdata: []byte{192, 0, 2, 7}},
	}
	inception := uint32(2000000000)
	expiration := uint32(2000003600)
	sig := signRRset(t, rrset, zone, priv, keyRR, uint8(owner.LabelCount()), inception, expiration)

	v := &Validator{Now: fixedTime(inception + 1000), Skew: time.Second}
	res, err := v.VerifyRRSet(rrset, []RR{sig}, []RR{keyRR})
	if err != nil || res.Status != StatusSecure {
		t.Fatalf("verify: status=%v err=%v", res.Status, err)
	}

	// Tampered RDATA must fail.
	bad := []RR{{Name: owner, Type: TypeA, Class: ClassIN, TTL: testTTL, Rdata: []byte{192, 0, 2, 8}}}
	if _, err := v.VerifyRRSet(bad, []RR{sig}, []RR{keyRR}); !IsBogus(err) {
		t.Fatalf("tampered RRset accepted: %v", err)
	}

	// A signature outside the validity window (Skew<0 forces zero tolerance)
	// is Bogus.
	expired := &Validator{Now: fixedTime(expiration + 3600), Skew: -1}
	if _, err := expired.VerifyRRSet(rrset, []RR{sig}, []RR{keyRR}); !IsBogus(err) {
		t.Fatalf("expired signature accepted: %v", err)
	}

	// And one before inception is likewise rejected.
	premature := &Validator{Now: fixedTime(inception - 3600), Skew: -1}
	if _, err := premature.VerifyRRSet(rrset, []RR{sig}, []RR{keyRR}); !IsBogus(err) {
		t.Fatalf("premature signature accepted: %v", err)
	}
}

func TestValidityWindowWraparound(t *testing.T) {
	// Window spanning the 2^32 wrap: inception near the end of the space,
	// expiration near the beginning. RFC 1982 arithmetic must accept now.
	inception := uint32(0xFFFFFFF0)  // just before wrap
	expiration := uint32(0x00000010) // just after wrap
	now := uint32(0x00000005)
	if !InValidityWindow(inception, expiration, now, 0) {
		t.Error("wraparound window rejected a timestamp inside it")
	}
	// Timestamp in the uncovered middle must be rejected.
	if InValidityWindow(inception, expiration, 0x80000000, 0) {
		t.Error("wraparound window accepted a timestamp in the gap")
	}
	// Normal non-wrapping window still works.
	if !InValidityWindow(100, 200, 150, 0) {
		t.Error("normal window rejected in-range timestamp")
	}
	if InValidityWindow(100, 200, 201, 0) {
		t.Error("normal window accepted out-of-range timestamp")
	}
}

func TestSerialCompare(t *testing.T) {
	cases := []struct {
		a, b uint32
		want int
	}{
		{100, 200, -1},
		{200, 100, 1},
		{0xFFFFFFFE, 5, -1}, // 5 is in the serial future
		{5, 0xFFFFFFFE, 1},
		{42, 42, 0},
	}
	for _, c := range cases {
		if got := SerialCompare(c.a, c.b); got != c.want {
			t.Errorf("SerialCompare(%d,%d)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestWildcardLabelsDetection(t *testing.T) {
	priv, keyRR := mustKey(t)
	zone := MustParseName("example.")
	keyRR.Name = zone

	// Answer owner is host.sub.example. but the RRSIG Labels field is 2,
	// indicating synthesis from *.example.
	owner := MustParseName("host.sub.example.")
	rrset := []RR{
		{Name: owner, Type: TypeA, Class: ClassIN, TTL: testTTL, Rdata: []byte{192, 0, 2, 9}},
	}
	sig := signRRset(t, rrset, zone, priv, keyRR, 2 /* labels */, 2000000000, 2000003600)

	v := &Validator{Now: fixedTime(2000001000), Skew: time.Second}
	res, err := v.VerifyRRSet(rrset, []RR{sig}, []RR{keyRR})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if res.WildcardName.String() != "*.sub.example." {
		t.Fatalf("wildcard = %q want *.sub.example.", res.WildcardName.String())
	}

	// Without a denial proof the wildcard answer must be rejected Bogus.
	if _, err := v.VerifyRRSetWithWildcard(rrset, []RR{sig}, []RR{keyRR}, owner, nil); !IsBogus(err) {
		t.Fatalf("wildcard answer without no-closer proof was accepted: %v", err)
	}

	// A proof that does not cover the exact qname is also Bogus.
	irrelevant := &DenialResult{
		Status:     StatusSecure,
		NextCloser: MustParseName("other.example."),
	}
	if _, err := v.VerifyRRSetWithWildcard(rrset, []RR{sig}, []RR{keyRR}, owner, irrelevant); !IsBogus(err) {
		t.Fatalf("unrelated proof accepted for wildcard: %v", err)
	}
}

func TestRevokedAndNonZoneKeysRejected(t *testing.T) {
	priv, keyRR := mustKey(t)
	zone := MustParseName("example.")
	keyRR.Name = zone

	revoked := keyRR
	revoked.Rdata = append([]byte(nil), keyRR.Rdata...)
	revoked.Rdata[0] = 0x01
	revoked.Rdata[1] = 0x80 // REVOKE flag

	parsed, _ := ParseDNSKEY(revoked.Rdata)
	if err := ValidateDNSKEY(parsed); !IsBogus(err) {
		t.Fatalf("revoked key accepted: %v", err)
	}

	nonZone := keyRR
	nonZone.Rdata = append([]byte(nil), keyRR.Rdata...)
	nonZone.Rdata[0], nonZone.Rdata[1] = 0x00, 0x00 // clear ZONE
	parsed2, _ := ParseDNSKEY(nonZone.Rdata)
	if err := ValidateDNSKEY(parsed2); !IsBogus(err) {
		t.Fatalf("non-zone key accepted: %v", err)
	}

	badProto := keyRR
	badProto.Rdata = append([]byte(nil), keyRR.Rdata...)
	badProto.Rdata[2] = 2
	parsed3, _ := ParseDNSKEY(badProto.Rdata)
	if err := ValidateDNSKEY(parsed3); !IsBogus(err) {
		t.Fatalf("protocol != 3 accepted: %v", err)
	}

	// The revoked key must not be selectable as a signer.
	owner := MustParseName("host.example.")
	rrset := []RR{{Name: owner, Type: TypeA, Class: ClassIN, TTL: testTTL, Rdata: []byte{1, 2, 3, 4}}}
	// Sign with the real (good) key but tag lookup against revoked set fails.
	sig := signRRset(t, rrset, zone, priv, keyRR, 3, 2000000000, 2000003600)
	parsedSig, _ := ParseRRSIG(sig.Rdata)
	if _, err := FindMatchingDNSKEY([]RR{revoked}, parsedSig.KeyTag); !IsBogus(err) {
		t.Fatalf("FindMatchingDNSKEY selected a revoked key: %v", err)
	}
}
