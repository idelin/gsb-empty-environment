package pcf

import (
	"container/heap"
	"errors"
)

// maxCodeLen is the maximum prefix-code length stored in a frame's code
// table. Each table entry is one byte: 0 means the symbol is unused.
const maxCodeLen = 15

var (
	errOversubscribed = errors.New("oversubscribed prefix code")
	errEmptyTable     = errors.New("code table has no symbols")
	errBadCodeLen     = errors.New("code length out of range")
	errSingleLen      = errors.New("single-symbol code must have length 1")
)

// validateCodeLengths checks that lengths describe a usable prefix code:
// every length is in [0,maxCodeLen], at least one symbol exists, and the
// Kraft sum does not exceed 1 (the code is not oversubscribed).
func validateCodeLengths(lengths []byte) error {
	n := 0
	kraft := 0
	for _, l := range lengths {
		if l > maxCodeLen {
			return errBadCodeLen
		}
		if l == 0 {
			continue
		}
		n++
		kraft += 1 << uint(maxCodeLen-int(l))
	}
	if n == 0 {
		return errEmptyTable
	}
	if kraft > 1<<maxCodeLen {
		return errOversubscribed
	}
	if n == 1 {
		for _, l := range lengths {
			if l != 0 && l != 1 {
				return errSingleLen
			}
		}
	}
	return nil
}

// huffNode is a node of the Huffman tree built by buildCodeLengths.
type huffNode struct {
	freq        uint64
	sym         int // -1 for internal nodes
	left, right int
	seq         int // creation order, deterministic tie-break
}

// huffQueue is a priority queue of node indices ordered by (freq, seq).
type huffQueue struct {
	nodes []huffNode
	order []int
}

func (q *huffQueue) Len() int { return len(q.order) }
func (q *huffQueue) Less(i, j int) bool {
	a := q.nodes[q.order[i]]
	b := q.nodes[q.order[j]]
	if a.freq != b.freq {
		return a.freq < b.freq
	}
	return a.seq < b.seq
}
func (q *huffQueue) Swap(i, j int)      { q.order[i], q.order[j] = q.order[j], q.order[i] }
func (q *huffQueue) Push(x interface{}) { q.order = append(q.order, x.(int)) }
func (q *huffQueue) Pop() interface{} {
	old := q.order
	n := len(old)
	it := old[n-1]
	q.order = old[:n-1]
	return it
}

// buildCodeLengths derives prefix-code lengths for the 256 byte symbols
// from their frequencies. Symbols with zero frequency get length 0.
// Lengths never exceed maxCodeLen; if the natural Huffman tree is deeper,
// a flat code is used as fallback.
func buildCodeLengths(freqs []uint64) []byte {
	lengths := make([]byte, 256)
	n := 0
	for _, f := range freqs {
		if f > 0 {
			n++
		}
	}
	if n == 0 {
		return lengths
	}
	if n == 1 {
		for s := range freqs {
			if freqs[s] > 0 {
				lengths[s] = 1
			}
		}
		return lengths
	}
	q := &huffQueue{}
	seq := 0
	for s, f := range freqs {
		if f > 0 {
			q.nodes = append(q.nodes, huffNode{freq: f, sym: s, left: -1, right: -1, seq: seq})
			seq++
		}
	}
	q.order = make([]int, len(q.nodes))
	for i := range q.order {
		q.order[i] = i
	}
	heap.Init(q)
	for len(q.order) > 1 {
		a := heap.Pop(q).(int)
		b := heap.Pop(q).(int)
		na, nb := q.nodes[a], q.nodes[b]
		q.nodes = append(q.nodes, huffNode{freq: na.freq + nb.freq, sym: -1, left: a, right: b, seq: seq})
		seq++
		heap.Push(q, len(q.nodes)-1)
	}
	root := q.order[0]
	maxDepth := 0
	var walk func(idx, depth int)
	walk = func(idx, depth int) {
		nd := q.nodes[idx]
		if nd.sym >= 0 {
			lengths[nd.sym] = byte(depth)
			if depth > maxDepth {
				maxDepth = depth
			}
			return
		}
		walk(nd.left, depth+1)
		walk(nd.right, depth+1)
	}
	walk(root, 0)
	if maxDepth <= maxCodeLen {
		return lengths
	}
	// Fallback: flat code, Kraft sum stays <= 1 by construction.
	l := 1
	for (1 << uint(l)) < n {
		l++
	}
	lengths = make([]byte, 256)
	for s, f := range freqs {
		if f > 0 {
			lengths[s] = byte(l)
		}
	}
	return lengths
}

