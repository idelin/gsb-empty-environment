package handoff

import (
	"errors"
	"fmt"
	"math/big"
	"net/netip"
	"sort"
)

// MustPrefix parses a canonical CIDR and panics on error. It is intended for
// tests and compile-time-known configuration.
func MustPrefix(s string) netip.Prefix {
	p, err := ParsePrefix(s)
	if err != nil {
		panic(err)
	}
	return p
}

// ParsePrefix parses a CIDR and masks host bits.
func ParsePrefix(s string) (netip.Prefix, error) {
	p, err := netip.ParsePrefix(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	return p.Masked(), nil
}

func prefixBits(p netip.Prefix) int {
	return p.Addr().BitLen()
}

func addrInt(a netip.Addr) (*big.Int, int) {
	if a.Is4() {
		b := a.As4()
		return new(big.Int).SetBytes(b[:]), 32
	}
	b := a.As16()
	return new(big.Int).SetBytes(b[:]), 128
}

func prefixRange(p netip.Prefix) (lo, hi *big.Int, bits int) {
	p = p.Masked()
	lo, bits = addrInt(p.Addr())
	ones := p.Bits()
	blockBits := uint(bits - ones)
	size := new(big.Int).Lsh(big.NewInt(1), blockBits)
	base := new(big.Int).Div(lo, size)
	lo = new(big.Int).Mul(base, size)
	hi = new(big.Int).Add(new(big.Int).Set(lo), size)
	return lo, hi, bits
}

func intToPrefix(lo *big.Int, ones, bits int) (netip.Prefix, error) {
	if ones < 0 || ones > bits {
		return netip.Prefix{}, fmt.Errorf("prefix length %d outside %d-bit domain", ones, bits)
	}
	var addr netip.Addr
	switch bits {
	case 32:
		var b [4]byte
		lo.FillBytes(b[:])
		addr = netip.AddrFrom4(b)
	case 128:
		var b [16]byte
		lo.FillBytes(b[:])
		addr = netip.AddrFrom16(b)
	default:
		return netip.Prefix{}, fmt.Errorf("unsupported address width %d", bits)
	}
	return netip.PrefixFrom(addr, ones).Masked(), nil
}

// PrefixesContain reports whether outer contains inner.
func PrefixesContain(outer, inner netip.Prefix) bool {
	if outer.Addr().BitLen() != inner.Addr().BitLen() {
		return false
	}
	loO, hiO, _ := prefixRange(outer)
	loI, hiI, _ := prefixRange(inner)
	return loO.Cmp(loI) <= 0 && hiI.Cmp(hiO) <= 0
}

// MergePrefixes returns the minimal set of CIDRs equal to the union of in.
func MergePrefixes(in []netip.Prefix) []netip.Prefix {
	if len(in) == 0 {
		return nil
	}
	bits := prefixBits(in[0])
	set := NewRangeSet(bits)
	for _, p := range in {
		set.AddPrefix(p)
	}
	return set.ToPrefixes()
}

// RangeSet is an immutable-ish sorted set of integer address intervals.
type RangeSet struct {
	bits   int
	ranges []ipRange
}

type ipRange struct {
	lo *big.Int
	hi *big.Int // exclusive
}

// NewRangeSet creates an empty address set for a 32- or 128-bit family.
func NewRangeSet(bits int) *RangeSet {
	return &RangeSet{bits: bits}
}

// AddPrefix adds every address in p.
func (s *RangeSet) AddPrefix(p netip.Prefix) {
	lo, hi, bits := prefixRange(p)
	if s.bits == 0 {
		s.bits = bits
	}
	s.add(lo, hi)
}

func (s *RangeSet) add(lo, hi *big.Int) {
	if lo.Cmp(hi) >= 0 {
		return
	}
	s.ranges = append(s.ranges, ipRange{lo: new(big.Int).Set(lo), hi: new(big.Int).Set(hi)})
	sort.Slice(s.ranges, func(i, j int) bool {
		c := s.ranges[i].lo.Cmp(s.ranges[j].lo)
		if c != 0 {
			return c < 0
		}
		return s.ranges[i].hi.Cmp(s.ranges[j].hi) < 0
	})

	merged := s.ranges[:0]
	for _, r := range s.ranges {
		if len(merged) == 0 || merged[len(merged)-1].hi.Cmp(r.lo) < 0 {
			merged = append(merged, r)
			continue
		}
		last := &merged[len(merged)-1]
		if last.hi.Cmp(r.hi) < 0 {
			last.hi = new(big.Int).Set(r.hi)
		}
	}
	s.ranges = merged
}

// RemovePrefix subtracts p.
func (s *RangeSet) RemovePrefix(p netip.Prefix) {
	lo, hi, _ := prefixRange(p)
	var next []ipRange
	for _, r := range s.ranges {
		if r.hi.Cmp(lo) <= 0 || r.lo.Cmp(hi) >= 0 {
			next = append(next, r)
			continue
		}
		if r.lo.Cmp(lo) < 0 {
			next = append(next, ipRange{lo: new(big.Int).Set(r.lo), hi: new(big.Int).Set(lo)})
		}
		if hi.Cmp(r.hi) < 0 {
			next = append(next, ipRange{lo: new(big.Int).Set(hi), hi: new(big.Int).Set(r.hi)})
		}
	}
	s.ranges = next
}

// ContainsAddr reports whether a is in the set.
func (s *RangeSet) ContainsAddr(a netip.Addr) bool {
	v, bits := addrInt(a)
	if s.bits != 0 && bits != s.bits {
		return false
	}
	i := sort.Search(len(s.ranges), func(i int) bool {
		return s.ranges[i].hi.Cmp(v) > 0
	})
	return i < len(s.ranges) && s.ranges[i].lo.Cmp(v) <= 0 && v.Cmp(s.ranges[i].hi) < 0
}

// ContainsPrefix reports whether every address of p is in the set.
func (s *RangeSet) ContainsPrefix(p netip.Prefix) bool {
	lo, hi, _ := prefixRange(p)
	probe := NewRangeSet(s.bits)
	probe.add(lo, hi)
	for _, r := range probe.ranges {
		i := sort.Search(len(s.ranges), func(i int) bool {
			return s.ranges[i].hi.Cmp(r.lo) > 0
		})
		if i >= len(s.ranges) || s.ranges[i].lo.Cmp(r.lo) > 0 || s.ranges[i].hi.Cmp(r.hi) < 0 {
			return false
		}
	}
	return true
}

// ToPrefixes decomposes the set into canonical CIDR blocks.
func (s *RangeSet) ToPrefixes() []netip.Prefix {
	var out []netip.Prefix
	for _, r := range s.ranges {
		out = append(out, rangeToPrefixes(r.lo, r.hi, s.bits)...)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Addr().Less(out[j].Addr())
	})
	return out
}

