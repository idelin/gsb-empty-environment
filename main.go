package main

import (
	"fmt"
	"sort"
)

// Rect 为半开区间 [X1,X2) x [Y1,Y2) 的轴对齐矩形。
// 半开语义保证共边矩形不重复计面积。
type Rect struct {
	X1, Y1, X2, Y2 int64
}

func (r Rect) Area() int64 { return (r.X2 - r.X1) * (r.Y2 - r.Y1) }

func (r Rect) Empty() bool { return r.X1 >= r.X2 || r.Y1 >= r.Y2 }

func (r Rect) Contains(x, y int64) bool {
	return x >= r.X1 && x < r.X2 && y >= r.Y1 && y < r.Y2
}

func intersect(a, b Rect) Rect {
	r := Rect{max64(a.X1, b.X1), max64(a.Y1, b.Y1), min64(a.X2, b.X2), min64(a.Y2, b.Y2)}
	if r.Empty() {
		return Rect{}
	}
	return r
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// subtract 返回 a \ b，结果为至多 4 个互不重叠的矩形。
func subtract(a, b Rect) []Rect {
	is := intersect(a, b)
	if is.Empty() {
		return []Rect{a}
	}
	var out []Rect
	if a.X1 < is.X1 { // 左条
		out = append(out, Rect{a.X1, a.Y1, is.X1, a.Y2})
	}
	if is.X2 < a.X2 { // 右条
		out = append(out, Rect{is.X2, a.Y1, a.X2, a.Y2})
	}
	if a.Y1 < is.Y1 { // 下条（仅限中间列）
		out = append(out, Rect{is.X1, a.Y1, is.X2, is.Y1})
	}
	if is.Y2 < a.Y2 { // 上条（仅限中间列）
		out = append(out, Rect{is.X1, is.Y2, is.X2, a.Y2})
	}
	return out
}

// subtractMany 计算 region \ (cutters 的并集)，结果矩形互不重叠。
func subtractMany(region, cutters []Rect) []Rect {
	out := region
	for _, c := range cutters {
		var next []Rect
		for _, r := range out {
			next = append(next, subtract(r, c)...)
		}
		out = next
		if len(out) == 0 {
			break
		}
	}
	return out
}

func totalArea(rs []Rect) int64 {
	var s int64
	for _, r := range rs {
		s += r.Area()
	}
	return s
}

// Layer 为一个图层：外矩形减去若干透明孔洞。
type Layer struct {
	Outer Rect
	Holes []Rect
}

// solidRegion 返回图层的实体（非透明）区域，矩形互不重叠。
func solidRegion(l Layer) []Rect {
	var holes []Rect
	for _, h := range l.Holes {
		h = intersect(h, l.Outer) // 孔洞裁剪到外框内
		if !h.Empty() {
			holes = append(holes, h)
		}
	}
	return subtractMany([]Rect{l.Outer}, holes)
}

// Engine 保存各图层实体区域与最终可见区域。
type Engine struct {
	layers  []Layer
	solids  [][]Rect // 每层实体区域
	visible [][]Rect // 每层最终可见区域（全部互不相交）
}

// Compute 自顶向下计算每层可见区域：
// 可见 = 实体 \ (所有更高层实体的并集)。
func Compute(layers []Layer) *Engine {
	e := &Engine{layers: layers, solids: make([][]Rect, len(layers)), visible: make([][]Rect, len(layers))}
	var covered []Rect // 高于当前层的实体并集（允许内部重叠，减法不受影响）
	for i := len(layers) - 1; i >= 0; i-- {
		solid := solidRegion(layers[i])
		e.solids[i] = solid
		e.visible[i] = subtractMany(solid, covered)
		covered = append(covered, solid...)
	}
	return e
}

// VisibleArea 返回每层最终可见面积。
func (e *Engine) VisibleArea() []int64 {
	out := make([]int64, len(e.layers))
	for i := range e.layers {
		out[i] = totalArea(e.visible[i])
	}
	return out
}

// Topmost 返回点 (x,y) 处最上层可见图层编号，无覆盖返回 -1。
func (e *Engine) Topmost(x, y int64) int {
	for i := len(e.layers) - 1; i >= 0; i-- {
		for _, r := range e.solids[i] {
			if r.Contains(x, y) {
				return i
			}
		}
	}
	return -1
}

// unionAreaCompressed 用坐标压缩独立计算矩形并集面积（O(n^2) 格子，非像素化），
// 用于面积守恒校验。
func unionAreaCompressed(rects []Rect) int64 {
	xsSet := map[int64]bool{}
	ysSet := map[int64]bool{}
	for _, r := range rects {
		xsSet[r.X1], xsSet[r.X2] = true, true
		ysSet[r.Y1], ysSet[r.Y2] = true, true
	}
	var xs, ys []int64
	for x := range xsSet {
		xs = append(xs, x)
	}
	for y := range ysSet {
		ys = append(ys, y)
	}
	sort.Slice(xs, func(i, j int) bool { return xs[i] < xs[j] })
	sort.Slice(ys, func(i, j int) bool { return ys[i] < ys[j] })
	xIdx := map[int64]int{}
	yIdx := map[int64]int{}
	for i, x := range xs {
		xIdx[x] = i
	}
	for i, y := range ys {
		yIdx[y] = i
	}
	covered := make([][]bool, len(xs))
	for i := range covered {
		covered[i] = make([]bool, len(ys))
	}
	for _, r := range rects {
		for i := xIdx[r.X1]; i < xIdx[r.X2]; i++ {
			for j := yIdx[r.Y1]; j < yIdx[r.Y2]; j++ {
				covered[i][j] = true
			}
		}
	}
	var area int64
	for i := 0; i+1 < len(xs); i++ {
		for j := 0; j+1 < len(ys); j++ {
			if covered[i][j] {
				area += (xs[i+1] - xs[i]) * (ys[j+1] - ys[j])
			}
		}
	}
	return area
}

func main() {
	// 场景覆盖：共边、完全遮挡、孔洞重叠。
	layers := []Layer{
		// L0 底层：10x10，孔洞 (2,2)-(4,4)
		{Outer: Rect{0, 0, 10, 10}, Holes: []Rect{{2, 2, 4, 4}}},
		// L1 中层：与 L0 共边 x=10，无重叠
		{Outer: Rect{10, 0, 20, 10}},
		// L2 顶层：外框与 L0 完全相同（几乎完全遮挡），
		// 孔洞 (3,3)-(5,5) 与 L0 孔洞部分重叠
		{Outer: Rect{0, 0, 10, 10}, Holes: []Rect{{3, 3, 5, 5}}},
	}

	e := Compute(layers)
	vis := e.VisibleArea()

	fmt.Println("=== 每层面积 ===")
	var sumVisible int64
	for i := range layers {
		solid := totalArea(e.solids[i])
		sumVisible += vis[i]
		fmt.Printf("L%d: 实体面积=%d  可见面积=%d  被遮挡=%d  可见矩形数=%d\n",
			i, solid, vis[i], solid-vis[i], len(e.visible[i]))
	}

	fmt.Println("\n=== 面积守恒检查 ===")
	var allSolid []Rect
	for _, s := range e.solids {
		allSolid = append(allSolid, s...)
	}
	union := unionAreaCompressed(allSolid)
	fmt.Printf("各层可见面积之和 = %d\n", sumVisible)
	fmt.Printf("并集面积(坐标压缩独立计算) = %d\n", union)
	if sumVisible == union {
		fmt.Println("守恒检查: 通过 (可见区域恰好划分并集)")
	} else {
		fmt.Println("守恒检查: 失败!")
	}

	fmt.Println("\n=== 点查询 (x,y) -> 最上层编号 ===")
	points := [][2]int64{
		{0, 0},   // L2 实体
		{2, 2},   // L0 孔洞内，但 L2 实体 -> L2
		{3, 3},   // L2 孔洞 ∩ L0 孔洞 -> 背景 -1
		{4, 4},   // L2 孔洞内，L0 实体 -> L0
		{10, 5},  // 共边 x=10，半开语义归 L1
		{15, 5},  // L1
		{25, 25}, // 无覆盖
	}
	for _, p := range points {
		fmt.Printf("(%d,%d) -> L%d\n", p[0], p[1], e.Topmost(p[0], p[1]))
	}
}
