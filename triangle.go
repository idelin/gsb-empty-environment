package bvh

import "math"

// Triangle 是三维三角形图元，实现 Primitive 接口。
type Triangle struct {
	V0, V1, V2 Vec3
}

// Bounds 返回三角形的 AABB。
func (t Triangle) Bounds() AABB {
	return AABB{
		Min: minVec(minVec(t.V0, t.V1), t.V2),
		Max: maxVec(maxVec(t.V0, t.V1), t.V2),
	}
}

// Centroid 返回三角形质心。
func (t Triangle) Centroid() Vec3 {
	return t.V0.Add(t.V1).Add(t.V2).Scale(1.0 / 3.0)
}

// Intersect 使用 Möller–Trumbore 算法做射线-三角形相交测试。
// 命中时返回最近正参数 t。
func (t Triangle) Intersect(ray Ray) (float64, bool) {
	const eps = 1e-9
	e1 := t.V1.Sub(t.V0)
	e2 := t.V2.Sub(t.V0)
	p := ray.Dir.Cross(e2)
	det := e1.Dot(p)
	if math.Abs(det) < eps {
		return 0, false
	}
	inv := 1.0 / det
	s := ray.Orig.Sub(t.V0)
	u := s.Dot(p) * inv
	if u < 0 || u > 1 {
		return 0, false
	}
	q := s.Cross(e1)
	v := ray.Dir.Dot(q) * inv
	if v < 0 || u+v > 1 {
		return 0, false
	}
	hitT := e2.Dot(q) * inv
	if hitT < eps {
		return 0, false
	}
	return hitT, true
}
