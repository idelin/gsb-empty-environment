package framearc

import "sort"

// codeEntry is one row of a prefix code table with its canonical code.
type codeEntry struct {
	symbol byte
	length uint8
	code   uint32
}

// canonicalCodes validates a (symbol, length) table and assigns canonical
// codes: entries sorted by (length, symbol) receive consecutive codes.
func canonicalCodes(table [][2]uint8) ([]codeEntry, error) {
	if len(table) == 0 || len(table) > maxTableLen {
		return nil, errBadTable
	}
	entries := make([]codeEntry, len(table))
	var seen [256]bool
	for i, e := range table {
		if e[1] < 1 || e[1] > maxCodeLen {
			return nil, errBadTable
		}
		if seen[e[0]] {
			return nil, errBadTable
		}
		seen[e[0]] = true
		entries[i] = codeEntry{symbol: e[0], length: e[1]}
	}
	sort.Slice(entries, func(a, b int) bool {
		if entries[a].length != entries[b].length {
			return entries[a].length < entries[b].length
		}
		return entries[a].symbol < entries[b].symbol
	})
	var code uint32
	var prevLen uint8
	for i := range entries {
		code <<= uint(entries[i].length - prevLen)
		if code >= 1<<uint(entries[i].length) {
			return nil, errBadTable
		}
		entries[i].code = code
		code++
		prevLen = entries[i].length
	}
	return entries, nil
}

// huffmanLengths derives code lengths from symbol frequencies. If the
// Huffman tree would exceed maxCodeLen (highly skewed input), it falls
// back to a fixed-length code, which is always a valid prefix code.
func huffmanLengths(freq *[256]uint64) []uint8 {
	lengths := make([]uint8, 256)
	type node struct {
		freq   uint64
		parent int
	}
	var nodes []node
	leafSym := map[int]int{}
	var roots []int
	for sym := 0; sym < 256; sym++ {
		if freq[sym] > 0 {
			leafSym[len(nodes)] = sym
			nodes = append(nodes, node{freq: freq[sym], parent: -1})
			roots = append(roots, len(nodes)-1)
		}
	}
	switch len(roots) {
	case 0:
		return lengths
	case 1:
		lengths[leafSym[roots[0]]] = 1
		return lengths
	}
	for len(roots) > 1 {
		// pick the two smallest roots
		a, b := 0, 1
		if nodes[roots[a]].freq > nodes[roots[b]].freq {
			a, b = b, a
		}
		for k := 2; k < len(roots); k++ {
			switch {
			case nodes[roots[k]].freq < nodes[roots[a]].freq:
				b = a
				a = k
			case nodes[roots[k]].freq < nodes[roots[b]].freq:
				b = k
			}
		}
		merged := len(nodes)
		nodes = append(nodes, node{
			freq:   nodes[roots[a]].freq + nodes[roots[b]].freq,
			parent: -1,
		})
		nodes[roots[a]].parent = merged
		nodes[roots[b]].parent = merged
		na, nb := roots[a], roots[b]
		roots = append(roots, merged)
		out := roots[:0]
		for _, r := range roots {
			if r != na && r != nb {
				out = append(out, r)
			}
		}
		roots = out
	}
	maxDepth := 0
	for idx, sym := range leafSym {
		depth := 0
		for n := idx; nodes[n].parent >= 0; n = nodes[n].parent {
			depth++
		}
		lengths[sym] = uint8(depth)
		if depth > maxDepth {
			maxDepth = depth
		}
	}
	if maxDepth > maxCodeLen {
		bits := 1
		for (1 << uint(bits)) < len(leafSym) {
			bits++
		}
		for _, sym := range leafSym {
			lengths[sym] = uint8(bits)
		}
	}
	return lengths
}

// bitWriter appends codes MSB-first and zero-pads the final byte.
type bitWriter struct {
	buf []byte
	cur byte
	n   uint
}

func (w *bitWriter) writeBits(code uint32, length uint8) {
	for i := int(length) - 1; i >= 0; i-- {
		w.cur = w.cur<<1 | byte(code>>uint(i)&1)
		w.n++
		if w.n == 8 {
			w.buf = append(w.buf, w.cur)
			w.cur = 0
			w.n = 0
		}
	}
}

func (w *bitWriter) finish() []byte {
	if w.n > 0 {
		w.buf = append(w.buf, w.cur<<(8-w.n))
		w.cur = 0
		w.n = 0
	}
	return w.buf
}

type bitReader struct {
	data []byte
	pos  int
}

func (r *bitReader) next() (byte, bool) {
	if r.pos >= len(r.data)*8 {
		return 0, false
	}
	b := r.data[r.pos/8] >> uint(7-r.pos%8) & 1
	r.pos++
	return b, true
}

// decodePayload decodes exactly origLen symbols from payload. Any
// structural problem (running out of bits, overlong code, non-zero
// padding) is an error; it never guesses which symbol is wrong.
func decodePayload(entries []codeEntry, payload []byte, origLen int) ([]byte, error) {
	type key struct {
		length uint8
		code   uint32
	}
	lut := make(map[key]byte, len(entries))
	for _, e := range entries {
		lut[key{e.length, e.code}] = e.symbol
	}
	out := make([]byte, 0, origLen)
	br := bitReader{data: payload}
	var code uint32
	var length uint8
	for len(out) < origLen {
		bit, ok := br.next()
		if !ok {
			return nil, errTruncated
		}
		code = code<<1 | uint32(bit)
		length++
		if length > maxCodeLen {
			return nil, errInvalidCode
		}
		if sym, ok := lut[key{length, code}]; ok {
			out = append(out, sym)
			code, length = 0, 0
		}
	}
	for br.pos < len(payload)*8 {
		bit, _ := br.next()
		if bit != 0 {
			return nil, errNonZeroPadding
		}
	}
	return out, nil
}
