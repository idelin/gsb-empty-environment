package netsec

import (
	"fmt"
	"net"
	"net/netip"
)

// PeerPolicy controls which relay peer addresses a TURN server may use.
// The zero value is strict: loopback, RFC 1918, IPv6 ULA, carrier-grade NAT,
// link-local, multicast, and reserved addresses are rejected.
//
// AllowUnsafe should only be used in controlled tests or for a server that is
// intentionally isolated from a larger network. It disables all checks.
type PeerPolicy struct {
	// AllowLoopback permits 127.0.0.0/8 and ::1.
	AllowLoopback bool

	// AllowPrivate permits RFC 1918, IPv6 ULA, and RFC 6598 (CGNAT) addresses.
	// Link-local and other unsafe ranges remain rejected.
	AllowPrivate bool

	// AllowUnsafe disables every peer-address restriction.
	AllowUnsafe bool

	// Allowed lists explicit CIDR exceptions. Matching addresses are accepted
	// even when they are loopback or private.
	Allowed []*net.IPNet
}

// DefaultPeerPolicy returns the strict default policy.
func DefaultPeerPolicy() PeerPolicy {
	return PeerPolicy{}
}

// Check verifies that ip is safe to use as a TURN relay peer.
func (p PeerPolicy) Check(ip net.IP) error {
	addr, ok := normalizedPeerIP(ip)
	if !ok {
		return fmt.Errorf("%w: invalid IP address", ErrForbiddenPeer)
	}

	if p.AllowUnsafe {
		return nil
	}

	for _, network := range p.Allowed {
		if network != nil && network.Contains(net.IP(addr.AsSlice())) {
			return nil
		}
	}

	if addr.IsLoopback() {
		if p.AllowLoopback {
			return nil
		}
		return fmt.Errorf("%w: loopback address %s", ErrForbiddenPeer, addr)
	}

	if isPrivatePeer(addr) {
		if p.AllowPrivate {
			return nil
		}
		return fmt.Errorf("%w: private address %s", ErrForbiddenPeer, addr)
	}

	if isOtherUnsafePeer(addr) {
		return fmt.Errorf("%w: non-public address %s", ErrForbiddenPeer, addr)
	}

	return nil
}

// IsPrivateOrLoopback reports whether ip is a loopback, private, carrier-grade
// NAT, link-local, multicast, unspecified, or reserved address. Invalid and
// nil addresses are treated as unsafe.
func IsPrivateOrLoopback(ip net.IP) bool {
	addr, ok := normalizedPeerIP(ip)
	if !ok {
		return true
	}
	return addr.IsLoopback() || isPrivatePeer(addr) || isOtherUnsafePeer(addr)
}

// CheckPeerAddress applies the strict default policy to ip.
func CheckPeerAddress(ip net.IP) error {
	return DefaultPeerPolicy().Check(ip)
}

// IsPeerAddressAllowed reports whether ip passes the strict default policy.
func IsPeerAddressAllowed(ip net.IP) bool {
	return CheckPeerAddress(ip) == nil
}

var (
	cgnatPrefix     = netip.MustParsePrefix("100.64.0.0/10") // RFC 6598
	benchmarkPrefix = netip.MustParsePrefix("198.18.0.0/15") // RFC 2544
	zeroPrefix      = netip.MustParsePrefix("0.0.0.0/8")
	reservedV4      = netip.MustParsePrefix("240.0.0.0/4")
	ipv4Compatible  = netip.MustParsePrefix("::/96")
	sixToFourPrefix = netip.MustParsePrefix("2002::/16")
	teredoPrefix    = netip.MustParsePrefix("2001::/32")
)

func normalizedPeerIP(ip net.IP) (netip.Addr, bool) {
	if len(ip) == 0 {
		return netip.Addr{}, false
	}

	// net.ParseIP returns a 16-byte representation for IPv4. Convert first so
	// IPv4-mapped IPv6 forms (for example ::ffff:127.0.0.1) cannot bypass the
	// IPv4 checks.
	if v4 := ip.To4(); v4 != nil {
		addr, ok := netip.AddrFromSlice(v4)
		if !ok {
			return netip.Addr{}, false
		}
		return addr.Unmap(), true
	}

	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

func isPrivatePeer(addr netip.Addr) bool {
	return addr.IsPrivate() || cgnatPrefix.Contains(addr)
}

func isOtherUnsafePeer(addr netip.Addr) bool {
	if !addr.IsGlobalUnicast() {
		return true
	}
	if addr.Is4() {
		return zeroPrefix.Contains(addr) || benchmarkPrefix.Contains(addr) || reservedV4.Contains(addr)
	}

	// IPv4-compatible IPv6 addresses are obsolete and should not be used as
	// relay targets.
	if ipv4Compatible.Contains(addr) {
		return true
	}

	bytes16 := addr.As16()
	if sixToFourPrefix.Contains(addr) {
		embedded := netip.AddrFrom4([4]byte{bytes16[2], bytes16[3], bytes16[4], bytes16[5]})
		return isEmbeddedUnsafeIPv4(embedded)
	}
	if teredoPrefix.Contains(addr) {
		// Teredo stores the client's IPv4 address in the final 32 bits,
		// complemented byte-wise.
		embedded := netip.AddrFrom4([4]byte{
			bytes16[12] ^ 0xff,
			bytes16[13] ^ 0xff,
			bytes16[14] ^ 0xff,
			bytes16[15] ^ 0xff,
		})
		return isEmbeddedUnsafeIPv4(embedded)
	}

	return false
}

func isEmbeddedUnsafeIPv4(addr netip.Addr) bool {
	return addr.IsLoopback() ||
		isPrivatePeer(addr) ||
		zeroPrefix.Contains(addr) ||
		benchmarkPrefix.Contains(addr) ||
		reservedV4.Contains(addr) ||
		isOtherUnsafePeer(addr)
}
