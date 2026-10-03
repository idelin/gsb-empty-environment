package broadphase

const nullNode = -1

// node 是动态 AABB 树的节点。叶子节点保存代理 ID，内部节点保存子树联合包围盒。
type node struct {
	aabb    AABB
	parent  int
	child1  int
	child2  int
	height  int
	proxyID int // 仅叶子有效，内部节点为 -1
	next    int // 空闲链表
}

func (n *node) isLeaf() bool { return n.child1 == nullNode }

// Tree 是一棵增量动态 AABB 树（参考 Box2D b2DynamicTree）。
// 插入、删除均为 O(log N)，并带旋转平衡以维持树质量。
type Tree struct {
	nodes      []node
	root       int
	freeList   int
	proxyIndex map[int]int // proxyID -> 叶子节点下标
}

// NewTree 创建一棵空树。
func NewTree() *Tree {
	return &Tree{root: nullNode, freeList: nullNode, proxyIndex: make(map[int]int)}
}

// Len 返回树中代理数量。
func (t *Tree) Len() int { return len(t.proxyIndex) }

// Height 返回树高（空树为 -1），用于校验平衡性。
func (t *Tree) Height() int {
	if t.root == nullNode {
		return -1
	}
	return t.nodes[t.root].height
}

// AABB 返回指定代理当前存储在树中的包围盒。
func (t *Tree) AABB(proxyID int) AABB {
	return t.nodes[t.proxyIndex[proxyID]].aabb
}

// Insert 插入一个代理。proxyID 重复会 panic。
func (t *Tree) Insert(proxyID int, aabb AABB) {
	if _, ok := t.proxyIndex[proxyID]; ok {
		panic("broadphase: duplicate proxy id")
	}
	leaf := t.allocateNode()
	t.nodes[leaf] = node{
		aabb:    aabb,
		parent:  nullNode,
		child1:  nullNode,
		child2:  nullNode,
		proxyID: proxyID,
	}
	t.proxyIndex[proxyID] = leaf
	t.insertLeaf(leaf)
}

// Remove 删除一个代理。不存在会 panic。
func (t *Tree) Remove(proxyID int) {
	leaf, ok := t.proxyIndex[proxyID]
	if !ok {
		panic("broadphase: unknown proxy id")
	}
	delete(t.proxyIndex, proxyID)
	t.removeLeaf(leaf)
	t.freeNode(leaf)
}

// Query 遍历树，对所有与 aabb 重叠的叶子回调其 proxyID。
// 回调返回 false 可提前终止。
func (t *Tree) Query(aabb AABB, visit func(proxyID int) bool) {
	if t.root == nullNode {
		return
	}
	stack := []int{t.root}
	for len(stack) > 0 {
		i := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		n := t.nodes[i]
		if !n.aabb.Overlaps(aabb) {
			continue
		}
		if n.isLeaf() {
			if !visit(n.proxyID) {
				return
			}
		} else {
			stack = append(stack, n.child1, n.child2)
		}
	}
}

// CollidePairs 执行树内自相交遍历，对每个包围盒重叠的叶子对回调一次。
// 每个无序叶子对恰好被回调一次。
func (t *Tree) CollidePairs(visit func(a, b int)) {
	if t.root == nullNode || t.nodes[t.root].isLeaf() {
		return
	}
	type nodePair struct{ a, b int }
	stack := []nodePair{{t.root, t.root}}
	for len(stack) > 0 {
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		a, b := t.nodes[p.a], t.nodes[p.b]

		if p.a == p.b {
			// 同一子树：展开为 (c1,c1)、(c2,c2)、(c1,c2)。
			if a.isLeaf() {
				continue
			}
			stack = append(stack,
				nodePair{a.child1, a.child1},
				nodePair{a.child2, a.child2},
				nodePair{a.child1, a.child2})
			continue
		}

		if !a.aabb.Overlaps(b.aabb) {
			continue
		}
		if a.isLeaf() && b.isLeaf() {
			visit(a.proxyID, b.proxyID)
			continue
		}
		// 下降更高（或唯一非叶子）的一侧，保持遍历平衡。
		if b.isLeaf() || (!a.isLeaf() && a.height >= b.height) {
			stack = append(stack, nodePair{a.child1, p.b}, nodePair{a.child2, p.b})
		} else {
			stack = append(stack, nodePair{p.a, b.child1}, nodePair{p.a, b.child2})
		}
	}
}

func (t *Tree) allocateNode() int {
	if t.freeList != nullNode {
		i := t.freeList
		t.freeList = t.nodes[i].next
		return i
	}
	t.nodes = append(t.nodes, node{})
	return len(t.nodes) - 1
}

func (t *Tree) freeNode(i int) {
	t.nodes[i].next = t.freeList
	t.nodes[i].proxyID = -1
	t.freeList = i
}

// bestSibling 以周长启发（表面积启发 SAH 的二维形式）自顶向下寻找最优兄弟节点。
func (t *Tree) bestSibling(leafAABB AABB) int {
	index := t.root
	for !t.nodes[index].isLeaf() {
		n := t.nodes[index]
		c1, c2 := n.child1, n.child2

		area := n.aabb.Perimeter()
		combinedArea := Combine(n.aabb, leafAABB).Perimeter()

		// 直接在此新建父节点的代价。
		cost := 2 * combinedArea
		// 下推到子节点时，祖先包围盒膨胀的继承代价。
		inheritance := 2 * (combinedArea - area)
		cost1 := descendCost(t.nodes[c1], leafAABB, inheritance)
		cost2 := descendCost(t.nodes[c2], leafAABB, inheritance)

		if cost < cost1 && cost < cost2 {
			break
		}
		if cost1 < cost2 {
			index = c1
		} else {
			index = c2
		}
	}
	return index
}

