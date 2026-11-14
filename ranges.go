package netsec

import (
	"net/netip"
	"strings"
)

type prefixRule struct {
	prefix   netip.Prefix
	category Category
	reason   string
}

func mustPrefix(text string) netip.Prefix {
	return netip.MustParsePrefix(text)
}

var ipv4Rules = []prefixRule{
	{mustPrefix("0.0.0.0/8"), CategoryReserved, "0.0.0.0/8 this-network block"},
	{mustPrefix("10.0.0.0/8"), CategoryPrivate, "10.0.0.0/8 private-use block"},
	{mustPrefix("100.64.0.0/10"), CategoryShared, "100.64.0.0/10 shared address space"},
	{mustPrefix("127.0.0.0/8"), CategoryLoopback, "127.0.0.0/8 loopback block"},
	{mustPrefix("169.254.0.0/16"), CategoryLinkLocal, "169.254.0.0/16 link-local block"},
	{mustPrefix("172.16.0.0/12"), CategoryPrivate, "172.16.0.0/12 private-use block"},
	{mustPrefix("192.0.0.0/24"), CategoryReserved, "192.0.0.0/24 IETF protocol assignments"},
	{mustPrefix("192.0.2.0/24"), CategoryDocumentation, "192.0.2.0/24 documentation block"},
	{mustPrefix("192.88.99.0/24"), CategoryReserved, "192.88.99.0/24 6to4 relay anycast block"},
	{mustPrefix("192.168.0.0/16"), CategoryPrivate, "192.168.0.0/16 private-use block"},
	{mustPrefix("198.18.0.0/15"), CategoryBenchmark, "198.18.0.0/15 benchmarking block"},
	{mustPrefix("198.51.100.0/24"), CategoryDocumentation, "198.51.100.0/24 documentation block"},
	{mustPrefix("203.0.113.0/24"), CategoryDocumentation, "203.0.113.0/24 documentation block"},
	{mustPrefix("224.0.0.0/4"), CategoryMulticast, "224.0.0.0/4 multicast block"},
	{mustPrefix("240.0.0.0/4"), CategoryReserved, "240.0.0.0/4 reserved block"},
}

var ipv6Rules = []prefixRule{
	{mustPrefix("100::/64"), CategoryDiscard, "100::/64 discard-only block"},
	{mustPrefix("2001:2::/48"), CategoryBenchmark, "2001:2::/48 benchmarking block"},
	{mustPrefix("2001:db8::/32"), CategoryDocumentation, "2001:db8::/32 documentation block"},
	{mustPrefix("2001::/23"), CategoryReserved, "2001::/23 IETF protocol assignments"},
	{mustPrefix("2002::/16"), CategoryReserved, "2002::/16 6to4 block"},
	{mustPrefix("3ffe::/16"), CategoryReserved, "3ffe::/16 former 6bone block"},
	{mustPrefix("3fff::/20"), CategoryDocumentation, "3fff::/20 documentation block"},
	{mustPrefix("5f00::/16"), CategoryReserved, "5f00::/16 segment-routing block"},
	{mustPrefix("fc00::/7"), CategoryUniqueLocal, "fc00::/7 unique-local block"},
	{mustPrefix("fe80::/10"), CategoryLinkLocal, "fe80::/10 link-local block"},
	{mustPrefix("fec0::/10"), CategorySiteLocal, "fec0::/10 deprecated site-local block"},
	{mustPrefix("ff00::/8"), CategoryMulticast, "ff00::/8 multicast block"},
}

var (
	ipv4Unspecified = netip.MustParseAddr("0.0.0.0")
	ipv4Broadcast   = netip.MustParseAddr("255.255.255.255")

	// These two anycast addresses are the globally reachable exceptions in
	// 192.0.0.0/24.
	ipv4PCPAnycast  = netip.MustParseAddr("192.0.0.9")
	ipv4TURNAnycast = netip.MustParseAddr("192.0.0.10")

	ipv6GlobalUnicast = mustPrefix("2000::/3")

	// Globally reachable exceptions inside 2001::/23.
	ipv6PCPAnycast  = mustPrefix("2001:1::1/128")
	ipv6TURNAnycast = mustPrefix("2001:1::2/128")
	ipv6AMT         = mustPrefix("2001:3::/32")
	ipv6AS112       = mustPrefix("2001:4:112::/48")
)

func classifyIP(addr netip.Addr) (Category, string) {
	if !addr.IsValid() {
		return CategoryReserved, "invalid address"
	}
	if addr.Is4() {
		return classifyIPv4(addr)
	}
	return classifyIPv6(addr)
}

