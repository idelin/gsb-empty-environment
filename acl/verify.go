package acl

import (
	"fmt"
	"math/rand"
	"net/netip"
	"sort"
)

// LookupRule returns the index of the first rule matching addr, or
// noRule when nothing matches (the caller then applies default deny).
func (t *Trie) LookupRule(addr uint32) int32 {
	current := int32(1)
	var winner int32 = noRule
	for depth := 0; depth <= ipv4Bits; depth++ {
		nd := &t.nodes[current]
		// Every exact rule on the root-to-leaf path matches the address.
		// Rules are indexed in list order, so the smallest index seen is
		// the first matching rule, regardless of prefix length.
		if nd.exact != noRule && nd.exact < winner {
			winner = nd.exact
		}
		if depth == ipv4Bits {
			break
		}
		bit := int((addr >> (uint(ipv4Bits) - 1 - uint(depth))) & 1)
		// A missing child moves to the sentinel node, which carries no
		// rules and whose children are also sentinels, so the rest of
		// the walk cannot pick up a leaf rule that does not contain the
		// address.
		current = nd.children[bit]
	}
	return winner
}

// Replay evaluates one address against the raw rule list in order,
// mirroring the documented first-match-wins semantics independently of
// the trie implementation.
func Replay(rules []Rule, addr uint32) int {
	for _, rule := range rules {
		if prefixContains(rule.Prefix, addr) {
			return rule.Action
		}
	}
	return Deny
}

func prefixContains(prefix netip.Prefix, addr uint32) bool {
	bits := prefix.Bits()
	if bits == 0 {
		return true
	}
	base := ipv4ToUint32(prefix.Masked().Addr().As4())
	mask := ^uint32(0) << uint(ipv4Bits-bits)
	return (base & mask) == (addr & mask)
}

func ipv4ToUint32(octets [4]byte) uint32 {
	return uint32(octets[0])<<24 | uint32(octets[1])<<16 |
		uint32(octets[2])<<8 | uint32(octets[3])
}

// Verify replays the rule lists against the computed difference blocks
// and returns an error describing the first inconsistency found.
//
// It checks, for every block: both endpoints and interior samples take
// the reported actions; random samples elsewhere take equal actions in
// both ACLs; and trie lookups agree with a linear replay of the rules.
func Verify(rulesOld, rulesNew []Rule, blocks []Block, samples int, rng *rand.Rand) error {
	old := Compile(rulesOld)
	newTree := Compile(rulesNew)

	check := func(addr uint32) error {
		replayOld := Replay(rulesOld, addr)
		replayNew := Replay(rulesNew, addr)

		trieOld := resolve(rulesOld, old.LookupRule(addr))
		trieNew := resolve(rulesNew, newTree.LookupRule(addr))
		if trieOld != replayOld || trieNew != replayNew {
			return fmt.Errorf("trie/replay disagreement at %08x: trie=(%d,%d) replay=(%d,%d)",
				addr, trieOld, trieNew, replayOld, replayNew)
		}

		block, covered := findBlock(blocks, addr)
		switch {
		case covered && (block.Old != replayOld || block.New != replayNew):
			return fmt.Errorf("block %s reports (%d,%d) but replay at %08x gives (%d,%d)",
				block.Prefix(), block.Old, block.New, addr, replayOld, replayNew)
		case !covered && replayOld != replayNew:
			return fmt.Errorf("address %08x differs (%d vs %d) but is outside all blocks",
				addr, replayOld, replayNew)
		}
		return nil
	}

	for _, block := range blocks {
		if err := check(block.Start); err != nil {
			return err
		}
		last, overflow := blockEnd(block)
		if overflow {
			return fmt.Errorf("block %s extends past the IPv4 space", block.Prefix())
		}
		if err := check(last); err != nil {
			return err
		}
		for i := 0; i < 4 && !overflow; i++ {
			offset := rng.Uint64() % block.Size
			if err := check(block.Start + uint32(offset)); err != nil {
				return err
			}
		}
	}

	for i := 0; i < samples; i++ {
		if err := check(rng.Uint32()); err != nil {
			return err
		}
	}
	return nil
}

func resolve(rules []Rule, index int32) int {
	if index == noRule {
		return Deny
	}
	return rules[index].Action
}

func blockEnd(block Block) (uint32, bool) {
	end := uint64(block.Start) + block.Size - 1
	return uint32(end), end > (uint64(1)<<ipv4Bits)-1
}

func findBlock(blocks []Block, addr uint32) (Block, bool) {
	// Blocks are sorted and disjoint, so locate the last block whose
	// start is not greater than addr and then test its end boundary.
	index := sort.Search(len(blocks), func(i int) bool {
		return blocks[i].Start > addr
	}) - 1
	if index < 0 {
		return Block{}, false
	}
	block := blocks[index]
	if uint64(addr) < uint64(block.Start)+block.Size {
		return block, true
	}
	return Block{}, false
}
