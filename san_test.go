package netsec

import (
	"crypto/x509"
	"net"
	"testing"
)

func mustLeaf(t *testing.T, dns []string, ips ...string) *x509.Certificate {
	t.Helper()
	tmpl := &x509.Certificate{SerialNumber: serial(t)}
	tmpl.DNSNames = dns
	for _, ip := range ips {
		tmpl.IPAddresses = append(tmpl.IPAddresses, net.ParseIP(ip))
	}
	return selfSignedLeaf(t, tmpl)
}

func TestLeafCoversHostDNS(t *testing.T) {
	leaf := mustLeaf(t, []string{"example.com", "*.example.com", "xn--bcher-kva.de"})

	cases := []struct {
		host string
		want bool
	}{
		{"example.com", true},
		{"EXAMPLE.COM", true},      // case-insensitive
		{"example.com.", true},     // trailing dot normalized away
		{"www.example.com", true},  // one-label wildcard
		{"a.b.example.com", false}, // two labels: wildcard matches only one
		{"deep.www.example.com", false},
		{"example.org", false},     // unrelated name
		{"notexample.com", false},  // suffix is not a label boundary
		{"xn--bcher-kva.de", true}, // A-label matches
		{"bücher.de", true},        // U-label canonicalizes to A-label
		{"BÜCHER.DE.", true},       // IDN case + trailing dot
		{"sub.xn--bcher-kva.de", false},
	}
	for _, tc := range cases {
		host, err := canonicalHost(tc.host)
		if err != nil {
			t.Fatalf("canonicalHost(%q): %v", tc.host, err)
		}
		if got := leafCoversHost(leaf, host); got != tc.want {
			t.Errorf("leafCoversHost(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

func TestWildcardRules(t *testing.T) {
	leaf := mustLeaf(t, []string{"*.example.com"})
	host, _ := canonicalHost("www.example.com")
	if !leafCoversHost(leaf, host) {
		t.Fatal("single-label wildcard should match www.example.com")
	}

	// Partial-wildcard labels are not honored as wildcards.
	partial := mustLeaf(t, []string{"a*.example.com"})
	if leafCoversHost(partial, host) {
		t.Error("partial wildcard a*.example.com must not match www.example.com")
	}

	// The wildcard never matches an empty label.
	empty, _ := canonicalHost(".example.com")
	if leafCoversHost(leaf, empty) {
		t.Error("wildcard must not match an empty left-most label")
	}
}

func TestLeafCoversHostIP(t *testing.T) {
	leaf := mustLeaf(t, nil, "2001:db8::1", "10.0.0.5")
	v6, _ := canonicalHost("2001:DB8::1")
	v4, _ := canonicalHost("10.0.0.5")
	other, _ := canonicalHost("10.0.0.6")

	if !leafCoversHost(leaf, v6) {
		t.Error("expected IPv6 SAN match after canonicalization")
	}
	if !leafCoversHost(leaf, v4) {
		t.Error("expected IPv4 SAN match")
	}
	if leafCoversHost(leaf, other) {
		t.Error("unexpected IPv4 SAN match")
	}
}

func TestLeafCoversHostNoCNFallback(t *testing.T) {
	tmpl := &x509.Certificate{
		SerialNumber: serial(t),
		Subject:      pkixCN("legacy.example"),
	}
	leaf := selfSignedLeaf(t, tmpl)
	host, _ := canonicalHost("legacy.example")
	if leafCoversHost(leaf, host) {
		t.Error("commonName must not be used as an identity fallback")
	}
}

func TestLeafCoversHostNil(t *testing.T) {
	if leafCoversHost(nil, "example.com") {
		t.Error("nil leaf must never cover a host")
	}
}
