package acl

import (
	"errors"
	"fmt"
	"net/netip"
	"sort"
)

const (
	// Deny is the implicit action when no rule matches an address.
	Deny = iota
	Allow
)

const (
	ipv4Bits = 32
	// Sentinel rule index. int32 cannot hold 1<<31-1 via 1<<31, so use
	// a plain large constant well beyond the 100k-rule input limit.
	noRule int32 = 1_000_000_000
)

// Rule is one input entry: a CIDR prefix with an allow/deny action.
type Rule struct {
	Prefix netip.Prefix
	Action int
}

// Trie holds a compiled first-match-wins ACL over IPv4.
type Trie struct {
	nodes []node
	path  []int32
}

// node is a binary trie node. exact is the index of the rule whose
// (masked) prefix ends here; min is the smallest rule index visible in
// the subtree rooted at this node, exact included.
type node struct {
	children [2]int32
	exact    int32
	min      int32
}

func newTrie() *Trie {
	// Index 0 is the missing-child sentinel; the real root starts at 1
	// so a child id of 0 can never be confused with a live node.
	nodes := make([]node, 2, 4096)
	nodes[0] = node{exact: noRule, min: noRule}
	nodes[1] = node{exact: noRule, min: noRule}
	return &Trie{nodes: nodes}
}

// ParseAction maps the JSON action strings to an action constant.
func ParseAction(s string) (int, error) {
	switch s {
	case "allow":
		return Allow, nil
	case "deny":
		return Deny, nil
	default:
		return 0, fmt.Errorf("invalid action %q: want allow or deny", s)
	}
}

// ParseRule parses one [CIDR, action] entry. The host part of the CIDR
// is masked off, so e.g. 10.1.2.3/8 becomes 10.0.0.0/8.
func ParseRule(cidr, action string) (Rule, error) {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return Rule{}, fmt.Errorf("invalid CIDR %q: %w", cidr, err)
	}
	if !prefix.Addr().Is4() {
		return Rule{}, fmt.Errorf("invalid CIDR %q: only IPv4 is supported", cidr)
	}
	act, err := ParseAction(action)
	if err != nil {
		return Rule{}, err
	}
	// netip.ParsePrefix keeps the host bits; normalize explicitly so
	// 10.1.2.3/24 is treated exactly like 10.1.2.0/24.
	return Rule{
		Prefix: netip.PrefixFrom(prefix.Addr(), prefix.Bits()).Masked(),
		Action: act,
	}, nil
}

// Compile builds a first-match-wins trie from the rules in list order.
func Compile(rules []Rule) *Trie {
	t := newTrie()
	for index, rule := range rules {
		t.insert(rule, int32(index))
	}
	return t
}

func (t *Trie) insert(rule Rule, index int32) {
	addr := rule.Prefix.Addr().As4()
	bits := rule.Prefix.Bits()
	value := uint32(addr[0])<<24 | uint32(addr[1])<<16 |
		uint32(addr[2])<<8 | uint32(addr[3])
	if cap(t.path) < bits+1 {
		t.path = make([]int32, 0, bits+1)
	}
	path := t.path[:0]
	current := int32(1)
	path = append(path, current)
	for depth := 0; depth < bits; depth++ {
		bit := int((value >> (uint(ipv4Bits) - 1 - uint(depth))) & 1)
		next := t.nodes[current].children[bit]
		if next == 0 {
			next = t.newNode()
			t.nodes[current].children[bit] = next
		}
		current = next
		path = append(path, current)
	}
	// First-match-wins: on duplicate prefixes the earlier rule already
	// stored at exact wins, so keep the first index.
	if t.nodes[current].exact == noRule {
		t.nodes[current].exact = index
	}
	// Propagate the rule index back up so every ancestor's min sees the
	// smallest index reachable anywhere in its subtree.
	for position := len(path) - 1; position >= 0; position-- {
		nodeID := path[position]
		if index < t.nodes[nodeID].min {
			t.nodes[nodeID].min = index
		}
	}
	t.path = path
}

func (t *Trie) newNode() int32 {
	t.nodes = append(t.nodes, node{exact: noRule, min: noRule})
	return int32(len(t.nodes) - 1)
}

// Len returns the number of trie nodes including the missing-child sentinel.
func (t *Trie) Len() int { return len(t.nodes) }

