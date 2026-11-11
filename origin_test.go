package netsec

import (
	"errors"
	"net/netip"
	"testing"
)

func mustResolver(t *testing.T, version string, cidrs ...string) *Resolver {
	t.Helper()
	resolver, err := NewResolver(Config{Version: version, TrustedCIDRs: cidrs})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	return resolver
}

func mustAddr(t *testing.T, s string) netip.Addr {
	t.Helper()
	addr, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatalf("ParseAddr(%q): %v", s, err)
	}
	return addr
}

func TestResolveStopsAtUntrustedHop(t *testing.T) {
	resolver := mustResolver(t, "v1", "10.0.0.0/8")
	origin, err := resolver.Resolve(mustAddr(t, "10.0.0.1"), []string{"10.0.0.2", "192.0.2.1", "203.0.113.9"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if want := mustAddr(t, "192.0.2.1"); origin.ClientIP != want {
		t.Fatalf("ClientIP = %v, want %v", origin.ClientIP, want)
	}
	if len(origin.Path) != 3 {
		t.Fatalf("Path = %v, want 3 hops", origin.Path)
	}
	if origin.ConfigVersion != "v1" {
		t.Fatalf("ConfigVersion = %q, want %q", origin.ConfigVersion, "v1")
	}
}

func TestResolveUntrustedPeerIgnoresRecords(t *testing.T) {
	resolver := mustResolver(t, "v1", "10.0.0.0/8")
	origin, err := resolver.Resolve(mustAddr(t, "203.0.113.1"), []string{"10.0.0.2"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if want := mustAddr(t, "203.0.113.1"); origin.ClientIP != want {
		t.Fatalf("ClientIP = %v, want %v", origin.ClientIP, want)
	}
	if len(origin.Path) != 1 {
		t.Fatalf("Path = %v, want only the peer", origin.Path)
	}
}

func TestResolveIPv4MappedUnifiesClassification(t *testing.T) {
	resolver := mustResolver(t, "v1", "10.0.0.0/8")
	// Mapped peer and mapped record must match plain IPv4 CIDRs.
	origin, err := resolver.Resolve(mustAddr(t, "::ffff:10.0.0.1"), []string{"::ffff:10.0.0.2", "192.0.2.1"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if want := mustAddr(t, "192.0.2.1"); origin.ClientIP != want {
		t.Fatalf("ClientIP = %v, want %v", origin.ClientIP, want)
	}
	for _, hop := range origin.Path {
		if hop.Is4In6() {
			t.Fatalf("hop %v still IPv4-mapped, want unified IPv4", hop)
		}
	}
}

func TestResolveRejectsAmbiguousRecords(t *testing.T) {
	resolver := mustResolver(t, "v1", "10.0.0.0/8")
	peer := mustAddr(t, "10.0.0.1")
	cases := []string{
		"",               // empty
		" 10.0.0.2",      // surrounding whitespace
		"10.0.0.2 ",      // trailing whitespace
		"10.0.0.2:8080",  // port attached
		"[2001:db8::1]",  // brackets
		"010.0.0.2",      // leading zeros
		"fe80::1%eth0",   // zone
		"10.0.0.2/32",    // prefix notation
		"example.com",    // hostname
		"2001:db8::1::2", // malformed IPv6
	}
	for _, record := range cases {
		if _, err := resolver.Resolve(peer, []string{record}); !errors.Is(err, ErrInvalidRecord) {
			t.Errorf("Resolve(record %q) err = %v, want ErrInvalidRecord", record, err)
		}
	}
}

func TestResolveIgnoresMalformedRecordsBeyondTrustBoundary(t *testing.T) {
	resolver := mustResolver(t, "v1", "10.0.0.0/8")
	// The malformed record is never consumed because 192.0.2.1 is untrusted.
	origin, err := resolver.Resolve(mustAddr(t, "10.0.0.1"), []string{"192.0.2.1", "not-an-ip"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if want := mustAddr(t, "192.0.2.1"); origin.ClientIP != want {
		t.Fatalf("ClientIP = %v, want %v", origin.ClientIP, want)
	}
}

func TestNewResolverRejectsBadCIDR(t *testing.T) {
	if _, err := NewResolver(Config{Version: "v1", TrustedCIDRs: []string{"10.0.0.0/33"}}); err == nil {
		t.Fatal("NewResolver accepted invalid CIDR")
	}
}
