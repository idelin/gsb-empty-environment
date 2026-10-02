package leasering

// 红黑树：承载哈希环上按 uint32 哈希值排序的虚拟节点。
// 仅依赖标准库，提供插入、删除、ceiling（顺时针查找）能力。

type rbColor bool

const (
	rbRed   rbColor = true
	rbBlack rbColor = false
)

type rbNode struct {
	key    uint32
	vnode  *vnode
	color  rbColor
	left   *rbNode
	right  *rbNode
	parent *rbNode
}

type rbTree struct {
	root *rbNode
	size int
}

func (t *rbTree) len() int { return t.size }

func (t *rbTree) get(key uint32) *rbNode {
	n := t.root
	for n != nil {
		switch {
		case key < n.key:
			n = n.left
		case key > n.key:
			n = n.right
		default:
			return n
		}
	}
	return nil
}

// ceiling 返回环上 key 的顺时针后继（第一个 >= key 的节点），
// 若不存在则返回 nil（调用方负责回绕到最小节点）。
func (t *rbTree) ceiling(key uint32) *rbNode {
	n := t.root
	var best *rbNode
	for n != nil {
		if n.key == key {
			return n
		}
		if n.key < key {
			n = n.right
		} else {
			best = n
			n = n.left
		}
	}
	return best
}

func (t *rbTree) min() *rbNode {
	n := t.root
	if n == nil {
		return nil
	}
	for n.left != nil {
		n = n.left
	}
	return n
}

func rbMin(n *rbNode) *rbNode {
	for n.left != nil {
		n = n.left
	}
	return n
}

func (t *rbTree) rotateLeft(x *rbNode) {
	y := x.right
	x.right = y.left
	if y.left != nil {
		y.left.parent = x
	}
	y.parent = x.parent
	switch {
	case x.parent == nil:
		t.root = y
	case x == x.parent.left:
		x.parent.left = y
	default:
		x.parent.right = y
	}
	y.left = x
	x.parent = y
}

func (t *rbTree) rotateRight(x *rbNode) {
	y := x.left
	x.left = y.right
	if y.right != nil {
		y.right.parent = x
	}
	y.parent = x.parent
	switch {
	case x.parent == nil:
		t.root = y
	case x == x.parent.right:
		x.parent.right = y
	default:
		x.parent.left = y
	}
	y.right = x
	x.parent = y
}

func (t *rbTree) insert(key uint32, v *vnode) {
	n := &rbNode{key: key, vnode: v, color: rbRed}
	var parent *rbNode
	cur := t.root
	for cur != nil {
		parent = cur
		if key < cur.key {
			cur = cur.left
		} else {
			cur = cur.right
		}
	}
	n.parent = parent
	switch {
	case parent == nil:
		t.root = n
	case key < parent.key:
		parent.left = n
	default:
		parent.right = n
	}
	t.size++
	t.insertFixup(n)
}

func (t *rbTree) insertFixup(z *rbNode) {
	for z.parent != nil && z.parent.color == rbRed {
		if z.parent == z.parent.parent.left {
			y := z.parent.parent.right
			if y != nil && y.color == rbRed {
				z.parent.color = rbBlack
				y.color = rbBlack
				z.parent.parent.color = rbRed
				z = z.parent.parent
			} else {
				if z == z.parent.right {
					z = z.parent
					t.rotateLeft(z)
				}
				z.parent.color = rbBlack
				z.parent.parent.color = rbRed
				t.rotateRight(z.parent.parent)
			}
		} else {
			y := z.parent.parent.left
			if y != nil && y.color == rbRed {
				z.parent.color = rbBlack
				y.color = rbBlack
				z.parent.parent.color = rbRed
				z = z.parent.parent
			} else {
				if z == z.parent.left {
					z = z.parent
					t.rotateRight(z)
				}
				z.parent.color = rbBlack
				z.parent.parent.color = rbRed
				t.rotateLeft(z.parent.parent)
			}
		}
	}
	t.root.color = rbBlack
}

func (t *rbTree) transplant(u, v *rbNode) {
	switch {
	case u.parent == nil:
		t.root = v
	case u == u.parent.left:
		u.parent.left = v
	default:
		u.parent.right = v
	}
	if v != nil {
		v.parent = u.parent
	}
}

func (t *rbTree) delete(z *rbNode) {
	y := z
	yOriginal := y.color
	var x, xParent *rbNode
	switch {
	case z.left == nil:
		x = z.right
		xParent = z.parent
		t.transplant(z, z.right)
	case z.right == nil:
		x = z.left
		xParent = z.parent
		t.transplant(z, z.left)
	default:
		y = rbMin(z.right)
		yOriginal = y.color
		x = y.right
		if y.parent == z {
			xParent = y
			if x != nil {
				x.parent = y
			}
		} else {
			xParent = y.parent
			t.transplant(y, y.right)
			y.right = z.right
			y.right.parent = y
		}
		t.transplant(z, y)
		y.left = z.left
		y.left.parent = y
		y.color = z.color
	}
	t.size--
	if yOriginal == rbBlack {
		t.deleteFixup(x, xParent)
	}
}

func rbIsBlack(n *rbNode) bool { return n == nil || n.color == rbBlack }

func (t *rbTree) deleteFixup(x, parent *rbNode) {
	for x != t.root && rbIsBlack(x) {
		if x == parent.left {
			w := parent.right
			if w.color == rbRed {
				w.color = rbBlack
				parent.color = rbRed
				t.rotateLeft(parent)
				w = parent.right
			}
			if rbIsBlack(w.left) && rbIsBlack(w.right) {
				w.color = rbRed
				x = parent
				parent = x.parent
			} else {
				if rbIsBlack(w.right) {
					if w.left != nil {
						w.left.color = rbBlack
					}
					w.color = rbRed
					t.rotateRight(w)
					w = parent.right
				}
				w.color = parent.color
				parent.color = rbBlack
				if w.right != nil {
					w.right.color = rbBlack
				}
				t.rotateLeft(parent)
				x = t.root
				parent = nil
			}
		} else {
			w := parent.left
			if w.color == rbRed {
				w.color = rbBlack
				parent.color = rbRed
				t.rotateRight(parent)
				w = parent.left
			}
			if rbIsBlack(w.left) && rbIsBlack(w.right) {
				w.color = rbRed
				x = parent
				parent = x.parent
			} else {
				if rbIsBlack(w.left) {
					if w.right != nil {
						w.right.color = rbBlack
					}
					w.color = rbRed
					t.rotateLeft(w)
					w = parent.left
				}
				w.color = parent.color
				parent.color = rbBlack
				if w.left != nil {
					w.left.color = rbBlack
				}
				t.rotateRight(parent)
				x = t.root
				parent = nil
			}
		}
	}
	if x != nil {
		x.color = rbBlack
	}
}

// inOrder 按哈希升序遍历（顺时针方向），供校验与调试。
func (t *rbTree) inOrder(fn func(key uint32, v *vnode)) {
	var walk func(n *rbNode)
	walk = func(n *rbNode) {
		if n == nil {
			return
		}
		walk(n.left)
		fn(n.key, n.vnode)
		walk(n.right)
	}
	walk(t.root)
}
