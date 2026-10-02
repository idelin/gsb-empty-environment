package hexgrid

import "math"

// vec3 is a 3D vector on the unit sphere. All spherical geometry in this
// package is done with dot/cross products so that the hot paths never call
// trigonometric functions.
type vec3 struct {
	x, y, z float64
}

func (v vec3) add(o vec3) vec3      { return vec3{v.x + o.x, v.y + o.y, v.z + o.z} }
func (v vec3) sub(o vec3) vec3      { return vec3{v.x - o.x, v.y - o.y, v.z - o.z} }
func (v vec3) scale(s float64) vec3 { return vec3{v.x * s, v.y * s, v.z * s} }
func (v vec3) dot(o vec3) float64   { return v.x*o.x + v.y*o.y + v.z*o.z }

func (v vec3) cross(o vec3) vec3 {
	return vec3{
		v.y*o.z - v.z*o.y,
		v.z*o.x - v.x*o.z,
		v.x*o.y - v.y*o.x,
	}
}

func (v vec3) length() float64 { return math.Sqrt(v.dot(v)) }

func (v vec3) norm() vec3 { return v.scale(1 / v.length()) }
