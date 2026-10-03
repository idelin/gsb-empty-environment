// Package rtree 实现二维 R-Tree 空间索引。
//
// 支持地理矩形（商铺外包矩形 / 经纬度点）的插入与范围窗口查询。
// 节点溢出时优先采用重插入（Re-Insert）策略，同一层在一次插入中
// 已经重插入过时退化为二次分裂（Quadratic Split），
// 以优化节点覆盖面积与重叠度，遏制树退化。
package rtree

import (
	"math"
	"sort"
)

const (
	// DefaultMaxEntries 节点默认最大条目数（页容量）。
	DefaultMaxEntries = 8
	// DefaultMinEntries 节点默认最小条目数（填充下界，约 40%）。
	DefaultMinEntries = 3
	// reinsertFraction 溢出时重插入的条目比例（经典论文取 30%）。
	reinsertFraction = 0.3
)

// Point 表示二维坐标（经度 X / 纬度 Y）。
type Point struct {
	X, Y float64
}

// Rect 表示轴对齐外包矩形（Bounding Box）。
type Rect struct {
	Min, Max Point
}

// NewRect 由两个对角点构造规范化矩形。
func NewRect(a, b Point) Rect {
	return Rect{
		Min: Point{math.Min(a.X, b.X), math.Min(a.Y, b.Y)},
		Max: Point{math.Max(a.X, b.X), math.Max(a.Y, b.Y)},
	}
}

// PointRect 由单点构造零面积矩形（用于商铺经纬度点）。
func PointRect(p Point) Rect { return Rect{Min: p, Max: p} }

// Area 返回矩形面积。
func (r Rect) Area() float64 {
	return (r.Max.X - r.Min.X) * (r.Max.Y - r.Min.Y)
}

// Margin 返回矩形周长（分裂时用于最小化边界周长）。
func (r Rect) Margin() float64 {
	return 2 * ((r.Max.X - r.Min.X) + (r.Max.Y - r.Min.Y))
}

// Union 返回能同时容纳 r 与 o 的最小矩形。
func (r Rect) Union(o Rect) Rect {
	return Rect{
		Min: Point{math.Min(r.Min.X, o.Min.X), math.Min(r.Min.Y, o.Min.Y)},
		Max: Point{math.Max(r.Max.X, o.Max.X), math.Max(r.Max.Y, o.Max.Y)},
	}
}

// Intersects 判断两个矩形是否相交（含边界接触）。
func (r Rect) Intersects(o Rect) bool {
	return r.Min.X <= o.Max.X && o.Min.X <= r.Max.X &&
		r.Min.Y <= o.Max.Y && o.Min.Y <= r.Max.Y
}

// Contains 判断 r 是否完整包含 o。
func (r Rect) Contains(o Rect) bool {
	return r.Min.X <= o.Min.X && r.Min.Y <= o.Min.Y &&
		r.Max.X >= o.Max.X && r.Max.Y >= o.Max.Y
}

// enlargement 返回为容纳 o 需要增加的面积。
func (r Rect) enlargement(o Rect) float64 {
	return r.Union(o).Area() - r.Area()
}

// overlapArea 返回 r 与 o 的重叠面积。
func (r Rect) overlapArea(o Rect) float64 {
	dx := math.Min(r.Max.X, o.Max.X) - math.Max(r.Min.X, o.Min.X)
	dy := math.Min(r.Max.Y, o.Max.Y) - math.Max(r.Min.Y, o.Min.Y)
	if dx <= 0 || dy <= 0 {
		return 0
	}
	return dx * dy
}

// center 返回矩形中心点。
func (r Rect) center() Point {
	return Point{(r.Min.X + r.Max.X) / 2, (r.Min.Y + r.Max.Y) / 2}
}

// distSq 返回两点距离的平方。
func distSq(a, b Point) float64 {
	dx, dy := a.X-b.X, a.Y-b.Y
	return dx*dx + dy*dy
}

// entry 是节点中的一个条目：内部节点指向子节点，叶子节点携带数据。
type entry struct {
	bbox  Rect
	child *node // 内部节点条目使用
	id    int   // 叶子条目：数据标识
	rect  Rect  // 叶子条目：数据外包矩形
}

// node 是 R-Tree 的节点。level 自底向上计数：叶子为 1，逐层递增。
type node struct {
	leaf    bool
	level   int
	entries []entry
	parent  *node
}

// bbox 计算节点所有条目的外包矩形。
func (n *node) bbox() Rect {
	b := n.entries[0].bbox
	for _, e := range n.entries[1:] {
		b = b.Union(e.bbox)
	}
	return b
}

// RTree 是二维 R-Tree 空间索引。
type RTree struct {
	root       *node
	maxEntries int
	minEntries int
	size       int
	height     int
	// disableReinsert 仅供测试对照：置 true 时溢出直接分裂。
	disableReinsert bool
}