// canonicalCodes assigns canonical prefix codes: symbols are ordered by
// (code length, symbol value) and codes are assigned consecutively.
func canonicalCodes(lengths []byte) []uint32 {
	codes := make([]uint32, 256)
	var count [maxCodeLen + 1]int
	for _, l := range lengths {
		count[l]++
	}
	count[0] = 0
	var next [maxCodeLen + 1]uint32
	code := uint32(0)
	for l := 1; l <= maxCodeLen; l++ {
		code = (code + uint32(count[l-1])) << 1
		next[l] = code
	}
	for sym := 0; sym < 256; sym++ {
		l := lengths[sym]
		if l > 0 {
			codes[sym] = next[l]
			next[l]++
		}
	}
	return codes
}

// bitWriter packs codes MSB-first into bytes.
type bitWriter struct {
	out   []byte
	cur   byte
	nbits int
}

func (w *bitWriter) write(code uint32, length int) {
	for i := length - 1; i >= 0; i-- {
		w.cur = (w.cur << 1) | byte((code>>uint(i))&1)
		w.nbits++
		if w.nbits == 8 {
			w.out = append(w.out, w.cur)
			w.cur = 0
			w.nbits = 0
		}
	}
}

func (w *bitWriter) finish() []byte {
	if w.nbits > 0 {
		w.out = append(w.out, w.cur<<uint(8-w.nbits))
		w.cur = 0
		w.nbits = 0
	}
	return w.out
}

// encodePayload compresses data with the canonical code given by lengths.
func encodePayload(data []byte, lengths []byte) []byte {
	codes := canonicalCodes(lengths)
	w := bitWriter{}
	for _, b := range data {
		w.write(codes[b], int(lengths[b]))
	}
	return w.finish()
}

// decodePayload decodes exactly rawLen symbols from payload using the
// canonical code described by lengths. It fails unless the payload holds
// exactly the coded bits plus fewer than 8 zero padding bits, so a corrupt
// or foreign payload is rejected instead of being misdecoded.
func decodePayload(payload []byte, lengths []byte, rawLen int) ([]byte, error) {
	if rawLen == 0 {
		if len(payload) != 0 {
			return nil, errors.New("payload present for empty frame")
		}
		return []byte{}, nil
	}
	var count [maxCodeLen + 1]int
	total := 0
	for _, l := range lengths {
		if l > 0 {
			count[l]++
			total++
		}
	}
	var offs [maxCodeLen + 1]int
	for l := 2; l <= maxCodeLen; l++ {
		offs[l] = offs[l-1] + count[l-1]
	}
	symbols := make([]byte, total)
	cur := offs
	for sym := 0; sym < 256; sym++ {
		if l := lengths[sym]; l > 0 {
			symbols[cur[l]] = byte(sym)
			cur[l]++
		}
	}
	bitpos := 0
	nbits := len(payload) * 8
	out := make([]byte, 0, rawLen)
	for len(out) < rawLen {
		code, first, index := 0, 0, 0
		sym := -1
		for l := 1; l <= maxCodeLen; l++ {
			if bitpos >= nbits {
				return nil, errors.New("payload truncated")
			}
			bit := int(payload[bitpos/8] >> uint(7-bitpos%8) & 1)
			bitpos++
			code |= bit
			c := count[l]
			if code-c < first {
				sym = int(symbols[index+(code-first)])
				break
			}
			index += c
			first = (first + c) << 1
			code <<= 1
		}
		if sym < 0 {
			return nil, errors.New("invalid code prefix")
		}
		out = append(out, byte(sym))
	}
	if nbits-bitpos > 7 {
		return nil, errors.New("payload has trailing bytes")
	}
	for bitpos < nbits {
		bit := int(payload[bitpos/8] >> uint(7-bitpos%8) & 1)
		if bit != 0 {
			return nil, errors.New("nonzero padding bits")
		}
		bitpos++
	}
	return out, nil
}
