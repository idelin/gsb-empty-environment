package broadphase

// Vec2 二维向量。
type Vec2 struct {
	X, Y float64
}

// Scale 返回 v*s。
func (v Vec2) Scale(s float64) Vec2 { return Vec2{v.X * s, v.Y * s} }

// AABB 轴对齐包围盒。
type AABB struct {
	Min, Max Vec2
}

// Overlaps 判断两个 AABB 是否重叠（边界接触视为重叠）。
func (a AABB) Overlaps(b AABB) bool {
	return a.Min.X <= b.Max.X && b.Min.X <= a.Max.X &&
		a.Min.Y <= b.Max.Y && b.Min.Y <= a.Max.Y
}

// Contains 判断 a 是否完全包含 b。
func (a AABB) Contains(b AABB) bool {
	return a.Min.X <= b.Min.X && a.Min.Y <= b.Min.Y &&
		a.Max.X >= b.Max.X && a.Max.Y >= b.Max.Y
}

// Fatten 向四周扩张 margin，得到"肥胖"包围盒。
func (a AABB) Fatten(margin float64) AABB {
	return AABB{
		Min: Vec2{a.Min.X - margin, a.Min.Y - margin},
		Max: Vec2{a.Max.X + margin, a.Max.Y + margin},
	}
}

// Extend 沿位移方向扩张包围盒，用于碰撞预测（speculative margin）。
func (a AABB) Extend(d Vec2) AABB {
	if d.X < 0 {
		a.Min.X += d.X
	} else {
		a.Max.X += d.X
	}
	if d.Y < 0 {
		a.Min.Y += d.Y
	} else {
		a.Max.Y += d.Y
	}
	return a
}

// Perimeter 返回周长，作为树的代价启发函数。
func (a AABB) Perimeter() float64 {
	return 2 * (a.Max.X - a.Min.X + a.Max.Y - a.Min.Y)
}

// Combine 返回同时包含 a、b 的最小 AABB。
func Combine(a, b AABB) AABB {
	return AABB{
		Min: Vec2{min(a.Min.X, b.Min.X), min(a.Min.Y, b.Min.Y)},
		Max: Vec2{max(a.Max.X, b.Max.X), max(a.Max.Y, b.Max.Y)},
	}
}
