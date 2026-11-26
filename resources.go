package rpki

import (
	"errors"
	"fmt"
	"math/big"
	"net"
	"sort"
	"strconv"
	"strings"
)

// IPFamily identifies the address-family width used by an RFC 3779 range.
type IPFamily uint8

const (
	IPv4 IPFamily = 4
	IPv6 IPFamily = 6
)

func (f IPFamily) bitLen() int {
	switch f {
	case IPv4:
		return 32
	case IPv6:
		return 128
	default:
		return 0
	}
}

// IPRange is an inclusive IP interval represented by family-width integers.
type IPRange struct {
	Family IPFamily
	Start  *big.Int
	End    *big.Int
}

// IPPrefix is an RFC 3779 address prefix.
type IPPrefix struct {
	Family IPFamily
	Bits   int
	Prefix *big.Int
}

// ASRange is an inclusive AS number interval.
type ASRange struct {
	Start uint64
	End   uint64
}

// ResourceSet is an expanded, non-inherited set of number resources.
type ResourceSet struct {
	IPv4 []IPRange
	IPv6 []IPRange
	ASNs []ASRange
	RDIs []ASRange
}

// IPChoice is one address-family RFC 3779 choice: inherit or explicit ranges.
type IPChoice struct {
	Inherit bool
	Ranges  []IPRange
}

// ASChoice is the RFC 3779 ASIdentifierChoice for ASNs or RDIs.
type ASChoice struct {
	Inherit bool
	Ranges  []ASRange
}

// ResourceClaims contains the unexpanded choices from a certificate extension.
type ResourceClaims struct {
	IPv4 *IPChoice
	IPv6 *IPChoice
	ASNs *ASChoice
	RDIs *ASChoice
}

var (
	ErrInvalidResource = errors.New("rpki: invalid resource")
	ErrOutOfScope      = errors.New("rpki: resource outside parent set")
	ErrInheritNoParent = errors.New("rpki: inherit resource without parent set")
)

// ParseIPPrefix parses CIDR notation and retains its canonical network prefix.
func ParseIPPrefix(cidr string) (IPPrefix, error) {
	ip, network, err := net.ParseCIDR(strings.TrimSpace(cidr))
	if err != nil {
		return IPPrefix{}, fmt.Errorf("%w: prefix %q: %v", ErrInvalidResource, cidr, err)
	}
	family := IPv6
	width := 128
	if v4 := ip.To4(); v4 != nil {
		family = IPv4
		width = 32
		ip = v4
	} else {
		ip = ip.To16()
	}
	ones, bits := network.Mask.Size()
	if bits != width {
		return IPPrefix{}, fmt.Errorf("%w: prefix %q has width %d", ErrInvalidResource, cidr, bits)
	}
	start := IPToBigInt(network.IP)
	return IPPrefix{Family: family, Bits: ones, Prefix: start}, nil
}

// Range returns the inclusive interval covered by a prefix.
func (p IPPrefix) Range() IPRange {
	width := p.Family.bitLen()
	size := new(big.Int).Lsh(big.NewInt(1), uint(width-p.Bits))
	start := new(big.Int).Set(p.Prefix)
	end := new(big.Int).Add(start, new(big.Int).Sub(size, big.NewInt(1)))
	return IPRange{Family: p.Family, Start: start, End: end}
}

// ParseIPRange parses an inclusive start/end IP interval.
func ParseIPRange(start, end string) (IPRange, error) {
	s, err := parseIP(start)
	if err != nil {
		return IPRange{}, err
	}
	e, err := parseIP(end)
	if err != nil {
		return IPRange{}, err
	}
	s4 := s.To4()
	e4 := e.To4()
	if (s4 == nil) != (e4 == nil) {
		return IPRange{}, fmt.Errorf("%w: mixed IP families %q..%q", ErrInvalidResource, start, end)
	}
	family := IPv6
	if s4 != nil {
		family = IPv4
		s = s4
		e = e4
	} else {
		s = s.To16()
		e = e.To16()
	}
	sn := IPToBigInt(s)
	en := IPToBigInt(e)
	if sn.Cmp(en) > 0 {
		return IPRange{}, fmt.Errorf("%w: IP range start after end: %s..%s", ErrInvalidResource, start, end)
	}
	return IPRange{Family: family, Start: sn, End: en}, nil
}

