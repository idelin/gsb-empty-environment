package bvhtrace

import "math"

const (
	sahBins        = 12
	leafPrimitives = 4
	sahTravelCost  = 1.0
	sahHitCost     = 1.0
	maxTreeDepth   = 64

	// stackCapacity bounds the worst-case depth of the traversal stack. The
	// builder hard-caps depth at maxTreeDepth and each level contributes at
	// most one entry, so this cannot overflow.
	stackCapacity = maxTreeDepth + 1
)

// node is a compact BVH node. A node is a leaf when Left < 0: in that case
// First is the start index into BVH.indices and Count is the number of
// triangles in the leaf. Internal nodes use Left and Right as node indices.
type node struct {
	box          AABB
	left, right  int
	first, count int
}

// BVH is a binary bounding volume hierarchy built with surface-area
// heuristics binning. It is safe for concurrent queries: Traverse, Occluded
// and the brute-force helpers only read the structure.
type BVH struct {
	tris     []Triangle
	triBoxes []AABB
	centers  []Vec3
	indices  []int
	nodes    []node
	root     int
}

type sahBin struct {
	box AABB
	n   int
}

// Build constructs a BVH over the given triangles in O(n log n) expected time.
// The slice is retained by reference and must not be mutated afterwards.
func Build(tris []Triangle) *BVH {
	bvh := &BVH{
		tris:     tris,
		triBoxes: make([]AABB, len(tris)),
		centers:  make([]Vec3, len(tris)),
		indices:  make([]int, len(tris)),
	}
	for i := range tris {
		bvh.indices[i] = i
		bvh.triBoxes[i] = triangleBounds(&tris[i])
		bvh.centers[i] = bvh.triBoxes[i].center()
	}
	if len(tris) == 0 {
		bvh.root = -1
		return bvh
	}
	bvh.root = bvh.buildRecursive(bvh.indices, 0, 0)
	return bvh
}

func (b *BVH) buildRecursive(ids []int, start, depth int) int {
	box := emptyAABB()
	centroidBox := emptyAABB()
	for _, id := range ids {
		triBox := b.triBoxes[id]
		box = box.extend(triBox)
		centroidBox = centroidBox.extend(pointBounds(b.centers[id]))
	}

	idx := len(b.nodes)
	b.nodes = append(b.nodes, node{box: box})

	if len(ids) <= leafPrimitives || depth >= maxTreeDepth {
		b.makeLeaf(idx, start, len(ids))
		return idx
	}

	axis, splitBin, splitCost := b.findSAHSplit(ids, box, centroidBox)
	leafCost := sahHitCost * float32(len(ids))
	if splitCost >= leafCost {
		b.makeLeaf(idx, start, len(ids))
		return idx
	}

	mid := partition(ids, func(id int) bool {
		c := b.centers[id].component(axis)
		return binIndex(c, centroidBox, axis) <= splitBin
	})
	if mid == 0 || mid == len(ids) {
		b.makeLeaf(idx, start, len(ids))
		return idx
	}

	left := b.buildRecursive(ids[:mid], start, depth+1)
	right := b.buildRecursive(ids[mid:], start+mid, depth+1)
	b.nodes[idx].left = left
	b.nodes[idx].right = right
	return idx
}

func (b *BVH) makeLeaf(idx, start, count int) {
	b.nodes[idx].left = -1
	b.nodes[idx].first = start
	b.nodes[idx].count = count
}

func (b *BVH) findSAHSplit(ids []int, box, centroidBox AABB) (axis, splitBin int, bestCost float32) {
	bestCost = float32(math.MaxFloat32)
	axis = 0
	splitBin = 0

	for candidate := 0; candidate < 3; candidate++ {
		if centroidBox.Min.component(candidate) == centroidBox.Max.component(candidate) {
			continue
		}
		var bins [sahBins]sahBin
		for i := range bins {
			bins[i].box = emptyAABB()
		}
		for _, id := range ids {
			triBox := b.triBoxes[id]
			c := b.centers[id].component(candidate)
			bin := binIndex(c, centroidBox, candidate)
			bins[bin].n++
			bins[bin].box = bins[bin].box.extend(triBox)
		}

		var leftCount [sahBins]int
		var leftBox [sahBins]AABB
		var rightCount [sahBins]int
		var rightBox [sahBins]AABB
		acc := emptyAABB()
		count := 0
		for i := 0; i < sahBins; i++ {
			acc = acc.extend(bins[i].box)
			count += bins[i].n
			leftCount[i] = count
			leftBox[i] = acc
		}
		acc = emptyAABB()
		count = 0
		for i := sahBins - 1; i >= 0; i-- {
			acc = acc.extend(bins[i].box)
			count += bins[i].n
			rightCount[i] = count
			rightBox[i] = acc
		}

		totalArea := box.surfaceArea()
		for i := 0; i < sahBins-1; i++ {
			if leftCount[i] == 0 || rightCount[i+1] == 0 {
				continue
			}
			cost := sahTravelCost + sahHitCost*
				(leftBox[i].surfaceArea()*float32(leftCount[i])+
					rightBox[i+1].surfaceArea()*float32(rightCount[i+1]))/totalArea
			if cost < bestCost {
				bestCost = cost
				axis = candidate
				splitBin = i
			}
		}
	}
	return axis, splitBin, bestCost
}

