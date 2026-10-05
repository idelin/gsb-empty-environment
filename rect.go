package main

import "sort"

// Rect 是半开轴对齐矩形 [X1,X2) x [Y1,Y2)。
// 半开语义保证共边相邻的矩形不重复计面积，边上的点归属右侧/上侧矩形。
type Rect struct {
	X1, Y1, X2, Y2 int64
}

func (r Rect) Area() int64 {
	if r.X2 <= r.X1 || r.Y2 <= r.Y1 {
		return 0
	}
	return (r.X2 - r.X1) * (r.Y2 - r.Y1)
}

func (r Rect) Contains(x, y int64) bool {
	return x >= r.X1 && x < r.X2 && y >= r.Y1 && y < r.Y2
}

func (r Rect) Empty() bool { return r.X2 <= r.X1 || r.Y2 <= r.Y1 }

// TotalArea 返回矩形列表面积之和（允许重叠，调用方保证语义）。
func TotalArea(rs []Rect) int64 {
	var s int64
	for _, r := range rs {
		s += r.Area()
	}
	return s
}

// subtractOne 返回 a \ b（a 减去 b 后剩余的不相交矩形切片，最多 4 块）。
func subtractOne(a, b Rect) []Rect {
	ix1, iy1 := max(a.X1, b.X1), max(a.Y1, b.Y1)
	ix2, iy2 := min(a.X2, b.X2), min(a.Y2, b.Y2)
	if ix1 >= ix2 || iy1 >= iy2 { // 无交集（含仅共边/共点）
		return []Rect{a}
	}
	out := make([]Rect, 0, 4)
	if a.Y1 < iy1 { // 下
		out = append(out, Rect{a.X1, a.Y1, a.X2, iy1})
	}
	if iy2 < a.Y2 { // 上
		out = append(out, Rect{a.X1, iy2, a.X2, a.Y2})
	}
	if a.X1 < ix1 { // 左
		out = append(out, Rect{a.X1, iy1, ix1, iy2})
	}
	if ix2 < a.X2 { // 右
		out = append(out, Rect{ix2, iy1, a.X2, iy2})
	}
	return out
}

// SubtractRects 计算 rects \ cutter（集合差），结果互不相交。
func SubtractRects(rects []Rect, cutter []Rect) []Rect {
	out := make([]Rect, 0, len(rects))
	for _, r := range rects {
		if r.Empty() {
			continue
		}
		pieces := []Rect{r}
		for _, c := range cutter {
			if c.Empty() {
				continue
			}
			next := make([]Rect, 0, len(pieces)+3)
			for _, p := range pieces {
				next = append(next, subtractOne(p, c)...)
			}
			pieces = next
			if len(pieces) == 0 {
				break
			}
		}
		out = append(out, pieces...)
	}
	return out
}

// NewLayerWithHoles 用「外框 - 孔洞」构造一层的实体（不透明）区域。
// 孔洞可与外框部分相交、可互相重叠，均按透明处理。
func NewLayerWithHoles(bounds Rect, holes []Rect) []Rect {
	return SubtractRects([]Rect{bounds}, holes)
}

// UnionArea 用扫描线精确计算矩形并集面积（坐标压缩，不栅格化）。
func UnionArea(rects []Rect) int64 {
	xs := make([]int64, 0, 2*len(rects))
	for _, r := range rects {
		if r.Empty() {
			continue
		}
		xs = append(xs, r.X1, r.X2)
	}
	if len(xs) == 0 {
		return 0
	}
	sort.Slice(xs, func(i, j int) bool { return xs[i] < xs[j] })
	xs = unique(xs)

	var total int64
	for i := 0; i+1 < len(xs); i++ {
		x1, x2 := xs[i], xs[i+1]
		// 收集覆盖该竖条的 y 区间并求并集长度
		var ivs [][2]int64
		for _, r := range rects {
			if r.X1 <= x1 && r.X2 >= x2 {
				ivs = append(ivs, [2]int64{r.Y1, r.Y2})
			}
		}
		sort.Slice(ivs, func(a, b int) bool { return ivs[a][0] < ivs[b][0] })
		var yLen, curS, curE int64
		for j, iv := range ivs {
			if j == 0 || iv[0] > curE {
				yLen += curE - curS
				curS, curE = iv[0], iv[1]
			} else if iv[1] > curE {
				curE = iv[1]
			}
		}
		yLen += curE - curS
		total += (x2 - x1) * yLen
	}
	return total
}

func unique(s []int64) []int64 {
	out := s[:0]
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}

// Normalize 把可能互相重叠的矩形列表归一化为互不相交的并集表示。
// 同一层内部矩形重叠时，可见面积只应计一次，必须先归一化。
func Normalize(rects []Rect) []Rect {
	var out []Rect
	for _, r := range rects {
		if r.Empty() {
			continue
		}
		out = append(out, SubtractRects([]Rect{r}, out)...)
	}
	return out
}
