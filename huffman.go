package deflate

import (
	"container/heap"
	"sort"
)

const maxCodeBits = 15

type hnode struct {
	freq        uint64
	sym         int
	left, right *hnode
}

type nodeHeap []*hnode

func (h nodeHeap) Len() int { return len(h) }
func (h nodeHeap) Less(i, j int) bool {
	if h[i].freq != h[j].freq {
		return h[i].freq < h[j].freq
	}
	return h[i].sym < h[j].sym
}
func (h nodeHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *nodeHeap) Push(x interface{}) { *h = append(*h, x.(*hnode)) }
func (h *nodeHeap) Pop() interface{} {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// buildCodeLengths computes Huffman code lengths for the given symbol
// frequencies, limited to maxBits bits (zlib-style overflow fixup).
func buildCodeLengths(freq []uint32, maxBits uint8) []uint8 {
	lengths := make([]uint8, len(freq))
	h := &nodeHeap{}
	for sym, f := range freq {
		if f > 0 {
			heap.Push(h, &hnode{freq: uint64(f), sym: sym})
		}
	}
	switch h.Len() {
	case 0:
		return lengths
	case 1:
		lengths[(*h)[0].sym] = 1
		return lengths
	}
	for h.Len() > 1 {
		a := heap.Pop(h).(*hnode)
		b := heap.Pop(h).(*hnode)
		heap.Push(h, &hnode{freq: a.freq + b.freq, sym: -1, left: a, right: b})
	}
	root := heap.Pop(h).(*hnode)
	var walk func(n *hnode, depth uint8)
	walk = func(nd *hnode, depth uint8) {
		if nd.left == nil && nd.right == nil {
			lengths[nd.sym] = depth
			return
		}
		walk(nd.left, depth+1)
		walk(nd.right, depth+1)
	}
	walk(root, 0)
	limitLengths(lengths, freq, maxBits)
	return lengths
}

// limitLengths clamps code lengths to maxBits while keeping the Kraft
// sum exactly 1, reassigning the longest codes to the rarest symbols.
func limitLengths(lengths []uint8, freq []uint32, maxBits uint8) {
	overflow := 0
	for i, l := range lengths {
		if l > maxBits {
			lengths[i] = maxBits
			overflow++
		}
	}
	if overflow == 0 {
		return
	}
	var count [maxCodeBits + 1]int
	for _, l := range lengths {
		if l > 0 {
			count[l]++
		}
	}
	for overflow > 0 {
		bits := int(maxBits) - 1
		for bits > 0 && count[bits] == 0 {
			bits--
		}
		if bits == 0 {
			break
		}
		count[bits]--
		count[bits+1] += 2
		count[maxBits]--
		overflow -= 2
	}
	// Reassign: least frequent symbols receive the longest codes.
	order := make([]int, 0, len(lengths))
	for i, l := range lengths {
		if l > 0 {
			order = append(order, i)
		}
	}
	sort.Slice(order, func(a, b int) bool {
		fa, fb := freq[order[a]], freq[order[b]]
		if fa != fb {
			return fa < fb
		}
		return order[a] < order[b]
	})
	idx := 0
	for bits := int(maxBits); bits >= 1; bits-- {
		for k := 0; k < count[bits]; k++ {
			lengths[order[idx]] = uint8(bits)
			idx++
		}
	}
}

// canonicalCodes assigns canonical Huffman codes per RFC 1951 section 3.2.2.
func canonicalCodes(lengths []uint8) []uint16 {
	var count [maxCodeBits + 1]uint16
	for _, l := range lengths {
		count[l]++
	}
	count[0] = 0
	var next [maxCodeBits + 1]uint16
	code := uint16(0)
	for bits := 1; bits <= maxCodeBits; bits++ {
		code = (code + count[bits-1]) << 1
		next[bits] = code
	}
	codes := make([]uint16, len(lengths))
	for i, l := range lengths {
		if l > 0 {
			codes[i] = next[l]
			next[l]++
		}
	}
	return codes
}

// huffmanDecoder decodes canonical Huffman codes (puff-style walk).
type huffmanDecoder struct {
	count   [maxCodeBits + 1]uint16
	symbols []uint16
}

func newHuffmanDecoder(lengths []uint8) *huffmanDecoder {
	d := &huffmanDecoder{}
	total := 0
	for _, l := range lengths {
		if l > 0 {
			d.count[l]++
			total++
		}
	}
	d.symbols = make([]uint16, total)
	var offs [maxCodeBits + 1]int
	for l := 1; l <= maxCodeBits; l++ {
		offs[l] = offs[l-1] + int(d.count[l-1])
	}
	for sym, l := range lengths {
		if l > 0 {
			d.symbols[offs[l]] = uint16(sym)
			offs[l]++
		}
	}
	return d
}

func (d *huffmanDecoder) decode(r *bitReader) (uint16, error) {
	code, first, index := 0, 0, 0
	for l := 1; l <= maxCodeBits; l++ {
		if r.err != nil {
			return 0, r.err
		}
		code |= int(r.readBit())
		cnt := int(d.count[l])
		if code-first < cnt {
			return d.symbols[index+(code-first)], nil
		}
		index += cnt
		first = (first + cnt) << 1
		code <<= 1
	}
	return 0, errCorrupt
}
