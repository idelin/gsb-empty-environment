package dialpolicy

import (
	"net/netip"
	"time"
)

// Default timeout values used by transports produced from a Guard.
const (
	defaultIdleTimeout           = 90 * time.Second
	defaultTLSTimeout            = 10 * time.Second
	defaultExpectContinueTimeout = time.Second
)

// PrefixPolicy is a CIDR-based Policy. Evaluation is default-deny: an address
// is allowed only when it matches an allowed prefix and no denied prefix.
// An empty PrefixPolicy allows nothing, which makes a misconfiguration fail
// closed rather than silently opening the network.
type PrefixPolicy struct {
	AllowPrefixes []netip.Prefix
	DenyPrefixes  []netip.Prefix
}

// Allow reports whether addr passes the prefix lists. Denied prefixes take
// precedence over allowed ones.
func (p PrefixPolicy) Allow(addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}
	for _, prefix := range p.DenyPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	for _, prefix := range p.AllowPrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// MustPrefix parses a CIDR literal and panics on error; it is intended for
// compile-time-known policy tables.
func MustPrefix(cidr string) netip.Prefix {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		panic(err)
	}
	return prefix.Masked()
}

// MustAddr parses an IP literal and panics on error.
func MustAddr(ip string) netip.Addr {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		panic(err)
	}
	return addr.Unmap()
}

// BlockedPrefixes is a conservative list of ranges a scraper must never reach
// directly: loopback, link-local, private internals, CGNAT, multicast and the
// unspecified address. It is meant to be used as DenyPrefixes in front of a
// broad allow list.
var BlockedPrefixes = []netip.Prefix{
	MustPrefix("0.0.0.0/8"),
	MustPrefix("10.0.0.0/8"),
	MustPrefix("100.64.0.0/10"),
	MustPrefix("127.0.0.0/8"),
	MustPrefix("169.254.0.0/16"),
	MustPrefix("172.16.0.0/12"),
	MustPrefix("192.0.0.0/24"),
	MustPrefix("192.168.0.0/16"),
	MustPrefix("::1/128"),
	MustPrefix("fc00::/7"),
	MustPrefix("fe80::/10"),
	MustPrefix("ff00::/8"),
	MustPrefix("::/128"),
}