// New 创建一棵空 R-Tree。maxEntries 为节点容量（建议 4~50），
// minEntries 为最小填充数；传入 <=0 时使用默认值。
func New(maxEntries, minEntries int) *RTree {
	if maxEntries <= 0 {
		maxEntries = DefaultMaxEntries
	}
	if minEntries <= 0 || minEntries > maxEntries/2 {
		minEntries = DefaultMinEntries
		if minEntries > maxEntries/2 {
			minEntries = maxEntries / 2
		}
	}
	return &RTree{
		root:       &node{leaf: true, level: 1},
		maxEntries: maxEntries,
		minEntries: minEntries,
		height:     1,
	}
}

// Size 返回索引中的数据条目数。
func (t *RTree) Size() int { return t.size }

// Height 返回树高（根为第 1 层）。
func (t *RTree) Height() int { return t.height }

// Insert 将一个外包矩形及其标识插入索引。
// id 由调用方分配（如商铺编号），rect 为其外包矩形或经纬度点矩形。
func (t *RTree) Insert(id int, rect Rect) {
	e := entry{bbox: rect, id: id, rect: rect}
	reinserted := make(map[int]bool)
	t.insert(e, 1, reinserted)
	t.size++
}

// insert 将条目插入到指定层（level=1 为叶子层），并处理溢出传播。
func (t *RTree) insert(e entry, level int, reinserted map[int]bool) {
	n := t.chooseLeaf(t.root, e.bbox, level)
	n.entries = append(n.entries, e)
	if e.child != nil {
		e.child.parent = n
	}
	if len(n.entries) > t.maxEntries {
		t.overflowTreatment(n, level, reinserted)
	}
	// AdjustTree：沿父链刷新祖先条目的外包矩形。
	t.adjustUp(n)
}

// adjustUp 自节点 n 向上，逐层把父条目 bbox 重算为子节点的实际外包矩形。
func (t *RTree) adjustUp(n *node) {
	for n.parent != nil {
		p := n.parent
		for i := range p.entries {
			if p.entries[i].child == n {
				p.entries[i].bbox = n.bbox()
				break
			}
		}
		n = p
	}
}

// chooseLeaf 自根向下选择目标层（level，叶子为 1）的节点：
// 每层挑选面积扩张最小（平局取面积最小）的子树。
// 依据节点自身记录的层级判断终止，根分裂长高后依然正确。
func (t *RTree) chooseLeaf(n *node, target Rect, level int) *node {
	if n.level <= level {
		return n
	}
	best := -1
	bestEnlarge := math.Inf(1)
	bestArea := math.Inf(1)
	for i, c := range n.entries {
		enl := c.bbox.enlargement(target)
		area := c.bbox.Area()
		if enl < bestEnlarge || (enl == bestEnlarge && area < bestArea) {
			best, bestEnlarge, bestArea = i, enl, area
		}
	}
	return t.chooseLeaf(n.entries[best].child, target, level)
}

// overflowTreatment 处理节点溢出：本层在本次插入中首次溢出时
// 执行重插入，否则执行二次分裂。
func (t *RTree) overflowTreatment(n *node, level int, reinserted map[int]bool) {
	if !t.disableReinsert && !reinserted[level] && n != t.root {
		reinserted[level] = true
		t.reinsert(n, level, reinserted)
		return
	}
	t.quadraticSplit(n, level, reinserted)
}

// reinsert 移除离节点中心最远的 p 个条目并重新插入，
// 促使条目在兄弟节点间重新分布，减少重叠、延缓分裂。
func (t *RTree) reinsert(n *node, level int, reinserted map[int]bool) {
	center := n.bbox().center()
	p := int(math.Ceil(float64(t.maxEntries+1) * reinsertFraction))

	idx := make([]int, len(n.entries))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool {
		return distSq(n.entries[idx[a]].bbox.center(), center) >
			distSq(n.entries[idx[b]].bbox.center(), center)
	})

	removeSet := make(map[int]bool, p)
	for _, i := range idx[:p] {
		removeSet[i] = true
	}
	removed := make([]entry, 0, p)
	kept := n.entries[:0]
	for i, e := range n.entries {
		if removeSet[i] {
			removed = append(removed, e)
		} else {
			kept = append(kept, e)
		}
	}
	n.entries = kept
	// 节点已收缩，先向上刷新祖先 bbox，再重插被移除的条目。
	t.adjustUp(n)

	// 重插入按离中心从近到远的顺序（经典论文做法），
	// 使远端条目最后插入、更可能落到合适的兄弟节点。
	for i := len(removed) - 1; i >= 0; i-- {
		t.insert(removed[i], level, reinserted)
	}
}

