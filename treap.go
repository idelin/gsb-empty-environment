package mtf

// Implicit treap with subtree sizes. The in-order sequence of nodes is the
// current MTF list (front first). Rank-of-symbol, select-by-index and
// move-to-front (split + merge) are all O(log n) expected; no linear
// re-shuffling of the alphabet ever happens.

type node struct {
	sym         byte
	prio        uint64
	size        int
	left, right *node
	parent      *node
}

func sz(n *node) int {
	if n == nil {
		return 0
	}
	return n.size
}

func (n *node) pull() {
	n.size = 1 + sz(n.left) + sz(n.right)
	if n.left != nil {
		n.left.parent = n
	}
	if n.right != nil {
		n.right.parent = n
	}
}

// split divides t into [0,k) and [k, ...) by in-order position.
func split(t *node, k int) (*node, *node) {
	if t == nil {
		return nil, nil
	}
	if sz(t.left) >= k {
		l, r := split(t.left, k)
		t.left = r
		t.pull()
		t.parent = nil
		return l, t
	}
	l, r := split(t.right, k-sz(t.left)-1)
	t.right = l
	t.pull()
	t.parent = nil
	return t, r
}

// merge concatenates l and r (all of l precedes all of r). Priorities are
// random, so the result stays balanced in expectation.
func merge(l, r *node) *node {
	if l == nil {
		if r != nil {
			r.parent = nil
		}
		return r
	}
	if r == nil {
		l.parent = nil
		return l
	}
	if l.prio < r.prio {
		l.right = merge(l.right, r)
		l.pull()
		l.parent = nil
		return l
	}
	r.left = merge(l, r.left)
	r.pull()
	r.parent = nil
	return r
}

// rank returns the 0-based in-order position of n by walking parent pointers.
func rank(n *node) int {
	r := sz(n.left)
	for n.parent != nil {
		if n.parent.right == n {
			r += sz(n.parent.left) + 1
		}
		n = n.parent
	}
	return r
}

// kth returns the node at in-order position k, or nil if out of range.
func kth(t *node, k int) *node {
	for t != nil {
		ls := sz(t.left)
		switch {
		case k < ls:
			t = t.left
		case k == ls:
			return t
		default:
			k -= ls + 1
			t = t.right
		}
	}
	return nil
}

func inorder(t *node, out *[]byte) {
	if t == nil {
		return
	}
	inorder(t.left, out)
	*out = append(*out, t.sym)
	inorder(t.right, out)
}
