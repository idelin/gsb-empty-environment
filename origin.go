package netsec

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
)

// ErrInvalidRecord is returned when a consumed forwarding record cannot be
// parsed as an unambiguous IP literal.
var ErrInvalidRecord = errors.New("netsec: invalid forwarding record")

// Config configures a Resolver. TrustedCIDRs lists the networks whose
// forwarding records may be consumed; Version is an opaque identifier bound
// to every resolved Origin so callers can audit which configuration produced
// a decision.
type Config struct {
	Version      string
	TrustedCIDRs []string
}

// Resolver determines the client address from the real peer and a chain of
// forwarding records ordered from nearest to farthest.
type Resolver struct {
	version string
	trusted []netip.Prefix
}

// NewResolver validates cfg and returns a Resolver.
func NewResolver(cfg Config) (*Resolver, error) {
	trusted := make([]netip.Prefix, 0, len(cfg.TrustedCIDRs))
	for _, cidr := range cfg.TrustedCIDRs {
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil {
			return nil, fmt.Errorf("netsec: invalid trusted CIDR %q: %w", cidr, err)
		}
		trusted = append(trusted, prefix.Masked())
	}
	return &Resolver{version: cfg.Version, trusted: trusted}, nil
}

// Origin is the outcome of resolving a request's source.
type Origin struct {
	// ClientIP is the farthest address reached along trusted hops.
	ClientIP netip.Addr
	// Path lists the consumed hops from nearest (the real peer) to farthest
	// (ClientIP). It always contains at least the peer address.
	Path []netip.Addr
	// ConfigVersion echoes Config.Version of the resolver that produced it.
	ConfigVersion string
}

// parseRecord strictly parses one forwarding record. Any syntactic ambiguity
// (ports, brackets, zones, leading zeros, surrounding or inner whitespace)
// is rejected. IPv4-mapped IPv6 addresses are unmapped so they classify
// uniformly as IPv4.
func parseRecord(record string) (netip.Addr, error) {
	if record == "" || record != strings.TrimSpace(record) {
		return netip.Addr{}, fmt.Errorf("%w: %q", ErrInvalidRecord, record)
	}
	addr, err := netip.ParseAddr(record)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("%w: %q", ErrInvalidRecord, record)
	}
	if addr.Zone() != "" {
		return netip.Addr{}, fmt.Errorf("%w: zoned address %q", ErrInvalidRecord, record)
	}
	return addr.Unmap(), nil
}

func (r *Resolver) isTrusted(addr netip.Addr) bool {
	addr = addr.Unmap()
	for _, prefix := range r.trusted {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// Resolve walks the forwarding chain. The next record is consumed only while
// the current hop belongs to a trusted CIDR; walking stops at the first
// untrusted hop and any remaining records are ignored. A record that must be
// consumed but fails strict parsing aborts the resolution with an error.
func (r *Resolver) Resolve(peer netip.Addr, records []string) (Origin, error) {
	if !peer.IsValid() {
		return Origin{}, errors.New("netsec: invalid peer address")
	}
	current := peer.Unmap()
	path := []netip.Addr{current}
	for _, record := range records {
		if !r.isTrusted(current) {
			break
		}
		next, err := parseRecord(record)
		if err != nil {
			return Origin{}, err
		}
		current = next
		path = append(path, current)
	}
	return Origin{
		ClientIP:      current,
		Path:          path,
		ConfigVersion: r.version,
	}, nil
}
