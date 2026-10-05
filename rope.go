package bracketdiag

import (
	"math/rand"
	"strings"
)

// chunkSize is the maximum number of bytes stored in a single treap node.
const chunkSize = 512

// node is an implicit treap node holding one text chunk. size and sum are
// cached aggregates for the whole subtree, so edits only recompute the
// nodes on the affected spine.
type node struct {
	text  string
	pri   uint32
	left  *node
	right *node
	size  int
	sum   summary
}

func (n *node) sz() int {
	if n == nil {
		return 0
	}
	return n.size
}

func sumOf(n *node) summary {
	if n == nil {
		return identitySummary()
	}
	return n.sum
}

func newNode(text string) *node {
	n := &node{text: text, pri: rand.Uint32()}
	return n.recalc()
}

func (n *node) recalc() *node {
	n.size = len(n.text) + n.left.sz() + n.right.sz()
	s := mergeSumm(sumOf(n.left), leafSummary(n.text), n.left.sz())
	n.sum = mergeSumm(s, sumOf(n.right), n.left.sz()+len(n.text))
	return n
}

// mergeNode joins two treaps where every byte in a precedes every byte in b.
// It is non-mutating: untouched subtrees are shared.
func mergeNode(a, b *node) *node {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.pri < b.pri {
		return (&node{text: a.text, pri: a.pri, left: a.left, right: mergeNode(a.right, b)}).recalc()
	}
	return (&node{text: b.text, pri: b.pri, left: mergeNode(a, b.left), right: b.right}).recalc()
}

// splitNode divides t so the left result holds the first k bytes.
func splitNode(t *node, k int) (*node, *node) {
	if t == nil {
		return nil, nil
	}
	ls := t.left.sz()
	switch {
	case k < ls:
		l, r := splitNode(t.left, k)
		return l, (&node{text: t.text, pri: t.pri, left: r, right: t.right}).recalc()
	case k > ls+len(t.text):
		l, r := splitNode(t.right, k-ls-len(t.text))
		return (&node{text: t.text, pri: t.pri, left: t.left, right: l}).recalc(), r
	}
	cut := k - ls
	var l, r *node
	if cut > 0 {
		l = (&node{text: t.text[:cut], pri: t.pri, left: t.left}).recalc()
	} else {
		l = t.left
	}
	if cut < len(t.text) {
		r = (&node{text: t.text[cut:], pri: t.pri, right: t.right}).recalc()
	} else {
		r = t.right
	}
	return l, r
}

func insertText(root *node, pos int, text string) *node {
	for len(text) > 0 {
		n := chunkSize
		if n > len(text) {
			n = len(text)
		}
		a, b := splitNode(root, pos)
		root = mergeNode(mergeNode(a, newNode(text[:n])), b)
		pos += n
		text = text[n:]
	}
	return root
}

func deleteRange(root *node, pos, end int) *node {
	a, bc := splitNode(root, pos)
	_, c := splitNode(bc, end-pos)
	return mergeNode(a, c)
}

func inorderString(t *node) string {
	var sb strings.Builder
	var rec func(n *node)
	rec = func(n *node) {
		if n == nil {
			return
		}
		rec(n.left)
		sb.WriteString(n.text)
		rec(n.right)
	}
	rec(t)
	return sb.String()
}
