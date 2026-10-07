package pageindex

import "sort"

// node is an immutable B-tree node. Every key carries a value, internal nodes
// included (CLRS-style B-tree). kids is nil for leaves and has len(keys)+1
// entries for internal nodes.
type node struct {
	leaf bool
	keys []uint64
	vals []uint64
	kids []PageNo
}

// find returns the smallest index i with keys[i] >= key.
func (n *node) find(key uint64) int {
	return sort.Search(len(n.keys), func(i int) bool { return n.keys[i] >= key })
}

func (n *node) clone() *node {
	nn := &node{leaf: n.leaf}
	nn.keys = append([]uint64(nil), n.keys...)
	nn.vals = append([]uint64(nil), n.vals...)
	nn.kids = append([]PageNo(nil), n.kids...)
	return nn
}

func insertUint64(s []uint64, i int, v uint64) []uint64 {
	s = append(s, 0)
	copy(s[i+1:], s[i:])
	s[i] = v
	return s
}

func insertPage(s []PageNo, i int, v PageNo) []PageNo {
	s = append(s, 0)
	copy(s[i+1:], s[i:])
	s[i] = v
	return s
}

func removeUint64At(s []uint64, i int) []uint64 {
	return append(s[:i], s[i+1:]...)
}

func removePageAt(s []PageNo, i int) []PageNo {
	return append(s[:i], s[i+1:]...)
}
