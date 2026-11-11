package ribstore

import "net/netip"

// pnode is a node in the binary prefix trie. Routes are stored per neighbor
// at the node matching the announced prefix; ROAs are stored at the node
// matching the ROA prefix.
type pnode struct {
	child  [2]*pnode
	routes map[string]uint32 // neighbor ID -> origin ASN
	roas   map[ROA]struct{}
}

// normKey maps a prefix to a 16-byte key and a bit length. IPv4 prefixes are
// placed in the ::ffff:0:0/96 space so a single trie holds both families.
func normKey(p netip.Prefix) (key [16]byte, bits int) {
	a := p.Addr()
	key = a.As16()
	bits = p.Bits()
	if a.Is4() {
		bits += 96
	}
	return key, bits
}

func keyToPrefix(key [16]byte, bits int) netip.Prefix {
	if bits >= 96 && isV4Mapped(key) {
		var b4 [4]byte
		copy(b4[:], key[12:16])
		return netip.PrefixFrom(netip.AddrFrom4(b4), bits-96).Masked()
	}
	return netip.PrefixFrom(netip.AddrFrom16(key), bits).Masked()
}

func isV4Mapped(key [16]byte) bool {
	for i := 0; i < 10; i++ {
		if key[i] != 0 {
			return false
		}
	}
	return key[10] == 0xff && key[11] == 0xff
}

func bitAt(key [16]byte, i int) int {
	return int((key[i/8] >> uint(7-i%8)) & 1)
}

func setBit(key *[16]byte, i, b int) {
	if b == 1 {
		key[i/8] |= 1 << uint(7-i%8)
	}
}

// nodeFor returns the node for p, creating intermediate nodes when create is
// set. It returns nil when the path does not exist and create is false.
func (s *Store) nodeFor(p netip.Prefix, create bool) *pnode {
	key, bits := normKey(p)
	n := s.root
	for i := 0; i < bits; i++ {
		b := bitAt(key, i)
		c := n.child[b]
		if c == nil {
			if !create {
				return nil
			}
			c = &pnode{}
			n.child[b] = c
		}
		n = c
	}
	return n
}

type trieItem struct {
	n    *pnode
	key  [16]byte
	bits int
}

// walkRouteNodesFrom visits every node at or below (start, key, bits) that
// holds at least one route, in deterministic order.
func (s *Store) walkRouteNodesFrom(start *pnode, key [16]byte, bits int, fn func(n *pnode, p netip.Prefix)) {
	stack := []trieItem{{n: start, key: key, bits: bits}}
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if len(it.n.routes) > 0 {
			fn(it.n, keyToPrefix(it.key, it.bits))
		}
		for b := 0; b < 2; b++ {
			if c := it.n.child[b]; c != nil {
				nk := it.key
				setBit(&nk, it.bits, b)
				stack = append(stack, trieItem{n: c, key: nk, bits: it.bits + 1})
			}
		}
	}
}