func parseIP(value string) (net.IP, error) {
	ip := net.ParseIP(strings.TrimSpace(value))
	if ip == nil {
		return nil, fmt.Errorf("%w: IP address %q", ErrInvalidResource, value)
	}
	return ip, nil
}

// IPToBigInt converts a 4- or 16-byte IP address to an unsigned integer.
func IPToBigInt(ip net.IP) *big.Int {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	} else {
		ip = ip.To16()
	}
	return new(big.Int).SetBytes(ip)
}

// BigIntToIP converts an interval integer back to net.IP in the requested family.
func BigIntToIP(family IPFamily, value *big.Int) net.IP {
	width := family.bitLen() / 8
	if width == 0 || value == nil || value.Sign() < 0 {
		return nil
	}
	max := new(big.Int).Lsh(big.NewInt(1), uint(family.bitLen()))
	if value.Cmp(max) >= 0 {
		return nil
	}
	bytes := value.Bytes()
	out := make(net.IP, width)
	copy(out[width-len(bytes):], bytes)
	if family == IPv4 {
		return net.IP(out)
	}
	return out
}

// ParseASN accepts 64500, AS64500, and RFC 5396 dotted 16-bit high/low notation.
func ParseASN(value string) (uint64, error) {
	trimmed := strings.TrimSpace(value)
	trimmed = strings.TrimPrefix(strings.ToUpper(trimmed), "AS")
	if trimmed == "" {
		return 0, fmt.Errorf("%w: empty ASN", ErrInvalidResource)
	}
	if strings.Contains(trimmed, ".") {
		parts := strings.Split(trimmed, ".")
		if len(parts) != 2 {
			return 0, fmt.Errorf("%w: dotted ASN %q", ErrInvalidResource, value)
		}
		high, err := strconv.ParseUint(parts[0], 10, 16)
		if err != nil {
			return 0, fmt.Errorf("%w: ASN %q: %v", ErrInvalidResource, value, err)
		}
		low, err := strconv.ParseUint(parts[1], 10, 16)
		if err != nil {
			return 0, fmt.Errorf("%w: ASN %q: %v", ErrInvalidResource, value, err)
		}
		return high<<16 | low, nil
	}
	asn, err := strconv.ParseUint(trimmed, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%w: ASN %q: %v", ErrInvalidResource, value, err)
	}
	return asn, nil
}

// ParseASRange accepts "64500-64600" or a single ASN.
func ParseASRange(value string) (ASRange, error) {
	parts := strings.Split(strings.TrimSpace(value), "-")
	if len(parts) == 1 {
		asn, err := ParseASN(parts[0])
		return ASRange{Start: asn, End: asn}, err
	}
	if len(parts) != 2 {
		return ASRange{}, fmt.Errorf("%w: AS range %q", ErrInvalidResource, value)
	}
	start, err := ParseASN(parts[0])
	if err != nil {
		return ASRange{}, err
	}
	end, err := ParseASN(parts[1])
	if err != nil {
		return ASRange{}, err
	}
	if start > end {
		return ASRange{}, fmt.Errorf("%w: AS range start after end: %q", ErrInvalidResource, value)
	}
	return ASRange{Start: start, End: end}, nil
}

// ContainsIPRange reports whether r is fully covered by one parent interval.
func (s ResourceSet) ContainsIPRange(r IPRange) bool {
	var ranges []IPRange
	switch r.Family {
	case IPv4:
		ranges = s.IPv4
	case IPv6:
		ranges = s.IPv6
	default:
		return false
	}
	if r.Start == nil || r.End == nil || r.Start.Cmp(r.End) > 0 {
		return false
	}
	for _, parent := range ranges {
		if parent.Family != r.Family || parent.Start == nil || parent.End == nil {
			continue
		}
		if parent.Start.Cmp(r.Start) <= 0 && r.End.Cmp(parent.End) <= 0 {
			return true
		}
	}
	return false
}

// ContainsASRange reports whether r is fully covered by an ASN interval.
func (s ResourceSet) ContainsASRange(r ASRange) bool {
	if r.Start > r.End {
		return false
	}
	for _, parent := range s.ASNs {
		if parent.Start <= r.Start && r.End <= parent.End {
			return true
		}
	}
	return false
}

