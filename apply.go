package main

import "fmt"

// aNode is the applier's own working node; the applier shares no state with
// the script generator so it can serve as an independent check.
type aNode struct {
	uid      int
	label    string
	parent   *aNode
	children []*aNode
}

func (w *aNode) toNode() *Node {
	n := &Node{Label: w.label}
	for _, ch := range w.children {
		n.Children = append(n.Children, ch.toNode())
	}
	return n
}

func aChildIndex(p *aNode, w *aNode) int {
	for i, ch := range p.children {
		if ch == w {
			return i
		}
	}
	return -1
}

// applyScript independently applies an edit script to the source tree. It
// re-derives node uids (postorder of the source), validates every reference,
// and recomputes every operation's cost from the cost table instead of
// trusting the cost declared on the op. A delete is charged the single-node
// delete cost of its label only: the node's children are lifted into its
// place and are never charged as part of the deletion.
//
// It returns the resulting tree, the recomputed per-op costs and their sum.
func applyScript(source *Node, ops []Op, c *CostTable) (*Node, []int, int, error) {
	vroot := &aNode{uid: 0}
	byUID := map[int]*aNode{0: vroot}
	var build func(n *Node) *aNode
	build = func(n *Node) *aNode {
		w := &aNode{label: n.Label}
		for _, ch := range n.Children {
			cw := build(ch)
			cw.parent = w
			w.children = append(w.children, cw)
		}
		w.uid = len(byUID)
		byUID[w.uid] = w
		return w
	}
	root := build(source)
	root.parent = vroot
	vroot.children = []*aNode{root}

	recomputed := make([]int, 0, len(ops))
	total := 0
	for k, op := range ops {
		var cost int
		switch op.Kind {
		case "rename":
			w, ok := byUID[op.UID]
			if !ok || op.UID == 0 {
				return nil, nil, 0, fmt.Errorf("op %d: rename of unknown node #%d", k, op.UID)
			}
			if w.label != op.From {
				return nil, nil, 0, fmt.Errorf("op %d: node #%d has label %q, not %q", k, op.UID, w.label, op.From)
			}
			cost = c.RenCost(op.From, op.To)
			w.label = op.To
		case "delete":
			w, ok := byUID[op.UID]
			if !ok || op.UID <= 0 {
				return nil, nil, 0, fmt.Errorf("op %d: delete of unknown node #%d", k, op.UID)
			}
			if w.label != op.Label {
				return nil, nil, 0, fmt.Errorf("op %d: node #%d has label %q, not %q", k, op.UID, w.label, op.Label)
			}
			cost = c.DelCost(w.label)
			p := w.parent
			idx := aChildIndex(p, w)
			spliced := make([]*aNode, 0, len(p.children)-1+len(w.children))
			spliced = append(spliced, p.children[:idx]...)
			for _, ch := range w.children {
				ch.parent = p
			}
			spliced = append(spliced, w.children...)
			spliced = append(spliced, p.children[idx+1:]...)
			p.children = spliced
			delete(byUID, op.UID)
		case "insert":
			p, ok := byUID[op.ParentUID]
			if !ok {
				return nil, nil, 0, fmt.Errorf("op %d: insert under unknown parent #%d", k, op.ParentUID)
			}
			if op.UID >= 0 {
				return nil, nil, 0, fmt.Errorf("op %d: inserted node uid #%d must be negative", k, op.UID)
			}
			if _, exists := byUID[op.UID]; exists {
				return nil, nil, 0, fmt.Errorf("op %d: duplicate uid #%d", k, op.UID)
			}
			if op.Index < 0 || op.Index > len(p.children) {
				return nil, nil, 0, fmt.Errorf("op %d: insert index %d out of range", k, op.Index)
			}
			if op.Adopt < 0 || op.Index+op.Adopt > len(p.children) {
				return nil, nil, 0, fmt.Errorf("op %d: adopt count %d out of range", k, op.Adopt)
			}
			cost = c.InsCost(op.Label)
			nw := &aNode{uid: op.UID, label: op.Label, parent: p}
			p.children = append(p.children, nil)
			copy(p.children[op.Index+1:], p.children[op.Index:])
			p.children[op.Index] = nw
			adopted := make([]*aNode, op.Adopt)
			copy(adopted, p.children[op.Index+1:op.Index+1+op.Adopt])
			for _, ch := range adopted {
				ch.parent = nw
			}
			nw.children = adopted
			p.children = append(p.children[:op.Index+1], p.children[op.Index+1+op.Adopt:]...)
			byUID[op.UID] = nw
		default:
			return nil, nil, 0, fmt.Errorf("op %d: unknown kind %q", k, op.Kind)
		}
		if cost != op.Cost {
			return nil, nil, 0, fmt.Errorf("op %d (%s) declares cost %d but the cost table gives %d",
				k, op.Kind, op.Cost, cost)
		}
		recomputed = append(recomputed, cost)
		total += cost
	}
	if len(vroot.children) != 1 {
		return nil, nil, 0, fmt.Errorf("script leaves a forest of %d trees, want exactly 1", len(vroot.children))
	}
	return vroot.children[0].toNode(), recomputed, total, nil
}

// verifyScript checks a patch end to end: the independent applier must
// reproduce the target tree and recompute a total cost equal to the
// reported distance.
func verifyScript(source, target *Node, ops []Op, dist int, c *CostTable) error {
	res, _, total, err := applyScript(source, ops, c)
	if err != nil {
		return err
	}
	if total != dist {
		return fmt.Errorf("script cost %d does not match reported distance %d", total, dist)
	}
	if !Equal(res, target) {
		return fmt.Errorf("applying the script to the source does not yield the target")
	}
	return nil
}
