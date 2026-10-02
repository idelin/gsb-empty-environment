package deflate

import (
	"container/heap"
	"sort"
)

const maxCodeBits = 15

// huffItem is a node in the Huffman construction heap. Leaf nodes are
// identified by their symbol index (0..n-1); internal nodes use ids >= n.
type huffItem struct {
	freq  uint32
	index int
	depth int // tree depth, used as tie-breaker like zlib
}

type huffHeap []huffItem

func (h huffHeap) Len() int { return len(h) }
func (h huffHeap) Less(i, j int) bool {
	if h[i].freq != h[j].freq {
		return h[i].freq < h[j].freq
	}
	return h[i].depth < h[j].depth
}
func (h huffHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *huffHeap) Push(x interface{}) { *h = append(*h, x.(huffItem)) }
func (h *huffHeap) Pop() interface{} {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// buildCodeLengths computes length-limited Huffman code lengths for the given
// symbol frequencies. The result satisfies the Kraft equality and no length
// exceeds maxBits. freqs may be modified (a minimum of two used symbols is
// enforced, as deflate does not permit empty or singleton codes here).
func buildCodeLengths(freqs []uint32, maxBits int) []uint8 {
	n := len(freqs)
	lengths := make([]uint8, n)

	nonzero := 0
	for _, f := range freqs {
		if f > 0 {
			nonzero++
		}
	}
	switch nonzero {
	case 0:
		freqs[0], freqs[1] = 1, 1
	case 1:
		if freqs[0] == 0 {
			freqs[0] = 1
		} else {
			freqs[1] = 1
		}
	}

	// Build the Huffman tree. Leaves keep their symbol index as node id.
	left := make([]int, 2*n)
	right := make([]int, 2*n)
	depth := make([]int, 2*n)
	h := &huffHeap{}
	for sym, f := range freqs {
		if f > 0 {
			heap.Push(h, huffItem{freq: f, index: sym})
		}
	}
	next := n
	for h.Len() > 1 {
		a := heap.Pop(h).(huffItem)
		b := heap.Pop(h).(huffItem)
		id := next
		next++
		left[id] = a.index
		right[id] = b.index
		d := a.depth
		if b.depth > d {
			d = b.depth
		}
		depth[id] = d + 1
		heap.Push(h, huffItem{freq: a.freq + b.freq, index: id, depth: depth[id]})
	}
	root := heap.Pop(h).(huffItem)

	// Compute leaf depths and count how many codes have each bit length.
	var blCount [maxCodeBits + 1]uint32
	overflow := 0
	type frame struct{ id, d int }
	stack := []frame{{root.index, 0}}
	for len(stack) > 0 {
		fr := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if fr.id < n {
			l := fr.d
			if l > maxBits {
				l = maxBits
				overflow++
			}
			blCount[l]++
			continue
		}
		stack = append(stack, frame{left[fr.id], fr.d + 1}, frame{right[fr.id], fr.d + 1})
	}

	// Resolve overflow the way zlib's gen_bitlen does: move leaves down the
	// tree until the Kraft sum is restored with all lengths <= maxBits.
	for overflow > 0 {
		bits := maxBits - 1
		for blCount[bits] == 0 {
			bits--
		}
		blCount[bits]--
		blCount[bits+1] += 2
		blCount[maxBits]--
		overflow -= 2
	}

	// Assign lengths: least frequent symbols get the longest codes.
	syms := make([]int, 0, n)
	for sym, f := range freqs {
		if f > 0 {
			syms = append(syms, sym)
		}
	}
	sort.Slice(syms, func(i, j int) bool {
		fi, fj := freqs[syms[i]], freqs[syms[j]]
		if fi != fj {
			return fi < fj
		}
		return syms[i] < syms[j]
	})
	idx := 0
	for bits := maxBits; bits >= 1; bits-- {
		for c := uint32(0); c < blCount[bits]; c++ {
			lengths[syms[idx]] = uint8(bits)
			idx++
		}
	}
	return lengths
}

// canonicalCodes assigns canonical Huffman codes (RFC 1951 3.2.2) for the
// given code lengths.
func canonicalCodes(lengths []uint8) []uint16 {
	var blCount [maxCodeBits + 1]uint16
	for _, l := range lengths {
		if l > 0 {
			blCount[l]++
		}
	}
	var nextCode [maxCodeBits + 1]uint16
	code := 0
	for bits := 1; bits <= maxCodeBits; bits++ {
		code = (code + int(blCount[bits-1])) << 1
		nextCode[bits] = uint16(code)
	}
	codes := make([]uint16, len(lengths))
	for i, l := range lengths {
		if l > 0 {
			codes[i] = nextCode[l]
			nextCode[l]++
		}
	}
	return codes
}

func reverseBits(code uint16, n uint8) uint16 {
	var r uint16
	for i := uint8(0); i < n; i++ {
		r = (r << 1) | (code & 1)
		code >>= 1
	}
	return r
}

// huffEncoder stores bit-reversed canonical codes so they can be emitted
// with the LSB-first bit writer.
type huffEncoder struct {
	codes []uint16
	lens  []uint8
}

func newHuffEncoder(lengths []uint8) huffEncoder {
	codes := canonicalCodes(lengths)
	for i, l := range lengths {
		if l > 0 {
			codes[i] = reverseBits(codes[i], l)
		}
	}
	return huffEncoder{codes: codes, lens: lengths}
}

func (e huffEncoder) write(w *bitWriter, sym int) {
	w.writeBits(uint32(e.codes[sym]), uint(e.lens[sym]))
}

// huffDecoder decodes canonical Huffman codes using the count/offset method
// (same approach as zlib's puff.c).
type huffDecoder struct {
	count  [maxCodeBits + 1]uint16
	symbol []uint16
}

func newHuffDecoder(lengths []uint8) (*huffDecoder, error) {
	d := &huffDecoder{}
	for _, l := range lengths {
		if l > maxCodeBits {
			return nil, errInvalidCode
		}
		d.count[l]++
	}
	d.count[0] = 0
	// Check for an over-subscribed set of lengths.
	left := 1
	for bits := 1; bits <= maxCodeBits; bits++ {
		left <<= 1
		left -= int(d.count[bits])
		if left < 0 {
			return nil, errInvalidCode
		}
	}
	var offs [maxCodeBits + 1]uint16
	for bits := 1; bits < maxCodeBits; bits++ {
		offs[bits+1] = offs[bits] + d.count[bits]
	}
	d.symbol = make([]uint16, len(lengths))
	for sym, l := range lengths {
		if l > 0 {
			d.symbol[offs[l]] = uint16(sym)
			offs[l]++
		}
	}
	return d, nil
}

func (d *huffDecoder) decode(r *bitReader) (uint16, error) {
	code, first, index := 0, 0, 0
	for l := 1; l <= maxCodeBits; l++ {
		b, err := r.readBits(1)
		if err != nil {
			return 0, err
		}
		code |= int(b)
		cnt := int(d.count[l])
		if code-first < cnt {
			return d.symbol[index+(code-first)], nil
		}
		index += cnt
		first = (first + cnt) << 1
		code <<= 1
	}
	return 0, errInvalidCode
}
