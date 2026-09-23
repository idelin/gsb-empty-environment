// Package codec implements the actual byte-level encoding for the
// frequency models in package freq. Floating point entropies are only
// estimates; the lengths produced here are the real byte lengths.
//
// Stream format (all integers unsigned varints unless noted):
//
//   offset 0   magic: 'S' 'F' 'M'           (3 bytes)
//   offset 3   format version               (1 byte, currently 0x01)
//   offset 4   flags: 0=order-0, 1=order-1  (1 byte)
//   then       alphabet size                (varint, 1..256)
//              symbol count (payload length)(varint)
//              number of coding groups      (varint)
//              per group:
//                context symbol             (varint; 0 for order-0)
//                code length per symbol     (alphabet bytes, each 1..maxCodeLen)
//   then       CRC-32 (IEEE) of everything above  (4 bytes, big endian)
//   then       payload bits, MSB-first, zero-padded to a byte boundary
//
// Every symbol in the declared alphabet gets a nonzero code length
// (codes are built from count+1), so zero-frequency and unknown-but-
// in-alphabet symbols are always encodable: that is the explicit
// escape strategy. Symbols outside the declared alphabet are rejected
// at encode time. Decoding is bounded: the declared symbol count must
// not exceed the caller-supplied output limit.
package codec

import (
	"errors"
	"sort"
)

// maxCodeLen bounds canonical Huffman code lengths; weights are
// rescaled if a deeper tree would be needed.
const maxCodeLen = 24

// ErrUnknownSymbol is returned when encoding a symbol outside the
// declared alphabet (explicit rejection strategy).
var ErrUnknownSymbol = errors.New("codec: symbol outside declared alphabet")

// codeLengths builds canonical Huffman code lengths for the given
// symbol weights. Every symbol with weight > 0 gets a code. Weights
// are rescaled if the natural tree would exceed maxCodeLen.
func codeLengths(weights []uint64) []uint8 {
	for {
		lengths := buildLengths(weights)
		maxl := 0
		for _, l := range lengths {
			if int(l) > maxl {
				maxl = int(l)
			}
		}
		if maxl <= maxCodeLen {
			return lengths
		}
		scaled := make([]uint64, len(weights))
		for i, w := range weights {
			if w > 1 {
				w >>= 1
			}
			scaled[i] = w
		}
		weights = scaled
	}
}

func buildLengths(weights []uint64) []uint8 {
	n := len(weights)
	lengths := make([]uint8, n)
	type node struct {
		weight uint64
		parent int
		leaf   bool
		sym    int
	}
	var nodes []node
	present := 0
	for s, w := range weights {
		if w == 0 {
			continue
		}
		present++
		nodes = append(nodes, node{weight: w, parent: -1, leaf: true, sym: s})
	}
	if present == 0 {
		return lengths
	}
	if present == 1 {
		// A single-symbol alphabet still gets a 1-bit code so the
		// format stays uniform (deterministic data has entropy 0 but
		// the real byte length is not 0).
		for _, nd := range nodes {
			lengths[nd.sym] = 1
		}
		return lengths
	}
	// Repeatedly merge the two lightest parentless nodes (O(n^2),
	// fine for alphabets <= 256).
	active := present
	for active > 1 {
		m1, m2 := -1, -1
		for i := range nodes {
			if nodes[i].parent != -1 {
				continue
			}
			if m1 == -1 || nodes[i].weight < nodes[m1].weight {
				m2 = m1
				m1 = i
			} else if m2 == -1 || nodes[i].weight < nodes[m2].weight {
				m2 = i
			}
		}
		nodes = append(nodes, node{weight: nodes[m1].weight + nodes[m2].weight, parent: -1})
		nodes[m1].parent = len(nodes) - 1
		nodes[m2].parent = len(nodes) - 1
		active--
	}
	for i := range nodes {
		if !nodes[i].leaf {
			continue
		}
		depth := 0
		for j := i; nodes[j].parent != -1; j = nodes[j].parent {
			depth++
		}
		lengths[nodes[i].sym] = uint8(depth)
	}
	return lengths
}

// canonicalCode assigns canonical prefix codes for the given lengths:
// shorter codes first, ties broken by symbol value.
type canonicalCode struct {
	lengths []uint8
	codes   []uint32
	maxLen  int
}

func newCanonicalCode(lengths []uint8) *canonicalCode {
	cc := &canonicalCode{lengths: lengths, codes: make([]uint32, len(lengths))}
	type sl struct {
		sym int
		l   int
	}
	var syms []sl
	for s, l := range lengths {
		if l == 0 {
			continue
		}
		syms = append(syms, sl{sym: s, l: int(l)})
		if int(l) > cc.maxLen {
			cc.maxLen = int(l)
		}
	}
	sort.Slice(syms, func(i, j int) bool {
		if syms[i].l != syms[j].l {
			return syms[i].l < syms[j].l
		}
		return syms[i].sym < syms[j].sym
	})
	code := uint32(0)
	prevLen := 0
	for _, s := range syms {
		code <<= uint(s.l - prevLen)
		cc.codes[s.sym] = code
		code++
		prevLen = s.l
	}
	return cc
}

// bitWriter accumulates bits MSB-first.
type bitWriter struct {
	buf   []byte
	acc   uint32
	nbits int
}

func (w *bitWriter) writeBits(code uint32, n int) {
	w.acc = w.acc<<uint(n) | code
	w.nbits += n
	for w.nbits >= 8 {
		w.buf = append(w.buf, byte(w.acc>>uint(w.nbits-8)))
		w.nbits -= 8
		w.acc &= (1 << uint(w.nbits)) - 1
	}
}

func (w *bitWriter) bytes() []byte {
	if w.nbits > 0 {
		w.buf = append(w.buf, byte(w.acc<<uint(8-w.nbits)))
		w.acc = 0
		w.nbits = 0
	}
	return w.buf
}

// bitReader reads bits MSB-first.
type bitReader struct {
	buf   []byte
	pos   int // bit position
	nbits int
}

func newBitReader(buf []byte) *bitReader {
	return &bitReader{buf: buf, nbits: len(buf) * 8}
}

// read returns the next bit, or false when the buffer is exhausted.
func (r *bitReader) read() (int, bool) {
	if r.pos >= r.nbits {
		return 0, false
	}
	b := int(r.buf[r.pos/8]>>uint(7-r.pos%8)) & 1
	r.pos++
	return b, true
}

// remainingBits reports how many unread bits are left.
func (r *bitReader) remainingBits() int { return r.nbits - r.pos }