// quadraticSplit 将溢出节点按二次算法分裂为两个节点，
// 最小化两组外包矩形的总覆盖面积，并向上传播。
func (t *RTree) quadraticSplit(n *node, level int, reinserted map[int]bool) {
	groupA, groupB := t.splitEntries(n.entries)

	n.entries = groupA
	sibling := &node{leaf: n.leaf, level: n.level, entries: groupB, parent: n.parent}
	for _, e := range groupB {
		if e.child != nil {
			e.child.parent = sibling
		}
	}

	if n == t.root {
		// 根分裂：树长高一层。
		newRoot := &node{
			leaf:  false,
			level: n.level + 1,
			entries: []entry{
				{bbox: n.bbox(), child: n},
				{bbox: sibling.bbox(), child: sibling},
			},
		}
		n.parent = newRoot
		sibling.parent = newRoot
		t.root = newRoot
		t.height++
		return
	}

	parent := n.parent
	parent.entries = append(parent.entries, entry{bbox: sibling.bbox(), child: sibling})
	if len(parent.entries) > t.maxEntries {
		t.overflowTreatment(parent, level+1, reinserted)
	}
}

// splitEntries 二次分裂：先挑选“最浪费空间”的一对种子，
// 再逐条分配剩余条目，保证每组至少 minEntries 个条目。
func (t *RTree) splitEntries(entries []entry) ([]entry, []entry) {
	// PickSeeds：选择组合后死空间最大的一对作为两组的种子。
	var s1, s2 int
	worst := math.Inf(-1)
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			d := entries[i].bbox.Union(entries[j].bbox).Area() -
				entries[i].bbox.Area() - entries[j].bbox.Area()
			if d > worst {
				worst, s1, s2 = d, i, j
			}
		}
	}

	groupA := []entry{entries[s1]}
	groupB := []entry{entries[s2]}
	bboxA := entries[s1].bbox
	bboxB := entries[s2].bbox

	rest := make([]entry, 0, len(entries)-2)
	for i, e := range entries {
		if i != s1 && i != s2 {
			rest = append(rest, e)
		}
	}

	// PickNext：每次挑选对两组偏好差异最大的条目分配。
	for len(rest) > 0 {
		// 若某组再分就会低于最小填充，则把剩余全部给它。
		if len(groupA)+len(rest) == t.minEntries {
			for _, e := range rest {
				groupA = append(groupA, e)
				bboxA = bboxA.Union(e.bbox)
			}
			break
		}
		if len(groupB)+len(rest) == t.minEntries {
			for _, e := range rest {
				groupB = append(groupB, e)
				bboxB = bboxB.Union(e.bbox)
			}
			break
		}

		bestIdx := -1
		bestDiff := math.Inf(-1)
		for i, e := range rest {
			dA := bboxA.enlargement(e.bbox)
			dB := bboxB.enlargement(e.bbox)
			if diff := math.Abs(dA - dB); diff > bestDiff {
				bestDiff, bestIdx = diff, i
			}
		}
		e := rest[bestIdx]
		rest = append(rest[:bestIdx], rest[bestIdx+1:]...)

		dA := bboxA.enlargement(e.bbox)
		dB := bboxB.enlargement(e.bbox)
		switch {
		case dA < dB:
			groupA = append(groupA, e)
			bboxA = bboxA.Union(e.bbox)
		case dB < dA:
			groupB = append(groupB, e)
			bboxB = bboxB.Union(e.bbox)
		case bboxA.Area() < bboxB.Area():
			groupA = append(groupA, e)
			bboxA = bboxA.Union(e.bbox)
		case bboxB.Area() < bboxA.Area():
			groupB = append(groupB, e)
			bboxB = bboxB.Union(e.bbox)
		case len(groupA) <= len(groupB):
			groupA = append(groupA, e)
			bboxA = bboxA.Union(e.bbox)
		default:
			groupB = append(groupB, e)
			bboxB = bboxB.Union(e.bbox)
		}
	}
	return groupA, groupB
}

// Search 返回所有与查询窗口 window 相交的数据条目 id。
// 通过比较子树外包矩形与窗口的相交关系快速剪枝无关分支。
func (t *RTree) Search(window Rect) []int {
	var out []int
	t.search(t.root, window, &out)
	return out
}

func (t *RTree) search(n *node, window Rect, out *[]int) {
	for _, e := range n.entries {
		if !e.bbox.Intersects(window) {
			continue // 剪枝：该子树/条目与窗口无关
		}
		if n.leaf {
			*out = append(*out, e.id)
		} else {
			t.search(e.child, window, out)
		}
	}
}

// VisitEntries 遍历所有数据条目（用于调试与校验）。
func (t *RTree) VisitEntries(fn func(id int, rect Rect)) {
	var walk func(n *node)
	walk = func(n *node) {
		for _, e := range n.entries {
			if n.leaf {
				fn(e.id, e.rect)
			} else {
				walk(e.child)
			}
		}
	}
	walk(t.root)
}
