// Package bvh 实现基于 AABB 的动态层次包围盒（BVH）树：
// SAH 最优二分构建、动态插入/删除、带剪枝的射线最近命中查询。
package bvh

import "math"

// Object 可被 BVH 管理的空间物体。
type Object interface {
	// Bounds 返回物体的轴对齐包围盒。
	Bounds() AABB
	// Intersect 返回射线与物体的最近正交距离 t 及是否命中。
	Intersect(r Ray) (t float64, ok bool)
}

type node struct {
	bounds      AABB
	left, right *node
	parent      *node
	obj         Object // 仅叶子节点非空
}

func (n *node) isLeaf() bool { return n.obj != nil }

// Stats 单次射线查询的遍历统计，用于验证剪枝效率。
type Stats struct {
	NodesVisited  int // 访问过的内部节点数
	LeavesVisited int // 实际做精确求交的叶子数
}

// BVH 动态层次包围盒树。
type BVH struct {
	root   *node
	leaves map[Object]*node
}

// New 创建空 BVH，可后续用 Insert 动态构建。
func New() *BVH {
	return &BVH{leaves: make(map[Object]*node)}
}

// Build 使用 SAH（表面积启发式）自顶向下构建最优二分树。
func Build(objects []Object) *BVH {
	t := New()
	if len(objects) == 0 {
		return t
	}
	items := make([]Object, len(objects))
	copy(items, objects)
	t.root = t.buildSAH(items)
	t.reindex()
	return t
}

// reindex 遍历整树重建 leaves 索引。
func (t *BVH) reindex() {
	t.leaves = make(map[Object]*node)
	stack := []*node{t.root}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n.isLeaf() {
			t.leaves[n.obj] = n
			continue
		}
		stack = append(stack, n.left, n.right)
	}
}

const sahBins = 16

// buildSAH 递归构建：沿质心包围盒最长轴分桶，枚举桶分割面取 SAH 代价最小者。
func (t *BVH) buildSAH(objs []Object) *node {
	if len(objs) == 1 {
		return &node{bounds: objs[0].Bounds(), obj: objs[0]}
	}

	centroidBounds := EmptyAABB()
	total := EmptyAABB()
	centroids := make([]Vec3, len(objs))
	for i, o := range objs {
		b := o.Bounds()
		total = total.Union(b)
		c := b.Min.Add(b.Max).Scale(0.5)
		centroids[i] = c
		centroidBounds = centroidBounds.Union(AABB{Min: c, Max: c})
	}

	extent := centroidBounds.Max.Sub(centroidBounds.Min)
	axis := 0
	if extent.Y > extent.X && extent.Y >= extent.Z {
		axis = 1
	} else if extent.Z > extent.X && extent.Z > extent.Y {
		axis = 2
	}

	axisMin := axisComp(centroidBounds.Min, axis)
	axisLen := axisComp(extent, axis)
	if axisLen < 1e-12 {
		// 质心全部重合，无法划分：直接均分
		mid := len(objs) / 2
		n := &node{}
		n.left = t.buildSAH(objs[:mid])
		n.right = t.buildSAH(objs[mid:])
		n.left.parent, n.right.parent = n, n
		n.bounds = n.left.bounds.Union(n.right.bounds)
		return n
	}

	type bin struct {
		bounds AABB
		count  int
	}
	bins := make([]bin, sahBins)
	for i := range bins {
		bins[i].bounds = EmptyAABB()
	}
	scale := float64(sahBins) / axisLen
	for i, o := range objs {
		idx := int(float64(axisComp(centroids[i], axis)-axisMin) * scale)
		if idx >= sahBins {
			idx = sahBins - 1
		}
		if idx < 0 {
			idx = 0
		}
		bins[idx].bounds = bins[idx].bounds.Union(o.Bounds())
		bins[idx].count++
	}

	// 枚举 sahBins-1 个分割面，计算 SAH 代价
	leftBounds := make([]AABB, sahBins-1)
	leftCount := make([]int, sahBins-1)
	acc := EmptyAABB()
	cnt := 0
	for i := 0; i < sahBins-1; i++ {
		acc = acc.Union(bins[i].bounds)
		cnt += bins[i].count
		leftBounds[i] = acc
		leftCount[i] = cnt
	}
	rightBounds := make([]AABB, sahBins-1)
	rightCount := make([]int, sahBins-1)
	acc = EmptyAABB()
	cnt = 0
	for i := sahBins - 1; i > 0; i-- {
		acc = acc.Union(bins[i].bounds)
		cnt += bins[i].count
		rightBounds[i-1] = acc
		rightCount[i-1] = cnt
	}

	parentSA := total.SurfaceArea()
	bestCost := math.Inf(1)
	bestSplit := -1
	for i := 0; i < sahBins-1; i++ {
		if leftCount[i] == 0 || rightCount[i] == 0 {
			continue
		}
		cost := (leftBounds[i].SurfaceArea()*float64(leftCount[i]) +
			rightBounds[i].SurfaceArea()*float64(rightCount[i])) / parentSA
		if cost < bestCost {
			bestCost = cost
			bestSplit = i
		}
	}

	if bestSplit < 0 {
		mid := len(objs) / 2
		n := &node{}
		n.left = t.buildSAH(objs[:mid])
		n.right = t.buildSAH(objs[mid:])
		n.left.parent, n.right.parent = n, n
		n.bounds = n.left.bounds.Union(n.right.bounds)
		return n
	}

	splitPos := axisMin + float64(bestSplit+1)/scale
	left, right := make([]Object, 0, len(objs)/2), make([]Object, 0, len(objs)/2)
	for i, o := range objs {
		if axisComp(centroids[i], axis) < splitPos {
			left = append(left, o)
		} else {
			right = append(right, o)
		}
	}
	if len(left) == 0 || len(right) == 0 {
		mid := len(objs) / 2
		left, right = objs[:mid], objs[mid:]
	}

	n := &node{}
	n.left = t.buildSAH(left)
	n.right = t.buildSAH(right)
	n.left.parent, n.right.parent = n, n
	n.bounds = n.left.bounds.Union(n.right.bounds)
	return n
}

