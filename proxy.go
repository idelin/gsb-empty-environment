package netsec

import (
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// TrustedProxies is a concurrency-safe set of proxy addresses and CIDR blocks.
// Replace swaps the complete set atomically, which lets callers apply proxy
// configuration changes without restarting the edge service.
type TrustedProxies struct {
	mu       sync.RWMutex
	networks []*net.IPNet
	entries  []string
	revision atomic.Uint64
}

// NewTrustedProxies parses addresses and CIDR blocks into a proxy set.
// Bare addresses are treated as /32 (IPv4) or /128 (IPv6) entries.
func NewTrustedProxies(entries ...string) (*TrustedProxies, error) {
	t := &TrustedProxies{}
	if err := t.Replace(entries...); err != nil {
		return nil, err
	}
	return t, nil
}

// MustTrustedProxies is NewTrustedProxies for static configuration.
func MustTrustedProxies(entries ...string) *TrustedProxies {
	t, err := NewTrustedProxies(entries...)
	if err != nil {
		panic(err)
	}
	return t
}

// Replace atomically installs a new proxy set. A failed parse leaves the old
// set untouched.
func (t *TrustedProxies) Replace(entries ...string) error {
	networks := make([]*net.IPNet, 0, len(entries))
	clean := make([]string, 0, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		network, canonical, err := parseProxyEntry(entry)
		if err != nil {
			return err
		}
		networks = append(networks, network)
		clean = append(clean, canonical)
	}
	sort.Strings(clean)

	t.mu.Lock()
	t.networks = networks
	t.entries = clean
	t.mu.Unlock()
	t.revision.Add(1)
	return nil
}

// Update is an alias for Replace.
func (t *TrustedProxies) Update(entries ...string) error {
	return t.Replace(entries...)
}

func parseProxyEntry(entry string) (*net.IPNet, string, error) {
	if strings.Contains(entry, "/") {
		ip, network, err := net.ParseCIDR(entry)
		if err != nil {
			return nil, "", fmt.Errorf("netsec: invalid trusted proxy %q: %w", entry, err)
		}
		normalized := normalizeIP(ip)
		mask := network.Mask
		if normalized.To4() != nil {
			ones, bits := mask.Size()
			if bits == 128 {
				ones -= 96
			}
			mask = net.CIDRMask(ones, 32)
			normalized = normalized.To4()
		}
		network = &net.IPNet{IP: normalized.Mask(mask), Mask: mask}
		return network, network.String(), nil
	}

	ip, err := parseIP(entry)
	if err != nil {
		return nil, "", fmt.Errorf("netsec: invalid trusted proxy %q: %w", entry, err)
	}
	bits := 128
	if ip.To4() != nil {
		ip = ip.To4()
		bits = 32
	}
	network := &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}
	return network, network.String(), nil
}

// Contains reports whether ip belongs to the trusted proxy set.
func (t *TrustedProxies) Contains(ip net.IP) bool {
	if t == nil || ip == nil {
		return false
	}
	ip = normalizeIP(ip)
	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, network := range t.networks {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// ContainsString parses addr and reports whether it is trusted.
func (t *TrustedProxies) ContainsString(addr string) bool {
	ip, err := parseIP(addr)
	return err == nil && t.Contains(ip)
}

// Revision increments whenever Replace installs a new proxy set.
func (t *TrustedProxies) Revision() uint64 {
	if t == nil {
		return 0
	}
	return t.revision.Load()
}

// Len returns the number of configured entries.
func (t *TrustedProxies) Len() int {
	if t == nil {
		return 0
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.networks)
}

// Entries returns the canonical entries in sorted order.
func (t *TrustedProxies) Entries() []string {
	if t == nil {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return append([]string(nil), t.entries...)
}