func binIndex(centroid float32, centroidBox AABB, axis int) int {
	lo := centroidBox.Min.component(axis)
	hi := centroidBox.Max.component(axis)
	bin := int(float32(sahBins) * (centroid - lo) / (hi - lo))
	if bin < 0 {
		return 0
	}
	if bin >= sahBins {
		return sahBins - 1
	}
	return bin
}

func partition(ids []int, left func(int) bool) int {
	i, j := 0, len(ids)-1
	for i <= j {
		if left(ids[i]) {
			i++
			continue
		}
		if !left(ids[j]) {
			j--
			continue
		}
		ids[i], ids[j] = ids[j], ids[i]
		i++
		j--
	}
	return i
}

func triangleBounds(tri *Triangle) AABB {
	return AABB{
		Min: Vec3{
			min3(tri.A.X, tri.B.X, tri.C.X),
			min3(tri.A.Y, tri.B.Y, tri.C.Y),
			min3(tri.A.Z, tri.B.Z, tri.C.Z),
		},
		Max: Vec3{
			max3(tri.A.X, tri.B.X, tri.C.X),
			max3(tri.A.Y, tri.B.Y, tri.C.Y),
			max3(tri.A.Z, tri.B.Z, tri.C.Z),
		},
	}
}

func pointBounds(p Vec3) AABB {
	return AABB{Min: p, Max: p}
}

// Traverse returns the closest hit along r, or ok == false when the ray
// escapes the mesh. Child nodes are visited near-to-far and the ray interval
// shrinks at every hit, so fully occluded subtrees are skipped.
func (b *BVH) Traverse(r Ray) (hit Hit, ok bool) {
	if b.root < 0 {
		return Hit{}, false
	}
	var stack [stackCapacity]int
	sp := 0
	stack[sp] = b.root
	sp++

	for sp > 0 {
		sp--
		n := &b.nodes[stack[sp]]
		if n.left < 0 {
			if _, _, boxHit := n.box.intersect(&r); !boxHit {
				continue
			}
			end := n.first + n.count
			for slot := n.first; slot < end; slot++ {
				primID := b.indices[slot]
				t, wa, wb, wc, triHit := intersectTriangle(&r, &b.tris[primID])
				if triHit && t < r.Tmax {
					r.Tmax = t
					hit = Hit{T: t, Primitive: primID, WA: wa, WB: wb, WC: wc}
					ok = true
				}
			}
			continue
		}

		b.pushChildren(&stack, &sp, n, &r)
	}
	return hit, ok
}

// pushChildren tests both child boxes exactly once and pushes the farther
// child below the nearer one, so the LIFO stack visits them near-to-far.
// Missed children are simply omitted.
func (b *BVH) pushChildren(stack *[stackCapacity]int, sp *int, n *node, r *Ray) {
	leftNear, _, leftHit := b.nodes[n.left].box.intersect(r)
	rightNear, _, rightHit := b.nodes[n.right].box.intersect(r)
	if !leftHit && !rightHit {
		return
	}
	if !leftHit {
		stack[*sp] = n.right
		*sp++
		return
	}
	if !rightHit {
		stack[*sp] = n.left
		*sp++
		return
	}
	if leftNear <= rightNear {
		stack[*sp] = n.right
		*sp++
		stack[*sp] = n.left
		*sp++
	} else {
		stack[*sp] = n.left
		*sp++
		stack[*sp] = n.right
		*sp++
	}
}

// Occluded is a shadow/any-hit query: it reports whether any triangle blocks
// r and terminates immediately at the first blocker instead of searching for
// the closest one.
func (b *BVH) Occluded(r Ray) bool {
	if b.root < 0 {
		return false
	}
	var stack [stackCapacity]int
	sp := 0
	stack[sp] = b.root
	sp++

	for sp > 0 {
		sp--
		n := &b.nodes[stack[sp]]
		if n.left < 0 {
			if _, _, boxHit := n.box.intersect(&r); !boxHit {
				continue
			}
			end := n.first + n.count
			for slot := n.first; slot < end; slot++ {
				primID := b.indices[slot]
				if _, _, _, _, triHit := intersectTriangle(&r, &b.tris[primID]); triHit {
					return true
				}
			}
			continue
		}

		b.pushChildren(&stack, &sp, n, &r)
	}
	return false
}

// BruteForceTraverse tests every triangle without acceleration and is used to
// validate BVH results. It applies the same ray interval and watertight
// intersection routine, so it must return the same closest hit.
func (b *BVH) BruteForceTraverse(r Ray) (hit Hit, ok bool) {
	for primID := range b.tris {
		t, wa, wb, wc, triHit := intersectTriangle(&r, &b.tris[primID])
		if triHit && t < r.Tmax {
			r.Tmax = t
			hit = Hit{T: t, Primitive: primID, WA: wa, WB: wb, WC: wc}
			ok = true
		}
	}
	return hit, ok
}

// BruteForceOccluded is the unaccelerated counterpart of Occluded.
func (b *BVH) BruteForceOccluded(r Ray) bool {
	for primID := range b.tris {
		if _, _, _, _, triHit := intersectTriangle(&r, &b.tris[primID]); triHit {
			return true
		}
	}
	return false
}

// Len reports the number of triangles covered by the structure.
func (b *BVH) Len() int { return len(b.tris) }

// NodeCount reports the number of allocated BVH nodes.
func (b *BVH) NodeCount() int { return len(b.nodes) }
