package rib

import (
	"fmt"
	"net/netip"
	"strings"
)

// CanonPolicy decides how non-canonical prefixes (host bits set) are handled.
type CanonPolicy int

const (
	// RejectHostBits rejects prefixes whose address has bits set beyond the
	// prefix length (e.g. 10.0.0.1/8).
	RejectHostBits CanonPolicy = iota
	// MaskHostBits canonicalizes by clearing host bits (10.0.0.1/8 -> 10.0.0.0/8).
	MaskHostBits
)

// ParsePrefixStrict parses "addr/bits" with validation stricter than
// netip.ParsePrefix: no whitespace, no zones, IPv4-mapped IPv6 forms are
// unmapped to IPv4, and host bits are rejected or masked per policy.
func ParsePrefixStrict(s string, pol CanonPolicy) (netip.Prefix, error) {
	if s == "" || strings.TrimSpace(s) != s {
		return netip.Prefix{}, fmt.Errorf("invalid prefix %q", s)
	}
	p, err := netip.ParsePrefix(s)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("invalid prefix %q: %w", s, err)
	}
	return NormalizePrefix(p, pol)
}

// NormalizePrefix applies unmapping and the host-bit policy to p.
func NormalizePrefix(p netip.Prefix, pol CanonPolicy) (netip.Prefix, error) {
	a := p.Addr()
	if a.Zone() != "" {
		return netip.Prefix{}, fmt.Errorf("prefix %q: zones not allowed", p)
	}
	if a.Is4In6() {
		if p.Bits() < 96 {
			return netip.Prefix{}, fmt.Errorf("prefix %q: mapped IPv4 needs bits >= 96", p)
		}
		p = netip.PrefixFrom(a.Unmap(), p.Bits()-96)
	}
	if p != p.Masked() {
		if pol == RejectHostBits {
			return netip.Prefix{}, fmt.Errorf("prefix %q: host bits set (want %q)", p, p.Masked())
		}
		p = p.Masked()
	}
	return p, nil
}

// ParseAddrStrict parses a bare IP address: no zone, 4-in-6 unmapped.
func ParseAddrStrict(s string) (netip.Addr, error) {
	if s == "" || strings.TrimSpace(s) != s {
		return netip.Addr{}, fmt.Errorf("invalid address %q", s)
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("invalid address %q: %w", s, err)
	}
	if a.Zone() != "" {
		return netip.Addr{}, fmt.Errorf("address %q: zones not allowed", s)
	}
	return a.Unmap(), nil
}
