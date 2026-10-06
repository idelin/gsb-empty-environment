package bboxidx

import "sort"

// fanout is the maximum number of children per tree node.
const fanout = 16

// node is a node of an immutable packed R-tree. Leaves hold entries;
// internal nodes hold child nodes. box is the union of everything below.
type node struct {
	box      Box
	leaf     bool
	children []*node
	entries  []entry
}

// tree is an immutable packed R-tree snapshot built by bulk loading.
type tree struct {
	root *node
}

// buildTree bulk-loads items into a packed R-tree using the
// sort-tile-recursive strategy. The result is immutable.
func buildTree(items []entry) *tree {
	if len(items) == 0 {
		return &tree{}
	}
	entries := make([]entry, len(items))
	copy(entries, items)
	level := packEntries(entries)
	for len(level) > 1 {
		level = packNodes(level)
	}
	return &tree{root: level[0]}
}

// packEntries sorts entries into STR tiles and packs them into leaf nodes.
func packEntries(entries []entry) []*node {
	slices := tileCount(len(entries))
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].box.centerX() < entries[j].box.centerX()
	})
	var leaves []*node
	sliceLen := slices * fanout
	for start := 0; start < len(entries); start += sliceLen {
		end := start + sliceLen
		if end > len(entries) {
			end = len(entries)
		}
		slice := entries[start:end]
		sort.Slice(slice, func(i, j int) bool {
			return slice[i].box.centerY() < slice[j].box.centerY()
		})
		for lo := 0; lo < len(slice); lo += fanout {
			hi := lo + fanout
			if hi > len(slice) {
				hi = len(slice)
			}
			chunk := make([]entry, hi-lo)
			copy(chunk, slice[lo:hi])
			leaves = append(leaves, &node{box: entriesBox(chunk), leaf: true, entries: chunk})
		}
	}
	return leaves
}

// packNodes groups nodes into parent nodes, again with STR tiling.
func packNodes(nodes []*node) []*node {
	slices := tileCount(len(nodes))
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].box.centerX() < nodes[j].box.centerX()
	})
	var parents []*node
	sliceLen := slices * fanout
	for start := 0; start < len(nodes); start += sliceLen {
		end := start + sliceLen
		if end > len(nodes) {
			end = len(nodes)
		}
		slice := nodes[start:end]
		sort.Slice(slice, func(i, j int) bool {
			return slice[i].box.centerY() < slice[j].box.centerY()
		})
		for lo := 0; lo < len(slice); lo += fanout {
			hi := lo + fanout
			if hi > len(slice) {
				hi = len(slice)
			}
			children := make([]*node, hi-lo)
			copy(children, slice[lo:hi])
			parents = append(parents, &node{box: childrenBox(children), children: children})
		}
	}
	return parents
}

// tileCount returns the number of vertical slices used by STR packing.
func tileCount(n int) int {
	leaves := (n + fanout - 1) / fanout
	s := 1
	for s*s < leaves {
		s++
	}
	return s
}

func entriesBox(entries []entry) Box {
	var b Box
	for _, e := range entries {
		b = b.Union(e.box)
	}
	return b
}

func childrenBox(children []*node) Box {
	var b Box
	for _, c := range children {
		b = b.Union(c.box)
	}
	return b
}

// search calls fn for every stored entry whose box intersects q.
func (t *tree) search(q Box, fn func(entry)) {
	if t.root == nil || !t.root.box.Intersects(q) {
		return
	}
	stack := []*node{t.root}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n.leaf {
			for _, e := range n.entries {
				if e.box.Intersects(q) {
					fn(e)
				}
			}
			continue
		}
		for _, c := range n.children {
			if c.box.Intersects(q) {
				stack = append(stack, c)
			}
		}
	}
}

// all calls fn for every stored entry.
func (t *tree) all(fn func(entry)) {
	if t.root == nil {
		return
	}
	stack := []*node{t.root}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n.leaf {
			for _, e := range n.entries {
				fn(e)
			}
			continue
		}
		stack = append(stack, n.children...)
	}
}
