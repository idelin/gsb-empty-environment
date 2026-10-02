package broadphase

// nullNode 表示空节点索引。
const nullNode int32 = -1

// treeNode 是动态 AABB 树的节点。
// 叶子节点保存实体代理（胖包围盒 + proxy ID），内部节点保存子树联合包围盒。
type treeNode struct {
	aabb   AABB
	parent int32
	left   int32
	right  int32
	next   int32 // 空闲链表指针
	height int32
	proxy  int // 仅叶子节点有效
}

func (n *treeNode) isLeaf() bool { return n.left == nullNode }

// Tree 是一棵增量动态 AABB 树（参考 Box2D b2DynamicTree 设计）：
//   - 插入采用 SAH（表面积启发式）贪心选择兄弟节点；
//   - 插入/删除后沿路径做旋转再平衡，保持树高 O(log N)；
//   - 节点通过切片 + 空闲链表复用，避免 GC 压力。
type Tree struct {
	nodes []treeNode
	root  int32
	free  int32
	count int
}

// NewTree 创建一棵空树。
func NewTree() *Tree {
	return &Tree{root: nullNode, free: nullNode}
}

// Count 返回树中叶子（代理）数量。
func (t *Tree) Count() int { return t.count }

func (t *Tree) alloc() int32 {
	if t.free != nullNode {
		id := t.free
		t.free = t.nodes[id].next
		return id
	}
	t.nodes = append(t.nodes, treeNode{})
	return int32(len(t.nodes) - 1)
}

func (t *Tree) release(id int32) {
	t.nodes[id] = treeNode{next: t.free, parent: nullNode, left: nullNode, right: nullNode, height: -1}
	t.free = id
}

// Insert 以给定（通常为胖）包围盒插入一个代理，返回代理节点 ID。
func (t *Tree) Insert(aabb AABB, proxy int) int32 {
	id := t.alloc()
	t.nodes[id] = treeNode{
		aabb:   aabb,
		parent: nullNode,
		left:   nullNode,
		right:  nullNode,
		height: 0,
		proxy:  proxy,
	}
	t.insertLeaf(id)
	t.count++
	return id
}

// Remove 从树中移除代理节点。
func (t *Tree) Remove(leaf int32) {
	t.removeLeaf(leaf)
	t.release(leaf)
	t.count--
}

// Move 以新包围盒重新挂载已有代理节点（删除 + 重插，节点 ID 不变）。
func (t *Tree) Move(proxy int32, aabb AABB) {
	t.removeLeaf(proxy)
	t.nodes[proxy].aabb = aabb
	t.insertLeaf(proxy)
}

// insertLeaf 使用 SAH 启发式为叶子寻找最优兄弟节点并挂载。
func (t *Tree) insertLeaf(leaf int32) {
	if t.root == nullNode {
		t.root = leaf
		t.nodes[leaf].parent = nullNode
		return
	}

	leafAABB := t.nodes[leaf].aabb
	index := t.root
	for !t.nodes[index].isLeaf() {
		left := t.nodes[index].left
		right := t.nodes[index].right

		area := t.nodes[index].aabb.SurfaceArea()
		combinedArea := Union(t.nodes[index].aabb, leafAABB).SurfaceArea()

		// 直接在此新建父节点的代价。
		cost := 2 * combinedArea
		// 下沉到子树时，祖先节点表面积膨胀的继承代价。
		inheritance := 2 * (combinedArea - area)

		costLeft := descendCost(&t.nodes[left], leafAABB, inheritance)
		costRight := descendCost(&t.nodes[right], leafAABB, inheritance)

		if cost < costLeft && cost < costRight {
			break
		}
		if costLeft < costRight {
			index = left
		} else {
			index = right
		}
	}

	sibling := index
	oldParent := t.nodes[sibling].parent
	newParent := t.alloc()
	t.nodes[newParent] = treeNode{
		aabb:   Union(leafAABB, t.nodes[sibling].aabb),
		parent: oldParent,
		left:   sibling,
		right:  leaf,
		height: t.nodes[sibling].height + 1,
	}
	if oldParent != nullNode {
		if t.nodes[oldParent].left == sibling {
			t.nodes[oldParent].left = newParent
		} else {
			t.nodes[oldParent].right = newParent
		}
	} else {
		t.root = newParent
	}
	t.nodes[sibling].parent = newParent
	t.nodes[leaf].parent = newParent

	// 自底向上修复包围盒与高度，并做旋转再平衡。
	index = t.nodes[leaf].parent
	for index != nullNode {
		index = t.balance(index)
		l := t.nodes[index].left
		r := t.nodes[index].right
		t.nodes[index].height = 1 + maxI32(t.nodes[l].height, t.nodes[r].height)
		t.nodes[index].aabb = Union(t.nodes[l].aabb, t.nodes[r].aabb)
		index = t.nodes[index].parent
	}
}

