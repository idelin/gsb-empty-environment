package main

import "fmt"

// ApplyScript is an independent verifier: it re-implements operation
// application from scratch (separate node type, own id assignment) and
// charges costs straight from the raw cost table. A delete is charged as a
// single node deletion — its children are lifted into its place, so the
// subtree is never charged as a whole.
func ApplyScript(src *Tree, ops []Op, c *CostTable) (result *Tree, cost int, err error) {
	byID := map[int]*anode{}
	counter := 0
	var build func(n *Tree) *anode
	build = func(n *Tree) *anode {
		a := &anode{label: n.Label}
		for _, ch := range n.Children {
			ca := build(ch)
			ca.parent = a
			a.children = append(a.children, ca)
		}
		counter++
		a.id = counter
		byID[a.id] = a
		return a
	}
	roots := []*anode{build(src)}

	cost = 0
	for i, op := range ops {
		switch op.Op {
		case "rename":
			w := byID[op.Node]
			if w == nil {
				return nil, 0, fmt.Errorf("op %d: rename of unknown node %d", i, op.Node)
			}
			cost += c.Ren(w.label, op.Label)
			w.label = op.Label
		case "delete":
			w := byID[op.Node]
			if w == nil {
				return nil, 0, fmt.Errorf("op %d: delete of unknown node %d", i, op.Node)
			}
			cost += c.Del(w.label) // single node only; children lift up
			if w.parent == nil {
				roots = liftAnode(roots, w)
			} else {
				w.parent.children = liftAnode(w.parent.children, w)
			}
			for _, ch := range w.children {
				ch.parent = w.parent
			}
			delete(byID, w.id)
		case "insert":
			if _, dup := byID[op.Node]; dup {
				return nil, 0, fmt.Errorf("op %d: duplicate node id %d", i, op.Node)
			}
			nw := &anode{id: op.Node, label: op.Label}
			var list *[]*anode
			if op.Parent == 0 {
				list = &roots
			} else {
				p := byID[op.Parent]
				if p == nil {
					return nil, 0, fmt.Errorf("op %d: insert parent %d not found", i, op.Parent)
				}
				list = &p.children
				nw.parent = p
			}
			cur := *list
			if op.Index < 0 || op.Adopt < 0 || op.Index+op.Adopt > len(cur) {
				return nil, 0, fmt.Errorf("op %d: insert index/adopt out of range", i)
			}
			nw.children = append(nw.children, cur[op.Index:op.Index+op.Adopt]...)
			for _, ch := range nw.children {
				ch.parent = nw
			}
			nl := make([]*anode, 0, len(cur)-op.Adopt+1)
			nl = append(nl, cur[:op.Index]...)
			nl = append(nl, nw)
			nl = append(nl, cur[op.Index+op.Adopt:]...)
			*list = nl
			byID[nw.id] = nw
			cost += c.Ins(op.Label)
		default:
			return nil, 0, fmt.Errorf("op %d: unknown op %q", i, op.Op)
		}
	}
	if len(roots) != 1 {
		return nil, 0, fmt.Errorf("script yields a forest of %d trees, want exactly 1", len(roots))
	}
	return anodeToTree(roots[0]), cost, nil
}

// anode is the applier's own working node type (independent of script.go).
type anode struct {
	id       int
	label    string
	parent   *anode
	children []*anode
}

func liftAnode(list []*anode, w *anode) []*anode {
	for i, x := range list {
		if x == w {
			out := make([]*anode, 0, len(list)-1+len(w.children))
			out = append(out, list[:i]...)
			out = append(out, w.children...)
			out = append(out, list[i+1:]...)
			return out
		}
	}
	panic("liftAnode: node not in list")
}

func anodeToTree(a *anode) *Tree {
	t := &Tree{Label: a.label}
	for _, ch := range a.children {
		t.Children = append(t.Children, anodeToTree(ch))
	}
	return t
}
