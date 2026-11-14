package netsec

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

var metadataAddresses = map[netip.Addr]struct{}{
	// AWS, Azure, GCP, DigitalOcean, OpenStack, and most other providers.
	netip.MustParseAddr("169.254.169.254"): {},
	// Amazon ECS task credentials.
	netip.MustParseAddr("169.254.170.2"): {},
	// Amazon ECS task metadata / credentials variants.
	netip.MustParseAddr("169.254.170.23"): {},
	// AWS Time Sync and metadata DNS endpoints.
	netip.MustParseAddr("169.254.169.123"): {},
	netip.MustParseAddr("169.254.169.253"): {},
	// Alibaba Cloud.
	netip.MustParseAddr("100.100.100.200"): {},
	// Oracle Cloud Infrastructure.
	netip.MustParseAddr("192.0.0.192"): {},
	// Azure platform / WireServer endpoint.
	netip.MustParseAddr("168.63.129.16"): {},
	// AWS IMDS over IPv6.
	netip.MustParseAddr("fd00:ec2::254"): {},
	netip.MustParseAddr("fd00:ec2::23"):  {},
}

var metadataHostnames = map[string]struct{}{
	"metadata":                     {},
	"metadata.google.internal":     {},
	"metadata.azure.internal":      {},
	"instance-data":                {},
	"instance-data.ec2.internal":   {},
	"metadata.tencentyun.com":      {},
	"metadata.platformequinix.com": {},
	"metadata.packet.net":          {},
	"metadata.digitalocean.com":    {},
}

var linkLocalIPv4 = netip.MustParsePrefix("169.254.0.0/16")
var linkLocalIPv6 = netip.MustParsePrefix("fe80::/10")

var forbiddenIPv6Prefixes = mustPrefixes(
	// IPv4-compatible addresses and IPv4 translation/encapsulation prefixes
	// can hide a link-local or metadata IPv4 address from a literal check.
	"64:ff9b::/96",
	"2001:0::/32",
	"2002::/16",
)

func normalizeHost(host string) string {
	host = strings.ToLower(host)
	host = strings.TrimSuffix(host, ".")
	return host
}

func parseIPLiteral(host string) (netip.Addr, bool) {
	if host == "" {
		return netip.Addr{}, false
	}

	if addr, err := netip.ParseAddr(host); err == nil {
		if addr.Zone() != "" {
			// Zones are only meaningful for scoped/link-local addresses and
			// make allowlist comparison ambiguous.
			return netip.Addr{}, true
		}
		if addr.Is4In6() {
			addr = addr.Unmap()
		}
		return addr, true
	}

	if addr, ok := parseLegacyIPv4(host); ok {
		return addr, true
	}

	return netip.Addr{}, false
}

// parseLegacyIPv4 accepts the inet_aton forms accepted by many DNS resolvers
// and URL libraries (one to four components, decimal/octal/hex). Rejecting
// these as ordinary hostnames closes a common pre-resolution SSRF bypass.
func parseLegacyIPv4(host string) (netip.Addr, bool) {
	parts := strings.Split(host, ".")
	if len(parts) < 1 || len(parts) > 4 {
		return netip.Addr{}, false
	}

	values := make([]uint64, len(parts))
	for i, part := range parts {
		if part == "" {
			return netip.Addr{}, false
		}
		value, ok := parseLegacyPart(part)
		if !ok {
			return netip.Addr{}, false
		}
		values[i] = value
	}

	var value uint64
	switch len(values) {
	case 1:
		value = values[0]
	case 2:
		if values[0] > 0xff || values[1] > 0xffffff {
			return netip.Addr{}, false
		}
		value = values[0]<<24 | values[1]
	case 3:
		if values[0] > 0xff || values[1] > 0xff || values[2] > 0xffff {
			return netip.Addr{}, false
		}
		value = values[0]<<24 | values[1]<<16 | values[2]
	case 4:
		for _, v := range values {
			if v > 0xff {
				return netip.Addr{}, false
			}
		}
		value = values[0]<<24 | values[1]<<16 | values[2]<<8 | values[3]
	}

	if value > 0xffffffff {
		return netip.Addr{}, false
	}
	return netip.AddrFrom4([4]byte{
		byte(value >> 24),
		byte(value >> 16),
		byte(value >> 8),
		byte(value),
	}), true
}