func classifyIPv4(addr netip.Addr) (Category, string) {
	switch addr {
	case ipv4Unspecified:
		return CategoryUnspecified, "0.0.0.0/32 unspecified address"
	case ipv4Broadcast:
		return CategoryReserved, "255.255.255.255/32 limited broadcast"
	case ipv4PCPAnycast, ipv4TURNAnycast:
		return CategoryPublic, "globally reachable IPv4 anycast"
	}

	for _, rule := range ipv4Rules {
		if rule.prefix.Contains(addr) {
			return rule.category, rule.reason
		}
	}
	return CategoryPublic, "global IPv4 unicast"
}

func classifyIPv6(addr netip.Addr) (Category, string) {
	switch addr {
	case netip.IPv6Unspecified():
		return CategoryUnspecified, "::/128 unspecified address"
	case netip.IPv6Loopback():
		return CategoryLoopback, "::1/128 loopback address"
	}

	// Do not let the 2001::/23 rule hide the few globally reachable
	// protocol anycast assignments.
	for _, prefix := range []netip.Prefix{ipv6PCPAnycast, ipv6TURNAnycast, ipv6AMT, ipv6AS112} {
		if prefix.Contains(addr) {
			return CategoryPublic, "globally reachable IPv6 anycast/assignment"
		}
	}

	for _, rule := range ipv6Rules {
		if rule.prefix.Contains(addr) {
			return rule.category, rule.reason
		}
	}
	if ipv6GlobalUnicast.Contains(addr) {
		return CategoryPublic, "global IPv6 unicast"
	}
	return CategoryReserved, "outside 2000::/3 global unicast"
}

// effectiveTargets reduces an address to the endpoint(s) that are actually
// reached. Unknown address forms are returned unchanged so the normal range
// rules can classify them.
func effectiveTargets(addr netip.Addr) ([]effectiveTarget, Mechanism) {
	if !addr.IsValid() {
		return []effectiveTarget{{addr: addr, label: "invalid"}}, MechanismNone
	}
	if addr.Is4() {
		return []effectiveTarget{{addr: addr}}, MechanismNone
	}

	b := addr.As16()

	// IPv4-mapped: 80 zero bits, 16 one bits, then the IPv4 address.
	if b[0] == 0 && b[1] == 0 && b[2] == 0 && b[3] == 0 &&
		b[4] == 0 && b[5] == 0 && b[6] == 0 && b[7] == 0 &&
		b[8] == 0 && b[9] == 0 && b[10] == 0xff && b[11] == 0xff {
		return []effectiveTarget{{
			addr:  netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}),
			label: "IPv4-mapped target",
		}}, MechanismIPv4Mapped
	}

	// IPv4-translated: ::ffff:0:a.b.c.d (SIIT). The address still carries an
	// IPv4 destination even though it is not a mapped 4-in-6 address.
	if b[0] == 0 && b[1] == 0 && b[2] == 0 && b[3] == 0 &&
		b[4] == 0 && b[5] == 0 && b[6] == 0 && b[7] == 0 &&
		b[8] == 0xff && b[9] == 0xff && b[10] == 0 && b[11] == 0 {
		return []effectiveTarget{{
			addr:  netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}),
			label: "IPv4-translated target",
		}}, MechanismIPv4Translated
	}

	// IPv4-compatible: ::a.b.c.d. :: and ::1 are handled as their own
	// special addresses by the classifier.
	if b[0] == 0 && b[1] == 0 && b[2] == 0 && b[3] == 0 &&
		b[4] == 0 && b[5] == 0 && b[6] == 0 && b[7] == 0 &&
		b[8] == 0 && b[9] == 0 && b[10] == 0 && b[11] == 0 {
		if addr == netip.IPv6Unspecified() || addr == netip.IPv6Loopback() {
			return []effectiveTarget{{addr: addr}}, MechanismNone
		}
		return []effectiveTarget{{
			addr:  netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}),
			label: "IPv4-compatible target",
		}}, MechanismIPv4Compatible
	}

	// 6to4: 2002:V4V4:V4V4::/48. The relay decapsulates to the embedded
	// IPv4 address.
	if b[0] == 0x20 && b[1] == 0x02 {
		return []effectiveTarget{{
			addr:  netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]}),
			label: "6to4 embedded target",
		}}, Mechanism6to4
	}

	// Teredo: 2001:0000::/32. The client address is obfuscated with all
	// ones in the last 32 bits. The server address is also represented by
	// the address and is checked conservatively.
	if b[0] == 0x20 && b[1] == 0x01 && b[2] == 0 && b[3] == 0 {
		server := netip.AddrFrom4([4]byte{b[4], b[5], b[6], b[7]})
		client := netip.AddrFrom4([4]byte{
			b[12] ^ 0xff, b[13] ^ 0xff, b[14] ^ 0xff, b[15] ^ 0xff,
		})
		return []effectiveTarget{
			{addr: client, label: "Teredo client target"},
			{addr: server, label: "Teredo server target"},
		}, MechanismTeredo
	}

	// NAT64 well-known prefix, 64:ff9b::/96.
	if b[0] == 0x00 && b[1] == 0x64 && b[2] == 0xff && b[3] == 0x9b &&
		b[4] == 0 && b[5] == 0 && b[6] == 0 && b[7] == 0 &&
		b[8] == 0 && b[9] == 0 && b[10] == 0 && b[11] == 0 {
		return []effectiveTarget{{
			addr:  netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}),
			label: "NAT64 embedded target",
		}}, MechanismNAT64
	}

	// Local-use NAT64 prefix, 64:ff9b:1::/48. RFC 6052 places the IPv4
	// address around the reserved "u" octet for a 48-bit prefix.
	if b[0] == 0x00 && b[1] == 0x64 && b[2] == 0xff && b[3] == 0x9b &&
		b[4] == 0x00 && b[5] == 0x01 && b[8] == 0 {
		return []effectiveTarget{{
			addr:  netip.AddrFrom4([4]byte{b[6], b[7], b[9], b[10]}),
			label: "NAT64 embedded target",
		}}, MechanismNAT64
	}

	// ISATAP uses a 0000:5efe or 0200:5efe interface identifier followed
	// by the IPv4 address. Only unwrap it when the outer address is a real
	// public unicast address; documentation, protocol, link-local,
	// multicast, and private outer prefixes stay classified as themselves.
	if (b[8] == 0x00 || b[8] == 0x02) && b[9] == 0x00 && b[10] == 0x5e && b[11] == 0xfe {
		baseCategory, _ := classifyIPv6(addr)
		if baseCategory == CategoryPublic {
			return []effectiveTarget{{
				addr:  netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}),
				label: "ISATAP embedded target",
			}}, MechanismISATAP
		}
	}

	return []effectiveTarget{{addr: addr}}, MechanismNone
}

