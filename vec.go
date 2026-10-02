package bvh

import "math"

// Vec3 是三维向量。二维场景下将 Z 分量置 0 即可。
type Vec3 [3]float64

func (v Vec3) Add(o Vec3) Vec3      { return Vec3{v[0] + o[0], v[1] + o[1], v[2] + o[2]} }
func (v Vec3) Sub(o Vec3) Vec3      { return Vec3{v[0] - o[0], v[1] - o[1], v[2] - o[2]} }
func (v Vec3) Scale(s float64) Vec3 { return Vec3{v[0] * s, v[1] * s, v[2] * s} }
func (v Vec3) Dot(o Vec3) float64   { return v[0]*o[0] + v[1]*o[1] + v[2]*o[2] }

func (v Vec3) Cross(o Vec3) Vec3 {
	return Vec3{
		v[1]*o[2] - v[2]*o[1],
		v[2]*o[0] - v[0]*o[2],
		v[0]*o[1] - v[1]*o[0],
	}
}

func (v Vec3) Len() float64 { return math.Sqrt(v.Dot(v)) }

func (v Vec3) Normalize() Vec3 {
	l := v.Len()
	if l < 1e-15 {
		return Vec3{}
	}
	return v.Scale(1 / l)
}

func minVec(a, b Vec3) Vec3 {
	return Vec3{math.Min(a[0], b[0]), math.Min(a[1], b[1]), math.Min(a[2], b[2])}
}

func maxVec(a, b Vec3) Vec3 {
	return Vec3{math.Max(a[0], b[0]), math.Max(a[1], b[1]), math.Max(a[2], b[2])}
}
