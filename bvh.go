// Package bvh 实现基于 AABB 的层次包围盒（BVH）树。
//
// 特性：
//   - 构建期使用表面积启发式（SAH）做空间最优二分；
//   - 支持动态图元的插入与删除（自底向上 refit 包围盒）；
//   - 射线相交测试（Ray Cast），利用包围盒快速剪枝，返回最近命中；
//   - 图元通过 Primitive 接口抽象，三维/二维场景均可使用。
package bvh

import (
	"math"
	"sort"
)

// leafSize 是叶子节点容纳的最大图元数。
const leafSize = 2

// Primitive 是可放入 BVH 的图元接口。三维用 Triangle；
// 二维场景可自定义图元（Z 分量恒为 0 即可）。
type Primitive interface {
	// Bounds 返回图元的 AABB。
	Bounds() AABB
	// Centroid 返回图元质心，用于 SAH 划分。
	Centroid() Vec3
	// Intersect 做精确射线相交测试，命中返回参数 t>0。
	Intersect(ray Ray) (t float64, ok bool)
}

// node 是 BVH 树节点。prims 非空时为叶子。
type node struct {
	bounds      AABB
	left, right *node
	parent      *node
	prims       []Primitive
}

func (n *node) isLeaf() bool { return n.prims != nil }

// Stats 记录一次 Raycast 的遍历统计，用于验证剪枝效率。
type Stats struct {
	NodesVisited  int // 弹栈处理的节点数
	LeavesVisited int // 实际进入的叶子数
	PrimTests     int // 精确图元相交测试次数
}

// Hit 是射线命中结果。未命中时 Prim 为 nil，T 为 +Inf。
type Hit struct {
	Prim Primitive
	T    float64
}

// BVH 是层次包围盒树。零值即可用，但建议用 New 创建。
type BVH struct {
	root   *node
	leaves map[Primitive]*node
	count  int
}

// New 创建空 BVH。
func New() *BVH {
	return &BVH{leaves: make(map[Primitive]*node)}
}

// Count 返回树中图元数量。
func (t *BVH) Count() int { return t.count }

// Bounds 返回整棵树的包围盒；空树返回 ok=false。
func (t *BVH) Bounds() (AABB, bool) {
	if t.root == nil {
		return AABB{}, false
	}
	return t.root.bounds, true
}

// LeafCount 返回叶子节点数量。
func (t *BVH) LeafCount() int {
	n := 0
	walk(t.root, func(nd *node) {
		if nd.isLeaf() {
			n++
		}
	})
	return n
}

func walk(n *node, fn func(*node)) {
	if n == nil {
		return
	}
	fn(n)
	if !n.isLeaf() {
		walk(n.left, fn)
		walk(n.right, fn)
	}
}

// Build 以 SAH 最优二分重建整棵树，替换原有内容。
func (t *BVH) Build(prims []Primitive) {
	t.leaves = make(map[Primitive]*node, len(prims))
	t.count = len(prims)
	if len(prims) == 0 {
		t.root = nil
		return
	}
	t.root = buildNode(prims, t.leaves)
}

// buildNode 递归构建：对每个轴按质心排序后扫描全部二分点，
// 取 SAH 代价最小者；若二分不优于直接成叶则停止。
func buildNode(prims []Primitive, leaves map[Primitive]*node) *node {
	n := &node{}
	bounds := prims[0].Bounds()
	cmin := prims[0].Centroid()
	cmax := cmin
	for _, p := range prims[1:] {
		bounds = bounds.Union(p.Bounds())
		c := p.Centroid()
		cmin = minVec(cmin, c)
		cmax = maxVec(cmax, c)
	}
	n.bounds = bounds

	makeLeaf := func() *node {
		n.prims = append([]Primitive(nil), prims...)
		for _, p := range prims {
			leaves[p] = n
		}
		return n
	}

	if len(prims) <= leafSize {
		return makeLeaf()
	}

	parentSA := bounds.SurfaceArea()
	bestCost := math.Inf(1)
	bestPos := 0
	var bestSorted []Primitive

	for axis := 0; axis < 3; axis++ {
		if cmax[axis]-cmin[axis] < 1e-12 {
			continue
		}
		sorted := append([]Primitive(nil), prims...)
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].Centroid()[axis] < sorted[j].Centroid()[axis]
		})
		nn := len(sorted)
		pre := make([]AABB, nn)
		suf := make([]AABB, nn)
		b := sorted[0].Bounds()
		pre[0] = b
		for i := 1; i < nn; i++ {
			b = b.Union(sorted[i].Bounds())
			pre[i] = b
		}
		b = sorted[nn-1].Bounds()
		suf[nn-1] = b
		for i := nn - 2; i >= 0; i-- {
			b = b.Union(sorted[i].Bounds())
			suf[i] = b
		}
		for i := 1; i < nn; i++ {
			cost := pre[i-1].SurfaceArea()*float64(i) +
				suf[i].SurfaceArea()*float64(nn-i)
			if cost < bestCost {
				bestCost = cost
				bestPos = i
				bestSorted = sorted
			}
		}
	}

	// SAH：二分期望代价（归一化后与父表面积比较）不优于成叶则停止。
	if bestSorted == nil || bestCost >= float64(len(prims))*parentSA {
		return makeLeaf()
	}

	n.left = buildNode(bestSorted[:bestPos], leaves)
	n.right = buildNode(bestSorted[bestPos:], leaves)
	n.left.parent = n
	n.right.parent = n
	return n
}

