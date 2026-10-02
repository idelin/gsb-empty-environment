package stbvh

import "math"

// 维度索引约定：0=X 1=Y 2=Z(高度) 3=T(时间，Unix 秒)
const (
	DimX = 0
	DimY = 1
	DimZ = 2
	DimT = 3
	NDim = 4
)

// Box4 是四维时空轴对齐包围盒（3D 空间 + 1D 时间）。
type Box4 struct {
	Min, Max [NDim]float64
}

// EmptyBox 返回一个"空"盒（Min=+Inf, Max=-Inf），便于 Expand 累积。
func EmptyBox() Box4 {
	var b Box4
	for d := 0; d < NDim; d++ {
		b.Min[d] = math.Inf(1)
		b.Max[d] = math.Inf(-1)
	}
	return b
}

// NewBox 由两个四维角点构造盒（自动取 min/max）。
func NewBox(a, b [NDim]float64) Box4 {
	var box Box4
	for d := 0; d < NDim; d++ {
		box.Min[d] = math.Min(a[d], b[d])
		box.Max[d] = math.Max(a[d], b[d])
	}
	return box
}

// Expand 将盒扩展以包含另一个盒。
func (b Box4) Expand(o Box4) Box4 {
	for d := 0; d < NDim; d++ {
		b.Min[d] = math.Min(b.Min[d], o.Min[d])
		b.Max[d] = math.Max(b.Max[d], o.Max[d])
	}
	return b
}

// ExpandPoint 将盒扩展以包含一个四维点。
func (b Box4) ExpandPoint(p [NDim]float64) Box4 {
	for d := 0; d < NDim; d++ {
		b.Min[d] = math.Min(b.Min[d], p[d])
		b.Max[d] = math.Max(b.Max[d], p[d])
	}
	return b
}

// Intersects 判断两个 4D 盒是否相交（闭区间语义）。
func (b Box4) Intersects(o Box4) bool {
	for d := 0; d < NDim; d++ {
		if b.Min[d] > o.Max[d] || o.Min[d] > b.Max[d] {
			return false
		}
	}
	return true
}

// Centroid 返回盒中心。
func (b Box4) Centroid() [NDim]float64 {
	var c [NDim]float64
	for d := 0; d < NDim; d++ {
		c[d] = (b.Min[d] + b.Max[d]) * 0.5
	}
	return c
}

// Extent 返回盒在维度 d 上的长度。
func (b Box4) Extent(d int) float64 { return b.Max[d] - b.Min[d] }

// WidestAxis 返回跨度最大的维度（用于构建时选轴）。
func (b Box4) WidestAxis() int {
	axis, widest := 0, math.Inf(-1)
	for d := 0; d < NDim; d++ {
		if e := b.Extent(d); e > widest {
			axis, widest = d, e
		}
	}
	return axis
}

// SurfaceArea4 的 4D 推广：用于 SAH 的"表面积"（3-体积之和）。
func (b Box4) halfSurface() float64 {
	e0, e1, e2, e3 := b.Extent(0), b.Extent(1), b.Extent(2), b.Extent(3)
	return e0*e1*e2 + e0*e1*e3 + e0*e2*e3 + e1*e2*e3
}