func descendCost(n *treeNode, leaf AABB, inheritance float64) float64 {
	if n.isLeaf() {
		return Union(n.aabb, leaf).SurfaceArea() + inheritance
	}
	return Union(n.aabb, leaf).SurfaceArea() - n.aabb.SurfaceArea() + inheritance
}

func (t *Tree) removeLeaf(leaf int32) {
	if leaf == t.root {
		t.root = nullNode
		return
	}
	parent := t.nodes[leaf].parent
	grand := t.nodes[parent].parent
	var sibling int32
	if t.nodes[parent].left == leaf {
		sibling = t.nodes[parent].right
	} else {
		sibling = t.nodes[parent].left
	}
	if grand != nullNode {
		if t.nodes[grand].left == parent {
			t.nodes[grand].left = sibling
		} else {
			t.nodes[grand].right = sibling
		}
		t.nodes[sibling].parent = grand
		t.release(parent)

		index := grand
		for index != nullNode {
			index = t.balance(index)
			l := t.nodes[index].left
			r := t.nodes[index].right
			t.nodes[index].aabb = Union(t.nodes[l].aabb, t.nodes[r].aabb)
			t.nodes[index].height = 1 + maxI32(t.nodes[l].height, t.nodes[r].height)
			index = t.nodes[index].parent
		}
	} else {
		t.root = sibling
		t.nodes[sibling].parent = nullNode
		t.release(parent)
	}
}

// balance 对高度差超过 1 的内部节点做 AVL 式旋转，返回新的子树根。
func (t *Tree) balance(iA int32) int32 {
	if t.nodes[iA].isLeaf() || t.nodes[iA].height < 2 {
		return iA
	}

	iB := t.nodes[iA].left
	iC := t.nodes[iA].right
	diff := t.nodes[iC].height - t.nodes[iB].height

	// C 子树过高：将 C 上旋。
	if diff > 1 {
		iF := t.nodes[iC].left
		iG := t.nodes[iC].right

		t.nodes[iC].left = iA
		t.nodes[iC].parent = t.nodes[iA].parent
		t.nodes[iA].parent = iC
		if t.nodes[iC].parent != nullNode {
			if t.nodes[t.nodes[iC].parent].left == iA {
				t.nodes[t.nodes[iC].parent].left = iC
			} else {
				t.nodes[t.nodes[iC].parent].right = iC
			}
		} else {
			t.root = iC
		}

		if t.nodes[iF].height > t.nodes[iG].height {
			t.nodes[iC].right = iF
			t.nodes[iA].right = iG
			t.nodes[iG].parent = iA
			t.nodes[iA].aabb = Union(t.nodes[iB].aabb, t.nodes[iG].aabb)
			t.nodes[iC].aabb = Union(t.nodes[iA].aabb, t.nodes[iF].aabb)
			t.nodes[iA].height = 1 + maxI32(t.nodes[iB].height, t.nodes[iG].height)
			t.nodes[iC].height = 1 + maxI32(t.nodes[iA].height, t.nodes[iF].height)
		} else {
			t.nodes[iC].right = iG
			t.nodes[iA].right = iF
			t.nodes[iF].parent = iA
			t.nodes[iA].aabb = Union(t.nodes[iB].aabb, t.nodes[iF].aabb)
			t.nodes[iC].aabb = Union(t.nodes[iA].aabb, t.nodes[iG].aabb)
			t.nodes[iA].height = 1 + maxI32(t.nodes[iB].height, t.nodes[iF].height)
			t.nodes[iC].height = 1 + maxI32(t.nodes[iA].height, t.nodes[iG].height)
		}
		return iC
	}

	// B 子树过高：将 B 上旋。
	if diff < -1 {
		iE := t.nodes[iB].left
		iF := t.nodes[iB].right

		t.nodes[iB].left = iA
		t.nodes[iB].parent = t.nodes[iA].parent
		t.nodes[iA].parent = iB
		if t.nodes[iB].parent != nullNode {
			if t.nodes[t.nodes[iB].parent].left == iA {
				t.nodes[t.nodes[iB].parent].left = iB
			} else {
				t.nodes[t.nodes[iB].parent].right = iB
			}
		} else {
			t.root = iB
		}

		if t.nodes[iE].height > t.nodes[iF].height {
			t.nodes[iB].right = iE
			t.nodes[iA].left = iF
			t.nodes[iF].parent = iA
			t.nodes[iA].aabb = Union(t.nodes[iC].aabb, t.nodes[iF].aabb)
			t.nodes[iB].aabb = Union(t.nodes[iA].aabb, t.nodes[iE].aabb)
			t.nodes[iA].height = 1 + maxI32(t.nodes[iC].height, t.nodes[iF].height)
			t.nodes[iB].height = 1 + maxI32(t.nodes[iA].height, t.nodes[iE].height)
		} else {
			t.nodes[iB].right = iF
			t.nodes[iA].left = iE
			t.nodes[iE].parent = iA
			t.nodes[iA].aabb = Union(t.nodes[iC].aabb, t.nodes[iE].aabb)
			t.nodes[iB].aabb = Union(t.nodes[iA].aabb, t.nodes[iF].aabb)
			t.nodes[iA].height = 1 + maxI32(t.nodes[iC].height, t.nodes[iE].height)
			t.nodes[iB].height = 1 + maxI32(t.nodes[iA].height, t.nodes[iF].height)
		}
		return iB
	}

	return iA
}

