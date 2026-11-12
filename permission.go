package netsec

import (
	"errors"
	"fmt"
	"net"
)

// Permission is the scope a host granted for one remote-listening request.
//
// Permissions are fail-closed. A zero Permission matches nothing: its family
// is FamilyUnspecified and its class is ClassEmpty, neither of which can be
// satisfied by a concrete listener. Construct permissions with
// NewPermission or ParsePermission.
type Permission struct {
	family Family
	class  Class
	ip     net.IP
	port   int
}

// NewPermission builds a permission from a classified address. The family and
// class must agree: FamilyUnspecified only pairs with ClassEmpty, and an
// empty or wildcard class carries no literal IP.
func NewPermission(family Family, class Class, ip net.IP, port int) (Permission, error) {
	if port < 0 || port > 65535 {
		return Permission{}, fmt.Errorf("netsec: permission port %d out of range", port)
	}
	switch class {
	case ClassEmpty:
		if family != FamilyUnspecified {
			return Permission{}, errors.New("netsec: empty permission must not name a family")
		}
		return Permission{family: family, class: class, port: port}, nil
	case ClassWildcard, ClassLoopback:
		if family != FamilyIPv4 && family != FamilyIPv6 {
			return Permission{}, fmt.Errorf("netsec: %s permission requires a concrete family", class)
		}
		return Permission{family: family, class: class, port: port}, nil
	case ClassLiteral:
		if ip == nil {
			return Permission{}, errors.New("netsec: literal permission requires an IP address")
		}
		canonical := ip.To4()
		fam := FamilyIPv6
		if canonical != nil {
			ip = canonical
			fam = FamilyIPv4
		}
		if family != fam {
			return Permission{}, fmt.Errorf("netsec: permission family %s does not match IP %s", family, ip)
		}
		return Permission{family: fam, class: class, ip: ip, port: port}, nil
	default:
		return Permission{}, fmt.Errorf("netsec: unknown permission class %d", class)
	}
}

// ParsePermission builds a permission from a host:port literal using the same
// classification rules as ParseID. An empty host produces a permission that
// matches nothing until an EmptyPolicy resolves the request.
func ParsePermission(host string, port int) (Permission, error) {
	id, err := ParseID(host, port)
	if err != nil {
		return Permission{}, err
	}
	return NewPermission(id.Family, id.Class, id.IP, id.Port)
}

// Family returns the permitted address family.
func (p Permission) Family() Family { return p.family }

// Class returns the permitted address class.
func (p Permission) Class() Class { return p.class }

// Port returns the permitted fixed port, or zero when any port may be
// allocated by the driver.
func (p Permission) Port() int { return p.port }

// Contains reports whether id falls inside the permission.
//
// The containment ordering is literal <= loopback <= wildcard within one
// family: a loopback grant accepts only loopback listeners, a wildcard grant
// accepts wildcard, loopback and literal listeners of that family, and a
// literal grant requires the exact address. IPv4-mapped IPv6 listeners are
// compared as IPv4 and never satisfy a native IPv6 permission.
func (p Permission) Contains(id ListenID) bool {
	if id.Empty {
		return false
	}
	if !p.containsPort(id.Port) {
		return false
	}
	if p.family != id.Family {
		return false
	}

	switch p.class {
	case ClassLiteral:
		return id.Class == ClassLiteral && p.ip.Equal(id.IP)
	case ClassLoopback:
		return id.Class == ClassLoopback
	case ClassWildcard:
		return id.Class == ClassWildcard ||
			id.Class == ClassLoopback ||
			id.Class == ClassLiteral
	default:
		return false
	}
}

func (p Permission) containsPort(port int) bool {
	if p.port == 0 {
		return true
	}
	return p.port == port
}

// String renders the permission in host:port form.
func (p Permission) String() string {
	switch p.class {
	case ClassEmpty:
		return ":" + fmt.Sprint(p.port)
	case ClassWildcard:
		var host string
		if p.family == FamilyIPv4 {
			host = "0.0.0.0"
		} else {
			host = "::"
		}
		return net.JoinHostPort(host, fmt.Sprint(p.port))
	case ClassLoopback:
		var host string
		if p.family == FamilyIPv4 {
			host = "127.0.0.1"
		} else {
			host = "::1"
		}
		return net.JoinHostPort(host, fmt.Sprint(p.port))
	default:
		return net.JoinHostPort(p.ip.String(), fmt.Sprint(p.port))
	}
}
