package pathsec

import (
	"net"
	"strings"
)

// Prefix is a canonical IPv4 or IPv6 CIDR prefix.
type Prefix struct {
	net.IPNet
}

// ParsePrefix parses a CIDR prefix. Host bits are masked so that
// 10.0.0.1/24 compares as 10.0.0.0/24.
func ParsePrefix(s string) (Prefix, error) {
	s = strings.TrimSpace(s)
	_, network, err := net.ParseCIDR(s)
	if err != nil {
		return Prefix{}, ErrInvalidPrefix
	}
	return Prefix{IPNet: *network}, nil
}

// MustParsePrefix is intended for tests and package-level examples.
func MustParsePrefix(s string) Prefix {
	p, err := ParsePrefix(s)
	if err != nil {
		panic(err)
	}
	return p
}

// AddressFamily returns 4 for IPv4 and 6 for IPv6.
func (p Prefix) AddressFamily() int {
	if p.IP.To4() != nil {
		return 4
	}
	return 6
}

// MaxLength returns 32 for IPv4 and 128 for IPv6.
func (p Prefix) MaxLength() int {
	if p.AddressFamily() == 4 {
		return 32
	}
	return 128
}

// Length returns the prefix length.
func (p Prefix) Length() int {
	ones, _ := p.Mask.Size()
	return ones
}

// Contains reports whether child is covered by p.
func (p Prefix) Contains(child Prefix) bool {
	if p.AddressFamily() != child.AddressFamily() {
		return false
	}
	return p.IPNet.Contains(child.IP) && child.Length() >= p.Length()
}

// String returns canonical CIDR text.
func (p Prefix) String() string {
	return p.IPNet.String()
}

func parsePrefixOrNil(s string) *Prefix {
	p, err := ParsePrefix(s)
	if err != nil {
		return nil
	}
	return &p
}
