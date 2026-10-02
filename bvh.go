package stbvh

import "sort"

// DefaultLeafSize 是叶子节点容纳的最大线段数。
const DefaultLeafSize = 4

// sahBins 是 SAH 分桶数量。
const sahBins = 12

type node struct {
	box         Box4
	left, right *node
	start, end  int // 叶子：segments[start:end)（segments 为构建期重排后的切片）
}

// BVH 是四维时空 BVH 索引，叶内线段按构建期重排存储。
type BVH struct {
	root     *node
	segments []Segment
}

// Build 由线段集合构建 4D BVH（自顶向下 + 分桶 SAH 划分）。
func Build(segs []Segment) *BVH {
	b := &BVH{segments: make([]Segment, len(segs))}
	copy(b.segments, segs)
	if len(b.segments) > 0 {
		b.root = b.build(0, len(b.segments))
	}
	return b
}

// BuildTrajectories 便捷入口：由轨迹集合构建索引。
func BuildTrajectories(trajs ...Trajectory) *BVH {
	var segs []Segment
	for i := range trajs {
		segs = append(segs, trajs[i].Segments()...)
	}
	return Build(segs)
}

func (b *BVH) build(start, end int) *node {
	n := &node{box: EmptyBox(), start: start, end: end}
	for i := start; i < end; i++ {
		n.box = n.box.Expand(b.segments[i].box)
	}
	count := end - start
	if count <= DefaultLeafSize {
		return n
	}

	split, ok := bestSplit(b.segments[start:end])
	if !ok {
		return n // 无法有效划分（如所有质心重合），留作叶子
	}
	mid := start + split
	n.left = b.build(start, mid)
	n.right = b.build(mid, end)
	n.start, n.end = 0, 0 // 内部节点不持有线段
	return n
}

// bestSplit 用分桶 SAH 在质心包围盒的最长轴上寻找最优划分，
// 返回排序后划分位置（相对子切片）以及是否可行。
func bestSplit(segs []Segment) (split int, ok bool) {
	cbox := EmptyBox()
	for i := range segs {
		cbox = cbox.ExpandPoint(segs[i].box.Centroid())
	}
	axis := cbox.WidestAxis()
	extent := cbox.Extent(axis)
	if extent <= 0 {
		return 0, false
	}

	type bin struct {
		box   Box4
		count int
	}
	var bins [sahBins]bin
	for i := range bins {
		bins[i].box = EmptyBox()
	}
	inv := float64(sahBins) / extent
	binOf := func(s *Segment) int {
		idx := int((s.box.Centroid()[axis] - cbox.Min[axis]) * inv)
		if idx >= sahBins {
			idx = sahBins - 1
		}
		return idx
	}
	for i := range segs {
		bi := binOf(&segs[i])
		bins[bi].box = bins[bi].box.Expand(segs[i].box)
		bins[bi].count++
	}

	// 扫描所有桶边界，取 SAH 代价最小者。
	bestCost, bestBin := -1.0, -1
	leftBox, leftCount := EmptyBox(), 0
	rightBox := make([]Box4, sahBins-1)
	rightCount := make([]int, sahBins-1)
	rb, rc := EmptyBox(), 0
	for i := sahBins - 1; i >= 1; i-- {
		rb = rb.Expand(bins[i].box)
		rc += bins[i].count
		rightBox[i-1], rightCount[i-1] = rb, rc
	}
	for i := 0; i < sahBins-1; i++ {
		leftBox = leftBox.Expand(bins[i].box)
		leftCount += bins[i].count
		if leftCount == 0 || rightCount[i] == 0 {
			continue
		}
		cost := float64(leftCount)*leftBox.halfSurface() +
			float64(rightCount[i])*rightBox[i].halfSurface()
		if bestBin < 0 || cost < bestCost {
			bestCost, bestBin = cost, i
		}
	}
	if bestBin < 0 {
		return 0, false
	}

	// 按桶序稳定划分：桶号 <= bestBin 的放左边。
	sort.SliceStable(segs, func(i, j int) bool {
		return binOf(&segs[i]) < binOf(&segs[j])
	})
	split = 0
	for split < len(segs) && binOf(&segs[split]) <= bestBin {
		split++
	}
	if split == 0 || split == len(segs) {
		return 0, false
	}
	return split, true
}

// Query 返回与 4D 查询盒相交的所有线段命中（含按查询时间窗裁剪后的片段）。
// 遍历过程中用节点包围盒做 4D 剪枝。
func (b *BVH) Query(q Box4) []Hit {
	var hits []Hit
	if b.root == nil {
		return hits
	}
	t1, t2 := q.Min[DimT], q.Max[DimT]
	// 迭代式遍历，避免递归开销。
	stack := []*node{b.root}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if !n.box.Intersects(q) {
			continue // 4D 剪枝
		}
		if n.left == nil {
			for i := n.start; i < n.end; i++ {
				s := &b.segments[i]
				if !s.box.Intersects(q) {
					continue
				}
				c0, c1 := s.clipToTimeWindow(t1, t2)
				hits = append(hits, Hit{Segment: *s, Clip0: c0, Clip1: c1})
			}
			continue
		}
		stack = append(stack, n.left, n.right)
	}
	return hits
}

// QueryRegion 便捷查询：空间立方体 [minX,maxX]x[minY,maxY]x[minZ,maxZ]
// 与时间段 [t1,t2]（Unix 秒）构成的 4D 盒。
func (b *BVH) QueryRegion(minX, maxX, minY, maxY, minZ, maxZ, t1, t2 float64) []Hit {
	return b.Query(Box4{
		Min: [NDim]float64{minX, minY, minZ, t1},
		Max: [NDim]float64{maxX, maxY, maxZ, t2},
	})
}

// Len 返回索引中的线段总数。
func (b *BVH) Len() int { return len(b.segments) }
