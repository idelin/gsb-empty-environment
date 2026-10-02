package bvh

import "math"

// AABB 轴对齐包围盒。
type AABB struct {
	Min, Max Vec3
}

// EmptyAABB 返回一个“空”包围盒（用于并集累积）。
func EmptyAABB() AABB {
	inf := math.Inf(1)
	return AABB{Min: Vec3{inf, inf, inf}, Max: Vec3{-inf, -inf, -inf}}
}

// Union 返回同时包含 a 与 b 的最小 AABB。
func (a AABB) Union(b AABB) AABB {
	return AABB{Min: minVec(a.Min, b.Min), Max: maxVec(a.Max, b.Max)}
}

// SurfaceArea 表面积，用于 SAH 代价评估。
func (a AABB) SurfaceArea() float64 {
	d := a.Max.Sub(a.Min)
	if d.X < 0 || d.Y < 0 || d.Z < 0 {
		return 0
	}
	return 2 * (d.X*d.Y + d.Y*d.Z + d.Z*d.X)
}

// Contains 判断 a 是否完全包含 b。
func (a AABB) Contains(b AABB) bool {
	return a.Min.X <= b.Min.X && a.Min.Y <= b.Min.Y && a.Min.Z <= b.Min.Z &&
		a.Max.X >= b.Max.X && a.Max.Y >= b.Max.Y && a.Max.Z >= b.Max.Z
}

// IntersectRay 使用 slab 法做射线-AABB 相交测试，
// 返回是否命中以及进入距离 tmin。maxT 用于提前剔除（只关心比 maxT 更近的命中）。
func (a AABB) IntersectRay(r Ray, maxT float64) (tmin float64, ok bool) {
	tmin, tmax := 0.0, maxT
	amin := [3]float64{a.Min.X, a.Min.Y, a.Min.Z}
	amax := [3]float64{a.Max.X, a.Max.Y, a.Max.Z}
	orig := [3]float64{r.Origin.X, r.Origin.Y, r.Origin.Z}
	dir := [3]float64{r.Dir.X, r.Dir.Y, r.Dir.Z}
	for i := 0; i < 3; i++ {
		if math.Abs(dir[i]) < 1e-12 {
			if orig[i] < amin[i] || orig[i] > amax[i] {
				return 0, false
			}
			continue
		}
		inv := 1 / dir[i]
		t0 := (amin[i] - orig[i]) * inv
		t1 := (amax[i] - orig[i]) * inv
		if t0 > t1 {
			t0, t1 = t1, t0
		}
		if t0 > tmin {
			tmin = t0
		}
		if t1 < tmax {
			tmax = t1
		}
		if tmin > tmax {
			return 0, false
		}
	}
	return tmin, true
}
