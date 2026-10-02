package main

import (
	"container/heap"
	"sort"
)

// code is a canonical prefix code assigned to one symbol.
type code struct {
	bits   uint32
	length int
}

type node struct {
	freq        int
	sym         byte
	leaf        bool
	seq         int // tie-breaker for deterministic codes
	left, right *node
}

type nodeHeap []*node

func (h nodeHeap) Len() int { return len(h) }
func (h nodeHeap) Less(i, j int) bool {
	if h[i].freq != h[j].freq {
		return h[i].freq < h[j].freq
	}
	return h[i].seq < h[j].seq
}
func (h nodeHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *nodeHeap) Push(x interface{}) { *h = append(*h, x.(*node)) }
func (h *nodeHeap) Pop() interface{} {
	old := *h
	n := old[len(old)-1]
	*h = old[:len(old)-1]
	return n
}

// buildCodes builds a Huffman code for the byte frequencies of data.
func buildCodes(data []byte) map[byte]code {
	freq := map[byte]int{}
	for _, b := range data {
		freq[b]++
	}
	syms := make([]int, 0, len(freq))
	for s := range freq {
		syms = append(syms, int(s))
	}
	sort.Ints(syms)

	h := &nodeHeap{}
	seq := 0
	for _, s := range syms {
		heap.Push(h, &node{freq: freq[byte(s)], sym: byte(s), leaf: true, seq: seq})
		seq++
	}
	if h.Len() == 1 {
		return map[byte]code{(*h)[0].sym: {bits: 0, length: 1}}
	}
	for h.Len() > 1 {
		a := heap.Pop(h).(*node)
		b := heap.Pop(h).(*node)
		heap.Push(h, &node{freq: a.freq + b.freq, left: a, right: b, seq: seq})
		seq++
	}
	root := heap.Pop(h).(*node)
	codes := map[byte]code{}
	var walk func(n *node, bits uint32, depth int)
	walk = func(n *node, bits uint32, depth int) {
		if n.leaf {
			codes[n.sym] = code{bits: bits, length: depth}
			return
		}
		walk(n.left, bits<<1, depth+1)
		walk(n.right, bits<<1|1, depth+1)
	}
	walk(root, 0, 0)
	return codes
}

func codeLenBounds(codes map[byte]code) (min, max int) {
	first := true
	for _, c := range codes {
		if first || c.length < min {
			min = c.length
		}
		if first || c.length > max {
			max = c.length
		}
		first = false
	}
	return
}

// decodeNode is a binary trie used for decoding.
type decodeNode struct {
	children [2]*decodeNode
	leaf     bool
	sym      byte
}

func buildDecodeTree(codes map[byte]code) *decodeNode {
	root := &decodeNode{}
	for sym, c := range codes {
		n := root
		for i := c.length - 1; i >= 0; i-- {
			b := int((c.bits >> uint(i)) & 1)
			if n.children[b] == nil {
				n.children[b] = &decodeNode{}
			}
			n = n.children[b]
		}
		n.leaf = true
		n.sym = sym
	}
	return root
}