// ClassifyAddr classifies an already parsed address. The zone is retained in
// Result.Zone, but is removed before range comparisons.
func ClassifyAddr(addr netip.Addr) Result {
	zone := addr.Zone()
	unzoned := addr.WithZone("")
	result := Result{
		Input:        unzoned.String(),
		Source:       unzoned,
		SourceFamily: familyOf(unzoned),
		Zone:         zone,
	}
	if !unzoned.IsValid() {
		result.Category = CategoryReserved
		result.Scope = CategoryReserved
		result.Internal = true
		result.Reason = "invalid address"
		result.Targets = []netip.Addr{unzoned}
		return result
	}

	targets, mechanism := effectiveTargets(unzoned)
	result.Mechanism = mechanism
	result.Targets = make([]netip.Addr, 0, len(targets))

	category := CategoryUnknown
	reasons := make([]string, 0, len(targets))
	for i, target := range targets {
		result.Targets = append(result.Targets, target.addr)
		targetCategory, targetReason := classifyIP(target.addr)
		if i == 0 {
			category = targetCategory
		} else if category == CategoryPublic {
			// Keep the primary destination's category unless it is public and
			// another reachable endpoint is not.
			category = worseCategory(category, targetCategory)
		}
		if i == 0 {
			if target.label != "" {
				targetReason = target.label + ": " + targetReason
			}
		} else if target.label != "" {
			targetReason = target.label + ": " + targetReason
		}
		if targetReason != "" {
			reasons = append(reasons, targetReason)
		}
	}

	if len(targets) == 0 {
		category = CategoryReserved
		reasons = append(reasons, "no effective target")
	}

	result.Address = targets[0].addr
	result.Normalized = result.Address
	switch mechanism {
	case MechanismIPv4Mapped, MechanismIPv4Compatible:
		// These forms are treated as IPv4 destinations by dual-stack
		// sockets.
		result.DialAddr = result.Address
	default:
		if zone != "" {
			result.DialAddr = unzoned.WithZone(zone)
		} else {
			result.DialAddr = unzoned
		}
	}
	result.Family = familyOf(result.Address)
	result.Category = category
	result.Scope = category
	result.Public = category == CategoryPublic
	result.Internal = !result.Public
	result.Reason = strings.Join(reasons, "; ")
	return result
}
