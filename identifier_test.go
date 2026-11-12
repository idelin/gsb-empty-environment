package netsec

import (
	"errors"
	"fmt"
	"net/netip"
	"testing"
)

func mustIP(t *testing.T, value string) Identifier {
	t.Helper()
	id, err := NewIPIdentifier(value)
	if err != nil {
		t.Fatalf("NewIPIdentifier(%q): %v", value, err)
	}
	return id
}

func TestParseIPValueAcceptsCanonicalPublicAddresses(t *testing.T) {
	t.Parallel()
	for _, value := range []string{
		"1.2.3.4",
		"8.8.8.8",
		"93.184.216.34",
		"2001:4860:4860::8888",
		"2606:4700::1111",
		"2a00:1450::1",
		"2001:4860:4860:0:1:1:1:1",
	} {
		t.Run(value, func(t *testing.T) {
			addr, err := ParseIPValue(value)
			if err != nil {
				t.Fatalf("ParseIPValue(%q): %v", value, err)
			}
			if addr.String() != value {
				t.Fatalf("canonical form changed: got %q, want %q", addr.String(), value)
			}
		})
	}
}

func TestParseIPValueRejectsNonCanonicalText(t *testing.T) {
	t.Parallel()
	for _, value := range []string{
		"010.0.0.1",
		"01.2.3.4",
		"1.2.3.04",
		"192.168.001.001",
		" 8.8.8.8",
		"8.8.8.8 ",
		"8.8.8.8\n",
		"+8.8.8.8",
		"8.8.8",
		"8.8.8.8.8",
		"2001:0DB8::1",
		"2A00:1450::1",
		"2001:4860:4860:0:0:0:0:8888",
		"2001:4860:4860:0000:0000:0000:0000:8888",
		"fe80::1%en0",
	} {
		t.Run(value, func(t *testing.T) {
			_, err := ParseIPValue(value)
			if !errors.Is(err, ErrNonCanonicalIP) {
				t.Fatalf("ParseIPValue(%q) = %v, want ErrNonCanonicalIP", value, err)
			}
		})
	}
}

func TestParseIPValueRejectsDisallowedScopes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		value  string
		reason string
	}{
		{"127.0.0.1", "loopback"},
		{"127.0.0.2", "loopback"},
		{"::1", "loopback"},
		{"10.0.0.1", "private"},
		{"172.16.0.1", "private"},
		{"172.31.255.255", "private"},
		{"192.168.1.1", "private"},
		{"fc00::1", "unique-local"},
		{"fd12:3456:789a::1", "unique-local"},
		{"169.254.1.1", "link-local"},
		{"fe80::1", "link-local"},
		{"224.0.0.1", "multicast"},
		{"ff02::1", "multicast"},
		{"::ffff:8.8.8.8", "IPv4-mapped"},
		{"::ffff:10.0.0.1", "IPv4-mapped"},
		{"0.0.0.0", "reserved"},
		{"100.64.0.1", "shared"},
		{"192.0.2.1", "documentation"},
		{"198.18.0.1", "benchmarking"},
		{"198.51.100.1", "documentation"},
		{"203.0.113.1", "documentation"},
		{"240.0.0.1", "reserved"},
		{"255.255.255.255", "reserved"},
		{"::", "unspecified"},
		{"::2", "reserved"},
		{"2001::1", "reserved"},
		{"2001:db8::1", "documentation"},
		{"2002::1", "6to4"},
		{"3fff::1", "documentation"},
		{"5f00::1", "reserved"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.value, func(t *testing.T) {
			_, err := ParseIPValue(tt.value)
			if !errors.Is(err, ErrDisallowedIPAddress) {
				t.Fatalf("ParseIPValue(%q) = %v, want ErrDisallowedIPAddress", tt.value, err)
			}
			if got := fmt.Sprint(err); got == "" || !contains(got, tt.reason) {
				t.Fatalf("error %q does not mention reason %q", got, tt.reason)
			}
		})
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestWildcardRejected(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"*.example.com", "*.8.8.8.8", "8.8.8.*"} {
		if _, err := ParseIPValue(value); !errors.Is(err, ErrWildcardIPIdentifier) {
			t.Fatalf("ParseIPValue(%q) = %v, want ErrWildcardIPIdentifier", value, err)
		}
	}
}

