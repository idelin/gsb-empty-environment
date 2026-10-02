package bvh

// Triangle 三角形图元，实现 Object 接口。
type Triangle struct {
	V0, V1, V2 Vec3
}

func (t *Triangle) Bounds() AABB {
	return AABB{
		Min: minVec(t.V0, minVec(t.V1, t.V2)),
		Max: maxVec(t.V0, maxVec(t.V1, t.V2)),
	}
}

// Intersect Möller–Trumbore 射线-三角形求交，返回最近正距离 t。
func (t *Triangle) Intersect(r Ray) (float64, bool) {
	const eps = 1e-9
	e1 := t.V1.Sub(t.V0)
	e2 := t.V2.Sub(t.V0)
	p := r.Dir.Cross(e2)
	det := e1.Dot(p)
	if det > -eps && det < eps {
		return 0, false
	}
	inv := 1 / det
	s := r.Origin.Sub(t.V0)
	u := s.Dot(p) * inv
	if u < 0 || u > 1 {
		return 0, false
	}
	q := s.Cross(e1)
	v := r.Dir.Dot(q) * inv
	if v < 0 || u+v > 1 {
		return 0, false
	}
	hit := e2.Dot(q) * inv
	if hit < eps {
		return 0, false
	}
	return hit, true
}
