package netsec

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Family identifies the IP address family of a listening endpoint.
//
// IPv4-mapped IPv6 addresses (for example "::ffff:203.0.113.4") are carried
// on an IPv6 socket but encode an IPv4 endpoint. They are classified as
// FamilyIPv4 and reported through ListenID.Mapped rather than being silently
// treated as native IPv6.
type Family int

const (
	// FamilyUnspecified is reported only for the empty host, which has no
	// concrete address family until an EmptyPolicy resolves it.
	FamilyUnspecified Family = iota
	FamilyIPv4
	FamilyIPv6
)

// String returns the canonical name of the address family.
func (f Family) String() string {
	switch f {
	case FamilyIPv4:
		return "ipv4"
	case FamilyIPv6:
		return "ipv6"
	default:
		return "unspecified"
	}
}

// Class partitions listener hosts into four mutually exclusive categories.
//
// The empty host is deliberately kept distinct: it is not a wildcard. A
// wildcard is an explicit unspecified literal address ("0.0.0.0" or "::"),
// while the empty host carries no decision at all and must be resolved by an
// EmptyPolicy before a grant can be issued.
type Class int

const (
	ClassEmpty Class = iota
	ClassWildcard
	ClassLoopback
	ClassLiteral
)

// String returns the canonical name of the address class.
func (c Class) String() string {
	switch c {
	case ClassWildcard:
		return "wildcard"
	case ClassLoopback:
		return "loopback"
	case ClassLiteral:
		return "literal"
	default:
		return "empty"
	}
}

// ListenID is a parsed, canonical listening endpoint identifier.
//
// IP holds the canonical, unmapped address: an IPv4-mapped IPv6 input is
// reduced to its embedded IPv4 address and Mapped is set to true. Port is
// the requested port; zero means the driver must allocate one.
type ListenID struct {
	Family Family
	Class  Class
	IP     net.IP
	Port   int

	// Mapped reports that the original literal was an IPv4-mapped IPv6
	// address (RFC 4291, section 2.5.5.2).
	Mapped bool

	// Empty reports that the original host was the empty string. It remains
	// unresolved and carries neither family, class-as-wildcard nor IP.
	Empty bool
}

// String renders the identifier in host:port form, retaining the mapped flag
// separately rather than pretending the socket was native IPv6.
func (id ListenID) String() string {
	if id.Empty {
		return ":" + strconv.Itoa(id.Port)
	}
	host := id.IP.String()
	if id.Mapped {
		host = "::ffff:" + host
	}
	return net.JoinHostPort(host, strconv.Itoa(id.Port))
}

// ParseID parses a host:port listener identifier into a classified ListenID.
//
// The host must be empty or a literal IP address; hostnames are rejected
// because they cannot be classified without name resolution and must never
// be implicitly widened to a wildcard. A zero port is legal and means "the
// driver allocates a port"; a negative port is not.
func ParseID(host string, port int) (ListenID, error) {
	if port < 0 || port > 65535 {
		return ListenID{}, fmt.Errorf("netsec: listener port %d out of range", port)
	}
	if host == "" {
		return ListenID{Class: ClassEmpty, Port: port, Empty: true}, nil
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return ListenID{}, fmt.Errorf("netsec: listener host %q is not a literal IP address", host)
	}

	id := ListenID{Port: port}
	// net.ParseIP stores every address in 16-byte form, so mapped-ness must
	// be detected from the original literal: an IPv4-capable address written
	// with a colon is an IPv4-mapped IPv6 address.
	if v4 := ip.To4(); v4 != nil && strings.Contains(host, ":") {
		id.Mapped = true
		ip = v4
	}

	if v4 := ip.To4(); v4 != nil {
		id.Family = FamilyIPv4
		ip = v4
	} else {
		id.Family = FamilyIPv6
	}
	id.IP = ip

	switch {
	case ip.IsUnspecified():
		id.Class = ClassWildcard
	case ip.IsLoopback():
		id.Class = ClassLoopback
	default:
		id.Class = ClassLiteral
	}
	return id, nil
}

// parseNetworkAddr parses the concrete network address a driver returns for
// an allocated lease, such as a *net.TCPAddr's "host:port" form.
func parseNetworkAddr(network, address string) (ListenID, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return ListenID{}, fmt.Errorf("netsec: unsupported listener network %q", network)
	}
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return ListenID{}, fmt.Errorf("netsec: malformed listener address %q: %w", address, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return ListenID{}, fmt.Errorf("netsec: listener address %q has no allocated port", address)
	}
	id, err := ParseID(host, port)
	if err != nil {
		return ListenID{}, err
	}
	if id.Empty {
		return ListenID{}, errors.New("netsec: driver returned an empty listener host instead of a concrete address")
	}
	return id, nil
}
