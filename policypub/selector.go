// Package policypub implements atomic, rollback-safe encryption policy
// publication for tunnel security associations (SAs).
//
// The core safety invariant is that a packet decision is ever only one of
// ActionEncrypt (backed by an SA confirmed to be installed) or ActionDrop.
// There is no plaintext/bypass action, so a half-published policy can never
// expose sensitive traffic.
package policypub

import (
	"errors"
	"fmt"
	"net/netip"
)

// Direction identifies which way a tunneled flow travels.
type Direction uint8

const (
	Outbound Direction = iota + 1
	Inbound
)

func (d Direction) String() string {
	switch d {
	case Outbound:
		return "outbound"
	case Inbound:
		return "inbound"
	default:
		return "unknown"
	}
}

// Rule is a bidirectional traffic selector. Flows from Local to Remote are
// protected with OutboundSA, and reverse flows with InboundSA.
type Rule struct {
	Name       string
	Local      netip.Prefix
	Remote     netip.Prefix
	OutboundSA string
	InboundSA  string
}

// dstNode is one level of the inner destination trie.
type dstNode struct {
	child  [2]*dstNode
	parent *dstNode
	value  *string
}

// srcNode is one level of the outer source trie; nodes terminating a source
// prefix carry the root of a destination trie.
type srcNode struct {
	child  [2]*srcNode
	parent *srcNode
	dst    *dstNode
}

// table2D is an immutable two-dimensional longest-prefix-match table. It
// prefers the longest matching source prefix; within that source prefix it
// prefers the longest matching destination prefix. Backtracking through parent
// links makes shorter, enclosing selectors reachable.
type table2D struct {
	root *srcNode
}

// bitAt returns the i-th bit (MSB first) of the address. IPv4 is indexed over
// its 4 raw bytes, never its 16-byte IPv4-mapped form, so prefix bit lengths
// line up with the trie depth.
func bitAt(addr netip.Addr, i int) uint {
	if addr.Is4() {
		raw := addr.As4()
		return uint(raw[i/8]>>(7-uint(i%8))) & 1
	}
	raw := addr.As16()
	return uint(raw[i/8]>>(7-uint(i%8))) & 1
}

func (t *table2D) insert(src, dst netip.Prefix, handle string) {
	srcWalk := t.root
	for i := 0; i < src.Bits(); i++ {
		bit := bitAt(src.Addr(), i)
		if srcWalk.child[bit] == nil {
			srcWalk.child[bit] = &srcNode{parent: srcWalk}
		}
		srcWalk = srcWalk.child[bit]
	}
	if srcWalk.dst == nil {
		srcWalk.dst = &dstNode{}
	}
	dstWalk := srcWalk.dst
	for i := 0; i < dst.Bits(); i++ {
		bit := bitAt(dst.Addr(), i)
		if dstWalk.child[bit] == nil {
			dstWalk.child[bit] = &dstNode{parent: dstWalk}
		}
		dstWalk = dstWalk.child[bit]
	}
	if dstWalk.value != nil {
		// Compile rejects duplicate selectors, so this is unreachable.
		panic("policypub: duplicate selector in compiled table")
	}
	dstWalk.value = &handle
}

// walkSrc descends the outer trie following addr and returns the deepest
// source node reached.
func walkSrc(root *srcNode, addr netip.Addr) *srcNode {
	node := root
	for depth := 0; depth < addr.BitLen(); depth++ {
		next := node.child[bitAt(addr, depth)]
		if next == nil {
			break
		}
		node = next
	}
	return node
}

// matchDst returns the SA handle behind the longest matching destination
// prefix under the given destination trie root.
func matchDst(root *dstNode, addr netip.Addr) (string, bool) {
	node := root
	for depth := 0; depth < addr.BitLen(); depth++ {
		next := node.child[bitAt(addr, depth)]
		if next == nil {
			break
		}
		node = next
	}
	for ; node != nil; node = node.parent {
		if node.value != nil {
			return *node.value, true
		}
	}
	return "", false
}

func (t *table2D) lookup(src, dst netip.Addr) (string, bool) {
	for node := walkSrc(t.root, src); node != nil; node = node.parent {
		if node.dst == nil {
			continue
		}
		if handle, ok := matchDst(node.dst, dst); ok {
			return handle, true
		}
	}
	return "", false
}

// Table is the immutable, compiled bidirectional lookup table. It is safe for
// concurrent readers and is never mutated after Compile returns.
type Table struct {
	out   *table2D
	in    *table2D
	rules []Rule
}

// Lookup resolves the SA handle for a flow, or ok=false when no selector
// matches. Inbound lookups match against the reversed selector direction.
func (t *Table) Lookup(dir Direction, src, dst netip.Addr) (string, bool) {
	switch dir {
	case Outbound:
		return t.out.lookup(src, dst)
	case Inbound:
		return t.in.lookup(src, dst)
	default:
		return "", false
	}
}

// Rules returns a copy of the rules the table was compiled from.
func (t *Table) Rules() []Rule {
	return append([]Rule(nil), t.rules...)
}

// Compile validates the bidirectional selectors and compiles them into an
// immutable longest-prefix-match lookup table covering both directions.
func Compile(rules []Rule) (*Table, error) {
	if len(rules) == 0 {
		return nil, errors.New("policypub: cannot compile empty rule set")
	}
	table := &Table{
		out:   &table2D{root: &srcNode{}},
		in:    &table2D{root: &srcNode{}},
		rules: append([]Rule(nil), rules...),
	}
	seen := make(map[string]struct{})
	for i, rule := range rules {
		if rule.Name == "" {
			return nil, fmt.Errorf("policypub: rule %d has no name", i)
		}
		if !rule.Local.IsValid() || !rule.Remote.IsValid() {
			return nil, fmt.Errorf("policypub: rule %q has an invalid prefix", rule.Name)
		}
		if rule.Local.Addr().BitLen() != rule.Remote.Addr().BitLen() {
			return nil, fmt.Errorf("policypub: rule %q mixes address families", rule.Name)
		}
		if rule.OutboundSA == "" || rule.InboundSA == "" {
			return nil, fmt.Errorf("policypub: rule %q is missing an SA handle", rule.Name)
		}
		local := rule.Local.Masked()
		remote := rule.Remote.Masked()
		key := local.String() + "->" + remote.String()
		if _, dup := seen[key]; dup {
			return nil, fmt.Errorf("policypub: duplicate selector %s", key)
		}
		seen[key] = struct{}{}

		table.out.insert(local, remote, rule.OutboundSA)
		table.in.insert(remote, local, rule.InboundSA)
	}
	return table, nil
}
