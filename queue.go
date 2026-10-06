package bnb

import "container/heap"

// nodeHeap is a priority queue of frontier nodes. The highest-priority node
// is the one with the largest upper bound; ties are broken by the path
// encoding (lexicographically smaller first).
type nodeHeap []*Node

func (h nodeHeap) Len() int { return len(h) }

func (h nodeHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if a.bound != b.bound {
		return a.bound > b.bound
	}
	return lessPath(a.path, b.path)
}

func (h nodeHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *nodeHeap) Push(x interface{}) {
	*h = append(*h, x.(*Node))
}

func (h *nodeHeap) Pop() interface{} {
	old := *h
	n := len(old)
	node := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return node
}

// peek returns the highest-priority node without removing it, or nil.
func (h *nodeHeap) peek() *Node {
	if len(*h) == 0 {
		return nil
	}
	return (*h)[0]
}

// maxBound returns the largest upper bound among queued nodes.
func (h *nodeHeap) maxBound() int64 {
	var best int64
	for i, n := range *h {
		if i == 0 || n.bound > best {
			best = n.bound
		}
	}
	return best
}

var _ heap.Interface = (*nodeHeap)(nil)
