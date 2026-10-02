package rib

import (
	"fmt"
	"net/netip"
)

// node is a path-compressed binary trie (Patricia) node. Nodes are immutable
// once published: every mutation clones the path from the root, so old
// snapshots keep working unchanged (structural sharing).
type node struct {
	bits   [16]byte // key bits, big-endian; v4 uses bits[:4]
	plen   int      // prefix length in bits
	kids   [2]*node
	routes []Route // best-first; empty on pure branch nodes
}

func bitAt(b [16]byte, i int) int {
	return int(b[i>>3]>>(7-uint(i&7))) & 1
}

// keyOf extracts comparable key bits and length from a canonical prefix.
func keyOf(p netip.Prefix) ([16]byte, int) {
	var b [16]byte
	a := p.Addr()
	if a.Is4() {
		a4 := a.As4()
		copy(b[:4], a4[:])
	} else {
		a16 := a.As16()
		copy(b[:], a16[:])
	}
	return b, p.Bits()
}

// addrKey extracts key bits for a lookup address; klen is 32 or 128.
func addrKey(a netip.Addr) ([16]byte, int) {
	var b [16]byte
	if a.Is4() {
		a4 := a.As4()
		copy(b[:4], a4[:])
		return b, 32
	}
	a16 := a.As16()
	copy(b[:], a16[:])
	return b, 128
}

func (n *node) prefix(is4 bool) netip.Prefix {
	if is4 {
		return netip.PrefixFrom(netip.AddrFrom4([4]byte(n.bits[:4])), n.plen).Masked()
	}
	return netip.PrefixFrom(netip.AddrFrom16(n.bits), n.plen).Masked()
}

// ins inserts route r for (key,klen) below n; from = bits already matched
// above n. Returns the new subtree root (shares untouched structure).
func ins(n *node, key [16]byte, klen int, r Route, from int) *node {
	if n == nil {
		return &node{bits: key, plen: klen, routes: []Route{r}}
	}
	m := from
	for m < n.plen && m < klen && bitAt(n.bits, m) == bitAt(key, m) {
		m++
	}
	switch {
	case m == n.plen && m == klen:
		// exact node: add route (dedup inside insertRoute)
		nn := *n
		nn.routes = insertRoute(n.routes, r)
		return &nn
	case m == n.plen:
		// n is a prefix of the key: descend on the next key bit
		b := bitAt(key, m)
		nn := *n
		nn.kids[b] = ins(n.kids[b], key, klen, r, m+1)
		return &nn
	default:
		// split: introduce a branch node at the divergence point m
		mid := &node{bits: n.bits, plen: m}
		if m == klen {
			mid.routes = []Route{r}
			mid.kids[bitAt(n.bits, m)] = n
		} else {
			mid.kids[bitAt(n.bits, m)] = n
			mid.kids[bitAt(key, m)] = &node{bits: key, plen: klen, routes: []Route{r}}
		}
		return mid
	}
}

// del removes route r for (key,klen) below n. Returns the new subtree root
// (nil if it vanished) and whether anything was removed. Path compression is
// restored by splicing out route-less single-child nodes.
func del(n *node, key [16]byte, klen int, r Route, from int) (*node, bool) {
	if n == nil {
		return nil, false
	}
	m := from
	for m < n.plen && m < klen && bitAt(n.bits, m) == bitAt(key, m) {
		m++
	}
	if m < n.plen {
		return n, false // key diverges: prefix not present
	}
	if n.plen < klen {
		b := bitAt(key, m)
		nc, ok := del(n.kids[b], key, klen, r, m+1)
		if !ok {
			return n, false
		}
		nn := *n
		nn.kids[b] = nc
		return compress(&nn), true
	}
	if !hasRoute(n.routes, r) {
		return n, false
	}
	nn := *n
	nn.routes, _ = removeRoutes(n.routes, r)
	return compress(&nn), true
}

// compress splices out n if it carries no routes and has fewer than two
// children (merging the path). Returns nil when the subtree is empty.
func compress(n *node) *node {
	if len(n.routes) > 0 {
		return n
	}
	if n.kids[0] == nil {
		return n.kids[1] // nil or the single child
	}
	if n.kids[1] == nil {
		return n.kids[0]
	}
	return n
}

// lookup finds the deepest node with routes whose prefix covers key.
// counter, if non-nil, accumulates the number of bit comparisons; it never
// exceeds klen (32/128) because compared ranges telescope along the path.
func lookup(n *node, key [16]byte, klen int, counter *int) *node {
	var best *node
	from := 0
	for n != nil {
		m := from
		for m < n.plen && bitAt(n.bits, m) == bitAt(key, m) {
			m++
			if counter != nil {
				*counter++
			}
		}
		if m < n.plen {
			if counter != nil {
				*counter++ // the failing comparison
			}
			return best
		}
		if len(n.routes) > 0 {
			best = n
		}
		if n.plen >= klen {
			return best
		}
		from = n.plen
		n = n.kids[bitAt(key, n.plen)]
	}
	return best
}

// walk visits route-bearing nodes in lexicographic order (prefix before its
// extensions, 0-branch before 1-branch).
func walk(n *node, is4 bool, fn func(netip.Prefix, []Route)) {
	if n == nil {
		return
	}
	if len(n.routes) > 0 {
		fn(n.prefix(is4), n.routes)
	}
	walk(n.kids[0], is4, fn)
	walk(n.kids[1], is4, fn)
}

// checkCompressed verifies the Patricia invariant: every node carries routes
// or has exactly two children, and child prefixes extend the parent's.
func checkCompressed(n *node, depth int) error {
	if n == nil {
		return nil
	}
	if depth > 0 && len(n.routes) == 0 && (n.kids[0] == nil || n.kids[1] == nil) {
		return fmt.Errorf("uncompressed branch at /%d", n.plen)
	}
	for b := 0; b < 2; b++ {
		k := n.kids[b]
		if k == nil {
			continue
		}
		if k.plen <= n.plen {
			return fmt.Errorf("child /%d not deeper than parent /%d", k.plen, n.plen)
		}
		if bitAt(k.bits, n.plen) != b {
			return fmt.Errorf("child /%d on wrong branch of /%d", k.plen, n.plen)
		}
		if err := checkCompressed(k, depth+1); err != nil {
			return err
		}
	}
	return nil
}
