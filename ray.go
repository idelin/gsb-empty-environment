package bvh

// Ray 是空间射线：Orig + t*Dir, t >= 0。Dir 不要求归一化，
// 但归一化后相交参数 t 即为距离。
type Ray struct {
	Orig, Dir Vec3
}

// At 返回射线上参数 t 处的点。
func (r Ray) At(t float64) Vec3 { return r.Orig.Add(r.Dir.Scale(t)) }
