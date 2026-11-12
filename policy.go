package taskpkg

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
)

// Policy is an immutable target binding: the canonical host name and port the
// host is allowed to dial, the IP addresses already verified for that host, the
// CIDRs that must never be reached, and the generation of this configuration.
//
// Policy values must not be mutated after they have been handed to a Binder.
type Policy struct {
	// CanonicalHost is the sole host name this policy licenses. It is stored
	// in canonical lower-case form.
	CanonicalHost string

	// Port is the sole TCP/UDP port this policy licenses.
	Port int

	// VerifiedIPs is the set of allowed peer addresses in canonical form
	// (unwrapped 4-in-6 form for IPv4-mapped addresses).
	VerifiedIPs map[netip.Addr]struct{}

	// Forbidden is evaluated against the actual peer at delivery time, so a
	// freshly forbidden CIDR rejects connections that were already in flight.
	Forbidden []*net.IPNet

	// Generation identifies the configuration. A binder only accepts policies
	// with a strictly greater generation than the one currently installed.
	Generation int64
}

// NewPolicy validates and normalizes a target-binding policy.
//
// host must be a canonical host name (IP literals are not accepted: the
// binding is always expressed as a host name plus verified answers), port must
// be a valid TCP/UDP port, and each forbidden entry must parse as a CIDR.
func NewPolicy(host string, port int, verified []net.IP, forbidden []string, generation int64) (*Policy, error) {
	canonical, err := canonicalHost(host)
	if err != nil {
		return nil, err
	}
	if port < 0 || port > 65535 {
		return nil, fmt.Errorf("taskpkg: port %d out of range", port)
	}
	if len(verified) == 0 {
		return nil, errors.New("taskpkg: policy must contain at least one verified IP")
	}

	verifiedSet := make(map[netip.Addr]struct{}, len(verified))
	for _, ip := range verified {
		addr, ok := canonicalIP(ip)
		if !ok {
			return nil, fmt.Errorf("taskpkg: invalid verified IP %q", ip.String())
		}
		verifiedSet[addr] = struct{}{}
	}

	nets := make([]*net.IPNet, 0, len(forbidden))
	for _, cidr := range forbidden {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, fmt.Errorf("taskpkg: invalid forbidden CIDR %q: %w", cidr, err)
		}
		nets = append(nets, network)
	}

	return &Policy{
		CanonicalHost: canonical,
		Port:          port,
		VerifiedIPs:   verifiedSet,
		Forbidden:     nets,
		Generation:    generation,
	}, nil
}

// allowsIP reports whether addr is part of the verified set. Addresses are
// compared in canonical unwrapped form, so an IPv4-mapped ::ffff:a.b.c.d
// address only matches the equivalent verified IPv4 address.
func (p *Policy) allowsIP(addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}
	addr = addr.Unmap()
	_, ok := p.VerifiedIPs[addr]
	return ok
}

// forbitsIP reports whether addr is covered by a currently forbidden CIDR.
// net.IPNet.Contains unwraps 4-in-6 addresses itself, keeping the check
// consistent with allowsIP.
func (p *Policy) forbitsIP(addr netip.Addr) bool {
	ip := net.IP(addr.AsSlice())
	for _, network := range p.Forbidden {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// canonicalHost lower-cases host and rejects empty values and IP literals.
func canonicalHost(host string) (string, error) {
	if host == "" {
		return "", errors.New("taskpkg: canonical host must not be empty")
	}
	canonical := toLowerCaseASCII(host)
	if ip, err := netip.ParseAddr(canonical); err == nil && ip.IsValid() {
		return "", fmt.Errorf("taskpkg: canonical host %q is an IP literal, not a host name", host)
	}
	return canonical, nil
}

// matchHost compares a dialed host against the pinned canonical host name.
func matchHost(pinned, host string) bool {
	if host == "" {
		return false
	}
	return pinned == toLowerCaseASCII(host)
}

func toLowerCaseASCII(s string) string {
	hasUpper := false
	for i := 0; i < len(s); i++ {
		if s[i] >= 'A' && s[i] <= 'Z' {
			hasUpper = true
			break
		}
	}
	if !hasUpper {
		return s
	}
	buf := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		buf[i] = c
	}
	return string(buf)
}

// canonicalIP converts a net.IP to a canonical, unwrapped netip.Addr.
func canonicalIP(ip net.IP) (netip.Addr, bool) {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

// joinHostPort formats an IP literal and a port without touching DNS.
func joinHostPort(addr netip.Addr, port int) string {
	return net.JoinHostPort(addr.String(), strconv.Itoa(port))
}