// ContainsRDIRange reports whether r is fully covered by an RDI interval.
func (s ResourceSet) ContainsRDIRange(r ASRange) bool {
	if r.Start > r.End {
		return false
	}
	for _, parent := range s.RDIs {
		if parent.Start <= r.Start && r.End <= parent.End {
			return true
		}
	}
	return false
}

// Contains reports whether every resource in child is wholly inside s.
// Equality is accepted; not even one address or AS number may extend beyond s.
func (s ResourceSet) Contains(child ResourceSet) bool {
	for _, r := range child.IPv4 {
		if !s.ContainsIPRange(r) {
			return false
		}
	}
	for _, r := range child.IPv6 {
		if !s.ContainsIPRange(r) {
			return false
		}
	}
	for _, r := range child.ASNs {
		if !s.ContainsASRange(r) {
			return false
		}
	}
	for _, r := range child.RDIs {
		if !s.ContainsRDIRange(r) {
			return false
		}
	}
	return true
}

// Empty reports whether no expanded resources are present.
func (s ResourceSet) Empty() bool {
	return len(s.IPv4) == 0 && len(s.IPv6) == 0 && len(s.ASNs) == 0 && len(s.RDIs) == 0
}

// Clone returns a deep copy safe for state construction.
func (s ResourceSet) Clone() ResourceSet {
	out := ResourceSet{
		IPv4: make([]IPRange, len(s.IPv4)),
		IPv6: make([]IPRange, len(s.IPv6)),
		ASNs: append([]ASRange(nil), s.ASNs...),
		RDIs: append([]ASRange(nil), s.RDIs...),
	}
	for i, r := range s.IPv4 {
		out.IPv4[i] = IPRange{Family: r.Family, Start: cloneBigInt(r.Start), End: cloneBigInt(r.End)}
	}
	for i, r := range s.IPv6 {
		out.IPv6[i] = IPRange{Family: r.Family, Start: cloneBigInt(r.Start), End: cloneBigInt(r.End)}
	}
	return out
}

func cloneBigInt(v *big.Int) *big.Int {
	if v == nil {
		return nil
	}
	return new(big.Int).Set(v)
}

// Normalize sorts and merges adjacent/overlapping intervals. It does not alter
// containment semantics and makes reports deterministic.
func (s ResourceSet) Normalize() ResourceSet {
	s.IPv4 = normalizeIPRanges(s.IPv4, IPv4)
	s.IPv6 = normalizeIPRanges(s.IPv6, IPv6)
	s.ASNs = normalizeASRanges(s.ASNs)
	s.RDIs = normalizeASRanges(s.RDIs)
	return s
}

func normalizeIPRanges(ranges []IPRange, family IPFamily) []IPRange {
	filtered := make([]IPRange, 0, len(ranges))
	for _, r := range ranges {
		if r.Family == family && r.Start != nil && r.End != nil && r.Start.Cmp(r.End) <= 0 {
			filtered = append(filtered, IPRange{Family: family, Start: new(big.Int).Set(r.Start), End: new(big.Int).Set(r.End)})
		}
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].Start.Cmp(filtered[j].Start) < 0 })
	out := make([]IPRange, 0, len(filtered))
	for _, r := range filtered {
		if len(out) == 0 {
			out = append(out, r)
			continue
		}
		last := &out[len(out)-1]
		next := new(big.Int).Add(last.End, big.NewInt(1))
		if r.Start.Cmp(last.End) <= 0 || r.Start.Cmp(next) == 0 {
			if r.End.Cmp(last.End) > 0 {
				last.End = new(big.Int).Set(r.End)
			}
			continue
		}
		out = append(out, r)
	}
	return out
}

func normalizeASRanges(ranges []ASRange) []ASRange {
	filtered := make([]ASRange, 0, len(ranges))
	for _, r := range ranges {
		if r.Start <= r.End {
			filtered = append(filtered, r)
		}
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].Start < filtered[j].Start })
	out := make([]ASRange, 0, len(filtered))
	for _, r := range filtered {
		if len(out) == 0 {
			out = append(out, r)
			continue
		}
		last := &out[len(out)-1]
		if r.Start <= last.End+1 {
			if r.End > last.End {
				last.End = r.End
			}
			continue
		}
		out = append(out, r)
	}
	return out
}

