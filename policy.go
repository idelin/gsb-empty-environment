package netsec

import (
	"fmt"
	"net"
	"net/netip"
	"strings"
)

// Decision is the result of a source-network admission check.
type Decision uint8

const (
	// Allow means the client is inside a registered network.
	Allow Decision = iota
	// Refuse means the client must receive REFUSED without any resolver data.
	Refuse
)

// Policy is the immutable set of client networks the resolver serves and the
// single TTL value exposed in every response record.
//
// The TTL is deliberately independent of cache age and cache hit/miss status.
type Policy struct {
	networks    []netip.Prefix
	responseTTL uint32
}

// NewPolicy creates a policy from parsed network prefixes. Prefixes are
// masked and de-duplicated. An empty list is valid and denies every client.
func NewPolicy(networks []netip.Prefix, responseTTL uint32) (*Policy, error) {
	unique := make(map[netip.Prefix]struct{}, len(networks))
	out := make([]netip.Prefix, 0, len(networks))
	for _, prefix := range networks {
		if !prefix.IsValid() || !prefix.Addr().IsValid() {
			return nil, fmt.Errorf("netsec: invalid client network")
		}
		addr := prefix.Addr()
		bits := prefix.Bits()
		if addr.Is4In6() {
			addr = addr.Unmap()
			bits -= 96
		}
		masked := netip.PrefixFrom(addr, bits)
		if !masked.IsValid() {
			return nil, fmt.Errorf("netsec: invalid client network")
		}
		prefix = masked.Masked()
		if !prefix.IsValid() {
			return nil, fmt.Errorf("netsec: invalid client network")
		}
		if _, ok := unique[prefix]; ok {
			continue
		}
		unique[prefix] = struct{}{}
		out = append(out, prefix)
	}
	return &Policy{networks: out, responseTTL: responseTTL}, nil
}

// NewPolicyFromCIDRs parses textual CIDR networks and creates a policy.
func NewPolicyFromCIDRs(cidrs []string, responseTTL uint32) (*Policy, error) {
	networks, err := ParseNetworks(cidrs...)
	if err != nil {
		return nil, err
	}
	return NewPolicy(networks, responseTTL)
}

// NewPolicyFromIPNets is a compatibility constructor for callers that still
// hold net.IPNet values.
func NewPolicyFromIPNets(networks []*net.IPNet, responseTTL uint32) (*Policy, error) {
	prefixes := make([]netip.Prefix, 0, len(networks))
	for _, network := range networks {
		if network == nil {
			return nil, fmt.Errorf("netsec: nil client network")
		}
		ip := network.IP
		if len(network.Mask) == net.IPv4len {
			ip = ip.To4()
		}
		addr, ok := netip.AddrFromSlice(ip)
		if !ok {
			return nil, fmt.Errorf("netsec: invalid client network")
		}
		bits, width := network.Mask.Size()
		if bits < 0 || width != addr.BitLen() {
			return nil, fmt.Errorf("netsec: invalid client network mask")
		}
		prefix := netip.PrefixFrom(addr, bits)
		if !prefix.IsValid() {
			return nil, fmt.Errorf("netsec: invalid client network")
		}
		prefixes = append(prefixes, prefix)
	}
	return NewPolicy(prefixes, responseTTL)
}

// ParseNetworks parses CIDR strings. Bare IP addresses are accepted as
// single-address networks.
func ParseNetworks(cidrs ...string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(cidrs))
	for _, cidr := range cidrs {
		value := strings.TrimSpace(cidr)
		if value == "" {
			return nil, fmt.Errorf("netsec: empty client network")
		}
		if prefix, err := netip.ParsePrefix(value); err == nil {
			out = append(out, prefix.Masked())
			continue
		}
		addr, err := netip.ParseAddr(value)
		if err != nil {
			return nil, fmt.Errorf("netsec: invalid client network %q: %w", cidr, err)
		}
		out = append(out, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return out, nil
}

// Admit reports whether client belongs to a registered network. Invalid
// addresses are always refused.
func (p *Policy) Admit(client netip.Addr) bool {
	if p == nil {
		return false
	}
	return p.Decide(client) == Allow
}

// Allows is an alias for Admit.
func (p *Policy) Allows(client netip.Addr) bool {
	return p.Admit(client)
}

// AllowsIP is a compatibility helper for callers that still use net.IP.
func (p *Policy) AllowsIP(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	return p.Admit(addr.Unmap())
}

// Decide returns the admission decision for a source address.
func (p *Policy) Decide(client netip.Addr) Decision {
	if p == nil || !client.IsValid() {
		return Refuse
	}
	client = client.Unmap()
	for _, prefix := range p.networks {
		if prefix.Contains(client) {
			return Allow
		}
	}
	return Refuse
}

// ResponseTTL returns the TTL written into every externally visible record.
func (p *Policy) ResponseTTL() uint32 {
	if p == nil {
		return 0
	}
	return p.responseTTL
}

// Networks returns a copy of the registered networks.
func (p *Policy) Networks() []netip.Prefix {
	if p == nil {
		return nil
	}
	return append([]netip.Prefix(nil), p.networks...)
}
