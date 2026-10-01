// Package rtsim implements a versioned binary-prefix routing table for IPv4
// and IPv6 plus a discrete-event packet simulation. It uses only the Go
// standard library and never touches the real network.
package rtsim

import (
	"fmt"
	"net/netip"
)

// Policy decides how ParsePrefix treats prefixes with non-zero host bits.
type Policy int

const (
	// RejectHostBits rejects any prefix whose address has bits set beyond
	// the prefix length (non-canonical form).
	RejectHostBits Policy = iota
	// Canonicalize silently masks host bits down to the canonical network
	// address.
	Canonicalize
)

// Prefix is an immutable IPv4 or IPv6 prefix in canonical form.
type Prefix struct {
	addr netip.Addr
	bits int
}

// ParsePrefix strictly parses CIDR notation ("10.0.0.0/8", "2001:db8::/32").
// It rejects malformed addresses, out-of-range lengths, zones, IPv4-mapped
// IPv6 forms, and (unless pol is Canonicalize) non-zero host bits.
func ParsePrefix(s string, pol Policy) (Prefix, error) {
	p, err := netip.ParsePrefix(s)
	if err != nil {
		return Prefix{}, fmt.Errorf("parse prefix %q: %v", s, err)
	}
	a := p.Addr()
	if a.Is4In6() {
		return Prefix{}, fmt.Errorf("parse prefix %q: IPv4-mapped IPv6 addresses are not accepted", s)
	}
	if a.Zone() != "" {
		return Prefix{}, fmt.Errorf("parse prefix %q: zones are not accepted", s)
	}
	if m := p.Masked(); m != p {
		if pol == RejectHostBits {
			return Prefix{}, fmt.Errorf("parse prefix %q: non-zero host bits, canonical form is %s", s, m)
		}
		p = m
	}
	return Prefix{addr: p.Addr(), bits: p.Bits()}, nil
}

// MustPrefix parses s or panics. Convenience for tests and examples.
func MustPrefix(s string, pol Policy) Prefix {
	p, err := ParsePrefix(s, pol)
	if err != nil {
		panic(err)
	}
	return p
}

// Addr returns the canonical (masked) network address.
func (p Prefix) Addr() netip.Addr { return p.addr }

// Bits returns the prefix length.
func (p Prefix) Bits() int { return p.bits }

// IsV4 reports whether the prefix is IPv4.
func (p Prefix) IsV4() bool { return p.addr.Is4() }

func (p Prefix) String() string { return netip.PrefixFrom(p.addr, p.bits).String() }

// key returns the address bytes used as trie key and the key width in bits.
func (p Prefix) key() ([]byte, int) {
	if p.addr.Is4() {
		b := p.addr.As4()
		return b[:], 32
	}
	b := p.addr.As16()
	return b[:], 128
}