func rangeToPrefixes(lo, hi *big.Int, bits int) []netip.Prefix {
	var out []netip.Prefix
	cur := new(big.Int).Set(lo)
	one := big.NewInt(1)
	for cur.Cmp(hi) < 0 {
		remaining := new(big.Int).Sub(hi, cur)
		maxSize := largestPowerOfTwoLE(remaining)
		alignment := new(big.Int).And(cur, new(big.Int).Sub(maxSize, one))
		for maxSize.Cmp(one) > 0 && alignment.Sign() != 0 {
			maxSize.Rsh(maxSize, 1)
			alignment = new(big.Int).And(cur, new(big.Int).Sub(maxSize, one))
		}
		ones := bits - maxSize.BitLen() + 1
		if maxSize.Cmp(one) == 0 {
			ones = bits
		}
		p, err := intToPrefix(cur, ones, bits)
		if err != nil {
			panic(err)
		}
		out = append(out, p)
		cur.Add(cur, maxSize)
	}
	return out
}

func largestPowerOfTwoLE(v *big.Int) *big.Int {
	if v.Sign() <= 0 {
		return big.NewInt(0)
	}
	p := new(big.Int).Lsh(big.NewInt(1), uint(v.BitLen()-1))
	return p
}

// EnumerateAddresses returns every address in p. It refuses domains larger
// than limit to keep accidental IPv6 enumeration safe.
func EnumerateAddresses(p netip.Prefix, limit int) ([]netip.Addr, error) {
	lo, hi, bits := prefixRange(p)
	count := new(big.Int).Sub(hi, lo)
	if limit <= 0 {
		limit = 1024
	}
	if !count.IsInt64() || count.Int64() > int64(limit) {
		return nil, fmt.Errorf("prefix contains %s addresses; limit is %d", count.String(), limit)
	}
	n := int(count.Int64())
	out := make([]netip.Addr, 0, n)
	cur := new(big.Int).Set(lo)
	one := big.NewInt(1)
	for i := 0; i < n; i++ {
		prefix, err := intToPrefix(cur, bits, bits)
		if err != nil {
			return nil, err
		}
		out = append(out, prefix.Addr())
		cur.Add(cur, one)
	}
	return out, nil
}

