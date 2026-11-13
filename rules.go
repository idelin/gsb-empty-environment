package netsec

import "net/netip"

var localPrefixRoots = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("::1/128"),
}

// DefaultLANPrefixes returns the local-only prefixes used when LAN access is
// enabled without an explicit list from the caller.
func DefaultLANPrefixes() []netip.Prefix {
	return append([]netip.Prefix(nil), localPrefixRoots...)
}

func isLANPrefix(prefix netip.Prefix) bool {
	if !prefix.IsValid() {
		return false
	}
	prefix = prefix.Masked()
	if prefix.Addr().Is4In6() {
		return false
	}
	for _, root := range localPrefixRoots {
		if root.Addr().Is4() == prefix.Addr().Is4() &&
			root.Bits() <= prefix.Bits() &&
			root.Contains(prefix.Addr()) {
			return true
		}
	}
	return false
}

// IsAnyDestination reports whether the rule applies to every destination.
func (r Rule) IsAnyDestination() bool {
	return !r.Destination.IsValid()
}

// Allows reports whether this ruleset permits the destination on the interface.
func (rs Ruleset) Allows(iface string, destination netip.Addr) bool {
	if !destination.IsValid() {
		return false
	}
	destination = destination.Unmap()
	for _, rule := range rs.Rules {
		if rule.Interface != iface {
			continue
		}
		if rule.IsAnyDestination() ||
			rule.Destination.Contains(destination) {
			return true
		}
	}
	return false
}

// AllowsDNS reports whether DNS traffic may leave the machine.
func (rs Ruleset) AllowsDNS() bool {
	return rs.DNS == DNSTunnel || rs.DNS == DNSDirect
}
