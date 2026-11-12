package idbind

import (
	"fmt"
	"net"
	"strings"
)

// TSType is an IKEv2 Traffic Selector type (RFC 7296 Section 3.13.1).
type TSType uint8

const (
	TS_IPV4_ADDR_RANGE TSType = 7
	TS_IPV6_ADDR_RANGE TSType = 8
)

// TrafficSelector is one IKEv2 traffic selector: an address range plus a
// protocol and port range. Protocol 0 and the port range 0-65535 mean
// "any".
type TrafficSelector struct {
	Type      TSType
	Protocol  uint8
	StartPort uint16
	EndPort   uint16
	StartAddr net.IP
	EndAddr   net.IP
}

// Contains reports whether ts fully covers other: same address family,
// other's address and port ranges inside ts's, and ts's protocol either
// wildcard (0) or equal to other's.
func (ts TrafficSelector) Contains(other TrafficSelector) bool {
	if ts.Type != other.Type {
		return false
	}
	start, ok := normalizeIP(ts.StartAddr, ts.Type)
	if !ok {
		return false
	}
	end, ok := normalizeIP(ts.EndAddr, ts.Type)
	if !ok {
		return false
	}
	oStart, ok := normalizeIP(other.StartAddr, other.Type)
	if !ok {
		return false
	}
	oEnd, ok := normalizeIP(other.EndAddr, other.Type)
	if !ok {
		return false
	}
	if compareIP(start, oStart) > 0 || compareIP(end, oEnd) < 0 {
		return false
	}
	if ts.Protocol != 0 && ts.Protocol != other.Protocol {
		return false
	}
	if other.StartPort < ts.StartPort || other.EndPort > ts.EndPort {
		return false
	}
	return true
}

func normalizeIP(ip net.IP, t TSType) (net.IP, bool) {
	switch t {
	case TS_IPV4_ADDR_RANGE:
		v4 := ip.To4()
		return v4, v4 != nil
	case TS_IPV6_ADDR_RANGE:
		v6 := ip.To16()
		return v6, v6 != nil && ip.To4() == nil
	default:
		return nil, false
	}
}

func compareIP(a, b net.IP) int {
	return strings.Compare(string(a), string(b))
}

// Policy is the set of traffic selectors an authenticated identity is
// allowed to use for CHILD_SAs.
type Policy struct {
	Allowed []TrafficSelector
}

// AuthorizeProposal verifies that every proposed traffic selector is
// covered by at least one allowed selector. A CHILD_SA proposal may
// narrow the policy but must never exceed it.
func (p *Policy) AuthorizeProposal(proposed []TrafficSelector) error {
	if len(proposed) == 0 {
		return fmt.Errorf("idbind: empty traffic selector proposal")
	}
	for i, ts := range proposed {
		covered := false
		for _, allowed := range p.Allowed {
			if allowed.Contains(ts) {
				covered = true
				break
			}
		}
		if !covered {
			return fmt.Errorf("idbind: proposed traffic selector %d (%v-%v proto %d ports %d-%d) exceeds identity policy",
				i, ts.StartAddr, ts.EndAddr, ts.Protocol, ts.StartPort, ts.EndPort)
		}
	}
	return nil
}

// PolicyStore maps authenticated identities to their CHILD_SA policies.
type PolicyStore struct {
	policies map[string]*Policy
}

// NewPolicyStore returns an empty store.
func NewPolicyStore() *PolicyStore {
	return &PolicyStore{policies: make(map[string]*Policy)}
}

// Set registers the policy for an identity, replacing any previous one.
func (s *PolicyStore) Set(id *IDPayload, p *Policy) {
	s.policies[identityKey(id)] = p
}

// PolicyFor returns the policy for an authenticated identity. It must be
// called only after BindIdentity has succeeded, so the policy is keyed
// on the certificate-bound identity, not on an unverified claim.
func (s *PolicyStore) PolicyFor(id *IDPayload) (*Policy, error) {
	p, ok := s.policies[identityKey(id)]
	if !ok {
		return nil, fmt.Errorf("idbind: no policy for identity %s %q", id.Type, string(id.Data))
	}
	return p, nil
}

func identityKey(id *IDPayload) string {
	return fmt.Sprintf("%d:%s", id.Type, string(normalizeIDValue(id)))
}
