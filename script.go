package main

import (
	"fmt"
	"sort"
)

// Op is one edit operation. Node ids refer to postorder ids of the source
// tree (1..n1); insert operations introduce fresh ids n1+1, n1+2, ...
//
//	delete: removes the node; its children take its place among its
//	        siblings, in order. Cost: Del(label) once — never a per-subtree
//	        charge.
//	insert: creates a new node as the Index-th child of Parent (0 = forest
//	        top level); the node adopts the Adopt consecutive children that
//	        currently follow position Index-1, i.e. insert is the exact
//	        inverse of delete. Cost: Ins(label).
//	rename: changes a node's label. Cost: Ren(old, new).
type Op struct {
	Op     string `json:"op"`
	Node   int    `json:"node"`
	Parent int    `json:"parent,omitempty"`
	Index  int    `json:"index,omitempty"`
	Adopt  int    `json:"adopt,omitempty"`
	Label  string `json:"label,omitempty"`
}

// wnode is a working-tree node used while synthesizing the script.
type wnode struct {
	id       int
	label    string
	parent   *wnode
	children []*wnode
}

func spliceLift(list []*wnode, w *wnode) []*wnode {
	for i, x := range list {
		if x == w {
			out := make([]*wnode, 0, len(list)-1+len(w.children))
			out = append(out, list[:i]...)
			out = append(out, w.children...)
			out = append(out, list[i+1:]...)
			return out
		}
	}
	panic("spliceLift: node not in list")
}

// countTopMapped counts the mapped proper descendants of t whose path from t
// contains no other mapped node (the "top-most" mapped descendants). After
// all deletes, exactly these nodes sit as a consecutive block of children
// where t must be inserted, and t adopts them.
func countTopMapped(ft *flatTree, mapped map[int]int, t int) int {
	total := 0
	var rec func(x int)
	rec = func(x int) {
		for _, ch := range ft.children[x] {
			if _, ok := mapped[ch]; ok {
				total++
			} else {
				rec(ch)
			}
		}
	}
	rec(t)
	return total
}

func wnodeEqual(w *wnode, t *Tree) bool {
	if w.label != t.Label || len(w.children) != len(t.Children) {
		return false
	}
	for i := range w.children {
		if !wnodeEqual(w.children[i], t.Children[i]) {
			return false
		}
	}
	return true
}

// buildScript turns a mapping into an operation sequence, simulating every
// step on a working forest so that insert positions and adoption counts are
// exact. It self-checks that the simulated result equals t2.
func buildScript(t1, t2 *Tree, mapping [][2]int, nc *normCosts) ([]Op, error) {
	ft1, ft2 := flatten(t1), flatten(t2)
	mappedSrc := map[int]int{}
	mappedTgt := map[int]int{}
	for _, pr := range mapping {
		mappedSrc[pr[0]] = pr[1]
		mappedTgt[pr[1]] = pr[0]
	}

	nodes := make([]*wnode, ft1.n+1)
	byID := map[int]*wnode{}
	for id := 1; id <= ft1.n; id++ {
		nodes[id] = &wnode{id: id, label: ft1.labels[id]}
		byID[id] = nodes[id]
	}
	for id := 1; id <= ft1.n; id++ {
		for _, cid := range ft1.children[id] {
			nodes[id].children = append(nodes[id].children, nodes[cid])
			nodes[cid].parent = nodes[id]
		}
	}
	roots := []*wnode{nodes[ft1.n]}

	tgtWid := map[int]int{}
	for s, t := range mappedSrc {
		tgtWid[t] = s
	}

	var ops []Op

	// Renames for mapped pairs (any order; ids are stable).
	srcs := make([]int, 0, len(mappedSrc))
	for s := range mappedSrc {
		srcs = append(srcs, s)
	}
	sort.Ints(srcs)
	for _, s := range srcs {
		t := mappedSrc[s]
		for _, L := range nc.renameChain(ft1.labels[s], ft2.labels[t]) {
			ops = append(ops, Op{Op: "rename", Node: s, Label: L})
			nodes[s].label = L
		}
	}

	// Deletes of unmapped source nodes, postorder (children before parents).
	for s := 1; s <= ft1.n; s++ {
		if _, ok := mappedSrc[s]; ok {
			continue
		}
		via := nc.delVia[ft1.labels[s]]
		for _, L := range nc.renameChain(ft1.labels[s], via) {
			ops = append(ops, Op{Op: "rename", Node: s, Label: L})
			nodes[s].label = L
		}
		ops = append(ops, Op{Op: "delete", Node: s})
		w := nodes[s]
		if w.parent == nil {
			roots = spliceLift(roots, w)
		} else {
			w.parent.children = spliceLift(w.parent.children, w)
		}
		for _, ch := range w.children {
			ch.parent = w.parent
		}
	}

	// Inserts of unmapped target nodes, preorder (parents before children).
	nextID := ft1.n
	for _, t := range ft2.preorder {
		if _, ok := mappedTgt[t]; ok {
			continue
		}
		p := ft2.parent[t]
		j := ft2.indexInParent[t]
		block := countTopMapped(ft2, mappedTgt, t)
		nextID++
		via := nc.insVia[ft2.labels[t]]
		parentWid := 0
		if p != 0 {
			parentWid = tgtWid[p]
		}
		ops = append(ops, Op{Op: "insert", Node: nextID, Parent: parentWid, Index: j, Adopt: block, Label: via})
		nw := &wnode{id: nextID, label: via}
		byID[nextID] = nw
		if p == 0 {
			if j < 0 || j+block > len(roots) {
				return nil, fmt.Errorf("internal error: root insert out of range")
			}
			nw.children = append(nw.children, roots[j:j+block]...)
			nr := make([]*wnode, 0, len(roots)-block+1)
			nr = append(nr, roots[:j]...)
			nr = append(nr, nw)
			nr = append(nr, roots[j+block:]...)
			roots = nr
		} else {
			pw := byID[parentWid]
			if j < 0 || j+block > len(pw.children) {
				return nil, fmt.Errorf("internal error: insert out of range")
			}
			nw.parent = pw
			nw.children = append(nw.children, pw.children[j:j+block]...)
			nch := make([]*wnode, 0, len(pw.children)-block+1)
			nch = append(nch, pw.children[:j]...)
			nch = append(nch, nw)
			nch = append(nch, pw.children[j+block:]...)
			pw.children = nch
		}
		for _, ch := range nw.children {
			ch.parent = nw
		}
		for _, L := range nc.renameChain(via, ft2.labels[t]) {
			ops = append(ops, Op{Op: "rename", Node: nextID, Label: L})
			nw.label = L
		}
		tgtWid[t] = nextID
	}

	if len(roots) != 1 || !wnodeEqual(roots[0], t2) {
		return nil, fmt.Errorf("internal error: script simulation does not yield the target tree")
	}
	return ops, nil
}
