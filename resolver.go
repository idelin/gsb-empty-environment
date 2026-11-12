package netsec

import (
	"fmt"
	"net/netip"
	"strings"
)

// Resolver evaluates requests against a pod's Server and AuthorizationPolicy
// resources. It is safe for concurrent use after construction.
type Resolver struct {
	defaultPolicy  DefaultPolicy
	nodeNets       []netip.Prefix
	probePaths     map[string]struct{}
	serverCounts   map[int32]int
	policiesByPort map[int32][]AuthorizationPolicy
	meshAuths      map[string]MeshTLSAuthentication
	netAuths       map[string][]netip.Prefix
}

// NewResolver builds a Resolver from cfg. It returns an error if any CIDR in
// NodeCIDRs or in a NetworkAuthentication is invalid.
func NewResolver(cfg Config) (*Resolver, error) {
	r := &Resolver{
		defaultPolicy:  cfg.DefaultPolicy,
		probePaths:     make(map[string]struct{}, len(cfg.ProbePaths)),
		serverCounts:   make(map[int32]int),
		policiesByPort: make(map[int32][]AuthorizationPolicy),
		meshAuths:      make(map[string]MeshTLSAuthentication, len(cfg.MeshTLSAuths)),
		netAuths:       make(map[string][]netip.Prefix, len(cfg.NetworkAuths)),
	}

	for _, cidr := range cfg.NodeCIDRs {
		p, err := netip.ParsePrefix(cidr)
		if err != nil {
			return nil, fmt.Errorf("netsec: invalid node CIDR %q: %w", cidr, err)
		}
		r.nodeNets = append(r.nodeNets, p)
	}
	for _, path := range cfg.ProbePaths {
		r.probePaths[path] = struct{}{}
	}
	for _, srv := range cfg.Servers {
		r.serverCounts[srv.Port]++
	}
	for _, pol := range cfg.Policies {
		r.policiesByPort[pol.Port] = append(r.policiesByPort[pol.Port], pol)
	}
	for _, m := range cfg.MeshTLSAuths {
		r.meshAuths[m.Name] = m
	}
	for _, n := range cfg.NetworkAuths {
		var prefixes []netip.Prefix
		for _, cidr := range n.Networks {
			p, err := netip.ParsePrefix(cidr)
			if err != nil {
				return nil, fmt.Errorf("netsec: invalid CIDR %q in NetworkAuthentication %q: %w", cidr, n.Name, err)
			}
			prefixes = append(prefixes, p)
		}
		r.netAuths[n.Name] = prefixes
	}
	return r, nil
}

// Evaluate decides whether req is permitted.
func (r *Resolver) Evaluate(req Request) Decision {
	switch n := r.serverCounts[req.Port]; {
	case n > 1:
		// A port selected by multiple Servers is a configuration error.
		return Deny
	case n == 0:
		// Ports not selected by any Server follow the cluster default.
		if r.defaultPolicy == DefaultAllow {
			return Allow
		}
		return Deny
	}

	// Kubelet probes from node networks are permitted only for declared
	// probe paths over GET.
	if r.isProbe(req) {
		return Allow
	}

	for _, pol := range r.policiesByPort[req.Port] {
		if r.policyAllows(pol, req) {
			return Allow
		}
	}
	return Deny
}

func (r *Resolver) isProbe(req Request) bool {
	if !strings.EqualFold(req.Method, "GET") {
		return false
	}
	if _, ok := r.probePaths[req.Path]; !ok {
		return false
	}
	addr, err := netip.ParseAddr(req.SourceIP)
	if err != nil {
		return false
	}
	for _, n := range r.nodeNets {
		if n.Contains(addr) {
			return true
		}
	}
	return false
}

func (r *Resolver) policyAllows(pol AuthorizationPolicy, req Request) bool {
	for _, ref := range pol.AuthenticationRefs {
		switch ref.Kind {
		case KindMeshTLSAuthentication:
			if m, ok := r.meshAuths[ref.Name]; ok && matchAnyIdentity(m.Identities, req.Identity) {
				return true
			}
		case KindNetworkAuthentication:
			if prefixes, ok := r.netAuths[ref.Name]; ok && matchAnyNetwork(prefixes, req.SourceIP) {
				return true
			}
		}
	}
	return false
}

func matchAnyIdentity(patterns []string, identity string) bool {
	if identity == "" {
		return false
	}
	for _, p := range patterns {
		if identityMatches(p, identity) {
			return true
		}
	}
	return false
}

// identityMatches reports whether identity satisfies pattern. A pattern of
// the form "*.<suffix>" requires "*" to replace exactly the leftmost
// complete DNS label, so it never spans multiple labels.
func identityMatches(pattern, identity string) bool {
	if pattern == "*" {
		return true
	}
	if suffix, ok := strings.CutPrefix(pattern, "*."); ok {
		if !strings.HasSuffix(identity, "."+suffix) {
			return false
		}
		leftmost := identity[:len(identity)-len(suffix)-1]
		return leftmost != "" && !strings.Contains(leftmost, ".")
	}
	return pattern == identity
}

func matchAnyNetwork(prefixes []netip.Prefix, sourceIP string) bool {
	addr, err := netip.ParseAddr(sourceIP)
	if err != nil {
		return false
	}
	for _, p := range prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}