// CheckWithin verifies explicit claims against parent without expanding inherit.
func (c ResourceClaims) CheckWithin(parent ResourceSet) error {
	checkIP := func(choice *IPChoice) error {
		if choice == nil || choice.Inherit {
			return nil
		}
		for _, r := range choice.Ranges {
			if !parent.ContainsIPRange(r) {
				return fmt.Errorf("%w: IP range %s..%s is not delegated", ErrOutOfScope, ipString(r.Start, r.Family), ipString(r.End, r.Family))
			}
		}
		return nil
	}
	if err := checkIP(c.IPv4); err != nil {
		return err
	}
	if err := checkIP(c.IPv6); err != nil {
		return err
	}
	checkAS := func(choice *ASChoice, rdi bool) error {
		if choice == nil || choice.Inherit {
			return nil
		}
		for _, r := range choice.Ranges {
			ok := parent.ContainsASRange(r)
			if rdi {
				ok = parent.ContainsRDIRange(r)
			}
			if !ok {
				kind := "ASN"
				if rdi {
					kind = "RDI"
				}
				return fmt.Errorf("%w: %s range %d..%d is not delegated", ErrOutOfScope, kind, r.Start, r.End)
			}
		}
		return nil
	}
	if err := checkAS(c.ASNs, false); err != nil {
		return err
	}
	if err := checkAS(c.RDIs, true); err != nil {
		return err
	}
	return nil
}

// Resolve expands inherit choices. When parent is nil this is a trust anchor and
// inherit is forbidden. Explicit child resources are containment-checked.
func (c ResourceClaims) Resolve(parent *ResourceSet) (ResourceSet, error) {
	out := ResourceSet{}
	resolveIP := func(choice *IPChoice, family IPFamily) ([]IPRange, error) {
		if choice == nil {
			return nil, nil
		}
		if choice.Inherit {
			if parent == nil {
				return nil, fmt.Errorf("%w: %s inherit at trust anchor", ErrInheritNoParent, familyName(family))
			}
			if family == IPv4 {
				return append([]IPRange(nil), parent.IPv4...), nil
			}
			return append([]IPRange(nil), parent.IPv6...), nil
		}
		return append([]IPRange(nil), choice.Ranges...), nil
	}
	var err error
	if out.IPv4, err = resolveIP(c.IPv4, IPv4); err != nil {
		return ResourceSet{}, err
	}
	if out.IPv6, err = resolveIP(c.IPv6, IPv6); err != nil {
		return ResourceSet{}, err
	}
	resolveAS := func(choice *ASChoice, parentRanges []ASRange, kind string) ([]ASRange, error) {
		if choice == nil {
			return nil, nil
		}
		if choice.Inherit {
			if parent == nil {
				return nil, fmt.Errorf("%w: %s inherit at trust anchor", ErrInheritNoParent, kind)
			}
			return append([]ASRange(nil), parentRanges...), nil
		}
		return append([]ASRange(nil), choice.Ranges...), nil
	}
	if parent != nil {
		if err := c.CheckWithin(*parent); err != nil {
			return ResourceSet{}, err
		}
	}
	if out.ASNs, err = resolveAS(c.ASNs, parentAS(parent, false), "ASN"); err != nil {
		return ResourceSet{}, err
	}
	if out.RDIs, err = resolveAS(c.RDIs, parentAS(parent, true), "RDI"); err != nil {
		return ResourceSet{}, err
	}
	return out.Normalize(), nil
}

func parentAS(parent *ResourceSet, rdi bool) []ASRange {
	if parent == nil {
		return nil
	}
	if rdi {
		return parent.RDIs
	}
	return parent.ASNs
}

func familyName(family IPFamily) string {
	if family == IPv4 {
		return "IPv4"
	}
	return "IPv6"
}

func ipString(v *big.Int, family IPFamily) string {
	ip := BigIntToIP(family, v)
	if ip == nil {
		return "<invalid>"
	}
	return ip.String()
}
