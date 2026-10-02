package rib

import (
	"net/netip"
	"testing"
)

func TestParsePrefixStrict(t *testing.T) {
	good := []string{
		"0.0.0.0/0", "::/0", "10.0.0.0/8", "192.0.2.0/24",
		"2001:db8::/32", "2001:db8:e800::/61", // non-byte-aligned
		"10.1.2.3/32", "2001:db8::1/128",
	}
	for _, s := range good {
		if _, err := ParsePrefixStrict(s, RejectHostBits); err != nil {
			t.Errorf("%s: unexpected error: %v", s, err)
		}
	}
	bad := []string{
		"", " 10.0.0.0/8", "10.0.0.0/8 ", "10.0.0.1/8", // host bits
		"2001:db8::1/32",  // host bits
		"fe80::1%eth0/64", // zone
		"10.0.0.0/33", "2001:db8::/129", "10.0.0.0", "10.0.0.0/x",
		"::ffff:10.0.0.0/95", // mapped v4 with bits < 96
	}
	for _, s := range bad {
		if _, err := ParsePrefixStrict(s, RejectHostBits); err == nil {
			t.Errorf("%s: expected rejection", s)
		}
	}
}

func TestParsePrefixMaskPolicy(t *testing.T) {
	p, err := ParsePrefixStrict("10.1.2.3/8", MaskHostBits)
	if err != nil {
		t.Fatal(err)
	}
	if p != netip.MustParsePrefix("10.0.0.0/8") {
		t.Fatalf("masked = %s", p)
	}
	p, err = ParsePrefixStrict("2001:db8::1/32", MaskHostBits)
	if err != nil {
		t.Fatal(err)
	}
	if p != netip.MustParsePrefix("2001:db8::/32") {
		t.Fatalf("masked = %s", p)
	}
}

func TestParsePrefixMappedIPv4(t *testing.T) {
	p, err := ParsePrefixStrict("::ffff:10.0.0.0/104", RejectHostBits)
	if err != nil {
		t.Fatal(err)
	}
	if p != netip.MustParsePrefix("10.0.0.0/8") {
		t.Fatalf("unmapped = %s", p)
	}
}

func TestParseAddrStrict(t *testing.T) {
	if _, err := ParseAddrStrict("fe80::1%eth0"); err == nil {
		t.Error("zone accepted")
	}
	a, err := ParseAddrStrict("::ffff:192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	if !a.Is4() || a != netip.MustParseAddr("192.0.2.1") {
		t.Fatalf("unmapped = %s", a)
	}
}