// uniform reports whether every address under node id is decided by the
// same rule. inherited is the best (smallest index) rule inherited from
// ancestors, or noRule. The returned index is the deciding rule.
func (t *Trie) uniform(id, inherited int32) (int32, bool) {
	nd := &t.nodes[id]
	bestAncestor := inherited
	if nd.exact != noRule && nd.exact < bestAncestor {
		bestAncestor = nd.exact
	}
	if nd.min == noRule || nd.min >= bestAncestor {
		return bestAncestor, true
	}
	return bestAncestor, false
}

// Block is one aligned IPv4 range where the two ACLs disagree.
type Block struct {
	Start uint32
	Size  uint64
	Old   int
	New   int
}

// Diff returns the minimal aligned CIDR blocks on which old and new
// take different actions, sorted by start address.
func Diff(rulesOld, rulesNew []Rule) ([]Block, error) {
	if int32(len(rulesOld)) >= noRule || int32(len(rulesNew)) >= noRule {
		return nil, errors.New("too many rules: at most 1073741822 supported")
	}
	old := Compile(rulesOld)
	newTree := Compile(rulesNew)

	blocks := make([]Block, 0)
	var walk func(idOld, idNew, inheritedOld, inheritedNew, depth int, start uint32)
	walk = func(idOld, idNew, inheritedOld, inheritedNew, depth int, start uint32) {
		decOld, uniformOld := old.uniform(int32(idOld), int32(inheritedOld))
		decNew, uniformNew := newTree.uniform(int32(idNew), int32(inheritedNew))
		if uniformOld && uniformNew {
			actionOld := actionFor(rulesOld, decOld)
			actionNew := actionFor(rulesNew, decNew)
			if actionOld != actionNew {
				blocks = append(blocks, Block{
					Start: start,
					Size:  1 << uint(ipv4Bits-depth),
					Old:   actionOld,
					New:   actionNew,
				})
			}
			return
		}
		if depth == ipv4Bits {
			return
		}
		childSize := uint64(1) << uint(ipv4Bits-depth-1)
		for bit := uint32(0); bit <= 1; bit++ {
			childStart := uint64(start) + uint64(bit)*childSize
			co := old.nodes[idOld].children[bit]
			cn := newTree.nodes[idNew].children[bit]
			walk(int(co), int(cn), int(decOld), int(decNew), depth+1, uint32(childStart))
		}
	}
	walk(1, 1, int(noRule), int(noRule), 0, 0)

	merged := mergeAdjacent(blocks)
	sort.Slice(merged, func(i, j int) bool { return merged[i].Start < merged[j].Start })
	return merged, nil
}

func actionFor(rules []Rule, index int32) int {
	if index == noRule {
		return Deny
	}
	return rules[index].Action
}

// mergeAdjacent combines same-direction sibling blocks into the largest
// aligned CIDR blocks. Input blocks are aligned, disjoint and cover the
// full difference set.
func mergeAdjacent(blocks []Block) []Block {
	byStart := make(map[uint32]Block, len(blocks))
	for _, block := range blocks {
		byStart[block.Start] = block
	}
	const space = uint64(1) << ipv4Bits
	for depth := ipv4Bits - 1; depth >= 0; depth-- {
		size := uint64(1) << uint(ipv4Bits-depth)
		half := uint64(1) << uint(ipv4Bits-depth-1)
		for start, block := range byStart {
			if block.Size != half {
				continue
			}
			if uint64(start)%size != 0 {
				continue
			}
			siblingStart := uint64(start) + half
			if siblingStart >= space {
				continue
			}
			sibling, ok := byStart[uint32(siblingStart)]
			if !ok || sibling.Size != half || sibling.Old != block.Old || sibling.New != block.New {
				continue
			}
			delete(byStart, uint32(siblingStart))
			byStart[start] = Block{Start: start, Size: size, Old: block.Old, New: block.New}
		}
	}
	merged := make([]Block, 0, len(byStart))
	for _, block := range byStart {
		merged = append(merged, block)
	}
	return merged
}

// Prefix renders the block as the CIDR prefix covering exactly its range.
func (b Block) Prefix() netip.Prefix {
	addr := netip.AddrFrom4([4]byte{
		byte(b.Start >> 24),
		byte(b.Start >> 16),
		byte(b.Start >> 8),
		byte(b.Start),
	})
	bits := ipv4Bits
	for size := b.Size; size > 1; size >>= 1 {
		bits--
	}
	return netip.PrefixFrom(addr, bits)
}

// ActionName renders an action constant as the JSON string form.
func ActionName(action int) string {
	if action == Allow {
		return "allow"
	}
	return "deny"
}
