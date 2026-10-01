// Package naive is an independent pointer-based ordered-tree implementation
// used to cross-check the succinct bp index. Every operation walks pointers
// iteratively; nothing here is shared with package bp.
package naive

import (
	"fmt"
	"math/rand"
	"strings"
)

// Node is one node of an ordered rooted tree.
type Node struct {
	Parent   *Node
	Children []*Node
	Id       int // preorder rank, assigned by Parse/Random
}

// Parse builds a pointer tree from a balanced-parentheses string,iteratively.
func Parse(p string) (*Node, error) {
	if len(p) == 0 || len(p)%2 != 0 {
		return nil, fmt.Errorf("naive: bad length %d", len(p))
	}
	var stack []*Node
	var root *Node
	for i := 0; i < len(p); i++ {
		switch p[i] {
		case '(':
			nd := &Node{}
			if len(stack) > 0 {
				nd.Parent = stack[len(stack)-1]
				nd.Parent.Children = append(nd.Parent.Children, nd)
			} else if root != nil {
				return nil, fmt.Errorf("naive: multiple roots at %d", i)
			} else {
				root = nd
			}
			stack = append(stack, nd)
		case ')':
			if len(stack) == 0 {
				return nil, fmt.Errorf("naive: unbalanced at %d", i)
			}
			stack = stack[:len(stack)-1]
		default:
			return nil, fmt.Errorf("naive: bad character %q", p[i])
		}
	}
	if len(stack) != 0 || root == nil {
		return nil, fmt.Errorf("naive: unbalanced")
	}
	AssignIds(root)
	return root, nil
}

// AssignIds numbers nodes in preorder, iteratively.
func AssignIds(root *Node) {
	id := 0
	stack := []*Node{root}
	for len(stack) > 0 {
		nd := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		nd.Id = id
		id++
		for i := len(nd.Children) - 1; i >= 0; i-- {
			stack = append(stack, nd.Children[i])
		}
	}
}

// Preorder returns all nodes in preorder, iteratively.
func Preorder(root *Node) []*Node {
	var out []*Node
	stack := []*Node{root}
	for len(stack) > 0 {
		nd := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		out = append(out, nd)
		for i := len(nd.Children) - 1; i >= 0; i-- {
			stack = append(stack, nd.Children[i])
		}
	}
	return out
}

// Encode renders the tree as a balanced-parentheses string, iteratively.
func Encode(root *Node) string {
	var b strings.Builder
	type frame struct {
		nd   *Node
		exit bool
	}
	stack := []frame{{nd: root}}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if f.exit {
			b.WriteByte(')')
			continue
		}
		b.WriteByte('(')
		stack = append(stack, frame{nd: f.nd, exit: true})
		for i := len(f.nd.Children) - 1; i >= 0; i-- {
			stack = append(stack, frame{nd: f.nd.Children[i]})
		}
	}
	return b.String()
}

// Depth walks parent pointers.
func (n *Node) Depth() int {
	d := 0
	for p := n.Parent; p != nil; p = p.Parent {
		d++
	}
	return d
}

// SubtreeSize counts descendants iteratively.
func (n *Node) SubtreeSize() int {
	c := 0
	stack := []*Node{n}
	for len(stack) > 0 {
		nd := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		c++
		stack = append(stack, nd.Children...)
	}
	return c
}

// ChildRank returns the index of n among its parent's children (0 for root).
func (n *Node) ChildRank() int {
	if n.Parent == nil {
		return 0
	}
	for i, c := range n.Parent.Children {
		if c == n {
			return i
		}
	}
	return -1
}

// FirstChild returns the first child or nil.
func (n *Node) FirstChild() *Node {
	if len(n.Children) == 0 {
		return nil
	}
	return n.Children[0]
}

// NextSibling returns the next sibling or nil.
func (n *Node) NextSibling() *Node {
	if n.Parent == nil {
		return nil
	}
	r := n.ChildRank()
	if r+1 < len(n.Parent.Children) {
		return n.Parent.Children[r+1]
	}
	return nil
}

// PrevSibling returns the previous sibling or nil.
func (n *Node) PrevSibling() *Node {
	if n.Parent == nil {
		return nil
	}
	r := n.ChildRank()
	if r > 0 {
		return n.Parent.Children[r-1]
	}
	return nil
}

// LCA finds the lowest common ancestor by depth alignment.
func LCA(a, b *Node) *Node {
	da, db := a.Depth(), b.Depth()
	for da > db {
		a = a.Parent
		da--
	}
	for db > da {
		b = b.Parent
		db--
	}
	for a != b {
		a = a.Parent
		b = b.Parent
	}
	return a
}

// Enumerate returns the parenthesis strings of all ordered rooted trees with
// n nodes (Catalan(n-1) many). Only used for small n in tests, so the
// recursion depth (<= 2n) is harmless.
func Enumerate(n int) []string {
	if n < 1 {
		return nil
	}
	var out []string
	buf := make([]byte, 2*n)
	var rec func(pos, open, closed int)
	rec = func(pos, open, closed int) {
		if pos == 2*n {
			out = append(out, string(buf))
			return
		}
		if open < n {
			buf[pos] = '('
			rec(pos+1, open+1, closed)
		}
		if closed < open {
			buf[pos] = ')'
			rec(pos+1, open, closed+1)
		}
	}
	rec(0, 0, 0)
	// Keep only single-root words: excess hits 0 only at the very end.
	res := out[:0]
	for _, w := range out {
		bal := 0
		single := true
		for i := 0; i < len(w); i++ {
			if w[i] == '(' {
				bal++
			} else {
				bal--
			}
			if bal == 0 && i < len(w)-1 {
				single = false
				break
			}
		}
		if single {
			res = append(res, w)
		}
	}
	return res
}

// Random grows a random ordered tree with n nodes by repeated random
// attachment, iteratively.
func Random(n int, rng *rand.Rand) *Node {
	root := &Node{}
	nodes := []*Node{root}
	for i := 1; i < n; i++ {
		p := nodes[rng.Intn(len(nodes))]
		nd := &Node{Parent: p}
		p.Children = append(p.Children, nd)
		nodes = append(nodes, nd)
	}
	AssignIds(root)
	return root
}
