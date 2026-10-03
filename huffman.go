package huffsync

import "sort"

// SymInvalid is a reserved pseudo-symbol. It is assigned a real (long)
// codeword so the decoder can recognise segment terminators and detect
// corruption, but it never appears in genuine data.
const SymInvalid = 256

// NumSymbols is the alphabet size: 256 byte values plus SymInvalid.
const NumSymbols = 257

// HuffCode is a Huffman codebook over NumSymbols symbols.
type HuffCode struct {
	code   [NumSymbols]uint64
	length [NumSymbols]int
	root   *huffNode
}

type huffNode struct {
	sym   int // -1 for internal nodes
	freq  uint64
	left  *huffNode
	right *huffNode
}

// BuildHuffman builds a codebook from symbol frequencies. freq[SymInvalid]
// must be non-zero (a tiny value such as 1) so the terminator gets a code.
func BuildHuffman(freq [NumSymbols]uint64) *HuffCode {
	var nodes []*huffNode
	for s := 0; s < NumSymbols; s++ {
		if freq[s] > 0 {
			nodes = append(nodes, &huffNode{sym: s, freq: freq[s]})
		}
	}
	if len(nodes) == 0 {
		panic("huffman: empty frequency table")
	}
	if len(nodes) == 1 {
		// Degenerate alphabet: give the single symbol a 1-bit code.
		leaf := nodes[0]
		nodes = []*huffNode{{sym: -1, freq: leaf.freq, left: leaf, right: leaf}}
	}
	for len(nodes) > 1 {
		sort.Slice(nodes, func(i, j int) bool { return nodes[i].freq < nodes[j].freq })
		l, r := nodes[0], nodes[1]
		nodes = append(nodes[2:], &huffNode{sym: -1, freq: l.freq + r.freq, left: l, right: r})
	}
	c := &HuffCode{root: nodes[0]}
	var walk func(n *huffNode, code uint64, depth int)
	walk = func(n *huffNode, code uint64, depth int) {
		if n.sym >= 0 {
			if depth == 0 {
				depth = 1
			}
			c.code[n.sym] = code
			c.length[n.sym] = depth
			return
		}
		walk(n.left, code<<1, depth+1)
		walk(n.right, code<<1|1, depth+1)
	}
	walk(c.root, 0, 0)
	return c
}

// CodeLen returns the bit length of sym's codeword.
func (c *HuffCode) CodeLen(sym int) int { return c.length[sym] }

func (c *HuffCode) encodeTo(b *BitString, sym int) {
	b.appendBits(c.code[sym], c.length[sym])
}

// decode reads one symbol starting at bit pos; end is the exclusive bit
// limit. It returns the symbol and the position just past its codeword.
// ok is false if the bits ran out before a leaf was reached (a hallmark
// of a corrupted or truncated segment).
func (c *HuffCode) decode(b *BitString, pos, end int) (sym, next int, ok bool) {
	n := c.root
	for {
		if pos >= end {
			return 0, pos, false
		}
		if b.At(pos) {
			n = n.right
		} else {
			n = n.left
		}
		pos++
		if n.sym >= 0 {
			return n.sym, pos, true
		}
	}
}