// Pairs 执行树内自相交遍历，对每一对胖包围盒重叠的叶子回调 (proxyA, proxyB)，
// 保证 proxyA < proxyB 且每对只报告一次。复杂度约 O(N log N + K)，K 为重叠对数。
func (t *Tree) Pairs(cb func(a, b int)) {
	if t.root == nullNode {
		return
	}

	selfStack := []int32{t.root}
	var pairStack [][2]int32

	for len(selfStack) > 0 {
		n := selfStack[len(selfStack)-1]
		selfStack = selfStack[:len(selfStack)-1]
		if t.nodes[n].isLeaf() {
			continue
		}
		l := t.nodes[n].left
		r := t.nodes[n].right
		// 子树内部各自的重叠对。
		selfStack = append(selfStack, l, r)
		// 左右子树之间的交叉重叠对。
		pairStack = append(pairStack, [2]int32{l, r})

		for len(pairStack) > 0 {
			p := pairStack[len(pairStack)-1]
			pairStack = pairStack[:len(pairStack)-1]
			a := p[0]
			b := p[1]
			if !t.nodes[a].aabb.Overlaps(t.nodes[b].aabb) {
				continue
			}
			aLeaf := t.nodes[a].isLeaf()
			bLeaf := t.nodes[b].isLeaf()
			switch {
			case aLeaf && bLeaf:
				x := t.nodes[a].proxy
				y := t.nodes[b].proxy
				if x > y {
					x, y = y, x
				}
				cb(x, y)
			case aLeaf:
				pairStack = append(pairStack, [2]int32{a, t.nodes[b].left}, [2]int32{a, t.nodes[b].right})
			case bLeaf:
				pairStack = append(pairStack, [2]int32{t.nodes[a].left, b}, [2]int32{t.nodes[a].right, b})
			default:
				// 都是内部节点：下降高度更大的一侧，保持遍历平衡。
				if t.nodes[a].height > t.nodes[b].height {
					pairStack = append(pairStack, [2]int32{t.nodes[a].left, b}, [2]int32{t.nodes[a].right, b})
				} else {
					pairStack = append(pairStack, [2]int32{a, t.nodes[b].left}, [2]int32{a, t.nodes[b].right})
				}
			}
		}
	}
}

func maxI32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}
