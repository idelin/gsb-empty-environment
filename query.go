package main

import "sort"

// LayerMap 用坐标压缩网格支持任意点最上层查询。
// 所有矩形边界的 x/y 坐标把平面切成 O(E^2) 个单元（E 为边数），
// 每个单元内最上层编号一致；查询时二分定位单元，O(log E)。
// 只存储压缩坐标与单元编号，与大坐标平面的实际尺寸无关。
type LayerMap struct {
	xs, ys []int64
	top    [][]int // top[yi][xi]，-1 表示无覆盖
}

func NewLayerMap(layers [][]Rect) *LayerMap {
	xset, yset := map[int64]struct{}{}, map[int64]struct{}{}
	for _, l := range layers {
		for _, r := range l {
			if r.Empty() {
				continue
			}
			xset[r.X1], xset[r.X2] = struct{}{}, struct{}{}
			yset[r.Y1], yset[r.Y2] = struct{}{}, struct{}{}
		}
	}
	xs, ys := sortedKeys(xset), sortedKeys(yset)
	m := &LayerMap{xs: xs, ys: ys}
	if len(xs) < 2 || len(ys) < 2 {
		return m
	}
	xIdx := indexOf(xs)
	yIdx := indexOf(ys)
	m.top = make([][]int, len(ys)-1)
	for i := range m.top {
		row := make([]int, len(xs)-1)
		for j := range row {
			row[j] = -1
		}
		m.top[i] = row
	}
	// 按层序填充，高层直接覆盖低层
	for li, l := range layers {
		for _, r := range l {
			if r.Empty() {
				continue
			}
			for yi := yIdx[r.Y1]; yi < yIdx[r.Y2]; yi++ {
				for xi := xIdx[r.X1]; xi < xIdx[r.X2]; xi++ {
					m.top[yi][xi] = li
				}
			}
		}
	}
	return m
}

// TopmostAt 返回点 (x,y) 处最上层编号，无覆盖返回 -1。
func (m *LayerMap) TopmostAt(x, y int64) int {
	if len(m.xs) < 2 || len(m.ys) < 2 {
		return -1
	}
	if x < m.xs[0] || x >= m.xs[len(m.xs)-1] || y < m.ys[0] || y >= m.ys[len(m.ys)-1] {
		return -1
	}
	xi := sort.Search(len(m.xs), func(i int) bool { return m.xs[i] > x }) - 1
	yi := sort.Search(len(m.ys), func(i int) bool { return m.ys[i] > y }) - 1
	return m.top[yi][xi]
}

func sortedKeys(set map[int64]struct{}) []int64 {
	out := make([]int64, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func indexOf(sorted []int64) map[int64]int {
	m := make(map[int64]int, len(sorted))
	for i, v := range sorted {
		m[v] = i
	}
	return m
}
