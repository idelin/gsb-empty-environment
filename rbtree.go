package leasehash

// 红黑树（CLRS 实现），作为一致性哈希环的有序拓扑结构。
// key 为虚拟节点的 64 位哈希值，value 为持有该虚拟节点的物理节点 ID。

const (
	red   = false
	black = true
)

type rbNode struct {
	key    uint64
	value  string
	color  bool
	left   *rbNode
	right  *rbNode
	parent *rbNode
}

type rbTree struct {
	root *rbNode
	size int
}

func (t *rbTree) len() int { return t.size }

// get 返回 key 对应的 value。
func (t *rbTree) get(key uint64) (string, bool) {
	n := t.root
	for n != nil {
		if key < n.key {
			n = n.left
		} else if key > n.key {
			n = n.right
		} else {
			return n.value, true
		}
	}
	return "", false
}

// successor 返回环上第一个 key >= hash 的节点；若 hash 超过最大值则回绕到最小节点。
// 这就是一致性哈希的"顺时针查找"。
func (t *rbTree) successor(hash uint64) (*rbNode, bool) {
	if t.root == nil {
		return nil, false
	}
	n := t.root
	var candidate *rbNode
	for n != nil {
		if hash <= n.key {
			candidate = n
			n = n.left
		} else {
			n = n.right
		}
	}
	if candidate != nil {
		return candidate, true
	}
	// 回绕：返回全树最小节点
	n = t.root
	for n.left != nil {
		n = n.left
	}
	return n, true
}

func (t *rbTree) minimum(n *rbNode) *rbNode {
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
	if x.parent == nil {
		t.root = y
	} else if x == x.parent.left {
		x.parent.left = y
	} else {
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
	if x.parent == nil {
		t.root = y
	} else if x == x.parent.right {
		x.parent.right = y
	} else {
		x.parent.left = y
	}
	y.right = x
	x.parent = y
}

// insert 插入 key/value；若 key 已存在则覆盖 value。
func (t *rbTree) insert(key uint64, value string) {
	var parent *rbNode
	cur := t.root
	for cur != nil {
		parent = cur
		if key < cur.key {
			cur = cur.left
		} else if key > cur.key {
			cur = cur.right
		} else {
			cur.value = value
			return
		}
	}
	n := &rbNode{key: key, value: value, color: red, parent: parent}
	if parent == nil {
		t.root = n
	} else if key < parent.key {
		parent.left = n
	} else {
		parent.right = n
	}
	t.size++
	t.insertFixup(n)
}

func (t *rbTree) insertFixup(z *rbNode) {
	for z.parent != nil && z.parent.color == red {
		if z.parent == z.parent.parent.left {
			y := z.parent.parent.right
			if y != nil && y.color == red {
				z.parent.color = black
				y.color = black
				z.parent.parent.color = red
				z = z.parent.parent
			} else {
				if z == z.parent.right {
					z = z.parent
					t.rotateLeft(z)
				}
				z.parent.color = black
				z.parent.parent.color = red
				t.rotateRight(z.parent.parent)
			}
		} else {
			y := z.parent.parent.left
			if y != nil && y.color == red {
				z.parent.color = black
				y.color = black
				z.parent.parent.color = red
				z = z.parent.parent
			} else {
				if z == z.parent.left {
					z = z.parent
					t.rotateRight(z)
				}
				z.parent.color = black
				z.parent.parent.color = red
				t.rotateLeft(z.parent.parent)
			}
		}
	}
	t.root.color = black
}

func (t *rbTree) transplant(u, v *rbNode) {
	if u.parent == nil {
		t.root = v
	} else if u == u.parent.left {
		u.parent.left = v
	} else {
		u.parent.right = v
	}
	if v != nil {
		v.parent = u.parent
	}
}

// remove 删除指定 key 的节点。
func (t *rbTree) remove(key uint64) bool {
	var z *rbNode
	cur := t.root
	for cur != nil {
		if key < cur.key {
			cur = cur.left
		} else if key > cur.key {
			cur = cur.right
		} else {
			z = cur
			break
		}
	}
	if z == nil {
		return false
	}
	t.size--
	y := z
	yOriginalColor := y.color
	var x *rbNode
	var xParent *rbNode
	if z.left == nil {
		x = z.right
		xParent = z.parent
		t.transplant(z, z.right)
	} else if z.right == nil {
		x = z.left
		xParent = z.parent
		t.transplant(z, z.left)
	} else {
		y = t.minimum(z.right)
		yOriginalColor = y.color
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
	if yOriginalColor == black {
		t.deleteFixup(x, xParent)
	}
	return true
}

// deleteFixup 使用 (x, xParent) 显式传递父节点，以支持 x 为 nil 的情形。
func (t *rbTree) deleteFixup(x *rbNode, xParent *rbNode) {
	for x != t.root && (x == nil || x.color == black) {
		if xParent == nil {
			break
		}
		if x == xParent.left {
			w := xParent.right
			if w != nil && w.color == red {
				w.color = black
				xParent.color = red
				t.rotateLeft(xParent)
				w = xParent.right
			}
			if w == nil {
				x = xParent
				xParent = x.parent
				continue
			}
			if (w.left == nil || w.left.color == black) && (w.right == nil || w.right.color == black) {
				w.color = red
				x = xParent
				xParent = x.parent
			} else {
				if w.right == nil || w.right.color == black {
					if w.left != nil {
						w.left.color = black
					}
					w.color = red
					t.rotateRight(w)
					w = xParent.right
				}
				w.color = xParent.color
				xParent.color = black
				if w.right != nil {
					w.right.color = black
				}
				t.rotateLeft(xParent)
				x = t.root
				xParent = nil
			}
		} else {
			w := xParent.left
			if w != nil && w.color == red {
				w.color = black
				xParent.color = red
				t.rotateRight(xParent)
				w = xParent.left
			}
			if w == nil {
				x = xParent
				xParent = x.parent
				continue
			}
			if (w.right == nil || w.right.color == black) && (w.left == nil || w.left.color == black) {
				w.color = red
				x = xParent
				xParent = x.parent
			} else {
				if w.left == nil || w.left.color == black {
					if w.right != nil {
						w.right.color = black
					}
					w.color = red
					t.rotateLeft(w)
					w = xParent.left
				}
				w.color = xParent.color
				xParent.color = black
				if w.left != nil {
					w.left.color = black
				}
				t.rotateRight(xParent)
				x = t.root
				xParent = nil
			}
		}
	}
	if x != nil {
		x.color = black
	}
}

// inOrder 按 key 升序遍历（即沿环顺时针方向）。
func (t *rbTree) inOrder(fn func(key uint64, value string)) {
	var walk func(n *rbNode)
	walk = func(n *rbNode) {
		if n == nil {
			return
		}
		walk(n.left)
		fn(n.key, n.value)
		walk(n.right)
	}
	walk(t.root)
}
