package main

import "fmt"

// Op is one edit operation. Node references (UID, ParentUID) use postorder
// ids of the source tree (1..n); id 0 is the virtual root above the forest;
// negative id -k refers to the node inserted for target postorder id k.
type Op struct {
	Kind      string `json:"op"` // "rename", "delete" or "insert"
	UID       int    `json:"uid"`
	From      string `json:"from,omitempty"`
	To        string `json:"to,omitempty"`
	Label     string `json:"label,omitempty"`
	ParentUID int    `json:"parentUid,omitempty"`
	Index     int    `json:"index,omitempty"`
	Adopt     int    `json:"adopt,omitempty"`
	Cost      int    `json:"cost"`
}

// wnode is the generator's working-tree node.
type wnode struct {
	uid      int
	t2id     int // target postorder id for matched/inserted nodes, else -1
	label    string
	parent   *wnode
	children []*wnode
}

func (w *wnode) toNode() *Node {
	n := &Node{Label: w.label}
	for _, ch := range w.children {
		n.Children = append(n.Children, ch.toNode())
	}
	return n
}

func childIndex(p *wnode, w *wnode) int {
	for i, ch := range p.children {
		if ch == w {
			return i
		}
	}
	panic("wnode: child not found")
}

// generateScript turns an optimal mapping into an executable edit script.
// It simulates the transformation on a working forest rooted at a virtual
// root, so every emitted index/adopt count refers to the tree state the
// applier will actually see. Operation order is: renames, then deletes in
// postorder (children before parents, so lifted nodes land correctly), then
// inserts in target preorder (parents before children).
//
// The mapping was computed against the closed cost table, so every logical
// operation is expanded here into the primitive chain that realizes its
// effective cost under the raw table (e.g. "insert b, rename b->c" when
// that is cheaper than inserting c directly).
func generateScript(t1, t2 *indexedTree, m *rawMapping, cc *closedCosts, raw *CostTable) ([]Op, error) {
	matched := make(map[int]int, len(m.pairs))
	matchedInv := make(map[int]int, len(m.pairs))
	for _, p := range m.pairs {
		matched[p[0]] = p[1]
		matchedInv[p[1]] = p[0]
	}
	deleted := make(map[int]bool, len(m.del))
	for _, d := range m.del {
		deleted[d] = true
	}
	inserted := make(map[int]bool, len(m.ins))
	for _, in := range m.ins {
		inserted[in] = true
	}

	vroot := &wnode{uid: 0, t2id: -1}
	byUID := map[int]*wnode{0: vroot}
	var build func(id int) *wnode
	build = func(id int) *wnode {
		w := &wnode{uid: id, label: t1.labels[id], t2id: -1}
		if j, ok := matched[id]; ok {
			w.t2id = j
		}
		byUID[id] = w
		for _, k := range t1.kids[id] {
			cw := build(k)
			cw.parent = w
			w.children = append(w.children, cw)
		}
		return w
	}
	root1 := build(t1.n)
	root1.parent = vroot
	vroot.children = []*wnode{root1}

	var ops []Op

	// emitRenames appends the primitive renames realizing a cheapest chain.
	emitRenames := func(uid int, chain []string) {
		for k := 0; k+1 < len(chain); k++ {
			ops = append(ops, Op{
				Kind: "rename", UID: uid,
				From: chain[k], To: chain[k+1],
				Cost: raw.RenCost(chain[k], chain[k+1]),
			})
		}
	}

	// 1. Renames of matched pairs whose labels differ.
	for i := 1; i <= t1.n; i++ {
		j, ok := matched[i]
		if !ok || t1.labels[i] == t2.labels[j] {
			continue
		}
		emitRenames(i, cc.renChain(t1.labels[i], t2.labels[j]))
		byUID[i].label = t2.labels[j]
	}

	// 2. Deletes in postorder: children are deleted before their parents, so
	//    when a node is deleted all its current children are matched nodes
	//    that must be lifted into its place.
	for i := 1; i <= t1.n; i++ {
		if !deleted[i] {
			continue
		}
		w := byUID[i]
		p := w.parent
		idx := childIndex(p, w)
		chain := cc.delChain(w.label)
		emitRenames(i, chain)
		ops = append(ops, Op{
			Kind: "delete", UID: i, Label: chain[len(chain)-1],
			Cost: raw.DelCost(chain[len(chain)-1]),
		})
		spliced := make([]*wnode, 0, len(p.children)-1+len(w.children))
		spliced = append(spliced, p.children[:idx]...)
		for _, ch := range w.children {
			ch.parent = p
		}
		spliced = append(spliced, w.children...)
		spliced = append(spliced, p.children[idx+1:]...)
		p.children = spliced
		delete(byUID, i)
	}

	// 3. Inserts in target preorder. An inserted node is placed among the
	//    children of its nearest already-existing ancestor and adopts the
	//    following siblings that map to its own descendants (this is the
	//    exact inverse of a delete-with-lift).
	insertedDone := map[int]bool{}
	var insertRec func(id int)
	insertRec = func(id int) {
		if inserted[id] {
			uid := 0
			for p := t2.parent[id]; p != 0; p = t2.parent[p] {
				if s, ok := matchedInv[p]; ok {
					uid = s
					break
				}
				if insertedDone[p] {
					uid = -p
					break
				}
			}
			P := byUID[uid]
			idx := 0
			for _, ch := range P.children {
				if t2.pre[ch.t2id] < t2.pre[id] {
					idx++
				}
			}
			adopt := 0
			for k := idx; k < len(P.children); k++ {
				x := P.children[k].t2id
				if t2.pre[id] < t2.pre[x] && id > x {
					adopt++
				} else {
					break
				}
			}
			chain := cc.insChain(t2.labels[id])
			ops = append(ops, Op{
				Kind: "insert", UID: -id, ParentUID: uid,
				Index: idx, Label: chain[0], Adopt: adopt,
				Cost: raw.InsCost(chain[0]),
			})
			emitRenames(-id, chain)
			nw := &wnode{uid: -id, t2id: id, label: t2.labels[id], parent: P}
			P.children = append(P.children, nil)
			copy(P.children[idx+1:], P.children[idx:])
			P.children[idx] = nw
			adopted := make([]*wnode, adopt)
			copy(adopted, P.children[idx+1:idx+1+adopt])
			for _, ch := range adopted {
				ch.parent = nw
			}
			nw.children = adopted
			P.children = append(P.children[:idx+1], P.children[idx+1+adopt:]...)
			byUID[-id] = nw
			insertedDone[id] = true
		}
		for _, k := range t2.kids[id] {
			insertRec(k)
		}
	}
	insertRec(t2.n)

	if len(vroot.children) != 1 || !Equal(vroot.children[0].toNode(), t2.root) {
		return nil, fmt.Errorf("internal error: generated script does not transform source into target")
	}
	return ops, nil
}
