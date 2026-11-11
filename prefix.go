package netsec

import (
	"fmt"
	"net/netip"
)

// Prefix is a single IP prefix (CIDR block) that a route can match.
// It wraps netip.Prefix so callers can keep using the standard library
// representation while the package controls matching semantics.
type Prefix struct {
	net netip.Prefix
}

// ParsePrefix parses an IPv4 or IPv6 CIDR such as "10.0.0.0/8".
func ParsePrefix(s string) (Prefix, error) {
	p, err := netip.ParsePrefix(s)
	if err != nil {
		return Prefix{}, fmt.Errorf("parse prefix %q: %w", s, err)
	}
	return Prefix{net: p.Masked()}, nil
}

// MustParsePrefix parses s and panics on error; intended for literals.
func MustParsePrefix(s string) Prefix {
	p, err := ParsePrefix(s)
	if err != nil {
		panic(err)
	}
	return p
}

// PrefixFromNet returns a Prefix wrapping a validated netip.Prefix.
func PrefixFromNet(p netip.Prefix) Prefix {
	return Prefix{net: p.Masked()}
}

// Net returns the masked standard-library prefix.
func (p Prefix) Net() netip.Prefix { return p.net }

// Valid reports whether the prefix was parsed successfully.
func (p Prefix) Valid() bool { return p.net.IsValid() }

// Contains reports whether p fully contains sub (CIDR containment,
// including the equal-prefix case).
func (p Prefix) Contains(sub Prefix) bool {
	if !p.Valid() || !sub.Valid() {
		return false
	}
	if p.net.Addr().BitLen() != sub.net.Addr().BitLen() {
		return false
	}
	if p.net.Bits() > sub.net.Bits() {
		return false
	}
	return p.net.Contains(sub.net.Addr())
}

// Bits returns the prefix length.
func (p Prefix) Bits() int { return p.net.Bits() }

func (p Prefix) String() string {
	if !p.Valid() {
		return "<invalid-prefix>"
	}
	return p.net.String()
}

// PrefixRange matches a route prefix when it is contained in Network and
// its prefix length lies within [MinLen, MaxLen]. A zero Network means
// the network bound is ignored (only the length bounds apply).
type PrefixRange struct {
	Network Prefix
	MinLen  int
	MaxLen  int
}

func (r PrefixRange) match(route Prefix) bool {
	if r.MinLen > 0 && route.Bits() < r.MinLen {
		return false
	}
	if r.MaxLen > 0 && route.Bits() > r.MaxLen {
		return false
	}
	if r.Network.Valid() && !r.Network.Contains(route) {
		return false
	}
	return true
}

func (r PrefixRange) validate() error {
	if r.MinLen < 0 || r.MaxLen < 0 {
		return fmt.Errorf("prefix range lengths must be non-negative: %+v", r)
	}
	if r.MinLen > 0 && r.MaxLen > 0 && r.MinLen > r.MaxLen {
		return fmt.Errorf("prefix range min length %d > max length %d", r.MinLen, r.MaxLen)
	}
	if r.Network.Valid() {
		bits := r.Network.Net().Addr().BitLen()
		if r.MinLen > bits || r.MaxLen > bits {
			return fmt.Errorf("prefix range length exceeds address width %d: %+v", bits, r)
		}
	}
	return nil
}

func (r PrefixRange) Match(route Route) bool { return r.match(route.Prefix) }
