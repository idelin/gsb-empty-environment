// Package floodsim 实现时空二维栅格状态演化仿真内核：
// 在离散 tick 下模拟地表淹没区域随降雨、渗透与地形坡度的动态扩散，
// 并支持查询任意历史时刻的积水连通区域边界多边形与道路通行断点。
package floodsim

// Grid 表示规则二维栅格，行优先存储。
// 单元 (x, y) 覆盖平面区域 [x, x+1] x [y, y+1]，索引 i = y*Width + x。
type Grid struct {
	Width  int
	Height int
	// Elev 为数字高程模型（DEM），单位米。
	Elev []float64
	// Infil 为每个单元的渗透率（每 tick 下渗水深，单位米/tick）。
	Infil []float64
}

// NewGrid 创建栅格；elev 长度必须为 w*h，infil 为 nil 时默认零渗透。
func NewGrid(w, h int, elev, infil []float64) *Grid {
	if len(elev) != w*h {
		panic("floodsim: elev length mismatch")
	}
	g := &Grid{Width: w, Height: h, Elev: append([]float64(nil), elev...)}
	g.Infil = make([]float64, w*h)
	if infil != nil {
		if len(infil) != w*h {
			panic("floodsim: infil length mismatch")
		}
		copy(g.Infil, infil)
	}
	return g
}

// Index 返回单元 (x, y) 的线性索引，越界返回 -1。
func (g *Grid) Index(x, y int) int {
	if x < 0 || y < 0 || x >= g.Width || y >= g.Height {
		return -1
	}
	return y*g.Width + x
}

// At 返回坐标 (px, py)（栅格平面坐标）所在单元索引，越界返回 -1。
func (g *Grid) At(px, py float64) int {
	return g.Index(int(px), int(py))
}