func descendCost(n node, leaf AABB, inheritance float64) float64 {
	if n.isLeaf() {
		return Combine(n.aabb, leaf).Perimeter() + inheritance
	}
	return Combine(n.aabb, leaf).Perimeter() - n.aabb.Perimeter() + inheritance
}

func (t *Tree) insertLeaf(leaf int) {
	if t.root == nullNode {
		t.root = leaf
		return
	}

	leafAABB := t.nodes[leaf].aabb
	sibling := t.bestSibling(leafAABB)

	oldParent := t.nodes[sibling].parent
	newParent := t.allocateNode()
	t.nodes[newParent] = node{
		aabb:    Combine(t.nodes[sibling].aabb, leafAABB),
		parent:  oldParent,
		child1:  sibling,
		child2:  leaf,
		height:  t.nodes[sibling].height + 1,
		proxyID: -1,
	}
	t.nodes[sibling].parent = newParent
	t.nodes[leaf].parent = newParent

	if oldParent == nullNode {
		t.root = newParent
	} else if t.nodes[oldParent].child1 == sibling {
		t.nodes[oldParent].child1 = newParent
	} else {
		t.nodes[oldParent].child2 = newParent
	}

	// 自底向上修复包围盒与高度，并做旋转平衡。
	index := t.nodes[leaf].parent
	for index != nullNode {
		index = t.balance(index)
		n := &t.nodes[index]
		c1, c2 := t.nodes[n.child1], t.nodes[n.child2]
		n.height = 1 + max(c1.height, c2.height)
		n.aabb = Combine(c1.aabb, c2.aabb)
		index = n.parent
	}
}

func (t *Tree) removeLeaf(leaf int) {
	if leaf == t.root {
		t.root = nullNode
		return
	}

	parent := t.nodes[leaf].parent
	grandParent := t.nodes[parent].parent
	var sibling int
	if t.nodes[parent].child1 == leaf {
		sibling = t.nodes[parent].child2
	} else {
		sibling = t.nodes[parent].child1
	}

	if grandParent == nullNode {
		t.root = sibling
		t.nodes[sibling].parent = nullNode
		t.freeNode(parent)
		return
	}

	if t.nodes[grandParent].child1 == parent {
		t.nodes[grandParent].child1 = sibling
	} else {
		t.nodes[grandParent].child2 = sibling
	}
	t.nodes[sibling].parent = grandParent
	t.freeNode(parent)

	index := grandParent
	for index != nullNode {
		index = t.balance(index)
		n := &t.nodes[index]
		c1, c2 := t.nodes[n.child1], t.nodes[n.child2]
		n.aabb = Combine(c1.aabb, c2.aabb)
		n.height = 1 + max(c1.height, c2.height)
		index = n.parent
	}
}

// balance 对子树根 iA 做 AVL 式旋转，返回新的子树根下标。
func (t *Tree) balance(iA int) int {
	a := t.nodes[iA]
	if a.isLeaf() || a.height < 2 {
		return iA
	}

	iB, iC := a.child1, a.child2
	diff := t.nodes[iC].height - t.nodes[iB].height

	if diff > 1 { // C 子树过高，将 C 上旋。
		return t.rotateUp(iA, iC, iB)
	}
	if diff < -1 { // B 子树过高，将 B 上旋。
		return t.rotateUp(iA, iB, iC)
	}
	return iA
}

// rotateUp 将 iA 的孩子 iTop 上旋为子树根，iOther 是 iA 的另一个孩子。
func (t *Tree) rotateUp(iA, iTop, iOther int) int {
	top := t.nodes[iTop]
	iF, iG := top.child1, top.child2

	// top 取代 a 的位置。
	top.child1 = iA
	top.parent = t.nodes[iA].parent
	t.nodes[iA].parent = iTop
	if top.parent != nullNode {
		p := &t.nodes[top.parent]
		if p.child1 == iA {
			p.child1 = iTop
		} else {
			p.child2 = iTop
		}
	} else {
		t.root = iTop
	}

	// 把 top 较高的子树留在 top 下，较矮的挂到 a 下。
	a := t.nodes[iA]
	// iTop 原来是 a 的 child1 还是 child2，较矮子树要挂回对应的槽位。
	topWasChild1 := a.child1 == iTop
	setShort := func(short int) {
		if topWasChild1 {
			a.child1 = short
		} else {
			a.child2 = short
		}
	}
	if t.nodes[iF].height > t.nodes[iG].height {
		top.child2 = iF
		setShort(iG)
		t.nodes[iG].parent = iA
		a.aabb = Combine(t.nodes[iOther].aabb, t.nodes[iG].aabb)
		top.aabb = Combine(a.aabb, t.nodes[iF].aabb)
		a.height = 1 + max(t.nodes[iOther].height, t.nodes[iG].height)
		top.height = 1 + max(a.height, t.nodes[iF].height)
	} else {
		top.child2 = iG
		setShort(iF)
		t.nodes[iF].parent = iA
		a.aabb = Combine(t.nodes[iOther].aabb, t.nodes[iF].aabb)
		top.aabb = Combine(a.aabb, t.nodes[iG].aabb)
		a.height = 1 + max(t.nodes[iOther].height, t.nodes[iF].height)
		top.height = 1 + max(a.height, t.nodes[iG].height)
	}
	t.nodes[iA] = a
	t.nodes[iTop] = top
	return iTop
}
