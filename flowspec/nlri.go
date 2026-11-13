// Package flowspec implements validation of BGP Flow Specification rules
// (RFC 8955) before they are installed into the forwarding plane.
//
// The validation procedure follows RFC 8955 Section 6: a rule is only
// eligible when the best-match unicast route for the embedded destination
// prefix was received from the same neighboring AS as the rule itself, and
// no more-specific unicast route inside that prefix was received from a
// different neighboring AS. Rules that fail validation are not installed
// and raise an alarm instead.
package flowspec

import (
	"errors"
	"fmt"
	"net/netip"
)

// ComponentType identifies the kind of a FlowSpec NLRI component as
// defined in RFC 8955, Section 4.2.
type ComponentType uint8

const (
	CompDestPrefix ComponentType = 1
	CompSrcPrefix  ComponentType = 2
	CompIPProtocol ComponentType = 3
	CompPort       ComponentType = 4
	CompDestPort   ComponentType = 5
	CompSrcPort    ComponentType = 6
	CompICMPType   ComponentType = 7
	CompICMPCode   ComponentType = 8
	CompTCPFlags   ComponentType = 9
	CompPktLength  ComponentType = 10
	CompDSCP       ComponentType = 11
	CompFragment   ComponentType = 12
	CompFlowLabel  ComponentType = 13
)

// ErrNoDestPrefix is returned when an NLRI carries no destination-prefix
// (type 1) component.
var ErrNoDestPrefix = errors.New("flowspec: NLRI has no destination prefix component")

// Component is a single FlowSpec NLRI component. Value holds the raw
// component payload as received on the wire (for prefix components this is
// the 1-octet length followed by the significant prefix octets).
type Component struct {
	Type  ComponentType
	Value []byte
}

// NLRI is the decoded Flow Specification NLRI: an ordered list of
// components. IPv6 indicates the address family of prefix components
// (false means IPv4).
type NLRI struct {
	IPv6       bool
	Components []Component
}

// ValidateOrder enforces RFC 8955, Section 4.2: components MUST appear in
// strictly ascending order of their type. An NLRI violating this is
// malformed and the rule must be discarded.
func (n NLRI) ValidateOrder() error {
	for i := 1; i < len(n.Components); i++ {
		prev, cur := n.Components[i-1].Type, n.Components[i].Type
		if cur <= prev {
			return fmt.Errorf("flowspec: NLRI components out of order: type %d follows type %d", cur, prev)
		}
	}
	return nil
}

// DestPrefix extracts the destination prefix carried by the type-1
// component of the NLRI.
func (n NLRI) DestPrefix() (netip.Prefix, error) {
	for _, c := range n.Components {
		if c.Type == CompDestPrefix {
			return DecodePrefixComponent(c.Value, n.IPv6)
		}
	}
	return netip.Prefix{}, ErrNoDestPrefix
}

// DecodePrefixComponent decodes the wire encoding of a prefix component:
// one length octet (in bits) followed by the significant prefix octets.
func DecodePrefixComponent(v []byte, is6 bool) (netip.Prefix, error) {
	if len(v) < 1 {
		return netip.Prefix{}, errors.New("flowspec: empty prefix component")
	}
	bits := int(v[0])
	addrLen := 4
	if is6 {
		addrLen = 16
	}
	if bits > addrLen*8 {
		return netip.Prefix{}, fmt.Errorf("flowspec: prefix length %d exceeds address size", bits)
	}
	if len(v) != 1+(bits+7)/8 {
		return netip.Prefix{}, fmt.Errorf("flowspec: prefix component has %d octets, want %d", len(v)-1, (bits+7)/8)
	}
	var addr netip.Addr
	if is6 {
		var b [16]byte
		copy(b[:], v[1:])
		addr = netip.AddrFrom16(b)
	} else {
		var b [4]byte
		copy(b[:], v[1:])
		addr = netip.AddrFrom4(b)
	}
	return netip.PrefixFrom(addr, bits).Masked(), nil
}

// EncodePrefixComponent encodes a prefix into the wire format used by
// prefix components.
func EncodePrefixComponent(p netip.Prefix) []byte {
	bits := p.Bits()
	out := make([]byte, 1+(bits+7)/8)
	out[0] = byte(bits)
	addr := p.Addr()
	if addr.Is4() {
		b := addr.As4()
		copy(out[1:], b[:])
	} else {
		b := addr.As16()
		copy(out[1:], b[:])
	}
	return out
}
