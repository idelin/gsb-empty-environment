package bboxindex

import "sort"

// maxEntries is the node fanout of the packed stable tree.
const maxEntries = 16

// entry is one indexed object. version orders mutations of the same id;
// deleted marks a tombstone in the delta buffer.
type entry struct {
	id      uint64
	box     Box
	version uint64
	deleted bool
}

// node is a node of the immutable packed R-tree. Leaves hold entries,
// internal nodes hold children.
type node struct {
	box      Box
	leaf     bool
	entries  []entry
	children []*node
}

// buildTree bulk-loads a packed R-tree from the given id -> box mapping
// using sort-tile-recurse style packing. Returns nil for an empty set.
func buildTree(items map[uint64]Box) *node {
	entries := make([]entry, 0, len(items))
	for id, b := range items {
		entries = append(entries, entry{id: id, box: b})
	}
	if len(entries) == 0 {
		return nil
	}
	return packLevel(packLeaves(entries))
}

func packLeaves(entries []entry) []*node {
	sort.Slice(entries, func(i, j int) bool {
		return centerX(entries[i].box) < centerX(entries[j].box)
	})
	var nodes []*node
	for i := 0; i < len(entries); i += maxEntries {
		j := i + maxEntries
		if j > len(entries) {
			j = len(entries)
		}
		n := &node{leaf: true, entries: append([]entry(nil), entries[i:j]...)}
		n.box = n.entries[0].box
		for _, e := range n.entries[1:] {
			n.box = union(n.box, e.box)
		}
		nodes = append(nodes, n)
	}
	return nodes
}

func packLevel(level []*node) *node {
	for len(level) > 1 {
		sort.Slice(level, func(i, j int) bool {
			return centerX(level[i].box) < centerX(level[j].box)
		})
		var next []*node
		for i := 0; i < len(level); i += maxEntries {
			j := i + maxEntries
			if j > len(level) {
				j = len(level)
			}
			p := &node{children: append([]*node(nil), level[i:j]...)}
			p.box = p.children[0].box
			for _, c := range p.children[1:] {
				p.box = union(p.box, c.box)
			}
			next = append(next, p)
		}
		level = next
	}
	return level[0]
}

// search appends all entries whose box intersects q to out.
func search(n *node, q Box, out *[]entry) {
	if n == nil || !n.box.Intersects(q) {
		return
	}
	if n.leaf {
		for _, e := range n.entries {
			if e.box.Intersects(q) {
				*out = append(*out, e)
			}
		}
		return
	}
	for _, c := range n.children {
		search(c, q, out)
	}
}

// collect appends every entry in the tree to out.
func collect(n *node, out *[]entry) {
	if n == nil {
		return
	}
	if n.leaf {
		*out = append(*out, n.entries...)
		return
	}
	for _, c := range n.children {
		collect(c, out)
	}
}
