package networkpolicy

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

// PrefixDifference validates an ipBlock and returns a set of non-overlapping
// prefixes whose union is exactly CIDR minus the union of excepts.
//
// Every except must be a syntactically valid CIDR of the same address family
// as cidr and must be wholly contained by it. Overlapping and duplicate
// exceptions are accepted; their union is removed.
func PrefixDifference(cidr string, except []string) ([]netip.Prefix, error) {
	base, err := netip.ParsePrefix(strings.TrimSpace(cidr))
	if err != nil {
		return nil, fmt.Errorf("invalid ipBlock cidr %q: %w", cidr, err)
	}
	base = base.Masked()

	if len(except) == 0 {
		return []netip.Prefix{base}, nil
	}

	exclusions := make([]netip.Prefix, 0, len(except))
	for _, raw := range except {
		excluded, parseErr := netip.ParsePrefix(strings.TrimSpace(raw))
		if parseErr != nil {
			return nil, fmt.Errorf("invalid ipBlock except %q: %w", raw, parseErr)
		}
		excluded = excluded.Masked()
		if excluded.Addr().BitLen() != base.Addr().BitLen() {
			return nil, fmt.Errorf("ipBlock except %q has a different address family than cidr %q", raw, cidr)
		}
		if excluded.Bits() < base.Bits() || !base.Contains(excluded.Addr()) {
			return nil, fmt.Errorf("ipBlock except %q is not a subnet of cidr %q", raw, cidr)
		}
		exclusions = append(exclusions, excluded)
	}

	// Subtracting one child CIDR from a parent produces one sibling prefix at
	// every level between the parent and the child. Applying that operation
	// for each exception yields an exact partition of the remaining space.
	remaining := []netip.Prefix{base}
	for _, excluded := range exclusions {
		next := make([]netip.Prefix, 0, len(remaining)+excluded.Bits())
		for _, current := range remaining {
			switch {
			case !prefixesOverlap(current, excluded):
				next = append(next, current)
			case excluded.Bits() <= current.Bits() && excluded.Contains(current.Addr()):
				// The entire current prefix is excluded (or is exactly the
				// exclusion).
			case current.Bits() < excluded.Bits() && current.Contains(excluded.Addr()):
				next = append(next, subtractCIDR(current, excluded)...)
			default:
				// Unreachable for CIDRs, which either nest or are disjoint.
				next = append(next, current)
			}
		}
		remaining = next
	}

	sort.Slice(remaining, func(i, j int) bool {
		cmp := remaining[i].Addr().Compare(remaining[j].Addr())
		if cmp != 0 {
			return cmp < 0
		}
		return remaining[i].Bits() < remaining[j].Bits()
	})
	return remaining, nil
}

// CIDRDifference is an alias for PrefixDifference.
func CIDRDifference(cidr string, except []string) ([]netip.Prefix, error) {
	return PrefixDifference(cidr, except)
}

// Prefixes validates and expands an IPBlock.
func (b IPBlock) Prefixes() ([]netip.Prefix, error) {
	return PrefixDifference(b.CIDR, b.Except)
}

// ValidateIPBlock checks that an IPBlock is well formed.
func ValidateIPBlock(block IPBlock) error {
	_, err := PrefixDifference(block.CIDR, block.Except)
	return err
}

func prefixesOverlap(a, b netip.Prefix) bool {
	if a.Addr().BitLen() != b.Addr().BitLen() {
		return false
	}
	if a.Bits() <= b.Bits() {
		return a.Contains(b.Addr())
	}
	return b.Contains(a.Addr())
}

func subtractCIDR(base, remove netip.Prefix) []netip.Prefix {
	if base.Bits() >= remove.Bits() {
		return nil
	}
	out := make([]netip.Prefix, 0, remove.Bits()-base.Bits())
	for bits := base.Bits() + 1; bits <= remove.Bits(); bits++ {
		path := netip.PrefixFrom(remove.Addr(), bits).Masked()
		siblingAddr := flipPrefixBit(path.Addr(), bits-1)
		out = append(out, netip.PrefixFrom(siblingAddr, bits).Masked())
	}
	return out
}

func flipPrefixBit(addr netip.Addr, bit int) netip.Addr {
	if addr.Is4() {
		raw := addr.As4()
		raw[bit/8] ^= byte(1 << uint(7-bit%8))
		return netip.AddrFrom4(raw)
	}
	raw := addr.As16()
	raw[bit/8] ^= byte(1 << uint(7-bit%8))
	return netip.AddrFrom16(raw)
}

type compiledIPBlock struct {
	prefixes []netip.Prefix
}

func compileIPBlock(block *IPBlock) (*compiledIPBlock, error) {
	if block == nil {
		return nil, nil
	}
	prefixes, err := block.Prefixes()
	if err != nil {
		return nil, err
	}
	return &compiledIPBlock{prefixes: prefixes}, nil
}

func (b *compiledIPBlock) containsAny(addrs []netip.Addr) bool {
	if b == nil {
		return false
	}
	for _, addr := range addrs {
		for _, prefix := range b.prefixes {
			if prefix.Contains(addr) {
				return true
			}
		}
	}
	return false
}
