package icmpassoc

import (
	"errors"
	"net/netip"
)

// IPVersion selects an address family. It is carried explicitly so an
// IPv4-mapped IPv6 address can never be silently treated as IPv4.
type IPVersion uint8

const (
	// Version4 is the IPv4 address family.
	Version4 IPVersion = 4
	// Version6 is the IPv6 address family.
	Version6 IPVersion = 6
)

// Protocol is the IP protocol number taken from the quoted original header
// (for example TCP or UDP).
type Protocol uint8

const (
	// ProtoICMP is the IPv4 ICMP protocol number (RFC 792).
	ProtoICMP Protocol = 1
	// ProtoTCP is the TCP protocol number (RFC 9293).
	ProtoTCP Protocol = 6
	// ProtoUDP is the UDP protocol number (RFC 768).
	ProtoUDP Protocol = 17
	// ProtoICMPv6 is the IPv6 ICMP protocol number (RFC 4443).
	ProtoICMPv6 Protocol = 58
)

// Endpoint is one side of a transport flow. The Version field makes the
// address family explicit and fixed; Addr must belong to that family.
type Endpoint struct {
	Version IPVersion
	Addr    netip.Addr
	Port    uint16
}

// FlowKey identifies a transport connection by its full four (or five)
// tuple. Both endpoints must use the same fixed IP version.
type FlowKey struct {
	Version  IPVersion
	Protocol Protocol
	Local    Endpoint
	Remote   Endpoint
}

var (
	// ErrCannotAssociate wraps every failure to bind an ICMP error to an
	// active connection. Insufficient quoted bytes and mismatches all use it.
	ErrCannotAssociate = errors.New("icmpassoc: ICMP error cannot be associated with an active connection")
	// ErrVersionMismatch indicates a fixed-version rule was asked to
	// translate an address from a different address family.
	ErrVersionMismatch = errors.New("icmpassoc: address mapper version mismatch")
	// ErrAlreadyRegistered is returned when a flow is registered while an
	// active connection with the same tuple still exists.
	ErrAlreadyRegistered = errors.New("icmpassoc: flow already registered")
	// ErrNotRegistered is returned when closing an unknown connection.
	ErrNotRegistered = errors.New("icmpassoc: connection not registered")
	// ErrNoMapper is returned when the host has no translation rule for the
	// address family of the quoted datagram.
	ErrNoMapper = errors.New("icmpassoc: no address mapper for IP version")
)

// tupleKey is the normalized internal map key.
type tupleKey struct {
	version  IPVersion
	protocol Protocol
	local    Endpoint
	remote   Endpoint
}

func keyOf(k FlowKey) (tupleKey, error) {
	if err := k.validate(); err != nil {
		return tupleKey{}, err
	}
	return tupleKey{
		version:  k.Version,
		protocol: k.Protocol,
		local:    normalize(k.Local),
		remote:   normalize(k.Remote),
	}, nil
}

func (k FlowKey) validate() error {
	if k.Local.Version != k.Version || k.Remote.Version != k.Version {
		return errors.New("icmpassoc: endpoint versions do not match flow version")
	}
	if !validAddr(k.Version, k.Local.Addr) || !validAddr(k.Version, k.Remote.Addr) {
		return errors.New("icmpassoc: address does not match fixed IP version")
	}
	return nil
}

func validAddr(version IPVersion, addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}
	switch version {
	case Version4:
		return addr.Is4()
	case Version6:
		return addr.Is6() && !addr.Is4In6()
	default:
		return false
	}
}

func normalize(ep Endpoint) Endpoint {
	ep.Addr = ep.Addr.Unmap()
	return ep
}