func parseLegacyPart(part string) (uint64, bool) {
	base := 10
	digits := part
	switch {
	case len(part) > 2 && (strings.HasPrefix(part, "0x") || strings.HasPrefix(part, "0X")):
		base = 16
		digits = part[2:]
	case len(part) > 1 && part[0] == '0':
		base = 8
		digits = part[1:]
	}
	if digits == "" {
		return 0, false
	}
	value, err := strconv.ParseUint(digits, base, 32)
	if err != nil {
		return 0, false
	}
	return value, true
}

func canonicalAddrString(addr netip.Addr) string {
	if addr.Is4In6() {
		addr = addr.Unmap()
	}
	return addr.String()
}

func isMetadataAddress(addr netip.Addr) bool {
	addr = addr.Unmap()
	_, ok := metadataAddresses[addr]
	return ok
}

// alwaysForbidden reports addresses that must never be contacted, even when a
// private IP literal was explicitly put in an allowlist.
func alwaysForbidden(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() {
		return true
	}
	if isMetadataAddress(addr) {
		return true
	}
	if addr.IsUnspecified() || addr.IsMulticast() {
		return true
	}
	if addr.Is4() {
		a := addr.As4()
		if a == [4]byte{255, 255, 255, 255} {
			return true
		}
		return linkLocalIPv4.Contains(addr)
	}
	if addr.IsLinkLocalUnicast() || linkLocalIPv6.Contains(addr) {
		return true
	}
	// IPv4-compatible IPv6 addresses (the deprecated ::/96 range) can encode
	// an IPv4 metadata address. Check the embedded address while still
	// permitting an explicitly allowlisted ::1 loopback literal.
	if isIPv4Compatible(addr) {
		a := addr.As16()
		embedded := netip.AddrFrom4([4]byte{a[12], a[13], a[14], a[15]})
		if alwaysForbidden(embedded) {
			return true
		}
	}
	for _, prefix := range forbiddenIPv6Prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// publiclyRoutable is deliberately stricter than the metadata/link-local
// checks. Hostnames must not resolve into private, loopback, documentation, or
// otherwise special-use address space unless the caller opts in.
func publiclyRoutable(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || alwaysForbidden(addr) || addr.IsLoopback() || addr.IsPrivate() {
		return false
	}
	if !addr.IsGlobalUnicast() {
		return false
	}

	if addr.Is4() {
		for _, prefix := range nonPublicIPv4Prefixes {
			if prefix.Contains(addr) {
				return false
			}
		}
		return true
	}

	for _, prefix := range nonPublicIPv6Prefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

var nonPublicIPv4Prefixes = mustPrefixes(
	"0.0.0.0/8",
	"100.64.0.0/10",
	"192.0.0.0/24",
	"192.0.2.0/24",
	"198.18.0.0/15",
	"198.51.100.0/24",
	"203.0.113.0/24",
	"240.0.0.0/4",
)

var nonPublicIPv6Prefixes = mustPrefixes(
	"64:ff9b::/96",
	"::/96",
	"100::/64",
	"2001:db8::/32",
	"2001:10::/28",
	"2001:20::/28",
)

func isIPv4Compatible(addr netip.Addr) bool {
	if !addr.Is6() || addr.Is4In6() {
		return false
	}
	a := addr.As16()
	for i := 0; i < 12; i++ {
		if a[i] != 0 {
			return false
		}
	}
	return true
}

func mustPrefixes(values ...string) []netip.Prefix {
	prefixes := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		prefixes = append(prefixes, netip.MustParsePrefix(value))
	}
	return prefixes
}

func validateResolvedAddress(addr netip.Addr, allowLoopback, allowPrivate bool) error {
	addr = addr.Unmap()
	if alwaysForbidden(addr) {
		return fmt.Errorf("%w: %s is a link-local, metadata, multicast, or unspecified address", ErrBlockedAddress, canonicalAddrString(addr))
	}
	if publiclyRoutable(addr) {
		return nil
	}
	if addr.IsLoopback() && allowLoopback {
		return nil
	}
	if addr.IsPrivate() && allowPrivate {
		return nil
	}
	return fmt.Errorf("%w: %s is not a public unicast address", ErrBlockedAddress, canonicalAddrString(addr))
}