// Insert 动态插入图元：沿"包围盒表面积增量最小"的路径下降到叶子，
// 在叶子处生成新内部节点，然后自底向上 refit。
func (t *BVH) Insert(p Primitive) {
	if t.leaves == nil {
		t.leaves = make(map[Primitive]*node)
	}
	leaf := &node{prims: []Primitive{p}, bounds: p.Bounds()}
	t.leaves[p] = leaf
	t.count++

	if t.root == nil {
		t.root = leaf
		return
	}

	cur := t.root
	pb := p.Bounds()
	for !cur.isLeaf() {
		dl := cur.left.bounds.Union(pb).SurfaceArea() - cur.left.bounds.SurfaceArea()
		dr := cur.right.bounds.Union(pb).SurfaceArea() - cur.right.bounds.SurfaceArea()
		if dl < dr {
			cur = cur.left
		} else {
			cur = cur.right
		}
	}

	parent := cur.parent
	internal := &node{
		bounds: cur.bounds.Union(pb),
		left:   cur,
		right:  leaf,
		parent: parent,
	}
	cur.parent = internal
	leaf.parent = internal
	if parent == nil {
		t.root = internal
		return
	}
	if parent.left == cur {
		parent.left = internal
	} else {
		parent.right = internal
	}
	t.refit(parent)
}

// Remove 删除图元。图元不存在时返回 false。
func (t *BVH) Remove(p Primitive) bool {
	leaf, ok := t.leaves[p]
	if !ok {
		return false
	}
	delete(t.leaves, p)
	t.count--

	rest := leaf.prims[:0]
	for _, q := range leaf.prims {
		if q != p {
			rest = append(rest, q)
		}
	}
	leaf.prims = rest

	if len(leaf.prims) > 0 {
		// 叶子中还有其他图元，只需 refit。
		t.refit(leaf)
		return true
	}

	// 叶子已空：用兄弟节点替代父节点，收缩树。
	parent := leaf.parent
	if parent == nil {
		t.root = nil
		return true
	}
	var sibling *node
	if parent.left == leaf {
		sibling = parent.right
	} else {
		sibling = parent.left
	}
	grand := parent.parent
	sibling.parent = grand
	if grand == nil {
		t.root = sibling
		return true
	}
	if grand.left == parent {
		grand.left = sibling
	} else {
		grand.right = sibling
	}
	t.refit(grand)
	return true
}

// refit 自节点向上重算所有祖先的包围盒。
func (t *BVH) refit(n *node) {
	for n != nil {
		if n.isLeaf() {
			b := n.prims[0].Bounds()
			for _, p := range n.prims[1:] {
				b = b.Union(p.Bounds())
			}
			n.bounds = b
		} else {
			n.bounds = n.left.bounds.Union(n.right.bounds)
		}
		n = n.parent
	}
}

// Raycast 返回射线上最近的命中图元。
// 遍历时先做 AABB slab 测试，未命中或进入距离已超过当前最近命中
// 的子树被整体跳过；子节点按进入距离近者优先入栈。
func (t *BVH) Raycast(ray Ray) (Hit, Stats) {
	var st Stats
	hit := Hit{T: math.Inf(1)}
	if t.root == nil {
		return hit, st
	}
	stack := make([]*node, 0, 64)
	stack = append(stack, t.root)
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		st.NodesVisited++

		tmin, ok := n.bounds.hit(ray)
		if !ok || tmin > hit.T {
			continue
		}
		if n.isLeaf() {
			st.LeavesVisited++
			for _, p := range n.prims {
				st.PrimTests++
				if pt, ok := p.Intersect(ray); ok && pt < hit.T {
					hit.T = pt
					hit.Prim = p
				}
			}
			continue
		}
		tl, okl := n.left.bounds.hit(ray)
		tr, okr := n.right.bounds.hit(ray)
		switch {
		case okl && okr:
			if tl < tr { // 远者先入栈，近者优先处理
				stack = append(stack, n.right, n.left)
			} else {
				stack = append(stack, n.left, n.right)
			}
		case okl:
			stack = append(stack, n.left)
		case okr:
			stack = append(stack, n.right)
		}
	}
	return hit, st
}
