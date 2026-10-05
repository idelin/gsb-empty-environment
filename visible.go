package main

import "fmt"

// ComputeVisible 按层序（索引越大越靠上）计算每层的最终可见区域。
// 可见区域 = 本层实体区域 − 所有更高层实体区域的并集。
// 自顶向下维护「已覆盖集合」，逐层做精确矩形减法，结果互不相交。
func ComputeVisible(layers [][]Rect) [][]Rect {
	visible := make([][]Rect, len(layers))
	var covered []Rect // 当前层之上所有层的实体矩形（可重叠，减法不要求不相交）
	for i := len(layers) - 1; i >= 0; i-- {
		solid := Normalize(layers[i]) // 同层重叠区域只计一次
		visible[i] = SubtractRects(solid, covered)
		covered = append(covered, solid...)
	}
	return visible
}

// VisibleAreas 返回每层可见面积。
func VisibleAreas(layers [][]Rect) ([]int64, [][]Rect) {
	visible := ComputeVisible(layers)
	areas := make([]int64, len(layers))
	for i, rs := range visible {
		areas[i] = TotalArea(rs)
	}
	return areas, visible
}

// CheckConservation 面积守恒检查：各层可见面积之和 == 所有层实体区域的并集面积。
// 两者都通过互不相交的分解精确计算，相等即证明无遗漏、无重复计数。
func CheckConservation(layers [][]Rect) (sumVisible, unionArea int64, ok bool) {
	areas, _ := VisibleAreas(layers)
	for _, a := range areas {
		sumVisible += a
	}
	var all []Rect
	for _, l := range layers {
		all = append(all, l...)
	}
	unionArea = UnionArea(all)
	return sumVisible, unionArea, sumVisible == unionArea
}

// TopmostAt 暴力查询：返回点 (x,y) 处最上层编号，无覆盖返回 -1。
func TopmostAt(layers [][]Rect, x, y int64) int {
	for i := len(layers) - 1; i >= 0; i-- {
		for _, r := range layers[i] {
			if r.Contains(x, y) {
				return i
			}
		}
	}
	return -1
}

func describeLayers(layers [][]Rect) {
	for i, l := range layers {
		fmt.Printf("  层 %d: 实体面积 %d, 矩形 %v\n", i, TotalArea(l), l)
	}
}
