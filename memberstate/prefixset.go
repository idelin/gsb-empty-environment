package memberstate

import (
	"net/netip"
)

// family selects the 32-bit IPv4 or 128-bit IPv6 bit space.
type family int

const (
	fam4 family = 4
	fam6 family = 6
)

func familyBits(f family) int {
	if f == fam4 {
		return 32
	}
	return 128
}

// trieNode is a binary radix node over prefix bits. cover means the whole
// subtree is selected; children are then irrelevant and kept nil so the trie
// stays minimal and never materializes individual addresses.
type trieNode struct {
	children [2]*trieNode
	cover    bool
}

// prefixSet is a set of IP prefixes represented as a binary prefix trie.
// All operations run over prefix structure (at most 32/128 bit levels); the
// source address space is never expanded into individual addresses.
type prefixSet struct {
	fam  family
	bits int
	root *trieNode
}

func newPrefixSet(f family) *prefixSet {
	return &prefixSet{fam: f, bits: familyBits(f)}
}

func bitAt(arr *[16]byte, index int) int {
	return int((arr[index/8] >> (7 - uint(index%8))) & 1)
}

func setBit(arr *[16]byte, index, value int) {
	byteIndex := index / 8
	mask := byte(1 << (7 - uint(index%8)))
	if value == 1 {
		arr[byteIndex] |= mask
	} else {
		arr[byteIndex] &^= mask
	}
}

func prefixBits(p netip.Prefix) [16]byte {
	var arr [16]byte
	addr := p.Addr().Unmap()
	if addr.Is4() {
		raw := addr.As4()
		copy(arr[:4], raw[:])
	} else {
		arr = addr.As16()
	}
	return arr
}

func (s *prefixSet) prefixAt(arr *[16]byte, bits int) netip.Prefix {
	if s.fam == fam4 {
		var raw [4]byte
		copy(raw[:], arr[:4])
		addr4 := netip.AddrFrom4(raw)
		return netip.PrefixFrom(addr4, bits)
	}
	addr6 := netip.AddrFrom16(*arr)
	return netip.PrefixFrom(addr6, bits)
}

// insert adds p, merging sibling nodes into a covered parent so the stored
// cover is always the minimal disjoint representation.
func (s *prefixSet) insert(p netip.Prefix) {
	addr := p.Addr().Unmap()
	if s.fam == fam4 && !addr.Is4() {
		panic("memberstate: prefix family mismatch")
	}
	if s.fam == fam6 && (!addr.Is6() || addr.Is4In6()) {
		panic("memberstate: prefix family mismatch")
	}
	p = p.Masked()
	arr := prefixBits(p)
	s.root = s.insertNode(s.root, &arr, 0, p.Bits())
}

func (s *prefixSet) insertNode(n *trieNode, arr *[16]byte, depth, limit int) *trieNode {
	if n == nil {
		n = &trieNode{}
	}
	if n.cover {
		return n
	}
	if depth == limit {
		return &trieNode{cover: true}
	}
	b := bitAt(arr, depth)
	n.children[b] = s.insertNode(n.children[b], arr, depth+1, limit)
	if child := n.children[b]; child.cover {
		if sibling := n.children[b^1]; sibling != nil && sibling.cover {
			return &trieNode{cover: true}
		}
	}
	return n
}

// containsAddr reports whether addr falls inside the covered prefix space.
func (s *prefixSet) containsAddr(addr netip.Addr) bool {
	addr = addr.Unmap()
	if s.fam == fam4 && !addr.Is4() {
		return false
	}
	if s.fam == fam6 && (!addr.Is6() || addr.Is4In6()) {
		return false
	}
	arr := prefixBits(netip.PrefixFrom(addr, s.bits))
	n := s.root
	for depth := 0; depth < s.bits; depth++ {
		if n == nil {
			return false
		}
		if n.cover {
			return true
		}
		n = n.children[bitAt(&arr, depth)]
	}
	return n != nil && n.cover
}

// emit returns the minimal disjoint prefix cover, in bit-lexicographic order.
func (s *prefixSet) emit() []netip.Prefix {
	var arr [16]byte
	out := make([]netip.Prefix, 0)
	return s.emitNode(s.root, 0, &arr, out)
}

func (s *prefixSet) emitNode(n *trieNode, depth int, arr *[16]byte, out []netip.Prefix) []netip.Prefix {
	if n == nil {
		return out
	}
	if n.cover {
		return append(out, s.prefixAt(arr, depth))
	}
	for b := 0; b < 2; b++ {
		if n.children[b] != nil {
			setBit(arr, depth, b)
			out = s.emitNode(n.children[b], depth+1, arr, out)
		}
	}
	return out
}

// emitComplement returns the minimal disjoint cover of the whole address
// family minus this set. Missing trie branches become whole-prefix pieces,
// so complementation never enumerates addresses.
func (s *prefixSet) emitComplement() []netip.Prefix {
	var arr [16]byte
	out := make([]netip.Prefix, 0)
	return s.emitComplementNode(s.root, 0, &arr, out)
}

func (s *prefixSet) emitComplementNode(n *trieNode, depth int, arr *[16]byte, out []netip.Prefix) []netip.Prefix {
	if n == nil {
		return append(out, s.prefixAt(arr, depth))
	}
	if n.cover {
		return out
	}
	if depth == s.bits {
		return out
	}
	for b := 0; b < 2; b++ {
		setBit(arr, depth, b)
		child := n.children[b]
		if child == nil {
			out = append(out, s.prefixAt(arr, depth+1))
		} else {
			out = s.emitComplementNode(child, depth+1, arr, out)
		}
	}
	return out
}

// complementOf builds a set covering everything except s.
func complementOf(s *prefixSet) *prefixSet {
	out := newPrefixSet(s.fam)
	for _, p := range s.emitComplement() {
		out.insert(p)
	}
	return out
}

// intersect appends the overlap of this set with a single scope prefix.
func (s *prefixSet) intersect(scope netip.Prefix, out []netip.Prefix) []netip.Prefix {
	scope = scope.Masked()
	arr := prefixBits(scope)
	n := s.root
	for depth := 0; depth < scope.Bits(); depth++ {
		if n == nil {
			return out
		}
		if n.cover {
			return append(out, scope)
		}
		n = n.children[bitAt(&arr, depth)]
	}
	if n == nil {
		return out
	}
	if n.cover {
		return append(out, scope)
	}
	return s.emitNode(n, scope.Bits(), &arr, out)
}
