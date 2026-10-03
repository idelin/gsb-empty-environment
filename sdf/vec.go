// Package sdf implements signed distance field (SDF) primitives,
// grid rasterization/sampling, and SDF-based ray marching (sphere tracing)
// in both 2D and 3D.
package sdf

import "math"

// Vec2 is a 2D vector/point.
type Vec2 struct {
	X, Y float64
}

func (a Vec2) Add(b Vec2) Vec2    { return Vec2{a.X + b.X, a.Y + b.Y} }
func (a Vec2) Sub(b Vec2) Vec2    { return Vec2{a.X - b.X, a.Y - b.Y} }
func (a Vec2) Mul(s float64) Vec2 { return Vec2{a.X * s, a.Y * s} }
func (a Vec2) Dot(b Vec2) float64 { return a.X*b.X + a.Y*b.Y }
func (a Vec2) Len() float64       { return math.Hypot(a.X, a.Y) }

// Norm returns the unit vector; a zero vector stays zero.
func (a Vec2) Norm() Vec2 {
	l := a.Len()
	if l == 0 {
		return Vec2{}
	}
	return a.Mul(1 / l)
}

func abs2(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// Vec3 is a 3D vector/point.
type Vec3 struct {
	X, Y, Z float64
}

func (a Vec3) Add(b Vec3) Vec3    { return Vec3{a.X + b.X, a.Y + b.Y, a.Z + b.Z} }
func (a Vec3) Sub(b Vec3) Vec3    { return Vec3{a.X - b.X, a.Y - b.Y, a.Z - b.Z} }
func (a Vec3) Mul(s float64) Vec3 { return Vec3{a.X * s, a.Y * s, a.Z * s} }
func (a Vec3) Dot(b Vec3) float64 { return a.X*b.X + a.Y*b.Y + a.Z*b.Z }
func (a Vec3) Len() float64       { return math.Sqrt(a.Dot(a)) }

// Norm returns the unit vector; a zero vector stays zero.
func (a Vec3) Norm() Vec3 {
	l := a.Len()
	if l == 0 {
		return Vec3{}
	}
	return a.Mul(1 / l)
}

func abs3(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func minf(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
