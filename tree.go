package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Tree is an ordered labeled tree.
type Tree struct {
	Label    string  `json:"label"`
	Children []*Tree `json:"children,omitempty"`
}

// ParseTree parses a tree from its JSON form: {"label":"a","children":[...]}.
func ParseTree(data []byte) (*Tree, error) {
	var t Tree
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("invalid tree JSON: %v", err)
	}
	if err := validateTree(&t); err != nil {
		return nil, err
	}
	return &t, nil
}

func validateTree(t *Tree) error {
	if t.Label == "" {
		return fmt.Errorf("tree nodes must have a non-empty label")
	}
	for _, c := range t.Children {
		if err := validateTree(c); err != nil {
			return err
		}
	}
	return nil
}

// Equal reports structural equality (order and labels).
func Equal(a, b *Tree) bool {
	if a.Label != b.Label || len(a.Children) != len(b.Children) {
		return false
	}
	for i := range a.Children {
		if !Equal(a.Children[i], b.Children[i]) {
			return false
		}
	}
	return true
}

func cloneTree(t *Tree) *Tree {
	c := &Tree{Label: t.Label}
	for _, ch := range t.Children {
		c.Children = append(c.Children, cloneTree(ch))
	}
	return c
}

func countNodes(t *Tree) int {
	n := 1
	for _, c := range t.Children {
		n += countNodes(c)
	}
	return n
}

func collectLabels(t *Tree, into map[string]bool) {
	into[t.Label] = true
	for _, c := range t.Children {
		collectLabels(c, into)
	}
}

// encodeForest renders a forest in a canonical, unambiguous form.
func encodeForest(f []*Tree) string {
	var sb strings.Builder
	for i, t := range f {
		if i > 0 {
			sb.WriteByte(' ')
		}
		encodeTree(&sb, t)
	}
	return sb.String()
}

func encodeTree(sb *strings.Builder, t *Tree) {
	sb.WriteByte('(')
	sb.WriteString(strconv.Quote(t.Label))
	for _, c := range t.Children {
		sb.WriteByte(' ')
		encodeTree(sb, c)
	}
	sb.WriteByte(')')
}

// flatTree is a tree flattened in postorder; node ids are 1..n in postorder,
// so the root has id n.
type flatTree struct {
	n             int
	labels        []string // 1-based
	l             []int    // l(i): postorder id of the leftmost leaf of subtree i
	parent        []int
	indexInParent []int
	children      [][]int // child ids in left-to-right order
	keyroots      []int   // ascending
	kr            []int   // kr[x]: keyroot k with l(k) == l(x)
	preorder      []int   // ids in preorder
}

func flatten(t *Tree) *flatTree {
	ft := &flatTree{}
	ft.labels = append(ft.labels, "")
	ft.l = append(ft.l, 0)
	ft.parent = append(ft.parent, 0)
	ft.indexInParent = append(ft.indexInParent, 0)
	ft.children = append(ft.children, nil)

	var post func(n *Tree, idx int) int
	post = func(n *Tree, idx int) int {
		childIDs := make([]int, 0, len(n.Children))
		leftmost := 0
		for i, c := range n.Children {
			cid := post(c, i)
			childIDs = append(childIDs, cid)
			if i == 0 {
				leftmost = ft.l[cid]
			}
		}
		ft.n++
		id := ft.n
		if len(childIDs) == 0 {
			leftmost = id
		}
		ft.labels = append(ft.labels, n.Label)
		ft.l = append(ft.l, leftmost)
		ft.parent = append(ft.parent, 0)
		ft.indexInParent = append(ft.indexInParent, idx)
		ft.children = append(ft.children, childIDs)
		for _, cid := range childIDs {
			ft.parent[cid] = id
		}
		return id
	}
	post(t, 0)

	krForL := make([]int, ft.n+1)
	for x := 1; x <= ft.n; x++ {
		p := ft.parent[x]
		if p == 0 || ft.l[p] != ft.l[x] {
			ft.keyroots = append(ft.keyroots, x)
			krForL[ft.l[x]] = x
		}
	}
	ft.kr = make([]int, ft.n+1)
	for x := 1; x <= ft.n; x++ {
		ft.kr[x] = krForL[ft.l[x]]
	}

	var pre func(id int)
	pre = func(id int) {
		ft.preorder = append(ft.preorder, id)
		for _, c := range ft.children[id] {
			pre(c)
		}
	}
	pre(ft.n)
	return ft
}

// path returns the child-index path from the root to node x.
func (ft *flatTree) path(x int) []int {
	var rev []int
	for x != 0 {
		rev = append(rev, ft.indexInParent[x])
		x = ft.parent[x]
	}
	out := make([]int, len(rev))
	for i, v := range rev {
		out[len(rev)-1-i] = v
	}
	return out
}