type trieNode struct {
	lo       *big.Int
	depth    int
	children [2]*trieNode
}

// EquivalenceClasses partitions domain into minimal CIDR blocks on which
// containment in every relevant prefix is unchanged. Relevant prefixes usually
// include announcements, exclusions, authority ranges, and critical prefixes.
func EquivalenceClasses(domain netip.Prefix, relevant ...netip.Prefix) ([]netip.Prefix, error) {
	domain = domain.Masked()
	dLo, _, bits := prefixRange(domain)
	if domain.Bits() < 0 || domain.Bits() > bits {
		return nil, errors.New("invalid domain prefix")
	}
	root := &trieNode{lo: new(big.Int).Set(dLo), depth: domain.Bits()}
	for _, p := range relevant {
		if p.Addr().BitLen() != bits {
			return nil, fmt.Errorf("prefix %s does not match domain family", p)
		}
		if !PrefixesContain(domain, p) {
			return nil, fmt.Errorf("relevant prefix %s is outside domain %s", p, domain)
		}
		insertTriePrefix(root, p, bits)
	}
	var out []netip.Prefix
	collectClasses(root, bits, &out)
	sort.Slice(out, func(i, j int) bool {
		return out[i].Addr().Less(out[j].Addr())
	})
	return out, nil
}

func insertTriePrefix(root *trieNode, p netip.Prefix, bits int) {
	targetLo, _, _ := prefixRange(p)
	node := root
	one := big.NewInt(1)
	for d := node.depth; d < p.Bits(); d++ {
		shift := uint(bits - d - 1)
		bitVal := new(big.Int).Rsh(targetLo, shift)
		bitVal.And(bitVal, one)
		bit := int(bitVal.Int64())
		if node.children[bit] == nil {
			half := new(big.Int).Lsh(big.NewInt(1), uint(bits-d-1))
			childLo := new(big.Int).Set(node.lo)
			if bit == 1 {
				childLo.Add(childLo, half)
			}
			node.children[bit] = &trieNode{lo: childLo, depth: d + 1}
		}
		node = node.children[bit]
	}
}

func collectClasses(node *trieNode, bits int, out *[]netip.Prefix) {
	if node.depth == bits || (node.children[0] == nil && node.children[1] == nil) {
		p, err := intToPrefix(node.lo, node.depth, bits)
		if err != nil {
			panic(err)
		}
		*out = append(*out, p)
		return
	}
	for bit := 0; bit <= 1; bit++ {
		child := node.children[bit]
		if child != nil {
			collectClasses(child, bits, out)
			continue
		}
		half := new(big.Int).Lsh(big.NewInt(1), uint(bits-node.depth-1))
		lo := new(big.Int).Set(node.lo)
		if bit == 1 {
			lo.Add(lo, half)
		}
		p, err := intToPrefix(lo, node.depth+1, bits)
		if err != nil {
			panic(err)
		}
		*out = append(*out, p)
	}
}
