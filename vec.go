package hexgrid

import "math"

// vec3 is a 3D vector used for icosahedron geometry. All runtime uses are
// plain multiply/add; trigonometric functions appear only in one-time init
// and in the (non-hot-path) lat/lng conversion entry points.
type vec3 struct {
	x, y, z float64
}

func (a vec3) add(b vec3) vec3      { return vec3{a.x + b.x, a.y + b.y, a.z + b.z} }
func (a vec3) sub(b vec3) vec3      { return vec3{a.x - b.x, a.y - b.y, a.z - b.z} }
func (a vec3) scale(s float64) vec3 { return vec3{a.x * s, a.y * s, a.z * s} }
func (a vec3) dot(b vec3) float64   { return a.x*b.x + a.y*b.y + a.z*b.z }
func (a vec3) cross(b vec3) vec3 {
	return vec3{a.y*b.z - a.z*b.y, a.z*b.x - a.x*b.z, a.x*b.y - a.y*b.x}
}
func (a vec3) norm() float64 { return math.Sqrt(a.dot(a)) }
func (a vec3) unit() vec3    { return a.scale(1 / a.norm()) }
