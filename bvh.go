package taskpkg

import "sort"

const bvhLeafSize = 4

// bvhNode is a compact binary BVH node. A negative left index denotes a leaf,
// whose primitive range is [start, start+count) in bvh.prims.
type bvhNode struct {
	bounds AABB
	left   int32
	right  int32
	start  int32
	count  int32
}

func (n bvhNode) isLeaf() bool {
	return n.left < 0
}

// bvh stores a binary hierarchy and the original primitive indices in leaf
// order. Nodes are laid out parent-first, so a reverse scan is a valid
// bottom-up refit.
type bvh struct {
	nodes []bvhNode
	prims []uint32
}

func buildBVH(primBounds []AABB, leafSize int) bvh {
	n := len(primBounds)
	if n == 0 {
		return bvh{}
	}
	if leafSize < 1 {
		leafSize = 1
	}

	centers := make([]Vec3, n)
	for i := range primBounds {
		centers[i] = primBounds[i].Center()
	}

	prims := make([]uint32, n)
	for i := range prims {
		prims[i] = uint32(i)
	}
	nodes := make([]bvhNode, 0, 2*n)

	var build func(start, end int) int32
	build = func(start, end int) int32 {
		nodeIndex := int32(len(nodes))
		nodes = append(nodes, bvhNode{left: -1, right: -1})

		bounds := EmptyAABB()
		centroidBounds := EmptyAABB()
		for i := start; i < end; i++ {
			prim := prims[i]
			bounds = bounds.Union(primBounds[prim])
			centroidBounds = centroidBounds.IncludePoint(centers[prim])
		}
		nodes[nodeIndex].bounds = bounds.ExpandedOutward()

		count := end - start
		if count <= leafSize {
			nodes[nodeIndex].start = int32(start)
			nodes[nodeIndex].count = int32(count)
			return nodeIndex
		}

		extent := centroidBounds.Extent()
		axis := 0
		if extent.Y > extent.X {
			axis = 1
		}
		if extent.Z > extent.Y && extent.Z > extent.X {
			axis = 2
		}
		axisExtent := extent.X
		switch axis {
		case 1:
			axisExtent = extent.Y
		case 2:
			axisExtent = extent.Z
		}
		if axisExtent <= 0 || !isFinite(axisExtent) {
			nodes[nodeIndex].start = int32(start)
			nodes[nodeIndex].count = int32(count)
			return nodeIndex
		}

		less := func(a, b uint32) bool {
			var ca, cb float64
			switch axis {
			case 0:
				ca, cb = centers[a].X, centers[b].X
			case 1:
				ca, cb = centers[a].Y, centers[b].Y
			default:
				ca, cb = centers[a].Z, centers[b].Z
			}
			if ca != cb {
				return ca < cb
			}
			return a < b
		}
		sort.Slice(prims[start:end], func(i, j int) bool {
			return less(prims[start+i], prims[start+j])
		})

		mid := (start + end) / 2
		left := build(start, mid)
		right := build(mid, end)
		nodes[nodeIndex].left = left
		nodes[nodeIndex].right = right
		nodes[nodeIndex].bounds = nodes[left].bounds.Union(nodes[right].bounds)
		return nodeIndex
	}
	build(0, n)
	return bvh{nodes: nodes, prims: prims}
}

// refitBVH recomputes every node's bounds from current leaf bounds. The
// topology and primitive order stay unchanged, which is what makes instance
// transform updates cheap without touching a shared mesh BVH.
func refitBVH(tree *bvh, leafBounds func(prim uint32) AABB) {
	for i := len(tree.nodes) - 1; i >= 0; i-- {
		n := &tree.nodes[i]
		if n.isLeaf() {
			bounds := EmptyAABB()
			start := int(n.start)
			end := start + int(n.count)
			for k := start; k < end; k++ {
				bounds = bounds.Union(leafBounds(tree.prims[k]))
			}
			n.bounds = bounds.ExpandedOutward()
			continue
		}
		n.bounds = tree.nodes[n.left].bounds.Union(tree.nodes[n.right].bounds)
	}
}
