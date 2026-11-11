package bvhtrace

import "math"

// Vec3 is a three component float32 vector used for geometry and shading data.
type Vec3 struct {
	X, Y, Z float32
}

func (v Vec3) Add(o Vec3) Vec3      { return Vec3{v.X + o.X, v.Y + o.Y, v.Z + o.Z} }
func (v Vec3) Sub(o Vec3) Vec3      { return Vec3{v.X - o.X, v.Y - o.Y, v.Z - o.Z} }
func (v Vec3) Scale(s float32) Vec3 { return Vec3{v.X * s, v.Y * s, v.Z * s} }
func (v Vec3) Dot(o Vec3) float32   { return v.X*o.X + v.Y*o.Y + v.Z*o.Z }

func (v Vec3) Cross(o Vec3) Vec3 {
	return Vec3{
		v.Y*o.Z - v.Z*o.Y,
		v.Z*o.X - v.X*o.Z,
		v.X*o.Y - v.Y*o.X,
	}
}

func (v Vec3) Length() float32 { return float32(math.Sqrt(float64(v.Dot(v)))) }

func (v Vec3) Normalize() Vec3 {
	l := v.Length()
	if l == 0 {
		return v
	}
	return v.Scale(1 / l)
}

// component returns the vector component by sheared-space axis index (0, 1, 2).
func (v Vec3) component(axis int) float32 {
	switch axis {
	case 0:
		return v.X
	case 1:
		return v.Y
	default:
		return v.Z
	}
}

func min3(a, b, c float32) float32 { return min(a, min(b, c)) }
func max3(a, b, c float32) float32 { return max(a, max(b, c)) }
