package raster

// 本文件实现“左上填充规则”（top-left fill rule），与 Direct3D/Metal
// 使用的约定一致，用于消除相邻三角形共享边上的缝隙与重复着色。
//
// 约定屏幕 y 轴向下，三角形统一规整为顺时针（CW，屏幕面积为正），
// 因此内部位于每条有向边的左侧，像素中心处的边函数值在内部严格为正。
// 对于落在边上的像素（边函数为 0），仅当该边是“顶边”或“左边”时
// 才计入三角形：
//   - 顶边：水平且朝右指向左（y 不变、x 减小）；
//   - 左边：边向下延伸（dy > 0），包括完全竖直向下的边。
// 这样每条共享边只归属于恰好一侧的三角形，每个像素最多着色一次。

// edge 描述一条有向边 v0 -> v1。
type edge struct {
	// 边函数 E(px,py) = a*px + b*py + c，px/py 为定点像素中心。
	a, b, c fixed
	// fillOnZero 为 true 表示边函数等于 0 时像素仍属于三角形。
	fillOnZero bool
}

// isTopLeft 判断有向边 (x0,y0)->(x1,y1) 是否为顶边或左边。
func isTopLeft(x0, y0, x1, y1 fixed) bool {
	dx := x1 - x0
	dy := y1 - y0
	// 左边：边向下（屏幕 y 轴向下时 dy>0）。
	if dy > 0 {
		return true
	}
	// 顶边：水平且自右向左。
	if dy == 0 && dx < 0 {
		return true
	}
	return false
}

// newEdge 构造边 v0 -> v1，并按包围盒左上角像素中心初始化边函数。
func newEdge(v0x, v0y, v1x, v1y fixed) edge {
	a := v0y - v1y
	b := v1x - v0x

	// E = (v1-v0) x (p-v0)，顶点处为 0：
	// E = (v1x-v0x)*(py-v0y) - (v1y-v0y)*(px-v0x)
	//   = a*px + b*py + c，其中 c = v0x*v1y - v1x*v0y。
	c := v0x*v1y - v1x*v0y

	return edge{
		a:          a,
		b:          b,
		c:          c,
		fillOnZero: isTopLeft(v0x, v0y, v1x, v1y),
	}
}

// inside 应用填充规则：内部严格为正；边上仅顶边/左边计入。
func (e edge) inside(v fixed) bool {
	if v > 0 {
		return true
	}
	return v == 0 && e.fillOnZero
}

func minF(a, b fixed) fixed {
	if a < b {
		return a
	}
	return b
}

func maxF(a, b fixed) fixed {
	if a > b {
		return a
	}
	return b
}