func axisComp(v Vec3, axis int) float64 {
	switch axis {
	case 1:
		return v.Y
	case 2:
		return v.Z
	}
	return v.X
}

// Insert 动态插入物体：按 SAH 代价（插入后包围盒表面积增量最小）
// 选择下降路径，在最佳兄弟节点处分裂，然后自底向上重拟合包围盒。
func (t *BVH) Insert(obj Object) {
	if _, exists := t.leaves[obj]; exists {
		return
	}
	leaf := &node{bounds: obj.Bounds(), obj: obj}
	t.leaves[obj] = leaf

	if t.root == nil {
		t.root = leaf
		return
	}

	// 寻找最佳兄弟节点
	best := t.root
	bestCost := t.root.bounds.Union(leaf.bounds).SurfaceArea()
	type item struct {
		n    *node
		cost float64
	}
	stack := []item{{t.root, bestCost}}
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		cur := it.n
		directCost := cur.bounds.Union(leaf.bounds).SurfaceArea()
		if directCost < bestCost {
			bestCost = directCost
			best = cur
		}
		// 下界估计：若继续下降的代价已不优于当前最优则剪枝
		if directCost-cur.bounds.SurfaceArea()+it.cost >= bestCost {
			continue
		}
		if !cur.isLeaf() {
			childCost := directCost - cur.bounds.SurfaceArea() + it.cost
			stack = append(stack, item{cur.left, childCost}, item{cur.right, childCost})
		}
	}

	// 在 best 处分裂：新建内部节点替代 best 原位置
	oldParent := best.parent
	branch := &node{bounds: best.bounds.Union(leaf.bounds)}
	branch.left, branch.right = best, leaf
	best.parent, leaf.parent = branch, branch
	branch.parent = oldParent
	if oldParent == nil {
		t.root = branch
	} else if oldParent.left == best {
		oldParent.left = branch
	} else {
		oldParent.right = branch
	}

	t.refit(branch.parent)
}

// Remove 删除物体：摘除叶子及其父节点，兄弟节点上提，自底向上重拟合。
func (t *BVH) Remove(obj Object) bool {
	leaf, exists := t.leaves[obj]
	if !exists {
		return false
	}
	delete(t.leaves, obj)

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
	} else if grand.left == parent {
		grand.left = sibling
	} else {
		grand.right = sibling
	}
	t.refit(grand)
	return true
}

// refit 自底向上重算祖先包围盒。
func (t *BVH) refit(n *node) {
	for n != nil {
		n.bounds = n.left.bounds.Union(n.right.bounds)
		n = n.parent
	}
}

// LeafCount 返回叶子（物体）数量。
func (t *BVH) LeafCount() int { return len(t.leaves) }

// RayCast 返回射线击中的最近物体。利用 AABB 相交测试剪枝，
// 并用当前最近命中距离 t 进一步剔除更远的子树。
func (t *BVH) RayCast(r Ray) (obj Object, hitT float64, ok bool) {
	obj, hitT, ok, _ = t.RayCastWithStats(r)
	return
}

// RayCastWithStats 同 RayCast，并返回遍历统计用于剪枝效率分析。
func (t *BVH) RayCastWithStats(r Ray) (obj Object, hitT float64, ok bool, stats Stats) {
	if t.root == nil {
		return nil, 0, false, stats
	}
	best := math.Inf(1)
	var bestObj Object

	type entry struct {
		n    *node
		tmin float64
	}
	stack := make([]entry, 0, 64)
	if _, hit := t.root.bounds.IntersectRay(r, best); hit {
		stack = append(stack, entry{t.root, 0})
	}
	for len(stack) > 0 {
		e := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if e.tmin > best {
			continue // 整个子树比当前最近命中更远
		}
		n := e.n
		if n.isLeaf() {
			stats.LeavesVisited++
			if ht, hit := n.obj.Intersect(r); hit && ht < best {
				best = ht
				bestObj = n.obj
			}
			continue
		}
		stats.NodesVisited++
		// 分别测试两个子节点，近者优先入栈（栈后进先出，故远的先压栈）
		tl, hitL := n.left.bounds.IntersectRay(r, best)
		tr, hitR := n.right.bounds.IntersectRay(r, best)
		if hitL && hitR {
			if tl < tr {
				stack = append(stack, entry{n.right, tr}, entry{n.left, tl})
			} else {
				stack = append(stack, entry{n.left, tl}, entry{n.right, tr})
			}
		} else if hitL {
			stack = append(stack, entry{n.left, tl})
		} else if hitR {
			stack = append(stack, entry{n.right, tr})
		}
	}
	if bestObj == nil {
		return nil, 0, false, stats
	}
	return bestObj, best, true, stats
}

// Validate 校验树结构完整性（包围盒包含性、叶子计数），供测试与调试。
func (t *BVH) Validate() bool {
	if t.root == nil {
		return len(t.leaves) == 0
	}
	leaves := 0
	ok := true
	var walk func(n *node)
	walk = func(n *node) {
		if n.isLeaf() {
			leaves++
			if !n.bounds.Contains(n.obj.Bounds()) {
				ok = false
			}
			return
		}
		u := n.left.bounds.Union(n.right.bounds)
		if !n.bounds.Contains(u) || !u.Contains(n.bounds) {
			ok = false
		}
		walk(n.left)
		walk(n.right)
	}
	walk(t.root)
	return ok && leaves == len(t.leaves)
}
