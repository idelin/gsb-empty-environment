package rpz

import "net/netip"

// bitTrie is a binary radix trie keyed on raw address bits. It stores one
// rule per network prefix and supports longest-prefix-match lookups used by
// the CLIENT-IP, NSIP and IP triggers.
type bitTrie struct {
	root *trieNode
}

type trieNode struct {
	children [2]*trieNode
	// rule is non-nil only at nodes that terminate an inserted prefix.
	rule *Rule
}

func newBitTrie() *bitTrie {
	return &bitTrie{root: &trieNode{}}
}

// Insert stores rule at the prefix. A rule already present at the exact
// prefix is merged rather than replaced.
func (t *bitTrie) Insert(prefix netip.Prefix, rule *Rule) {
	addr := prefix.Addr()
	bits := prefix.Bits()
	raw := addr.AsSlice()

	node := t.root
	for bit := 0; bit < bits; bit++ {
		side := (raw[bit/8] >> (7 - uint(bit%8))) & 1
		if node.children[side] == nil {
			node.children[side] = &trieNode{}
		}
		node = node.children[side]
	}
	if node.rule != nil {
		node.rule = mergeRule(node.rule, rule)
	} else {
		node.rule = rule
	}
}

// Lookup returns the rule whose prefix is the longest one containing addr, or
// nil when nothing matches.
func (t *bitTrie) Lookup(addr netip.Addr) *Rule {
	raw := addr.AsSlice()
	node := t.root
	var best *Rule

	if node.rule != nil {
		best = node.rule
	}
	for bit := 0; bit < len(raw)*8; bit++ {
		side := (raw[bit/8] >> (7 - uint(bit%8))) & 1
		node = node.children[side]
		if node == nil {
			break
		}
		if node.rule != nil {
			best = node.rule
		}
	}
	return best
}

// Empty reports whether no prefix has been inserted.
func (t *bitTrie) Empty() bool {
	return t.root.children[0] == nil && t.root.children[1] == nil && t.root.rule == nil
}
