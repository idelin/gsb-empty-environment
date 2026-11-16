// Package flowspec validates and installs BGP Flow Specification rules
// according to the origin-authentication rules in RFC 8955 section 6.
//
// A Flow Specification rule learned from a BGP neighbor is installed only when:
//
//   - the NLRI components are encoded in strictly ascending type order, and
//   - for rules carrying a destination-prefix component, the best unicast
//     route covering that prefix is reachable through the same neighbor AS
//     (or is an internal best path), and no more-specific unicast route
//     reachable through another neighbor AS exists inside the prefix.
//
// Rules that fail validation are never installed and are reported through the
// configured Alerter.
package flowspec

import (
	"errors"
	"net/netip"
)

// AFI values carried in the FlowSpec SAFI NLRI (RFC 3232 address family
// numbers).
const (
	AFIIPv4 uint16 = 1
	AFIIPv6 uint16 = 2
)

// NLRI component types defined by RFC 8955 section 4.2.2 and RFC 8956
// (IPv6 flow label).
const (
	TypeDestinationPrefix ComponentType = 1
	TypeSourcePrefix      ComponentType = 2
	TypeIPProtocol        ComponentType = 3
	TypePort              ComponentType = 4
	TypeDestinationPort   ComponentType = 5
	TypeSourcePort        ComponentType = 6
	TypeICMPType          ComponentType = 7
	TypeICMPCode          ComponentType = 8
	TypeTCPFlags          ComponentType = 9
	TypePacketLength      ComponentType = 10
	TypeDSCP              ComponentType = 11
	TypeFragment          ComponentType = 12
	TypeFlowLabel         ComponentType = 13
)

// ComponentType identifies a FlowSpec NLRI component.
type ComponentType uint8

// Component is one NLRI component. Value holds the on-wire component value,
// exactly as encoded after the type/length header: for match components it
// begins with the operator byte defined by RFC 8955 section 4.2.3; for a
// prefix component it is the prefix-encoding payload (see Prefix).
type Component struct {
	Type  ComponentType
	Value []byte
}

// Prefix returns the IP prefix carried by a destination- or source-prefix
// component under the given address family.
func (c Component) Prefix(afi uint16) (netip.Prefix, error) {
	if c.Type != TypeDestinationPrefix && c.Type != TypeSourcePrefix {
		return netip.Prefix{}, errors.New("flowspec: component is not an IP prefix component")
	}
	return decodePrefix(c.Value, afi)
}

// NLRI is the decoded body of a BGP FlowSpec NLRI: an ordered list of
// components.
type NLRI struct {
	AFI        uint16
	Components []Component
}

// DestinationPrefix returns the prefix of the TypeDestinationPrefix component
// and reports whether the NLRI carries one.
func (n NLRI) DestinationPrefix() (netip.Prefix, bool, error) {
	for _, comp := range n.Components {
		if comp.Type == TypeDestinationPrefix {
			pfx, err := comp.Prefix(n.AFI)
			return pfx, true, err
		}
	}
	return netip.Prefix{}, false, nil
}

// Rule is a Flow Specification rule learned from a BGP neighbor.
type Rule struct {
	// Key identifies the rule for install/withdraw bookkeeping, typically the
	// wire NLRI or an NLRI digest.
	Key string

	// NLRI is the parsed FlowSpec NLRI.
	NLRI NLRI

	// NeighborAS is the AS of the BGP neighbor that advertised the rule
	// (the remote peer AS; 0 is not a valid advertising AS).
	NeighborAS ASN
}

// ASN is a 4-byte BGP autonomous system number.
type ASN uint32

// UnicastRoute is the best unicast route for one prefix as seen by the
// routing process.
type UnicastRoute struct {
	// Prefix is the route prefix.
	Prefix netip.Prefix

	// OriginAS is the AS that originated the route (leftmost AS in the
	// AS_PATH).
	OriginAS ASN

	// PeerAS is the AS of the BGP neighbor from which the best path was
	// learned. For routes originated locally this is the local AS.
	PeerAS ASN

	// Internal marks an iBGP-learned or locally originated best path. RFC 8955
	// section 6 relaxes the same-neighbor-AS requirement for internal paths.
	Internal bool
}

// RIB is the unicast routing information base consulted during validation.
// Implementations return the current best path only.
type RIB interface {
	// BestRoute returns the longest-prefix-match unicast route covering p.
	// ok is false when no route covers the prefix.
	BestRoute(p netip.Prefix) (route UnicastRoute, ok bool)

	// MoreSpecificRoutes returns every best route whose prefix is strictly
	// more specific than p (contained in p, with a longer prefix length).
	MoreSpecificRoutes(p netip.Prefix) []UnicastRoute
}
