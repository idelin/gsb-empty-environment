package spectest

import (
	"errors"
	netip "net/netip"
	"testing"

	. "prescreen.local/ps-c7499fbc67"
)

func TestIPv4SpecialPurposeRangesRejected(t *testing.T) {
	rejected := []string{
		"0.0.0.0", "10.1.2.3", "100.64.0.1", "127.0.0.1",
		"169.254.10.20", "172.16.1.1", "192.0.0.1",
		// Documentation ranges (the explicit requirement).
		"192.0.2.1", "198.51.100.7", "203.0.113.99",
		"192.31.196.1", "192.52.193.1", "192.88.99.1", "192.168.1.1",
		"192.175.48.1", "198.18.0.1", "198.19.255.254",
		"224.0.0.1", "239.255.255.250", "240.0.0.1", "255.255.255.255",
	}
	for _, raw := range rejected {
		t.Run(raw, func(t *testing.T) {
			if got, err := ParsePublicIP(raw); err == nil {
				t.Fatalf("ParsePublicIP(%q) = %v, want rejection", raw, got)
			} else if !errors.Is(err, ErrReservedIP) {
				t.Fatalf("ParsePublicIP(%q) error = %v, want ErrReservedIP", raw, err)
			}
		})
	}
}

func TestIPv6SpecialPurposeRangesRejected(t *testing.T) {
	rejected := []string{
		"::", "::1", "::ffff:192.0.2.1", "64:ff9b::808:808",
		"100::1", "2001::1", "2001:2::1", "2001:db8::1",
		"2002::1", "2620:4f:8000::1", "3fff::1", "fc00::1",
		"fd12:3456::1", "fe80::1", "ff02::1",
	}
	for _, raw := range rejected {
		t.Run(raw, func(t *testing.T) {
			if got, err := ParsePublicIP(raw); err == nil {
				t.Fatalf("ParsePublicIP(%q) = %v, want rejection", raw, got)
			} else if !errors.Is(err, ErrReservedIP) && !errors.Is(err, ErrInvalidIP) {
				t.Fatalf("ParsePublicIP(%q) error = %v, want reserved/invalid", raw, err)
			}
		})
	}
}

func TestPublicAddressesAccepted(t *testing.T) {
	accepted := []string{
		"1.1.1.1", "8.8.8.8", "93.184.216.34", "203.0.114.1",
		"2606:4700:4700::1111", "2001:4860:4860::8888", "2a00:1450:4001:81b::200e",
	}
	for _, raw := range accepted {
		t.Run(raw, func(t *testing.T) {
			got, err := ParsePublicIP(raw)
			if err != nil {
				t.Fatalf("ParsePublicIP(%q): %v", raw, err)
			}
			if got.String() != raw {
				t.Fatalf("ParsePublicIP(%q) = %q", raw, got)
			}
		})
	}
}

func TestIPv4MappedPublicAddressIsNormalized(t *testing.T) {
	got, err := ParsePublicIP("::ffff:8.8.8.8")
	if err != nil {
		t.Fatalf("ParsePublicIP mapped public: %v", err)
	}
	if got.String() != "8.8.8.8" {
		t.Fatalf("mapped address normalized to %q", got)
	}
	if _, err := ParsePublicIP("::ffff:10.0.0.1"); !errors.Is(err, ErrReservedIP) {
		t.Fatalf("mapped private error = %v", err)
	}
}

func TestDocumentationIPRejectedEvenIfBundleClaimsPublic(t *testing.T) {
	order, err := NewOrder("192.0.2.44", 443, ChallengeHTTP01, "acct", "nonce",
		DigestBytes([]byte("key")), 1, tt(0), tt(10))
	if err == nil {
		t.Fatalf("NewOrder accepted documentation address: %+v", order)
	}
	if !errors.Is(err, ErrReservedIP) {
		t.Fatalf("error = %v, want ErrReservedIP", err)
	}

	// Also construct the struct directly; Aggregate must re-run the policy.
	bad := testOrder(t, 1, 0, 10)
	bad.IP = mustAddr(t, "192.0.2.44")
	b := Bundle{ClaimedPublicIP: true}
	a := Audit(Request{Order: bad}, b)
	if a.Valid {
		t.Fatalf("Audit accepted documentation address")
	}
	if len(a.Problems) == 0 || !errors.Is(a.Err(), ErrReservedIP) {
		t.Fatalf("Audit error = %v, want ErrReservedIP", a.Err())
	}
}

func mustAddr(t *testing.T, s string) netip.Addr {
	t.Helper()
	addr, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatal(err)
	}
	return addr
}
