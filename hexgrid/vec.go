package hexgrid

import "math"

// vec3 is a 3D vector used for spherical geometry on the unit sphere and
// for gnomonic projection onto icosahedron face planes.
type vec3 struct {
	x, y, z float64
}

func (a vec3) add(b vec3) vec3      { return vec3{a.x + b.x, a.y + b.y, a.z + b.z} }
func (a vec3) sub(b vec3) vec3      { return vec3{a.x - b.x, a.y - b.y, a.z - b.z} }
func (a vec3) scale(t float64) vec3 { return vec3{a.x * t, a.y * t, a.z * t} }
func (a vec3) dot(b vec3) float64   { return a.x*b.x + a.y*b.y + a.z*b.z }
func (a vec3) cross(b vec3) vec3 {
	return vec3{a.y*b.z - a.z*b.y, a.z*b.x - a.x*b.z, a.x*b.y - a.y*b.x}
}
func (a vec3) norm() float64    { return math.Sqrt(a.dot(a)) }
func (a vec3) normalized() vec3 { return a.scale(1 / a.norm()) }

// latLngToVec converts WGS84-style degrees to a unit vector.
func latLngToVec(lat, lng float64) vec3 {
	latR := lat * math.Pi / 180
	lngR := lng * math.Pi / 180
	return vec3{
		math.Cos(latR) * math.Cos(lngR),
		math.Cos(latR) * math.Sin(lngR),
		math.Sin(latR),
	}
}

// vecToLatLng converts a (non-zero) vector back to degrees.
func vecToLatLng(s vec3) (lat, lng float64) {
	n := s.normalized()
	lat = math.Atan2(n.z, math.Sqrt(n.x*n.x+n.y*n.y)) * 180 / math.Pi
	lng = math.Atan2(n.y, n.x) * 180 / math.Pi
	return lat, lng
}
