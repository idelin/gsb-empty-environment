package pageindex

import "sort"

// PageID is an explicit page number. Pages are the unit of allocation,
// reuse and garbage collection. A PageID is never recycled while any
// active snapshot can still reach it.
type PageID uint64

// minDegree t: every non-root node holds between t-1 and 2t-1 keys.
const minDegree = 3

const maxKeys = 2*minDegree - 1

// node is an immutable B-tree node stored in exactly one page.
// Once a node is published in the store it is never mutated; every
// modification allocates a fresh page (copy-on-write).
type node struct {
	leaf bool
	keys []uint64
	vals []uint64 // aligned with keys (internal nodes carry values too)
	kids []PageID // internal nodes only: len(kids) == len(keys)+1
}

func (n *node) shallowCopy() *node {
	return &node{
		leaf: n.leaf,
		keys: dupUint(n.keys),
		vals: dupUint(n.vals),
		kids: dupPage(n.kids),
	}
}

func lowerBound(keys []uint64, key uint64) int {
	return sort.Search(len(keys), func(i int) bool { return keys[i] >= key })
}

func dupUint(s []uint64) []uint64 {
	out := make([]uint64, len(s))
	copy(out, s)
	return out
}

func dupPage(s []PageID) []PageID {
	out := make([]PageID, len(s))
	copy(out, s)
	return out
}

func insertUint(s []uint64, i int, v uint64) []uint64 {
	out := make([]uint64, 0, len(s)+1)
	out = append(out, s[:i]...)
	out = append(out, v)
	return append(out, s[i:]...)
}

func removeUint(s []uint64, i int) []uint64 {
	out := make([]uint64, 0, len(s)-1)
	out = append(out, s[:i]...)
	return append(out, s[i+1:]...)
}

func insertPage(s []PageID, i int, v PageID) []PageID {
	out := make([]PageID, 0, len(s)+1)
	out = append(out, s[:i]...)
	out = append(out, v)
	return append(out, s[i:]...)
}

func removePage(s []PageID, i int) []PageID {
	out := make([]PageID, 0, len(s)-1)
	out = append(out, s[:i]...)
	return append(out, s[i+1:]...)
}
