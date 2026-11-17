package netsec

// node is an AVL tree node. Trees are ordered strictly by the raw key
// bytes (bytes.Compare), never by hash value, so keys that collide under
// the keyed hash remain distinct and sorted.
type node struct {
	key    []byte
	value  []byte
	height int
	left   *node
	right  *node
}

func nodeHeight(n *node) int {
	if n == nil {
		return 0
	}
	return n.height
}

func updateHeight(n *node) {
	h := nodeHeight(n.left)
	if rh := nodeHeight(n.right); rh > h {
		h = rh
	}
	n.height = h + 1
}

func rotateRight(y *node) *node {
	x := y.left
	y.left = x.right
	x.right = y
	updateHeight(y)
	updateHeight(x)
	return x
}

func rotateLeft(x *node) *node {
	y := x.right
	x.right = y.left
	y.left = x
	updateHeight(x)
	updateHeight(y)
	return y
}

func rebalance(n *node) *node {
	updateHeight(n)
	bf := nodeHeight(n.left) - nodeHeight(n.right)
	if bf > 1 {
		if nodeHeight(n.left.left) < nodeHeight(n.left.right) {
			n.left = rotateLeft(n.left)
		}
		return rotateRight(n)
	}
	if bf < -1 {
		if nodeHeight(n.right.right) < nodeHeight(n.right.left) {
			n.right = rotateRight(n.right)
		}
		return rotateLeft(n)
	}
	return n
}

// avlInsert inserts or replaces (k, v). It reports whether a new key was
// added. Every key comparison goes through cmp.
func avlInsert(n *node, k, v []byte, cmp func(a, b []byte) int) (*node, bool) {
	if n == nil {
		return &node{key: k, value: v, height: 1}, true
	}
	c := cmp(k, n.key)
	if c == 0 {
		n.value = v
		return n, false
	}
	var inserted bool
	if c < 0 {
		n.left, inserted = avlInsert(n.left, k, v, cmp)
	} else {
		n.right, inserted = avlInsert(n.right, k, v, cmp)
	}
	return rebalance(n), inserted
}

func avlGet(n *node, k []byte, cmp func(a, b []byte) int) ([]byte, bool) {
	for n != nil {
		c := cmp(k, n.key)
		if c == 0 {
			return n.value, true
		}
		if c < 0 {
			n = n.left
		} else {
			n = n.right
		}
	}
	return nil, false
}

func avlDelete(n *node, k []byte, cmp func(a, b []byte) int) (*node, bool) {
	if n == nil {
		return nil, false
	}
	c := cmp(k, n.key)
	var deleted bool
	switch {
	case c < 0:
		n.left, deleted = avlDelete(n.left, k, cmp)
	case c > 0:
		n.right, deleted = avlDelete(n.right, k, cmp)
	default:
		if n.left == nil {
			return n.right, true
		}
		if n.right == nil {
			return n.left, true
		}
		successor := n.right
		for successor.left != nil {
			successor = successor.left
		}
		n.key, n.value = successor.key, successor.value
		n.right, _ = avlDelete(n.right, successor.key, cmp)
		return rebalance(n), true
	}
	if !deleted {
		return n, false
	}
	return rebalance(n), true
}

func avlForEach(n *node, fn func(k, v []byte)) {
	if n == nil {
		return
	}
	avlForEach(n.left, fn)
	fn(n.key, n.value)
	avlForEach(n.right, fn)
}
