package sdf

import "math"

// Vec3 is a 3D vector / point.
type Vec3 struct {
	X, Y, Z float64
}

// V3 constructs a Vec3.
func V3(x, y, z float64) Vec3 { return Vec3{X: x, Y: y, Z: z} }

func (a Vec3) Add(b Vec3) Vec3      { return Vec3{a.X + b.X, a.Y + b.Y, a.Z + b.Z} }
func (a Vec3) Sub(b Vec3) Vec3      { return Vec3{a.X - b.X, a.Y - b.Y, a.Z - b.Z} }
func (a Vec3) Scale(s float64) Vec3 { return Vec3{a.X * s, a.Y * s, a.Z * s} }
func (a Vec3) Dot(b Vec3) float64   { return a.X*b.X + a.Y*b.Y + a.Z*b.Z }

func (a Vec3) Cross(b Vec3) Vec3 {
	return Vec3{
		a.Y*b.Z - a.Z*b.Y,
		a.Z*b.X - a.X*b.Z,
		a.X*b.Y - a.Y*b.X,
	}
}

func (a Vec3) LenSq() float64 { return a.X*a.X + a.Y*a.Y + a.Z*a.Z }
func (a Vec3) Len() float64   { return math.Sqrt(a.LenSq()) }

func (a Vec3) Normalize() Vec3 {
	l := a.Len()
	if l == 0 {
		return Vec3{}
	}
	return a.Scale(1 / l)
}

func (a Vec3) Abs() Vec3 { return Vec3{math.Abs(a.X), math.Abs(a.Y), math.Abs(a.Z)} }

// Lerp linearly interpolates between a and b.
func (a Vec3) Lerp(b Vec3, t float64) Vec3 {
	return a.Add(b.Sub(a).Scale(t))
}
