package evidence

import (
	"fmt"
	netip "net/netip"
	"strings"
)

// The deny lists are intentionally explicit.  Relying only on
// Addr.IsGlobalUnicast would accept private, documentation and benchmarking
// ranges in some Go versions, which is unsafe for certificate issuance.
var ipv4Denied = mustPrefixes(
	"0.0.0.0/8",       // "this network"
	"10.0.0.0/8",      // private
	"100.64.0.0/10",   // shared address space
	"127.0.0.0/8",     // loopback
	"169.254.0.0/16",  // link-local
	"172.16.0.0/12",   // private
	"192.0.0.0/24",    // IETF protocol assignments
	"192.0.2.0/24",    // TEST-NET-1
	"192.31.196.0/24", // AS112-v4
	"192.52.193.0/24", // AMT
	"192.88.99.0/24",  // deprecated 6to4 relay anycast
	"192.168.0.0/16",  // private
	"192.175.48.0/24", // AS112 direct delegation
	"198.18.0.0/15",   // benchmarking
	"198.51.100.0/24", // TEST-NET-2
	"203.0.113.0/24",  // TEST-NET-3
	"224.0.0.0/4",     // multicast
	"240.0.0.0/4",     // reserved, includes 255.255.255.255
)

var ipv6Denied = mustPrefixes(
	"::/8",              // unspecified, loopback, IPv4-compatible, IPv4-mapped
	"64:ff9b::/96",      // well-known NAT64
	"64:ff9b:1::/48",    // local-use NAT64
	"100::/64",          // discard-only
	"2001::/23",         // IETF protocol assignments (Teredo, benchmarking, ORCHID...)
	"2001:db8::/32",     // documentation
	"2002::/16",         // 6to4
	"2620:4f:8000::/48", // AS112 direct delegation
	"3fff::/20",         // documentation (RFC 9637)
	"5f00::/16",         // segment routing SIDs
	"fc00::/7",          // unique local
	"fe80::/10",         // link-local
	"ff00::/8",          // multicast
)

var ipv6GlobalUnicast = netip.MustParsePrefix("2000::/3")

func mustPrefixes(values ...string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(values))
	for _, v := range values {
		out = append(out, netip.MustParsePrefix(v))
	}
	return out
}

// NormalizePublicAddr validates addr and returns its canonical public form.
// IPv4-mapped IPv6 addresses are normalized to IPv4 and then checked against
// the same policy; scoped addresses and all special-purpose ranges are
// rejected.
func NormalizePublicAddr(addr netip.Addr) (netip.Addr, error) {
	if !addr.IsValid() {
		return netip.Addr{}, fmt.Errorf("%w: invalid address", ErrInvalidIP)
	}
	if addr.Zone() != "" {
		return netip.Addr{}, fmt.Errorf("%w: scoped address %q", ErrInvalidIP, addr.String())
	}
	addr = addr.Unmap()
	if addr.Is4() {
		if addr.IsUnspecified() || addr.IsLoopback() || addr.IsMulticast() ||
			addr.IsLinkLocalUnicast() || addr.IsPrivate() {
			return netip.Addr{}, fmt.Errorf("%w: %s", ErrReservedIP, addr)
		}
		for _, p := range ipv4Denied {
			if p.Contains(addr) {
				return netip.Addr{}, fmt.Errorf("%w: %s is in %s", ErrReservedIP, addr, p)
			}
		}
		return addr, nil
	}
	if addr.Is6() {
		if !ipv6GlobalUnicast.Contains(addr) {
			return netip.Addr{}, fmt.Errorf("%w: %s is outside 2000::/3", ErrReservedIP, addr)
		}
		if addr.IsUnspecified() || addr.IsLoopback() || addr.IsMulticast() ||
			addr.IsLinkLocalUnicast() || addr.IsPrivate() {
			return netip.Addr{}, fmt.Errorf("%w: %s", ErrReservedIP, addr)
		}
		for _, p := range ipv6Denied {
			if p.Contains(addr) {
				return netip.Addr{}, fmt.Errorf("%w: %s is in %s", ErrReservedIP, addr, p)
			}
		}
		return addr, nil
	}
	return netip.Addr{}, fmt.Errorf("%w: %q is not an IP address", ErrInvalidIP, addr.String())
}

// ParsePublicIP parses and validates a public IP literal.  Host:port strings,
// scoped literals and IPv4-mapped IPv6 literals are rejected.
func ParsePublicIP(raw string) (netip.Addr, error) {
	if strings.TrimSpace(raw) != raw || raw == "" {
		return netip.Addr{}, fmt.Errorf("%w: %q", ErrInvalidIP, raw)
	}
	addr, err := netip.ParseAddr(raw)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("%w: %q: %v", ErrInvalidIP, raw, err)
	}
	return NormalizePublicAddr(addr)
}

// IsPublicIP reports whether addr is a strictly public unicast address.
func IsPublicIP(addr netip.Addr) bool {
	_, err := NormalizePublicAddr(addr)
	return err == nil
}

// ValidatePublicIP is the error-returning form of IsPublicIP.
func ValidatePublicIP(addr netip.Addr) error {
	_, err := NormalizePublicAddr(addr)
	return err
}

// ParsePublicAddrPort parses "ip:port" and requires the address to be public
// and the port to be non-zero.
func ParsePublicAddrPort(raw string) (netip.AddrPort, error) {
	if strings.TrimSpace(raw) != raw || raw == "" {
		return netip.AddrPort{}, fmt.Errorf("%w: %q", ErrInvalidIP, raw)
	}
	ap, err := netip.ParseAddrPort(raw)
	if err != nil {
		return netip.AddrPort{}, fmt.Errorf("%w: %q: %v", ErrInvalidIP, raw, err)
	}
	if ap.Port() == 0 {
		return netip.AddrPort{}, fmt.Errorf("%w: zero port", ErrInvalidIP)
	}
	addr, err := NormalizePublicAddr(ap.Addr())
	if err != nil {
		return netip.AddrPort{}, err
	}
	return netip.AddrPortFrom(addr, ap.Port()), nil
}

// NormalizePublicAddrPort canonicalizes both the address and the port.
func NormalizePublicAddrPort(ap netip.AddrPort) (netip.AddrPort, error) {
	if !ap.IsValid() || ap.Port() == 0 {
		return netip.AddrPort{}, fmt.Errorf("%w: invalid address/port", ErrInvalidIP)
	}
	addr, err := NormalizePublicAddr(ap.Addr())
	if err != nil {
		return netip.AddrPort{}, err
	}
	return netip.AddrPortFrom(addr, ap.Port()), nil
}
