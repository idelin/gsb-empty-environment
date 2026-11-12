package netsec

import (
	"encoding/binary"
	"net/netip"
)

const (
	// TransactionIDSize is the size of a STUN transaction ID in bytes.
	TransactionIDSize = 12

	xorPortMask     = 0x2112
	addressFamilyV4 = 0x0001
	addressFamilyV6 = 0x0002
)

var magicCookieBytes = [4]byte{0x21, 0x12, 0xA4, 0x42}

// DecodeXORPeerAddress decodes an XOR-PEER-ADDRESS attribute value as defined
// by RFC 8656 (which uses the XOR-MAPPED-ADDRESS encoding from RFC 5389).
//
// The transaction ID is the 96-bit ID from the STUN message carrying the
// attribute. It is needed for IPv6 addresses; IPv4 addresses are XORed with
// the magic cookie only. The returned address preserves the address family
// encoded in the attribute.
func DecodeXORPeerAddress(value []byte, transactionID [TransactionIDSize]byte) (netip.AddrPort, error) {
	if len(value) != 8 && len(value) != 20 {
		return netip.AddrPort{}, badRequest("invalid XOR-PEER-ADDRESS length")
	}

	family := binary.BigEndian.Uint16(value[0:2])
	port := binary.BigEndian.Uint16(value[2:4]) ^ xorPortMask

	switch family {
	case addressFamilyV4:
		if len(value) != 8 {
			return netip.AddrPort{}, badRequest("IPv4 XOR-PEER-ADDRESS must be 8 bytes")
		}

		var raw [4]byte
		for i := range raw {
			raw[i] = value[4+i] ^ magicCookieBytes[i]
		}
		return netip.AddrPortFrom(netip.AddrFrom4(raw), port), nil

	case addressFamilyV6:
		if len(value) != 20 {
			return netip.AddrPort{}, badRequest("IPv6 XOR-PEER-ADDRESS must be 20 bytes")
		}

		var raw [16]byte
		for i := 0; i < 4; i++ {
			raw[i] = value[4+i] ^ magicCookieBytes[i]
		}
		for i := 4; i < 16; i++ {
			raw[i] = value[4+i] ^ transactionID[i-4]
		}
		return netip.AddrPortFrom(netip.AddrFrom16(raw), port), nil

	default:
		return netip.AddrPort{}, badRequest("unsupported XOR-PEER-ADDRESS address family")
	}
}

// DecodeXORPeerAddressBytes is a convenience wrapper for callers that keep the
// transaction ID as a byte slice. The slice must contain exactly 12 bytes.
func DecodeXORPeerAddressBytes(value, transactionID []byte) (netip.AddrPort, error) {
	if len(transactionID) != TransactionIDSize {
		return netip.AddrPort{}, badRequest("invalid transaction ID length")
	}

	var txID [TransactionIDSize]byte
	copy(txID[:], transactionID)
	return DecodeXORPeerAddress(value, txID)
}

// PeerForbidden reports whether peer is contained in any of the caller-supplied
// forbidden prefixes. IPv4-mapped IPv6 addresses are unmapped before the
// comparison so an IPv4 prefix cannot be bypassed by encoding the address as
// an IPv6 address.
func PeerForbidden(peer netip.Addr, forbidden []netip.Prefix) bool {
	if !peer.IsValid() {
		return true
	}

	candidates := []netip.Addr{peer}
	if unmapped := peer.Unmap(); unmapped != peer {
		candidates = append(candidates, unmapped)
	}

	for _, prefix := range forbidden {
		if !prefix.IsValid() {
			continue
		}
		for _, candidate := range candidates {
			if prefix.Contains(candidate) {
				return true
			}
		}
	}
	return false
}

// CheckPeerAllowed returns a 403 ProtocolError when peer is in a forbidden
// prefix, and nil otherwise.
func CheckPeerAllowed(peer netip.AddrPort, forbidden []netip.Prefix) error {
	if PeerForbidden(peer.Addr(), forbidden) {
		return forbiddenPeer(peer.String())
	}
	return nil
}

// DefaultForbiddenPrefixes returns a conservative set covering loopback,
// private, link-local, carrier-grade NAT, unique-local, and the common cloud
// metadata endpoints. Callers remain responsible for supplying the policy that
// matches their deployment.
func DefaultForbiddenPrefixes() []netip.Prefix {
	return []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"),
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("100.64.0.0/10"),
		netip.MustParsePrefix("127.0.0.0/8"),
		netip.MustParsePrefix("169.254.0.0/16"),
		netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.168.0.0/16"),
		netip.MustParsePrefix("::/128"),
		netip.MustParsePrefix("::1/128"),
		netip.MustParsePrefix("fc00::/7"),
		netip.MustParsePrefix("fe80::/10"),
	}
}
