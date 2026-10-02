package broadphase

// Vec3 是三维向量，用于描述空间位置与包围盒顶点。
type Vec3 struct {
	X, Y, Z float64
}

// AABB 是轴对齐包围盒（Axis-Aligned Bounding Box）。
type AABB struct {
	Min, Max Vec3
}

// NewAABB 以中心点和半尺寸构造包围盒。
func NewAABB(center Vec3, half Vec3) AABB {
	return AABB{
		Min: Vec3{center.X - half.X, center.Y - half.Y, center.Z - half.Z},
		Max: Vec3{center.X + half.X, center.Y + half.Y, center.Z + half.Z},
	}
}

// Overlaps 判断两个包围盒是否相交（含边界接触）。
func (a AABB) Overlaps(b AABB) bool {
	return a.Min.X <= b.Max.X && a.Max.X >= b.Min.X &&
		a.Min.Y <= b.Max.Y && a.Max.Y >= b.Min.Y &&
		a.Min.Z <= b.Max.Z && a.Max.Z >= b.Min.Z
}

// Contains 判断 a 是否完整包含 b。
func (a AABB) Contains(b AABB) bool {
	return a.Min.X <= b.Min.X && a.Max.X >= b.Max.X &&
		a.Min.Y <= b.Min.Y && a.Max.Y >= b.Max.Y &&
		a.Min.Z <= b.Min.Z && a.Max.Z >= b.Max.Z
}

// Fattened 返回沿各轴向外扩张 margin 后的“胖”包围盒。
// 胖包围盒用于吸收小幅度运动，避免每帧都重构树节点。
func (a AABB) Fattened(margin float64) AABB {
	return AABB{
		Min: Vec3{a.Min.X - margin, a.Min.Y - margin, a.Min.Z - margin},
		Max: Vec3{a.Max.X + margin, a.Max.Y + margin, a.Max.Z + margin},
	}
}

// SurfaceArea 返回包围盒表面积，用于 SAH（表面积启发式）插入代价评估。
func (a AABB) SurfaceArea() float64 {
	dx := a.Max.X - a.Min.X
	dy := a.Max.Y - a.Min.Y
	dz := a.Max.Z - a.Min.Z
	return 2 * (dx*dy + dy*dz + dz*dx)
}

// Union 返回同时包含 a 与 b 的最小包围盒。
func Union(a, b AABB) AABB {
	return AABB{
		Min: Vec3{minF(a.Min.X, b.Min.X), minF(a.Min.Y, b.Min.Y), minF(a.Min.Z, b.Min.Z)},
		Max: Vec3{maxF(a.Max.X, b.Max.X), maxF(a.Max.Y, b.Max.Y), maxF(a.Max.Z, b.Max.Z)},
	}
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
