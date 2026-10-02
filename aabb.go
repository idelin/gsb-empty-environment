package bvh

import "math"

// AABB 是轴对齐包围盒。对二维场景，某一轴的厚度为 0。
type AABB struct {
	Min, Max Vec3
}

// Union 返回能同时包住 a 与 b 的最小 AABB。
func (a AABB) Union(b AABB) AABB {
	return AABB{Min: minVec(a.Min, b.Min), Max: maxVec(a.Max, b.Max)}
}

// Expand 返回包住点 p 的扩展包围盒。
func (a AABB) Expand(p Vec3) AABB {
	return AABB{Min: minVec(a.Min, p), Max: maxVec(a.Max, p)}
}

// Extent 返回包围盒在各轴上的尺寸。
func (a AABB) Extent() Vec3 { return a.Max.Sub(a.Min) }

// SurfaceArea 返回表面积，作为 SAH 的代价度量。
// 对退化（二维平面/线/点）包围盒回退为周长，保证二维场景可用。
func (a AABB) SurfaceArea() float64 {
	d := a.Extent()
	sa := 2 * (d[0]*d[1] + d[1]*d[2] + d[2]*d[0])
	if sa <= 0 {
		return 2 * (d[0] + d[1] + d[2])
	}
	return sa
}

// Contains 判断点是否在包围盒内（含边界）。
func (a AABB) Contains(p Vec3) bool {
	return p[0] >= a.Min[0] && p[0] <= a.Max[0] &&
		p[1] >= a.Min[1] && p[1] <= a.Max[1] &&
		p[2] >= a.Min[2] && p[2] <= a.Max[2]
}

// hit 使用 slab 法做射线-AABB 相交测试，返回进入参数 tmin（>=0）。
// 对平行于轴的射线做了特殊处理，避免 0*Inf 产生 NaN。
func (a AABB) hit(ray Ray) (float64, bool) {
	tmin := 0.0
	tmax := math.Inf(1)
	for i := 0; i < 3; i++ {
		if math.Abs(ray.Dir[i]) < 1e-12 {
			if ray.Orig[i] < a.Min[i] || ray.Orig[i] > a.Max[i] {
				return 0, false
			}
			continue
		}
		inv := 1.0 / ray.Dir[i]
		t0 := (a.Min[i] - ray.Orig[i]) * inv
		t1 := (a.Max[i] - ray.Orig[i]) * inv
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
