// Package dad implements RFC 7527 enhanced Duplicate Address Detection
// decisions for the ND receive path of an IPv6 interface configuration
// host.
package dad

import (
	"errors"
	"fmt"
	"net/netip"
)

const (
	icmpTypeNS = 135
	icmpTypeNA = 136

	optTypeNonce = 14

	// NonceLen is the RFC 7527 nonce size in octets.
	NonceLen = 6
)

// Message is a parsed ND message relevant to DAD.
type Message struct {
	IsNA     bool
	Src      netip.Addr
	HopLimit int
	Target   netip.Addr
	Nonce    []byte // nil when the message carries no nonce option
}

// ErrNotND is returned when a packet is neither an NS nor an NA.
var ErrNotND = errors.New("dad: not an NS/NA message")

// Parse decodes an ICMPv6 Neighbor Solicitation or Advertisement received
// on the interface. src and hopLimit come from the IPv6 header, which the
// host stack has already validated (including the ICMPv6 checksum).
func Parse(src netip.Addr, hopLimit int, pkt []byte) (Message, error) {
	if len(pkt) < 24 {
		return Message{}, ErrNotND
	}
	var m Message
	switch pkt[0] {
	case icmpTypeNS:
	case icmpTypeNA:
		m.IsNA = true
	default:
		return Message{}, ErrNotND
	}
	if pkt[1] != 0 {
		return Message{}, fmt.Errorf("dad: unexpected ICMPv6 code %d", pkt[1])
	}
	m.Src = src
	m.HopLimit = hopLimit
	var target [16]byte
	copy(target[:], pkt[8:24])
	m.Target = netip.AddrFrom16(target)

	opts := pkt[24:]
	for len(opts) >= 2 {
		otype, olen := opts[0], int(opts[1])
		if olen == 0 {
			return Message{}, errors.New("dad: zero-length ND option")
		}
		n := olen * 8
		if len(opts) < n {
			return Message{}, errors.New("dad: truncated ND option")
		}
		if otype == optTypeNonce {
			m.Nonce = append([]byte(nil), opts[2:n]...)
		}
		opts = opts[n:]
	}
	return m, nil
}

// marshalNS builds a DAD probe: an NS with a nonce option. The source
// address (unspecified) and the ICMPv6 checksum are filled in by the host
// stack when the probe is sent.
func marshalNS(target netip.Addr, nonce []byte) []byte {
	pkt := make([]byte, 4+4+16+8)
	pkt[0] = icmpTypeNS
	t := target.As16()
	copy(pkt[8:24], t[:])
	pkt[24] = optTypeNonce
	pkt[25] = 1 // (2-byte header + 6-byte nonce) / 8 octets
	copy(pkt[26:32], nonce)
	return pkt
}