func TestIdentifierTypeAndChallengeValidation(t *testing.T) {
	t.Parallel()
	id := Identifier{Type: "dns", Value: "example.com"}
	if err := id.Validate(); !errors.Is(err, ErrIdentifierType) {
		t.Fatalf("dns identifier: got %v, want ErrIdentifierType", err)
	}
	if err := ValidateIdentifier(id); !errors.Is(err, ErrIdentifierType) {
		t.Fatalf("ValidateIdentifier: got %v", err)
	}

	ip := mustIP(t, "8.8.8.8")
	if err := ValidateIPValue(ip.Value); err != nil {
		t.Fatalf("ValidateIPValue: %v", err)
	}
	if _, err := ParseIPIdentifier(ip.Value); err != nil {
		t.Fatalf("ParseIPIdentifier: %v", err)
	}
	if err := ValidateIdentifiers([]Identifier{ip}); err != nil {
		t.Fatalf("ValidateIdentifiers: %v", err)
	}
	if err := ValidateIdentifiers(nil); err == nil {
		t.Fatal("ValidateIdentifiers(nil) = nil")
	}
	if err := ValidateIdentifiers([]Identifier{ip, {Type: "ip", Value: "10.0.0.1"}}); !errors.Is(err, ErrDisallowedIPAddress) {
		t.Fatalf("ValidateIdentifiers with private address: got %v", err)
	}
	for _, method := range []string{ChallengeHTTP01, ChallengeTLSALPN01} {
		if err := ip.ValidateChallenge(method); err != nil {
			t.Fatalf("ValidateChallenge(%q): %v", method, err)
		}
		if err := ValidateChallenge(ip, method); err != nil {
			t.Fatalf("ValidateChallenge(%q): %v", method, err)
		}
		if !SupportsChallenge(method) {
			t.Fatalf("SupportsChallenge(%q) = false", method)
		}
	}
	for _, method := range []string{ChallengeDNS01, "tls-sni-02", "", "HTTP-01"} {
		if err := ip.ValidateChallenge(method); !errors.Is(err, ErrUnsupportedChallenge) {
			t.Fatalf("ValidateChallenge(%q) = %v, want ErrUnsupportedChallenge", method, err)
		}
		if SupportsChallenge(method) {
			t.Fatalf("SupportsChallenge(%q) = true", method)
		}
	}
}

func TestParseIdentifierJSON(t *testing.T) {
	t.Parallel()
	id, err := ParseIdentifierJSON([]byte(`{"type":"ip","value":"8.8.8.8"}`))
	if err != nil {
		t.Fatalf("ParseIdentifierJSON: %v", err)
	}
	if id.Type != "ip" || id.Value != "8.8.8.8" {
		t.Fatalf("unexpected identifier: %+v", id)
	}
	if _, err := ParseIdentifierJSON([]byte(`{"type":"ip","value":"010.0.0.1"}`)); !errors.Is(err, ErrNonCanonicalIP) {
		t.Fatalf("non-canonical JSON identifier: got %v", err)
	}
	if _, err := ParseIdentifierJSON([]byte(`{"type":"dns","value":"example.com"}`)); !errors.Is(err, ErrIdentifierType) {
		t.Fatalf("dns JSON identifier: got %v", err)
	}
	if _, err := ParseIdentifierJSON([]byte(`{"type":"ip","value":"8.8.8.8","wildcard":true}`)); !errors.Is(err, ErrInvalidIPIdentifier) {
		t.Fatalf("unknown JSON field: got %v", err)
	}
	if _, err := ParseIdentifierJSON([]byte(`{"type":"ip","value":"8.8.8.8"} trailing`)); !errors.Is(err, ErrInvalidIPIdentifier) {
		t.Fatalf("trailing JSON data: got %v", err)
	}
}

func TestReverseDNSName(t *testing.T) {
	t.Parallel()
	if got := ReverseDNSName(netip.MustParseAddr("1.2.3.4")); got != "4.3.2.1.in-addr.arpa" {
		t.Fatalf("IPv4 reverse name = %q", got)
	}
	const want = "1.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.8.b.d.0.1.0.0.2.ip6.arpa"
	if got := ReverseDNSName(netip.MustParseAddr("2001:db8::1")); got != want {
		t.Fatalf("IPv6 reverse name = %q, want %q", got, want)
	}
	id := mustIP(t, "8.8.8.8")
	name, err := id.TLSALPN01ServerName()
	if err != nil {
		t.Fatalf("TLSALPN01ServerName: %v", err)
	}
	if name != "8.8.8.8.in-addr.arpa" {
		t.Fatalf("server name = %q", name)
	}

	// Formatting helpers work for canonical RFC example addresses even though
	// order creation rejects them as reserved.
	reserved := Identifier{Type: "ip", Value: "2001:db8::1"}
	if name, err := reserved.ReverseDNSName(); err != nil || name != want {
		t.Fatalf("ReverseDNSName(reserved) = %q, %v", name, err)
	}
}
